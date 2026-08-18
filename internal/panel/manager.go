package panel

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"crypto/rand"
)

var (
	ErrBusy       = errors.New("proxy is busy")
	ErrNotFound   = errors.New("proxy not found")
	ErrLimit      = errors.New("proxy limit reached")
	ErrForbidden  = errors.New("forbidden")
	ErrUserExists = errors.New("user already exists")
)

type Manager struct {
	cfg     Config
	network NetworkManager
	xray    XrayController
	store   *StateStore

	stateMu sync.RWMutex
	state   State
	info    NetworkInfo
	prefix  *net.IPNet
	initErr error

	addMu  sync.Mutex
	opMu   sync.RWMutex
	busy   sync.Map
	jobsMu sync.RWMutex
	jobs   map[string]*Job
}

func NewManager(cfg Config, network NetworkManager, xray XrayController) *Manager {
	return &Manager{cfg: cfg, network: network, xray: xray, store: NewStateStore(cfg.DataDir), jobs: map[string]*Job{}}
}

func (m *Manager) Start(ctx context.Context) error {
	state, err := m.store.Load()
	if err != nil {
		m.initErr = err
		return err
	}
	interfaceOverride := state.IPv6Interface
	if interfaceOverride == "" {
		interfaceOverride = m.cfg.IPv6Interface
	}
	prefixOverride := state.IPv6Prefix
	if prefixOverride == "" {
		prefixOverride = m.cfg.IPv6Prefix
	}
	info, prefix, err := m.network.Discover(ctx, interfaceOverride, prefixOverride)
	if err != nil {
		m.initErr = err
		return err
	}
	m.info, m.prefix, m.state = info, prefix, state
	if err := m.ensureAuthState(); err != nil {
		m.initErr = err
		return err
	}
	if err := m.normalizeProxyIDs(); err != nil {
		m.initErr = err
		return err
	}
	if err := m.recoverPending(ctx); err != nil {
		m.initErr = err
		return err
	}
	if err := m.migrateChangedPrefix(ctx); err != nil {
		m.initErr = err
		return err
	}
	if err := m.reconcileAddresses(ctx); err != nil {
		m.initErr = err
		return err
	}
	for len(m.snapshotProxies()) < m.cfg.InitialProxies {
		if _, err := m.addBeforeXray(ctx, nil, "socks5", m.cfg.AdminUsername); err != nil {
			m.initErr = err
			return err
		}
	}
	if err := m.xray.Start(ctx, m.snapshotProxies(), m.info); err != nil {
		m.initErr = err
		return err
	}
	m.initErr = nil
	go m.monitorXray(ctx)
	return nil
}

// migrateChangedPrefix preserves proxy IDs and ports while replacing addresses
// that belonged to an ISP prefix used before the host rebooted.
func (m *Manager) migrateChangedPrefix(ctx context.Context) error {
	for _, proxy := range m.snapshotProxies() {
		ip := net.ParseIP(proxy.IPv6)
		if ip != nil && m.prefix.Contains(ip) {
			continue
		}
		log.Printf("检测到 IPv6 前缀变化，正在迁移端口 %d", proxy.Port)
		opID, cidr, err := m.prepareRotation(ctx, proxy)
		if err != nil {
			return fmt.Errorf("migrate proxy %d to prefix %s: %w", proxy.Port, m.prefix, err)
		}
		if err := m.activateAddress(ctx, cidr); err != nil {
			m.abortPending(opID, cidr)
			return fmt.Errorf("migrate proxy %d to prefix %s: %w", proxy.Port, m.prefix, err)
		}

		updated := proxy
		updated.IPv6 = cidr.IP.String()
		updated.Status = "healthy"
		updated.LastError = ""
		updated.LastRotatedAt = time.Now().UTC()
		m.stateMu.Lock()
		index := m.proxyIndexLocked(proxy.ID)
		if index < 0 {
			m.stateMu.Unlock()
			m.abortPending(opID, cidr)
			return ErrNotFound
		}
		m.state.Proxies[index] = updated
		delete(m.state.Pending, opID)
		saveErr := m.store.Save(m.state)
		if saveErr != nil {
			m.state.Proxies[index] = proxy
			m.state.Pending[opID] = PendingOperation{ID: opID, ProxyID: proxy.ID, Kind: "prefix-migrate", Candidate: cidr.IP.String(), CreatedAt: time.Now().UTC()}
		}
		m.stateMu.Unlock()
		if saveErr != nil {
			m.abortPending(opID, cidr)
			return saveErr
		}
	}
	return nil
}

