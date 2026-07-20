//go:build linux

package panel

import (
	"net"
	"testing"
)

func TestPruneCoveredPrefixesDropsNestedHostRoute(t *testing.T) {
	prefixes := map[string]*net.IPNet{}
	for _, value := range []string{"2408:824e:cb01:3360::/64", "2408:824e:cb01:3360::3/128"} {
		ip, cidr, err := net.ParseCIDR(value)
		if err != nil {
			t.Fatal(err)
		}
		cidr.IP = ip
		prefixes[value] = cidr
	}
	pruneCoveredPrefixes(prefixes)
	if len(prefixes) != 1 || prefixes["2408:824e:cb01:3360::/64"] == nil {
		t.Fatalf("unexpected prefixes after pruning: %#v", prefixes)
	}
}

func TestPruneCoveredPrefixesKeepsDistinctPublicNetworks(t *testing.T) {
	prefixes := map[string]*net.IPNet{}
	for _, value := range []string{"2408:824e:cb01:3360::/64", "2408:824e:cb01:3361::/64"} {
		_, cidr, _ := net.ParseCIDR(value)
		prefixes[value] = cidr
	}
	pruneCoveredPrefixes(prefixes)
	if len(prefixes) != 2 {
		t.Fatalf("distinct networks must remain ambiguous: %#v", prefixes)
	}
}
