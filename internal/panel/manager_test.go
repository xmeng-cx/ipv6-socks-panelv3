package panel

import (
	"context"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"
)

type fakeNetwork struct {
	mu        sync.Mutex
	prefix    *net.IPNet
	addresses map[string]bool
}

func newFakeNetwork() *fakeNetwork {
	_, prefix, _ := net.ParseCIDR("2408:824e:cb01:3363::/64")
	return &fakeNetwork{prefix: prefix, addresses: map[string]bool{}}
}
func (f *fakeNetwork) Discover(_ context.Context, interfaceOverride string, prefixOverride string) (NetworkInfo, *net.IPNet, error) {
	prefix := f.prefix
	if prefixOverride != "" {
		ip, parsed, err := net.ParseCIDR(prefixOverride)
		if err != nil || ip.To4() != nil {
			return NetworkInfo{}, nil, err
		}
		parsed.IP = ip.Mask(parsed.Mask)
		prefix = parsed
	}
	if interfaceOverride == "" {
		interfaceOverride = "eth0"
	}
	return NetworkInfo{Interface: interfaceOverride, Prefix: prefix.String(), IPv4: "192.168.10.168"}, prefix, nil
}
func (f *fakeNetwork) ListAddresses(context.Context, string) ([]net.IP, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var result []net.IP
	for value := range f.addresses {
		result = append(result, net.ParseIP(value))
	}
	return result, nil
}
func (f *fakeNetwork) AddAddress(_ context.Context, _ string, cidr *net.IPNet) error {
	f.mu.Lock()
	f.addresses[cidr.IP.String()] = true
	f.mu.Unlock()
	return nil
}
func (f *fakeNetwork) DeleteAddress(_ context.Context, _ string, cidr *net.IPNet) error {
	f.mu.Lock()
	delete(f.addresses, cidr.IP.String())
	f.mu.Unlock()
	return nil
}
func (f *fakeNetwork) WaitReady(context.Context, string, net.IP, time.Duration) error   { return nil }
func (f *fakeNetwork) CheckEgress(context.Context, net.IP, string, time.Duration) error { return nil }

type fakeXray struct {
	mu                       sync.Mutex
	running                  bool
	added, deleted, replaced int
}

func (f *fakeXray) Start(context.Context, []Proxy, NetworkInfo) error { f.running = true; return nil }
func (f *fakeXray) Stop(context.Context) error                        { f.running = false; return nil }
func (f *fakeXray) Running() bool                                     { return f.running }
func (f *fakeXray) AddProxy(context.Context, Proxy, NetworkInfo) error {
	f.mu.Lock()
	f.added++
	f.mu.Unlock()
	return nil
}
func (f *fakeXray) DeleteProxy(context.Context, Proxy) error {
	f.mu.Lock()
	f.deleted++
	f.mu.Unlock()
	return nil
}
func (f *fakeXray) ReplaceOutbound(context.Context, Proxy, string, string) error {
	f.mu.Lock()
	f.replaced++
	f.mu.Unlock()
	return nil
}

func testConfig(t *testing.T) Config {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return Config{DataDir: t.TempDir(), InitialProxies: 0, BasePort: port, MaxProxies: 3, SocksUsername: "u", SocksPassword: "secret1", AdminUsername: "xmeng", AdminPassword: "5201314", SocksUDP: true, IPCheckTimeout: time.Second, DADTimeout: time.Second, RotateAllWorkers: 2}
}

