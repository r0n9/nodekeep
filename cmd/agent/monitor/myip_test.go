package monitor

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsPublicIPv6(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"240e:46d:8901:8e4:c8f:6105:1d3f:9222", true},
		{"2001:4860:4860::8888", true},
		{"::1", false},
		{"fe80::1", false},
		{"fd00::1", false},
		{"fc00::1", false},
		{"2001:db8::1", false},
		{"192.168.1.1", false},
		{"", false},
	}
	for _, tc := range cases {
		got := isPublicIPv6(net.ParseIP(tc.ip))
		if got != tc.want {
			t.Errorf("isPublicIPv6(%q) = %v, want %v", tc.ip, got, tc.want)
		}
	}
}

func TestParseCachedIP(t *testing.T) {
	v4, v6 := parseCachedIP("IPs[IPv4:1.2.3.4,IPv6:2001:db8::1]")
	if v4 != "1.2.3.4" || v6 != "2001:db8::1" {
		t.Fatalf("parseCachedIP = %q, %q; want 1.2.3.4, 2001:db8::1", v4, v6)
	}

	v4, v6 = parseCachedIP("IPs[IPv4:1.2.3.4,IPv6:]")
	if v4 != "1.2.3.4" || v6 != "" {
		t.Fatalf("parseCachedIP = %q, %q; want 1.2.3.4, empty", v4, v6)
	}

	v4, v6 = parseCachedIP("IPs[IPv4:,IPv6:2001:db8::1]")
	if v4 != "" || v6 != "2001:db8::1" {
		t.Fatalf("parseCachedIP = %q, %q; want empty, 2001:db8::1", v4, v6)
	}
}

func TestFetchIPParsesJSONAndPlainText(t *testing.T) {
	jsonSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ip":"203.0.113.50","country_code":"US"}`))
	}))
	defer jsonSrv.Close()

	res := fetchIP([]string{jsonSrv.URL}, http.DefaultClient, false)
	if res.IP != "203.0.113.50" || res.CountryCode != "us" {
		t.Fatalf("fetchIP json = %+v, want 203.0.113.50, us", res)
	}

	plainSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("203.0.113.60\n"))
	}))
	defer plainSrv.Close()

	res = fetchIP([]string{plainSrv.URL}, http.DefaultClient, false)
	if res.IP != "203.0.113.60" {
		t.Fatalf("fetchIP plain = %+v, want 203.0.113.60", res)
	}

	ipv6Srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ip":"2606:4700:4700::1111"}`))
	}))
	defer ipv6Srv.Close()

	res = fetchIP([]string{ipv6Srv.URL}, http.DefaultClient, true)
	if res.IP != "2606:4700:4700::1111" {
		t.Fatalf("fetchIP v6 = %+v, want 2606:4700:4700::1111", res)
	}

	// Make sure IPv6 fetch rejects IPv4 response
	res = fetchIP([]string{jsonSrv.URL}, http.DefaultClient, true)
	if res.IP != "" {
		t.Fatalf("fetchIP v6 accepted IPv4 response: %+v", res)
	}
}

func TestGetLocalIPv6(t *testing.T) {
	ip := getLocalIPv6()
	if ip != "" {
		parsed := net.ParseIP(ip)
		if parsed == nil || !isPublicIPv6(parsed) {
			t.Fatalf("getLocalIPv6 returned invalid or non-public IP: %q", ip)
		}
	}
}

func TestRefreshIPPreservesExistingCache(t *testing.T) {
	cachedIPMu.Lock()
	cachedIP = "IPs[IPv4:198.51.100.1,IPv6:2001:db8::99]"
	cachedCountry = "jp"
	cachedIPMu.Unlock()

	// Empty servers will fail to fetch anything new
	origV4 := ipv4Servers
	origV6 := ipv6Servers
	defer func() {
		ipv4Servers = origV4
		ipv6Servers = origV6
	}()
	ipv4Servers = []string{}
	ipv6Servers = []string{}

	RefreshIP()

	cached, country := CachedIP()
	v4, v6 := parseCachedIP(cached)
	if v4 != "198.51.100.1" {
		t.Errorf("v4 = %q, want preserved 198.51.100.1", v4)
	}
	// If local interface has IPv6, it might update v6; if not, it should preserve prevIPv6
	if v6 == "" {
		t.Errorf("v6 is empty, expected preserved or local IPv6")
	}
	if country != "jp" {
		t.Errorf("country = %q, want preserved jp", country)
	}
}
