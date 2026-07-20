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