func (m *Manager) Shutdown(ctx context.Context) error {
	var errs []error
	if err := m.xray.Stop(ctx); err != nil {
		errs = append(errs, err)
	}
	for _, proxy := range m.snapshotProxies() {
		if cidr, err := m.proxyCIDR(proxy.IPv6); err == nil {
			if err := m.network.DeleteAddress(ctx, m.info.Interface, cidr); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) monitorXray(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !m.xray.Running() {
				restartCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
				_ = m.xray.Start(restartCtx, m.snapshotProxies(), m.info)
				cancel()
			}
		}
	}
}

func (m *Manager) recoverPending(ctx context.Context) error {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	for _, op := range m.state.Pending {
		if op.Candidate == "" {
			continue
		}
		cidr, err := m.proxyCIDR(op.Candidate)
		if err == nil {
			_ = m.network.DeleteAddress(ctx, m.info.Interface, cidr)
		}
	}
	m.state.Pending = map[string]PendingOperation{}
	return m.store.Save(m.state)
}

func (m *Manager) reconcileAddresses(ctx context.Context) error {
	addresses, err := m.network.ListAddresses(ctx, m.info.Interface)
	if err != nil {
		return err
	}
	present := map[string]bool{}
	for _, ip := range addresses {
		present[ip.String()] = true
	}
	for _, proxy := range m.snapshotProxies() {
		if present[proxy.IPv6] {
			continue
		}
		cidr, err := m.proxyCIDR(proxy.IPv6)
		if err != nil {
			return fmt.Errorf("restore proxy %s: %w", proxy.ID, err)
		}
		if err := m.network.AddAddress(ctx, m.info.Interface, cidr); err != nil {
			return err
		}
		if err := m.network.WaitReady(ctx, m.info.Interface, cidr.IP, m.cfg.DADTimeout); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) List() []Proxy {
	proxies := m.snapshotProxies()
	sort.Slice(proxies, func(i, j int) bool { return proxies[i].Port < proxies[j].Port })
	return proxies
}

func (m *Manager) ListForUser(username string) []Proxy {
	all := m.List()
	result := make([]Proxy, 0, len(all))
	for _, proxy := range all {
		if proxy.Owner == username {
			result = append(result, proxy)
		}
	}
	return result
}

func (m *Manager) NetworkInfo() NetworkInfo { return m.info }
func (m *Manager) Config() Config           { return m.cfg }
func (m *Manager) Healthy() (bool, string) {
	if m.initErr != nil {
		return false, m.initErr.Error()
	}
	if !m.xray.Running() {
		return false, "Xray 未运行"
	}
	return true, "ok"
}

func (m *Manager) Add(ctx context.Context, requestedPort *int) (Proxy, error) {
	return m.AddWithProtocol(ctx, requestedPort, "socks5")
}

func (m *Manager) AddWithProtocol(ctx context.Context, requestedPort *int, protocol string) (Proxy, error) {
	return m.AddWithProtocolForUser(ctx, requestedPort, protocol, m.cfg.AdminUsername)
}

func (m *Manager) AddWithProtocolForUser(ctx context.Context, requestedPort *int, protocol, owner string) (Proxy, error) {
	m.opMu.RLock()
	defer m.opMu.RUnlock()
	m.addMu.Lock()
	defer m.addMu.Unlock()
	return m.addRunning(ctx, requestedPort, protocol, owner)
}

func (m *Manager) addBeforeXray(ctx context.Context, requestedPort *int, protocol, owner string) (Proxy, error) {
	proxy, opID, cidr, err := m.prepareNew(ctx, requestedPort, protocol, owner)
	if err != nil {
		return Proxy{}, err
	}
	if err := m.activateAddress(ctx, cidr); err != nil {
		m.abortPending(opID, cidr)
		return Proxy{}, err
	}
	proxy.Status = "healthy"
	if err := m.commitAdd(opID, proxy); err != nil {
		m.abortPending(opID, cidr)
		return Proxy{}, err
	}
	return proxy, nil
}

func (m *Manager) addRunning(ctx context.Context, requestedPort *int, protocol, owner string) (Proxy, error) {
	proxy, opID, cidr, err := m.prepareNew(ctx, requestedPort, protocol, owner)
	if err != nil {
		return Proxy{}, err
	}
	if err := m.activateAddress(ctx, cidr); err != nil {
		m.abortPending(opID, cidr)
		return Proxy{}, err
	}
	proxy.Status = "healthy"
	if err := m.xray.AddProxy(ctx, proxy, m.info); err != nil {
		m.abortPending(opID, cidr)
		return Proxy{}, err
	}
	if err := m.commitAdd(opID, proxy); err != nil {
		_ = m.xray.DeleteProxy(context.Background(), proxy)
		m.abortPending(opID, cidr)
		return Proxy{}, err
	}
	return proxy, nil
}

func (m *Manager) prepareNew(ctx context.Context, requestedPort *int, protocol, owner string) (Proxy, string, *net.IPNet, error) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	if len(m.state.Proxies) >= m.cfg.MaxProxies {
		return Proxy{}, "", nil, ErrLimit
	}
	protocol, err := normalizeProtocol(protocol)
	if err != nil {
		return Proxy{}, "", nil, err
	}
	user, ok := m.userLocked(owner)
	if !ok {
		return Proxy{}, "", nil, ErrForbidden
	}
	port, err := m.allocatePortLocked(requestedPort)
	if err != nil {
		return Proxy{}, "", nil, err
	}
	used, err := m.usedAddressesLocked(ctx)
	if err != nil {
		return Proxy{}, "", nil, err
	}
	ip, err := randomIPv6(m.prefix, used)
	if err != nil {
		return Proxy{}, "", nil, err
	}
	id, opID := strconv.Itoa(port), randomID()
	proxy := Proxy{ID: id, Port: port, Protocol: protocol, IPv6: ip.String(), Status: "creating", CreatedAt: time.Now().UTC(), Owner: user.Username, Username: user.Username, Password: user.ProxyPassword}
	cidr := &net.IPNet{IP: ip, Mask: m.prefix.Mask}
	m.state.Pending[opID] = PendingOperation{ID: opID, ProxyID: id, Kind: "add", Candidate: ip.String(), CreatedAt: time.Now().UTC()}
	if err := m.store.Save(m.state); err != nil {
		delete(m.state.Pending, opID)
		return Proxy{}, "", nil, err
	}
	return proxy, opID, cidr, nil
}

