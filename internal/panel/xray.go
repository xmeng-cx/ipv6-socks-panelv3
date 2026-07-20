package panel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

type XrayController interface {
	Start(context.Context, []Proxy, NetworkInfo) error
	Stop(context.Context) error
	Running() bool
	AddProxy(context.Context, Proxy, NetworkInfo) error
	DeleteProxy(context.Context, Proxy) error
	ReplaceOutbound(context.Context, Proxy, string, string) error
}

type XrayProcess struct {
	cfg     Config
	mu      sync.Mutex
	cmd     *exec.Cmd
	running atomic.Bool
}

func NewXrayProcess(cfg Config) XrayController { return &XrayProcess{cfg: cfg} }

func (x *XrayProcess) Running() bool { return x.running.Load() }

func (x *XrayProcess) Start(ctx context.Context, proxies []Proxy, network NetworkInfo) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.running.Load() {
		return nil
	}
	configPath := filepath.Join(x.cfg.DataDir, "xray.json")
	if err := writeJSONFile(configPath, xrayConfig(proxies, network, x.cfg)); err != nil {
		return err
	}
	test := exec.CommandContext(ctx, x.cfg.XrayBinary, "run", "-test", "-c", configPath)
	if output, err := test.CombinedOutput(); err != nil {
		return fmt.Errorf("xray config test failed: %w: %s", err, output)
	}
	cmd := exec.Command(x.cfg.XrayBinary, "run", "-c", configPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start xray: %w", err)
	}
	x.cmd = cmd
	x.running.Store(true)
	go func(current *exec.Cmd) {
		_ = current.Wait()
		x.mu.Lock()
		if x.cmd == current {
			x.running.Store(false)
			x.cmd = nil
		}
		x.mu.Unlock()
	}(cmd)

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", x.cfg.XrayAPI, 300*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		if !x.running.Load() {
			return fmt.Errorf("xray exited before API became ready")
		}
		time.Sleep(150 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	return fmt.Errorf("xray API %s did not become ready", x.cfg.XrayAPI)
}

