package panel

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestXrayConfigUsesStrictIPv6AndSharedCredentials(t *testing.T) {
	cfg := Config{XrayAPI: "127.0.0.1:10085", SocksListen: "0.0.0.0", SocksUsername: "shared", SocksPassword: "secret", SocksUDP: true}
	proxy := Proxy{ID: "abc", Port: 20000, IPv6: "2408:824e:cb01:3363::1234"}
	value := xrayConfig([]Proxy{proxy}, NetworkInfo{IPv4: "192.168.10.168"}, cfg)
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, expected := range []string{"UseIPv6", proxy.IPv6, "shared", "secret", "route-abc", "HandlerService", "RoutingService", "192.168.10.168"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("config missing %q: %s", expected, text)
		}
	}
}

func TestXrayConfigBuildsHysteria2Inbound(t *testing.T) {
	// HY2 must remain reachable on every host interface even if SOCKS5 is restricted.
	cfg := Config{DataDir: "/data", XrayAPI: "127.0.0.1:10085", SocksListen: "127.0.0.1", SocksUsername: "xmeng", SocksPassword: "5201314", HY2ObfsPassword: "obfs-secret"}
	proxy := Proxy{ID: "20000", Port: 20000, Protocol: "hy2", IPv6: "2408:824e:cb01:3363::1234"}
	data, err := json.Marshal(xrayConfig([]Proxy{proxy}, NetworkInfo{IPv4: "192.168.1.14"}, cfg))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, expected := range []string{"hysteria", `"listen":"0.0.0.0"`, "hy2.crt", "hy2.key", "5201314", "h3", `"minVersion":"1.3"`, "finalmask", "salamander", "obfs-secret"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("Hysteria2 config missing %q: %s", expected, text)
		}
	}
}
