package panel

import (
	"crypto/rand"
	"fmt"
	"net"
)

func randomIPv6(prefix *net.IPNet, used map[string]bool) (net.IP, error) {
	base := prefix.IP.To16()
	if base == nil {
		return nil, fmt.Errorf("prefix is not IPv6")
	}
	ones, bits := prefix.Mask.Size()
	if bits != 128 || ones >= 128 {
		return nil, fmt.Errorf("prefix must leave at least one host bit")
	}
	for attempt := 0; attempt < 128; attempt++ {
		candidate := make(net.IP, net.IPv6len)
		if _, err := rand.Read(candidate); err != nil {
			return nil, fmt.Errorf("generate random address: %w", err)
		}
		for i := range candidate {
			candidate[i] = (base[i] & prefix.Mask[i]) | (candidate[i] & ^prefix.Mask[i])
		}
		if candidate.Equal(base) || used[candidate.String()] {
			continue
		}
		return candidate, nil
	}
	return nil, fmt.Errorf("unable to generate an unused IPv6 address")
}

func isPublicIPv6(ip net.IP) bool {
	ip = ip.To16()
	if ip == nil || ip.To4() != nil || !ip.IsGlobalUnicast() {
		return false
	}
	// fc00::/7 is unique-local and must never be selected as the public egress prefix.
	return ip[0]&0xfe != 0xfc
}