func (x *XrayProcess) Stop(ctx context.Context) error {
	x.mu.Lock()
	cmd := x.cmd
	x.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	_ = cmd.Process.Signal(os.Interrupt)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if !x.running.Load() {
			return nil
		}
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (x *XrayProcess) AddProxy(ctx context.Context, proxy Proxy, network NetworkInfo) error {
	if err := x.apiConfig(ctx, "ado", map[string]any{"outbounds": []any{xrayOutbound(proxy.ID, proxy.IPv6)}}); err != nil {
		return err
	}
	if err := x.apiConfigWithFlags(ctx, "adrules", []string{"--append"}, map[string]any{"routing": map[string]any{"rules": []any{xrayRule(proxy.ID)}}}); err != nil {
		_ = x.apiTags(context.Background(), "rmo", outboundTag(proxy.ID))
		return err
	}
	if err := x.apiConfig(ctx, "adi", map[string]any{"inbounds": []any{xrayInbound(proxy, network, x.cfg)}}); err != nil {
		_ = x.apiTags(context.Background(), "rmrules", ruleTag(proxy.ID))
		_ = x.apiTags(context.Background(), "rmo", outboundTag(proxy.ID))
		return err
	}
	return nil
}

func (x *XrayProcess) DeleteProxy(ctx context.Context, proxy Proxy) error {
	var errs []error
	if err := x.apiTags(ctx, "rmi", inboundTag(proxy.ID)); err != nil {
		errs = append(errs, err)
	}
	if err := x.apiTags(ctx, "rmrules", ruleTag(proxy.ID)); err != nil {
		errs = append(errs, err)
	}
	if err := x.apiTags(ctx, "rmo", outboundTag(proxy.ID)); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (x *XrayProcess) ReplaceOutbound(ctx context.Context, proxy Proxy, oldIP, newIP string) error {
	tag := outboundTag(proxy.ID)
	if err := x.apiTags(ctx, "rmo", tag); err != nil {
		return err
	}
	if err := x.apiConfig(ctx, "ado", map[string]any{"outbounds": []any{xrayOutbound(proxy.ID, newIP)}}); err != nil {
		rollbackErr := x.apiConfig(context.Background(), "ado", map[string]any{"outbounds": []any{xrayOutbound(proxy.ID, oldIP)}})
		if rollbackErr != nil {
			return fmt.Errorf("add new outbound: %v; rollback old outbound: %v", err, rollbackErr)
		}
		return err
	}
	return nil
}

func (x *XrayProcess) apiConfig(ctx context.Context, command string, config any) error {
	return x.apiConfigWithFlags(ctx, command, nil, config)
}

func (x *XrayProcess) apiConfigWithFlags(ctx context.Context, command string, flags []string, config any) error {
	runtimeDir := filepath.Join(x.cfg.DataDir, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(runtimeDir, command+"-*.json")
	if err != nil {
		return err
	}
	path := file.Name()
	defer os.Remove(path)
	encoder := json.NewEncoder(file)
	if err := encoder.Encode(config); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	args := []string{"api", command, "--server=" + x.cfg.XrayAPI, "--timeout=8"}
	args = append(args, flags...)
	args = append(args, path)
	return x.runAPI(ctx, args...)
}

func (x *XrayProcess) apiTags(ctx context.Context, command string, tags ...string) error {
	args := []string{"api", command, "--server=" + x.cfg.XrayAPI, "--timeout=8"}
	args = append(args, tags...)
	return x.runAPI(ctx, args...)
}

func (x *XrayProcess) runAPI(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, x.cfg.XrayBinary, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("xray %v failed: %w: %s", args[:2], err, output)
	}
	return nil
}

func writeJSONFile(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

func xrayConfig(proxies []Proxy, network NetworkInfo, cfg Config) map[string]any {
	inbounds := []any{map[string]any{
		"tag": "api-in", "listen": "127.0.0.1", "port": apiPort(cfg.XrayAPI),
		"protocol": "dokodemo-door", "settings": map[string]any{"address": "127.0.0.1"},
	}}
	outbounds := []any{}
	rules := []any{map[string]any{"type": "field", "ruleTag": "api-rule", "inboundTag": []string{"api-in"}, "outboundTag": "api"}}
	for _, proxy := range proxies {
		inbounds = append(inbounds, xrayInbound(proxy, network, cfg))
		outbounds = append(outbounds, xrayOutbound(proxy.ID, proxy.IPv6))
		rules = append(rules, xrayRule(proxy.ID))
	}
	return map[string]any{
		"log":       map[string]any{"loglevel": "warning"},
		"api":       map[string]any{"tag": "api", "services": []string{"HandlerService", "RoutingService"}},
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"routing":   map[string]any{"domainStrategy": "AsIs", "rules": rules},
	}
}

func xrayInbound(proxy Proxy, network NetworkInfo, cfg Config) map[string]any {
	settings := map[string]any{
		"auth": "password", "udp": cfg.SocksUDP,
		"users": []any{map[string]any{"user": cfg.SocksUsername, "pass": cfg.SocksPassword}},
	}
	udpIP := cfg.UDPAdvertiseIP
	if udpIP == "" {
		udpIP = network.IPv4
	}
	if cfg.SocksUDP && udpIP != "" {
		settings["ip"] = udpIP
	}
	return map[string]any{"tag": inboundTag(proxy.ID), "listen": cfg.SocksListen, "port": proxy.Port, "protocol": "socks", "settings": settings}
}

func xrayOutbound(id, ipv6 string) map[string]any {
	return map[string]any{
		"tag": outboundTag(id), "protocol": "freedom", "sendThrough": ipv6,
		"settings": map[string]any{"domainStrategy": "UseIPv6"},
	}
}

func xrayRule(id string) map[string]any {
	return map[string]any{"type": "field", "ruleTag": ruleTag(id), "inboundTag": []string{inboundTag(id)}, "outboundTag": outboundTag(id)}
}

func apiPort(address string) int {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return 10085
	}
	var value int
	_, _ = fmt.Sscanf(port, "%d", &value)
	if value == 0 {
		return 10085
	}
	return value
}

func inboundTag(id string) string  { return "socks-" + id }
func outboundTag(id string) string { return "egress-" + id }
func ruleTag(id string) string     { return "route-" + id }