func (m *Manager) activateAddress(ctx context.Context, cidr *net.IPNet) error {
	if err := m.network.AddAddress(ctx, m.info.Interface, cidr); err != nil {
		return err
	}
	if err := m.network.WaitReady(ctx, m.info.Interface, cidr.IP, m.cfg.DADTimeout); err != nil {
		return err
	}
	return m.network.CheckEgress(ctx, cidr.IP, m.cfg.IPCheckURL, m.cfg.IPCheckTimeout)
}

func (m *Manager) commitAdd(opID string, proxy Proxy) error {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	m.state.Proxies = append(m.state.Proxies, proxy)
	delete(m.state.Pending, opID)
	if err := m.store.Save(m.state); err != nil {
		m.state.Proxies = m.state.Proxies[:len(m.state.Proxies)-1]
		return err
	}
	return nil
}

func (m *Manager) abortPending(opID string, cidr *net.IPNet) {
	_ = m.network.DeleteAddress(context.Background(), m.info.Interface, cidr)
	m.stateMu.Lock()
	delete(m.state.Pending, opID)
	_ = m.store.Save(m.state)
	m.stateMu.Unlock()
}

func (m *Manager) Rotate(ctx context.Context, id string) (Proxy, error) {
	m.opMu.RLock()
	defer m.opMu.RUnlock()
	return m.rotate(ctx, id)
}

