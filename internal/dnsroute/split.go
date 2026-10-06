package dnsroute

import (
	"net"
	"strings"
)

// splitDomainsAndSubnets separates a raw user-provided list into DNS-style
// domains (including geosite: tags) and network-style subnets (CIDR and
// geoip: tags). Order is preserved within each output slice.
//
// Classification:
//   - "geosite:TAG"       → domains
//   - "geoip:TAG"         → subnets
//   - valid CIDR          → subnets (IPv4 and IPv6)
//   - everything else     → domains (incl. bare IPs without /mask)
func splitDomainsAndSubnets(input []string) (domains, subnets []string) {
	for _, raw := range input {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		if strings.HasPrefix(s, "geoip:") {
			subnets = append(subnets, s)
			continue
		}
		if strings.HasPrefix(s, "geosite:") {
			domains = append(domains, s)
			continue
		}
		if _, _, err := net.ParseCIDR(s); err == nil {
			subnets = append(subnets, s)
			continue
		}
		domains = append(domains, s)
	}
	return domains, subnets
}

// isIPv6Entry reports whether a list entry is an IPv6 network or address:
// a CIDR such as 2001:db8::/32, or a bare address such as 2001:db8::1 —
// bare IPs land among the domains (splitDomainsAndSubnets). IPv4-mapped
// addresses count as IPv4, as net.IP.To4 reads them; tags and domain names
// are never IPv6.
func isIPv6Entry(s string) bool {
	s = strings.TrimSpace(s)
	if ip, _, err := net.ParseCIDR(s); err == nil {
		return ip.To4() == nil
	}
	ip := net.ParseIP(s)
	return ip != nil && ip.To4() == nil
}

// withoutIPv6 returns entries minus the IPv6 ones, keeping their order.
func withoutIPv6(entries []string) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if !isIPv6Entry(e) {
			out = append(out, e)
		}
	}
	return out
}
