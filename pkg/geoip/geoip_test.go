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

func TestExtractIPsFromPlainIPv6(t *testing.T) {
	ipv4, ipv6 := ExtractIPs("2001:db8::1")
	if ipv4 != "" || ipv6 != "2001:db8::1" {
		t.Fatalf("ExtractIPs plain IPv6 = %q, %q", ipv4, ipv6)
	}
}

func TestExtractIPsRejectsInvalidInput(t *testing.T) {
	ipv4, ipv6 := ExtractIPs("not-an-ip")
	if ipv4 != "" || ipv6 != "" {
		t.Fatalf("ExtractIPs invalid = %q, %q", ipv4, ipv6)
	}
}

func TestShortIPv6(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"   ", ""},
		{"2001:db8::1", "2001:db8::1"},
		{"::1", "::1"},
		{"::ffff:192.0.2.128", "::ffff:192.0.2.128"},
		{"240e:390:860:1100:215:5dff:fe22:3a1b", "240e:390:…:fe22:3a1b"},
		{"2600:1f18:43e7:8200::1", "2600:1f18:…::1"},
		{"2400:cb00:2048:1::c629:d7a2", "2400:cb00:…:c629:d7a2"},
		{"2409:8a15:3221:a1b2:5c8f:e932:1a2b:3c4d", "2409:8a15:…:1a2b:3c4d"},
	}

	for _, tc := range tests {
		got := ShortIPv6(tc.input)
		if got != tc.want {
			t.Errorf("ShortIPv6(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