func (m *Manager) rotate(ctx context.Context, id string) (Proxy, error) {
	if _, loaded := m.busy.LoadOrStore(id, true); loaded {
		return Proxy{}, ErrBusy
	}
	defer m.busy.Delete(id)
	proxy, err := m.findProxy(id)
	if err != nil {
		return Proxy{}, err
	}
	opID, cidr, err := m.prepareRotation(ctx, proxy)
	if err != nil {
		return Proxy{}, err
	}
	if err := m.activateAddress(ctx, cidr); err != nil {
		m.abortPending(opID, cidr)
		return Proxy{}, err
	}
	if err := m.xray.ReplaceOutbound(ctx, proxy, proxy.IPv6, cidr.IP.String()); err != nil {
		m.abortPending(opID, cidr)
		return Proxy{}, err
	}

	updated := proxy
	updated.IPv6, updated.Status, updated.LastError = cidr.IP.String(), "healthy", ""
	updated.LastRotatedAt = time.Now().UTC()
	m.stateMu.Lock()
	index := m.proxyIndexLocked(id)
	if index < 0 {
		m.stateMu.Unlock()
		return Proxy{}, ErrNotFound
	}
	m.state.Proxies[index] = updated
	delete(m.state.Pending, opID)
	saveErr := m.store.Save(m.state)
	if saveErr != nil {
		m.state.Proxies[index] = proxy
	}
	m.stateMu.Unlock()
	if saveErr != nil {
		_ = m.xray.ReplaceOutbound(context.Background(), updated, updated.IPv6, proxy.IPv6)
		m.abortPending(opID, cidr)
		return Proxy{}, saveErr
	}
	if oldCIDR, err := m.proxyCIDR(proxy.IPv6); err == nil {
		_ = m.network.DeleteAddress(ctx, m.info.Interface, oldCIDR)
	}
	return updated, nil
}

func (m *Manager) prepareRotation(ctx context.Context, proxy Proxy) (string, *net.IPNet, error) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	used, err := m.usedAddressesLocked(ctx)
	if err != nil {
		return "", nil, err
	}
	ip, err := randomIPv6(m.prefix, used)
	if err != nil {
		return "", nil, err
	}
	opID := randomID()
	m.state.Pending[opID] = PendingOperation{ID: opID, ProxyID: proxy.ID, Kind: "rotate", Candidate: ip.String(), CreatedAt: time.Now().UTC()}
	if err := m.store.Save(m.state); err != nil {
		delete(m.state.Pending, opID)
		return "", nil, err
	}
	return opID, &net.IPNet{IP: ip, Mask: m.prefix.Mask}, nil
}

func (m *Manager) Delete(ctx context.Context, id string) error {
	m.opMu.RLock()
	defer m.opMu.RUnlock()
	if _, loaded := m.busy.LoadOrStore(id, true); loaded {
		return ErrBusy
	}
	defer m.busy.Delete(id)
	proxy, err := m.findProxy(id)
	if err != nil {
		return err
	}
	if err := m.xray.DeleteProxy(ctx, proxy); err != nil {
		return err
	}
	m.stateMu.Lock()
	index := m.proxyIndexLocked(id)
	if index < 0 {
		m.stateMu.Unlock()
		return ErrNotFound
	}
	m.state.Proxies = append(m.state.Proxies[:index], m.state.Proxies[index+1:]...)
	saveErr := m.store.Save(m.state)
	if saveErr != nil {
		m.state.Proxies = append(m.state.Proxies, proxy)
	}
	m.stateMu.Unlock()
	if saveErr != nil {
		_ = m.xray.AddProxy(context.Background(), proxy, m.info)
		return saveErr
	}
	if cidr, err := m.proxyCIDR(proxy.IPv6); err == nil {
		return m.network.DeleteAddress(ctx, m.info.Interface, cidr)
	}
	return nil
}

func (m *Manager) RotateAll() *Job {
	return m.rotateAllFor(m.cfg.AdminUsername, m.snapshotProxies())
}

