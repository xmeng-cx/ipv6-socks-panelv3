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

func TestChooseDiscoveredPrefixUsesDefaultRouteSource(t *testing.T) {
	prefixes := map[string]*net.IPNet{}
	for _, value := range []string{"2408:824e:cb01:1111::/64", "2408:824e:cb01:2222::/64"} {
		_, cidr, _ := net.ParseCIDR(value)
		prefixes[value] = cidr
	}
	chosen, err := chooseDiscoveredPrefix(prefixes, net.ParseIP("2408:824e:cb01:2222::1234"))
	if err != nil {
		t.Fatal(err)
	}
	if chosen.String() != "2408:824e:cb01:2222::/64" {
		t.Fatalf("unexpected prefix: %s", chosen)
	}
}

func TestChooseDiscoveredPrefixRejectsHostOnlyAddress(t *testing.T) {
	_, hostOnly, _ := net.ParseCIDR("2408:824e:cb01:2222::3/128")
	if _, err := chooseDiscoveredPrefix(map[string]*net.IPNet{hostOnly.String(): hostOnly}, nil); err == nil {
		t.Fatal("expected /128 prefix to be rejected")
	}
}
