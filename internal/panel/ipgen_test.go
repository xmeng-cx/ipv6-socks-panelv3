package panel

import (
	"net"
	"testing"
)

func TestRandomIPv6StaysInsidePrefixAndAvoidsUsed(t *testing.T) {
	_, prefix, err := net.ParseCIDR("2408:824e:cb01:3363::/64")
	if err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{}
	for i := 0; i < 64; i++ {
		ip, err := randomIPv6(prefix, used)
		if err != nil {
			t.Fatal(err)
		}
		if !prefix.Contains(ip) {
			t.Fatalf("%s is outside %s", ip, prefix)
		}
		if used[ip.String()] {
			t.Fatalf("generated duplicate %s", ip)
		}
		used[ip.String()] = true
	}
}

func TestPublicIPv6ExcludesULA(t *testing.T) {
	if isPublicIPv6(net.ParseIP("fdbf:2718:acc5::1")) {
		t.Fatal("ULA must not be public")
	}
	if !isPublicIPv6(net.ParseIP("2408:824e:cb01:3363::1")) {
		t.Fatal("GUA should be public")
	}
	if isPublicIPv6(net.ParseIP("fe80::1")) {
		t.Fatal("link local must not be public")
	}
}