func (m *Manager) RotateAllForUser(username string) *Job {
	return m.rotateAllFor(username, m.ListForUser(username))
}

func (m *Manager) rotateAllFor(owner string, proxies []Proxy) *Job {
	job := &Job{ID: randomID(), Owner: owner, Status: "running", Total: len(proxies), CreatedAt: time.Now().UTC(), Items: map[string]*JobItem{}}
	for _, proxy := range proxies {
		job.Items[proxy.ID] = &JobItem{ProxyID: proxy.ID, Status: "pending", OldIPv6: proxy.IPv6}
	}
	m.jobsMu.Lock()
	m.jobs[job.ID] = job
	m.jobsMu.Unlock()
	go m.runRotateAll(job.ID, proxies)
	return cloneJob(job)
}

func (m *Manager) JobForUser(username, id string) (*Job, bool) {
	job, ok := m.Job(id)
	return job, ok && job.Owner == username
}

func (m *Manager) runRotateAll(jobID string, proxies []Proxy) {
	sem := make(chan struct{}, m.cfg.RotateAllWorkers)
	var wg sync.WaitGroup
	for _, proxy := range proxies {
		proxy := proxy
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			m.updateJobItem(jobID, proxy.ID, func(item *JobItem) { item.Status = "running" })
			ctx, cancel := context.WithTimeout(context.Background(), m.cfg.DADTimeout+m.cfg.IPCheckTimeout+20*time.Second)
			updated, err := m.Rotate(ctx, proxy.ID)
			cancel()
			m.jobsMu.Lock()
			job := m.jobs[jobID]
			item := job.Items[proxy.ID]
			job.Completed++
			if err != nil {
				item.Status, item.Error = "failed", err.Error()
				job.Failed++
			} else {
				item.Status, item.NewIPv6 = "succeeded", updated.IPv6
				job.Succeeded++
			}
			m.jobsMu.Unlock()
		}()
	}
	wg.Wait()
	m.jobsMu.Lock()
	job := m.jobs[jobID]
	job.Status, job.CompletedAt = "completed", time.Now().UTC()
	m.jobsMu.Unlock()
}

func (m *Manager) Job(id string) (*Job, bool) {
	m.jobsMu.RLock()
	defer m.jobsMu.RUnlock()
	job, ok := m.jobs[id]
	if !ok {
		return nil, false
	}
	return cloneJob(job), true
}

func (m *Manager) updateJobItem(jobID, proxyID string, update func(*JobItem)) {
	m.jobsMu.Lock()
	defer m.jobsMu.Unlock()
	if job := m.jobs[jobID]; job != nil && job.Items[proxyID] != nil {
		update(job.Items[proxyID])
	}
}

func cloneJob(job *Job) *Job {
	copyJob := *job
	copyJob.Items = map[string]*JobItem{}
	for id, item := range job.Items {
		itemCopy := *item
		copyJob.Items[id] = &itemCopy
	}
	return &copyJob
}

func (m *Manager) allocatePortLocked(requested *int) (int, error) {
	used := map[int]bool{}
	for _, proxy := range m.state.Proxies {
		used[proxy.Port] = true
	}
	if requested != nil {
		port := *requested
		if port < m.cfg.BasePort || port >= m.cfg.BasePort+m.cfg.MaxProxies {
			return 0, fmt.Errorf("port must be between %d and %d", m.cfg.BasePort, m.cfg.BasePort+m.cfg.MaxProxies-1)
		}
		if used[port] {
			return 0, fmt.Errorf("port %d is already assigned", port)
		}
		if !portAvailable(port) {
			return 0, fmt.Errorf("port %d is already in use", port)
		}
		return port, nil
	}
	for port := m.cfg.BasePort; port < m.cfg.BasePort+m.cfg.MaxProxies; port++ {
		if !used[port] && portAvailable(port) {
			return port, nil
		}
	}
	return 0, fmt.Errorf("no free port in configured range")
}

