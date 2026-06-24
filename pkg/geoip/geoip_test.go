package geoip

import "testing"

func TestExtractIPsFromAgentFormat(t *testing.T) {
	ipv4, ipv6 := ExtractIPs("IPs[IPv4:203.0.113.10,IPv6:2001:db8::1]")
	if ipv4 != "203.0.113.10" {
		t.Fatalf("ipv4 = %q, want 203.0.113.10", ipv4)
	}
	if ipv6 != "2001:db8::1" {
		t.Fatalf("ipv6 = %q, want 2001:db8::1", ipv6)
	}
}

func TestExtractIPsFromPlainIP(t *testing.T) {
	ipv4, ipv6 := ExtractIPs("203.0.113.10")
	if ipv4 != "203.0.113.10" || ipv6 != "" {
		t.Fatalf("ExtractIPs plain IPv4 = %q, %q", ipv4, ipv6)
	}
}

func TestExtractIPsRejectsInvalidInput(t *testing.T) {
	ipv4, ipv6 := ExtractIPs("not-an-ip")
	if ipv4 != "" || ipv6 != "" {
		t.Fatalf("ExtractIPs invalid = %q, %q", ipv4, ipv6)
	}
}
