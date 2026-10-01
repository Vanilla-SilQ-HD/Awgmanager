package diagnostics

import "testing"

const v4RouteTable = "default dev ppp0 scope link\n203.0.113.7 via 192.168.1.1 dev eth3\n"

// F586: маршрут до IPv6 endpoint лежит в таблице IPv6, и искать его надо
// там — в `ip route show` его нет, и проверка всегда писала fail.
func TestFindEndpointRoute_IPv6EndpointReadsIPv6Table(t *testing.T) {
	ip := extractEndpointIP("peer: abc=\n  endpoint: [2a01:4f8:c0c:1234::1]:51820\n")
	if ip != "2a01:4f8:c0c:1234::1" {
		t.Fatalf("endpoint ip = %q", ip)
	}
	v6 := "2a01:4f8:c0c:1234::1 via fe80::1 dev eth3 metric 1024 pref medium\nfe80::/64 dev br0 proto kernel metric 256 pref medium\n"
	got := findEndpointRoute(v4RouteTable, ip, func() (string, error) { return v6, nil })
	if got != "2a01:4f8:c0c:1234::1 via fe80::1 dev eth3 metric 1024 pref medium" {
		t.Fatalf("IPv6 endpoint route = %q", got)
	}
}

func TestFindEndpointRoute_IPv4EndpointDoesNotReadIPv6Table(t *testing.T) {
	got := findEndpointRoute(v4RouteTable, "203.0.113.7", func() (string, error) {
		t.Fatal("IPv6 table read for an IPv4 endpoint")
		return "", nil
	})
	if got != "203.0.113.7 via 192.168.1.1 dev eth3" {
		t.Fatalf("IPv4 endpoint route = %q", got)
	}
}