func portAvailable(port int) bool {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	_ = listener.Close()
	packet, err := net.ListenPacket("udp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	_ = packet.Close()
	return true
}

func (m *Manager) usedAddressesLocked(ctx context.Context) (map[string]bool, error) {
	used := map[string]bool{}
	addresses, err := m.network.ListAddresses(ctx, m.info.Interface)
	if err != nil {
		return nil, err
	}
	for _, ip := range addresses {
		used[ip.String()] = true
	}
	for _, proxy := range m.state.Proxies {
		used[proxy.IPv6] = true
	}
	for _, op := range m.state.Pending {
		if op.Candidate != "" {
			used[op.Candidate] = true
		}
	}
	return used, nil
}

func (m *Manager) findProxy(id string) (Proxy, error) {
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	for _, proxy := range m.state.Proxies {
		if proxy.ID == id {
			return proxy, nil
		}
	}
	return Proxy{}, ErrNotFound
}

func (m *Manager) proxyIndexLocked(id string) int {
	for i := range m.state.Proxies {
		if m.state.Proxies[i].ID == id {
			return i
		}
	}
	return -1
}

func (m *Manager) snapshotProxies() []Proxy {
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	return append([]Proxy(nil), m.state.Proxies...)
}

func (m *Manager) proxyCIDR(address string) (*net.IPNet, error) {
	ip := net.ParseIP(address)
	if ip == nil || ip.To4() != nil || !m.prefix.Contains(ip) {
		return nil, fmt.Errorf("IPv6 %q is outside managed prefix %s", address, m.prefix)
	}
	return &net.IPNet{IP: ip, Mask: m.prefix.Mask}, nil
}

func randomID() string {
	data := make([]byte, 8)
	if _, err := rand.Read(data); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(data)
}

// UpdatePrefix validates a new prefix, moves every active SOCKS5 outbound to it,
// and persists the selection. An empty value switches back to automatic discovery.
func (m *Manager) UpdatePrefix(ctx context.Context, requested string) (NetworkInfo, error) {
	return m.UpdateNetwork(ctx, m.info.Interface, requested)
}

// UpdateNetwork validates a new interface/prefix pair, moves every active
// outbound to it, and persists the selection. Empty values enable discovery.
func (m *Manager) UpdateNetwork(ctx context.Context, requestedInterface, requestedPrefix string) (NetworkInfo, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.addMu.Lock()
	defer m.addMu.Unlock()

	requestedInterface = strings.TrimSpace(requestedInterface)
	requestedPrefix = strings.TrimSpace(requestedPrefix)
	info, newPrefix, err := m.network.Discover(ctx, requestedInterface, requestedPrefix)
	if err != nil {
		return m.info, err
	}
	if m.info.Interface == info.Interface && m.prefix.String() == newPrefix.String() {
		m.stateMu.Lock()
		m.state.IPv6Interface = requestedInterface
		m.state.IPv6Prefix = requestedPrefix
		err = m.store.Save(m.state)
		m.stateMu.Unlock()
		if err == nil {
			m.info = info
			m.cfg.IPv6Interface = requestedInterface
			m.cfg.IPv6Prefix = requestedPrefix
		}
		return m.info, err
	}

	proxies := m.snapshotProxies()
	used := map[string]bool{}
	addresses, err := m.network.ListAddresses(ctx, info.Interface)
	if err != nil {
		return m.info, err
	}
	for _, ip := range addresses {
		used[ip.String()] = true
	}
	type replacement struct {
		old  Proxy
		new  Proxy
		cidr *net.IPNet
	}
	replacements := make([]replacement, 0, len(proxies))
	rollbackAddresses := func() {
		for _, item := range replacements {
			_ = m.network.DeleteAddress(context.Background(), info.Interface, item.cidr)
		}
	}
	for _, proxy := range proxies {
		ip, generateErr := randomIPv6(newPrefix, used)
		if generateErr != nil {
			rollbackAddresses()
			return m.info, generateErr
		}
		used[ip.String()] = true
		cidr := &net.IPNet{IP: ip, Mask: newPrefix.Mask}
		if err = m.activateAddressFor(ctx, info.Interface, cidr); err != nil {
			_ = m.network.DeleteAddress(context.Background(), info.Interface, cidr)
			rollbackAddresses()
			return m.info, err
		}
		updated := proxy
		updated.IPv6 = ip.String()
		updated.Status = "healthy"
		updated.LastError = ""
		updated.LastRotatedAt = time.Now().UTC()
		replacements = append(replacements, replacement{old: proxy, new: updated, cidr: cidr})
	}

	replaced := 0
	for i, item := range replacements {
		if err = m.xray.ReplaceOutbound(ctx, item.old, item.old.IPv6, item.new.IPv6); err != nil {
			for j := replaced - 1; j >= 0; j-- {
				prior := replacements[j]
				_ = m.xray.ReplaceOutbound(context.Background(), prior.new, prior.new.IPv6, prior.old.IPv6)
			}
			rollbackAddresses()
			return m.info, fmt.Errorf("switch port %d to prefix %s: %w", replacements[i].old.Port, newPrefix, err)
		}
		replaced++
	}

	m.stateMu.Lock()
	oldState := m.state
	m.state.Proxies = make([]Proxy, len(replacements))
	for i, item := range replacements {
		m.state.Proxies[i] = item.new
	}
	m.state.IPv6Interface = requestedInterface
	m.state.IPv6Prefix = requestedPrefix
	err = m.store.Save(m.state)
	if err != nil {
		m.state = oldState
	}
	m.stateMu.Unlock()
	if err != nil {
		for i := len(replacements) - 1; i >= 0; i-- {
			item := replacements[i]
			_ = m.xray.ReplaceOutbound(context.Background(), item.new, item.new.IPv6, item.old.IPv6)
		}
		rollbackAddresses()
		return m.info, err
	}

	oldPrefix := m.prefix
	oldInfo := m.info
	m.prefix, m.info = newPrefix, info
	m.cfg.IPv6Interface, m.cfg.IPv6Prefix = requestedInterface, requestedPrefix
	for _, item := range replacements {
		oldIP := net.ParseIP(item.old.IPv6)
		if oldIP != nil && oldPrefix.Contains(oldIP) {
			_ = m.network.DeleteAddress(ctx, oldInfo.Interface, &net.IPNet{IP: oldIP, Mask: oldPrefix.Mask})
		}
	}
	return m.info, nil
}

func (m *Manager) activateAddressFor(ctx context.Context, iface string, cidr *net.IPNet) error {
	if err := m.network.AddAddress(ctx, iface, cidr); err != nil {
		return err
	}
	if err := m.network.WaitReady(ctx, iface, cidr.IP, m.cfg.DADTimeout); err != nil {
		return err
	}
	return m.network.CheckEgress(ctx, cidr.IP, m.cfg.IPCheckURL, m.cfg.IPCheckTimeout)
}

func (m *Manager) normalizeProxyIDs() error {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	changed := false
	idMap := map[string]string{}
	seen := map[string]bool{}
	for i := range m.state.Proxies {
		protocol, err := normalizeProtocol(m.state.Proxies[i].Protocol)
		if err != nil {
			return fmt.Errorf("proxy port %d: %w", m.state.Proxies[i].Port, err)
		}
		if m.state.Proxies[i].Protocol != protocol {
			m.state.Proxies[i].Protocol = protocol
			changed = true
		}
		newID := strconv.Itoa(m.state.Proxies[i].Port)
		if seen[newID] {
			return fmt.Errorf("duplicate proxy port %s in state", newID)
		}
		seen[newID] = true
		oldID := m.state.Proxies[i].ID
		idMap[oldID] = newID
		if oldID != newID {
			m.state.Proxies[i].ID = newID
			changed = true
		}
	}
	for id, op := range m.state.Pending {
		if newID, ok := idMap[op.ProxyID]; ok && op.ProxyID != newID {
			op.ProxyID = newID
			m.state.Pending[id] = op
			changed = true
		}
	}
	if changed {
		return m.store.Save(m.state)
	}
	return nil
}

func normalizeProtocol(protocol string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "", "socks", "socks5", "sk5":
		return "socks5", nil
	case "hy2", "hysteria", "hysteria2":
		return "hy2", nil
	default:
		return "", fmt.Errorf("unsupported inbound protocol %q", protocol)
	}
}
