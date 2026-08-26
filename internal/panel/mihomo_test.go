package panel

import (
	"strings"
	"testing"
)

func TestGenerateMihomoConfig(t *testing.T) {
	config := GenerateMihomoConfig([]Proxy{
		{Port: 20001, Protocol: "hy2", Username: "alice", Password: "alice123"},
		{Port: 20002, Protocol: "socks5", Username: "alice", Password: "alice123"},
	}, "panel.example.com", "5201314", []string{"example.cn", "1.1.1.1", "2001:db8::/32"})
	wants := []string{
		"mode: global",
		`type: hysteria2`,
		`name: "HY2-20001"`,
		`obfs-password: "5201314"`,
		`type: socks5`,
		`name: "SOCKS5-20002"`,
		`server: "panel.example.com"`,
		`- MATCH,全局代理`,
		`DOMAIN-SUFFIX,example.cn,DIRECT`,
		`IP-CIDR,1.1.1.1/32,DIRECT,no-resolve`,
		`IP-CIDR6,2001:db8::/32,DIRECT,no-resolve`,
	}
	for _, want := range wants {
		if !strings.Contains(config, want) {
			t.Fatalf("config missing %q:\n%s", want, config)
		}
	}
}
