package panel

import (
	"fmt"
	"net"
	"strings"
)

func (m *Manager) DirectRules() []string {
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	return append([]string(nil), m.state.DirectRules...)
}

func (m *Manager) UpdateDirectRules(values []string) ([]string, error) {
	rules, err := normalizeDirectRules(values)
	if err != nil {
		return nil, err
	}
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	old := m.state.DirectRules
	m.state.DirectRules = rules
	if err := m.store.Save(m.state); err != nil {
		m.state.DirectRules = old
		return nil, err
	}
	return append([]string(nil), rules...), nil
}

func normalizeDirectRules(values []string) ([]string, error) {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if strings.Contains(value, ",") {
			return nil, fmt.Errorf("直连项 %q 不能包含逗号", value)
		}
		if ip := net.ParseIP(value); ip == nil {
			if _, _, err := net.ParseCIDR(value); err != nil && !validDomainRule(value) {
				return nil, fmt.Errorf("%q 不是有效的域名、IP 或 CIDR", value)
			}
		}
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result, nil
}

func validDomainRule(value string) bool {
	value = strings.TrimPrefix(strings.TrimPrefix(value, "*."), ".")
	if len(value) < 1 || len(value) > 253 || !strings.Contains(value, ".") {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func mihomoDirectRule(value string) string {
	if ip := net.ParseIP(value); ip != nil {
		if ip.To4() != nil {
			return "IP-CIDR," + ip.String() + "/32,DIRECT,no-resolve"
		}
		return "IP-CIDR6," + ip.String() + "/128,DIRECT,no-resolve"
	}
	if ip, network, err := net.ParseCIDR(value); err == nil {
		network.IP = ip.Mask(network.Mask)
		kind := "IP-CIDR"
		if ip.To4() == nil {
			kind = "IP-CIDR6"
		}
		return kind + "," + network.String() + ",DIRECT,no-resolve"
	}
	value = strings.TrimPrefix(strings.TrimPrefix(value, "*."), ".")
	return "DOMAIN-SUFFIX," + value + ",DIRECT"
}
