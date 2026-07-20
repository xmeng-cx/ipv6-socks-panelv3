//go:build linux

package panel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

type LinuxNetwork struct{}

func NewNetworkManager() NetworkManager { return &LinuxNetwork{} }

func (n *LinuxNetwork) Discover(_ context.Context, ifaceOverride, prefixOverride string) (NetworkInfo, *net.IPNet, error) {
	var link netlink.Link
	var err error
	if ifaceOverride != "" {
		link, err = netlink.LinkByName(ifaceOverride)
		if err != nil {
			return NetworkInfo{}, nil, fmt.Errorf("find interface %q: %w", ifaceOverride, err)
		}
	} else {
		routes, routeErr := netlink.RouteList(nil, netlink.FAMILY_V6)
		if routeErr != nil {
			return NetworkInfo{}, nil, fmt.Errorf("list IPv6 routes: %w", routeErr)
		}
		bestMetric := int(^uint(0) >> 1)
		for _, route := range routes {
			isDefault := route.Dst == nil
			if route.Dst != nil {
				ones, bits := route.Dst.Mask.Size()
				isDefault = bits == 128 && ones == 0
			}
			if isDefault && route.LinkIndex > 0 && route.Priority < bestMetric {
				candidate, findErr := netlink.LinkByIndex(route.LinkIndex)
				if findErr == nil {
					link, bestMetric = candidate, route.Priority
				}
			}
		}
		if link == nil {
			return NetworkInfo{}, nil, fmt.Errorf("no IPv6 default route found; set IPV6_INTERFACE and IPV6_PREFIX")
		}
	}

	var prefix *net.IPNet
	if prefixOverride != "" {
		ip, cidr, parseErr := net.ParseCIDR(prefixOverride)
		if parseErr != nil || ip.To4() != nil {
			return NetworkInfo{}, nil, fmt.Errorf("invalid IPv6 prefix %q", prefixOverride)
		}
		cidr.IP = ip.Mask(cidr.Mask)
		prefix = cidr
	} else {
		addrs, addrErr := netlink.AddrList(link, netlink.FAMILY_V6)
		if addrErr != nil {
			return NetworkInfo{}, nil, fmt.Errorf("list IPv6 addresses on %s: %w", link.Attrs().Name, addrErr)
		}
		prefixes := map[string]*net.IPNet{}
		for _, addr := range addrs {
			if addr.IPNet == nil || !isPublicIPv6(addr.IP) {
				continue
			}
			networkIP := addr.IP.Mask(addr.Mask)
			cidr := &net.IPNet{IP: networkIP, Mask: addr.Mask}
			prefixes[cidr.String()] = cidr
		}
		pruneCoveredPrefixes(prefixes)
		if len(prefixes) == 0 {
			return NetworkInfo{}, nil, fmt.Errorf("no public IPv6 prefix on %s; set IPV6_PREFIX", link.Attrs().Name)
		}
		if len(prefixes) > 1 {
			return NetworkInfo{}, nil, fmt.Errorf("multiple public IPv6 prefixes on %s; set IPV6_PREFIX explicitly", link.Attrs().Name)
		}
		for _, cidr := range prefixes {
			prefix = cidr
		}
	}

	ipv4 := ""
	v4addrs, _ := netlink.AddrList(link, netlink.FAMILY_V4)
	for _, addr := range v4addrs {
		if addr.IP.IsGlobalUnicast() {
			ipv4 = addr.IP.String()
			break
		}
	}
	return NetworkInfo{Interface: link.Attrs().Name, Prefix: prefix.String(), IPv4: ipv4}, prefix, nil
}

// pruneCoveredPrefixes removes host routes such as DHCPv6 /128 addresses when
// the same address is already covered by an on-link /64 prefix.
func pruneCoveredPrefixes(prefixes map[string]*net.IPNet) {
	for key, candidate := range prefixes {
		candidateOnes, candidateBits := candidate.Mask.Size()
		for otherKey, other := range prefixes {
			if key == otherKey {
				continue
			}
			otherOnes, otherBits := other.Mask.Size()
			if candidateBits == otherBits && otherOnes < candidateOnes && other.Contains(candidate.IP) {
				delete(prefixes, key)
				break
			}
		}
	}
}

func (n *LinuxNetwork) ListAddresses(_ context.Context, iface string) ([]net.IP, error) {
	link, err := netlink.LinkByName(iface)
	if err != nil {
		return nil, err
	}
	addrs, err := netlink.AddrList(link, netlink.FAMILY_V6)
	if err != nil {
		return nil, err
	}
	result := make([]net.IP, 0, len(addrs))
	for _, addr := range addrs {
		result = append(result, append(net.IP(nil), addr.IP...))
	}
	return result, nil
}

func (n *LinuxNetwork) AddAddress(_ context.Context, iface string, cidr *net.IPNet) error {
	link, err := netlink.LinkByName(iface)
	if err != nil {
		return err
	}
	err = netlink.AddrAdd(link, &netlink.Addr{IPNet: cidr, Flags: unix.IFA_F_NOPREFIXROUTE})
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "file exists") {
		return fmt.Errorf("add %s to %s: %w", cidr, iface, err)
	}
	return nil
}

func (n *LinuxNetwork) DeleteAddress(_ context.Context, iface string, cidr *net.IPNet) error {
	link, err := netlink.LinkByName(iface)
	if err != nil {
		return err
	}
	err = netlink.AddrDel(link, &netlink.Addr{IPNet: cidr})
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "cannot assign requested address") {
		return fmt.Errorf("delete %s from %s: %w", cidr, iface, err)
	}
	return nil
}

func (n *LinuxNetwork) WaitReady(ctx context.Context, iface string, ip net.IP, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	for {
		link, err := netlink.LinkByName(iface)
		if err != nil {
			return err
		}
		addrs, err := netlink.AddrList(link, netlink.FAMILY_V6)
		if err != nil {
			return err
		}
		for _, addr := range addrs {
			if !addr.IP.Equal(ip) {
				continue
			}
			if addr.Flags&unix.IFA_F_DADFAILED != 0 {
				return fmt.Errorf("IPv6 duplicate address detection failed for %s", ip)
			}
			if addr.Flags&unix.IFA_F_TENTATIVE == 0 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("timed out waiting for IPv6 %s to become ready", ip)
		case <-ticker.C:
		}
	}
}

func (n *LinuxNetwork) CheckEgress(ctx context.Context, ip net.IP, target string, timeout time.Duration) error {
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		if err := checkEgressOnce(ctx, ip, target, timeout); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 300 * time.Millisecond):
		}
	}
	return lastErr
}

func checkEgressOnce(ctx context.Context, ip net.IP, target string, timeout time.Duration) error {
	dialer := &net.Dialer{Timeout: timeout, LocalAddr: &net.TCPAddr{IP: ip}}
	transport := &http.Transport{Proxy: nil, DialContext: dialer.DialContext, ForceAttemptHTTP2: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: timeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("IPv6 egress check failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("IPv6 egress check returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return err
	}
	observed := strings.TrimSpace(string(body))
	var payload struct {
		IP string `json:"ip"`
	}
	if json.Unmarshal(body, &payload) == nil && payload.IP != "" {
		observed = payload.IP
	}
	observedIP := net.ParseIP(strings.TrimSpace(observed))
	if observedIP == nil || !observedIP.Equal(ip) {
		return fmt.Errorf("egress mismatch: expected %s, observed %q", ip, observed)
	}
	return nil
}
