package panel

import (
	"fmt"
	"strconv"
	"strings"
)

func GenerateMihomoConfig(proxies []Proxy, server, obfsPassword string, directRules []string) string {
	var b strings.Builder
	b.WriteString("# IPv6 Socks Panel 自动生成的 Mihomo 配置\n")
	b.WriteString("mixed-port: 7890\nallow-lan: true\nbind-address: \"*\"\nmode: global\nlog-level: info\nipv6: true\nunified-delay: true\ntcp-concurrent: true\n")
	b.WriteString("external-controller: 0.0.0.0:9090\nsecret: \"xmeng\"\nexternal-ui: ui\nexternal-ui-url: \"https://github.com/Zephyruso/zashboard/releases/latest/download/dist.zip\"\n")
	b.WriteString("tun:\n  enable: true\n  stack: mixed\n  auto-route: true\n  auto-redirect: true\n  auto-detect-interface: true\n")
	b.WriteString("dns:\n  enable: true\n  listen: 0.0.0.0:1053\n  ipv6: true\n  enhanced-mode: fake-ip\n  fake-ip-range: 198.18.0.1/16\n  fake-ip-filter:\n    - \"*.lan\"\n    - \"*.local\"\n  default-nameserver:\n    - 223.5.5.5\n    - 119.29.29.29\n  nameserver:\n    - https://dns.alidns.com/dns-query\n    - https://doh.pub/dns-query\n")
	b.WriteString("proxies:")
	names := make([]string, 0, len(proxies))
	if len(proxies) == 0 {
		b.WriteString(" []\n")
	} else {
		b.WriteByte('\n')
	}
	for _, proxy := range proxies {
		name := strings.ToUpper(proxy.Protocol) + "-" + strconv.Itoa(proxy.Port)
		if proxy.Protocol == "socks5" {
			name = "SOCKS5-" + strconv.Itoa(proxy.Port)
		}
		names = append(names, name)
		fmt.Fprintf(&b, "  - name: %s\n    type: %s\n    server: %s\n    port: %d\n", yamlString(name), map[bool]string{true: "hysteria2", false: "socks5"}[proxy.Protocol == "hy2"], yamlString(server), proxy.Port)
		if proxy.Protocol == "hy2" {
			fmt.Fprintf(&b, "    password: %s\n    sni: %s\n    skip-cert-verify: true\n    obfs: salamander\n    obfs-password: %s\n    alpn:\n      - h3\n      - h2\n      - http/1.1\n    udp: true\n", yamlString(proxy.Password), yamlString(server), yamlString(obfsPassword))
		} else {
			fmt.Fprintf(&b, "    username: %s\n    password: %s\n    udp: true\n", yamlString(proxy.Username), yamlString(proxy.Password))
		}
	}
	b.WriteString("proxy-groups:\n  - name: \"全局代理\"\n    type: select\n    proxies:\n")
	if len(names) == 0 {
		b.WriteString("      - DIRECT\n")
	} else {
		for _, name := range names {
			fmt.Fprintf(&b, "      - %s\n", yamlString(name))
		}
		b.WriteString("      - \"自动选择\"\n")
	}
	b.WriteString("  - name: \"自动选择\"\n    type: url-test\n    url: https://www.gstatic.com/generate_204\n    interval: 300\n    tolerance: 80\n    proxies:\n")
	if len(names) == 0 {
		b.WriteString("      - DIRECT\n")
	} else {
		for _, name := range names {
			fmt.Fprintf(&b, "      - %s\n", yamlString(name))
		}
	}
	b.WriteString("rules:\n  - DOMAIN-SUFFIX,lan,DIRECT\n  - DOMAIN-SUFFIX,local,DIRECT\n  - IP-CIDR,10.0.0.0/8,DIRECT,no-resolve\n  - IP-CIDR,172.16.0.0/12,DIRECT,no-resolve\n  - IP-CIDR,192.168.0.0/16,DIRECT,no-resolve\n  - IP-CIDR6,fc00::/7,DIRECT,no-resolve\n")
	for _, rule := range directRules {
		fmt.Fprintf(&b, "  - %s\n", mihomoDirectRule(rule))
	}
	b.WriteString("  - MATCH,全局代理\n")
	return b.String()
}

func yamlString(value string) string { return strconv.Quote(value) }