func TestManagerAddRotateDeleteLifecycle(t *testing.T) {
	cfg := testConfig(t)
	network := newFakeNetwork()
	xray := &fakeXray{}
	manager := NewManager(cfg, network, xray)
	ctx := context.Background()
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	proxy, err := manager.Add(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if proxy.ID != strconv.Itoa(proxy.Port) {
		t.Fatalf("proxy ID must equal its port: %#v", proxy)
	}
	oldIP := proxy.IPv6
	rotated, err := manager.Rotate(ctx, proxy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.IPv6 == oldIP {
		t.Fatal("rotation did not change IPv6")
	}
	if err := manager.Delete(ctx, proxy.ID); err != nil {
		t.Fatal(err)
	}
	if len(manager.List()) != 0 {
		t.Fatal("proxy was not deleted")
	}
	if len(network.addresses) != 0 {
		t.Fatalf("managed address leaked: %#v", network.addresses)
	}
	if xray.added != 1 || xray.replaced != 1 || xray.deleted != 1 {
		t.Fatalf("unexpected xray calls: %#v", xray)
	}
}

func TestManagerRejectsDuplicateManualPortAndLimit(t *testing.T) {
	cfg := testConfig(t)
	cfg.MaxProxies = 1
	manager := NewManager(cfg, newFakeNetwork(), &fakeXray{})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	port := cfg.BasePort
	if _, err := manager.Add(context.Background(), &port); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Add(context.Background(), nil); err == nil {
		t.Fatal("expected max proxy error")
	}
}

func TestManagerMigratesAddressesWhenPrefixChanges(t *testing.T) {
	cfg := testConfig(t)
	old := Proxy{ID: "stable-line", Port: cfg.BasePort, IPv6: "2408:824e:cb01:9999::1234", Status: "healthy", CreatedAt: time.Now().UTC()}
	store := NewStateStore(cfg.DataDir)
	if err := store.Save(State{Version: stateVersion, Proxies: []Proxy{old}, Pending: map[string]PendingOperation{}}); err != nil {
		t.Fatal(err)
	}
	network := newFakeNetwork()
	manager := NewManager(cfg, network, &fakeXray{})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	proxies := manager.List()
	if len(proxies) != 1 {
		t.Fatalf("expected one migrated proxy, got %d", len(proxies))
	}
	if proxies[0].ID != strconv.Itoa(old.Port) || proxies[0].Port != old.Port {
		t.Fatalf("migration did not normalize ID to port: %#v", proxies[0])
	}
	if !network.prefix.Contains(net.ParseIP(proxies[0].IPv6)) {
		t.Fatalf("migrated IPv6 %s is outside %s", proxies[0].IPv6, network.prefix)
	}
}

func TestManagerUpdatesAndPersistsPrefix(t *testing.T) {
	cfg := testConfig(t)
	network := newFakeNetwork()
	manager := NewManager(cfg, network, &fakeXray{})
	ctx := context.Background()
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	proxy, err := manager.Add(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	oldIP := proxy.IPv6
	info, err := manager.UpdatePrefix(ctx, "2408:824e:cb01:7777::/64")
	if err != nil {
		t.Fatal(err)
	}
	if info.Prefix != "2408:824e:cb01:7777::/64" {
		t.Fatalf("unexpected prefix: %s", info.Prefix)
	}
	updated := manager.List()[0]
	if updated.ID != strconv.Itoa(updated.Port) || updated.IPv6 == oldIP {
		t.Fatalf("proxy was not migrated correctly: %#v", updated)
	}
	_, expectedPrefix, _ := net.ParseCIDR(info.Prefix)
	if !expectedPrefix.Contains(net.ParseIP(updated.IPv6)) {
		t.Fatalf("updated IPv6 %s is outside %s", updated.IPv6, expectedPrefix)
	}
	state, err := NewStateStore(cfg.DataDir).Load()
	if err != nil {
		t.Fatal(err)
	}
	if state.IPv6Prefix != info.Prefix {
		t.Fatalf("prefix was not persisted: %#v", state)
	}
}

func TestManagerAddsHysteria2WithPortID(t *testing.T) {
	cfg := testConfig(t)
	manager := NewManager(cfg, newFakeNetwork(), &fakeXray{})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	proxy, err := manager.AddWithProtocol(context.Background(), nil, "hy2")
	if err != nil {
		t.Fatal(err)
	}
	if proxy.Protocol != "hy2" || proxy.ID != strconv.Itoa(proxy.Port) {
		t.Fatalf("unexpected Hysteria2 proxy: %#v", proxy)
	}
}

func TestManagerUpdatesAndPersistsInterface(t *testing.T) {
	cfg := testConfig(t)
	manager := NewManager(cfg, newFakeNetwork(), &fakeXray{})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	info, err := manager.UpdateNetwork(context.Background(), "eth1", "2408:824e:cb01:7777::/64")
	if err != nil {
		t.Fatal(err)
	}
	if info.Interface != "eth1" {
		t.Fatalf("unexpected interface: %s", info.Interface)
	}
	state, err := NewStateStore(cfg.DataDir).Load()
	if err != nil {
		t.Fatal(err)
	}
	if state.IPv6Interface != "eth1" {
		t.Fatalf("interface was not persisted: %#v", state)
	}
}
