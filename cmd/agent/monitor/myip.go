package monitor

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type geoIP struct {
	CountryCode string `json:"country_code,omitempty"`
	IP          string `json:"ip,omitempty"`
}

var ipv4Servers = []string{
	"https://api-ipv4.ip.sb/geoip",
	"https://api4.ipify.org?format=json",
	"https://ipv4.icanhazip.com",
	"https://4.ipw.cn",
}

var ipv6Servers = []string{
	"https://api-ipv6.ip.sb/geoip",
	"https://api6.ipify.org?format=json",
	"https://ipv6.icanhazip.com",
	"https://6.ipw.cn",
}

var (
	ipv4HTTPClient = &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp4", addr)
			},
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	ipv6HTTPClient = &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp6", addr)
			},
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	cachedIPMu    sync.RWMutex
	cachedIP      string
	cachedCountry string
)

func UpdateIP() {
	ticker := time.NewTicker(time.Minute * 10)
	defer ticker.Stop()
	for range ticker.C {
		RefreshIP()
	}
}

func RefreshIP() {
	var wg sync.WaitGroup
	var ipv4, ipv6 geoIP
	wg.Add(2)
	go func() {
		defer wg.Done()
		ipv4 = fetchIP(ipv4Servers, ipv4HTTPClient, false)
	}()
	go func() {
		defer wg.Done()
		ipv6 = fetchIP(ipv6Servers, ipv6HTTPClient, true)
		if ipv6.IP == "" {
			if localV6 := getLocalIPv6(); localV6 != "" {
				ipv6.IP = localV6
			}
		}
	}()
	wg.Wait()

	cachedIPMu.Lock()
	defer cachedIPMu.Unlock()

	prevIPv4, prevIPv6 := parseCachedIP(cachedIP)
	if ipv4.IP == "" {
		ipv4.IP = prevIPv4
	}
	if ipv6.IP == "" {
		ipv6.IP = prevIPv6
	}

	country := ipv4.CountryCode
	if country == "" {
		country = ipv6.CountryCode
	}
	if country == "" {
		country = cachedCountry
	}

	if ipv4.IP == "" && ipv6.IP == "" {
		return
	}

	cachedIP = fmt.Sprintf("IPs[IPv4:%s,IPv6:%s]", ipv4.IP, ipv6.IP)
	cachedCountry = strings.ToLower(country)
}

func CachedIP() (string, string) {
	cachedIPMu.RLock()
	defer cachedIPMu.RUnlock()
	return cachedIP, cachedCountry
}

func fetchIP(servers []string, client *http.Client, isIPv6 bool) geoIP {
	for _, u := range servers {
		req, err := http.NewRequest("GET", u, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "nodekeep-agent/1.0")
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			continue
		}

		var result geoIP
		if err := json.Unmarshal(body, &result); err == nil && result.IP != "" {
			result.IP = strings.TrimSpace(result.IP)
			result.CountryCode = strings.ToLower(strings.TrimSpace(result.CountryCode))
		} else {
			result.IP = strings.TrimSpace(string(body))
		}

		parsed := net.ParseIP(result.IP)
		if parsed == nil {
			continue
		}
		if isIPv6 && (parsed.To4() != nil || parsed.To16() == nil) {
			continue
		}
		if !isIPv6 && parsed.To4() == nil {
			continue
		}
		return result
	}
	return geoIP{}
}

func getLocalIPv6() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	allowlist := currentNICAllowlist()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if !shouldIncludeNIC(iface.Name, allowlist) {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if isPublicIPv6(ip) {
				return ip.String()
			}
		}
	}
	return ""
}

func isPublicIPv6(ip net.IP) bool {
	if ip == nil || ip.To4() != nil || ip.To16() == nil {
		return false
	}
	if !ip.IsGlobalUnicast() {
		return false
	}
	if ip.IsLinkLocalUnicast() {
		return false
	}
	if ip.IsPrivate() {
		return false
	}
	// 2001:db8::/32 documentation
	if len(ip) >= 4 && ip[0] == 0x20 && ip[1] == 0x01 && ip[2] == 0x0d && ip[3] == 0xb8 {
		return false
	}
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	return true
}

func parseCachedIP(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "IPs[") && strings.HasSuffix(raw, "]") {
		raw = strings.TrimSuffix(strings.TrimPrefix(raw, "IPs["), "]")
		var ipv4, ipv6 string
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			switch {
			case strings.HasPrefix(part, "IPv4:"):
				ipv4 = strings.TrimSpace(strings.TrimPrefix(part, "IPv4:"))
			case strings.HasPrefix(part, "IPv6:"):
				ipv6 = strings.TrimSpace(strings.TrimPrefix(part, "IPv6:"))
			}
		}
		return ipv4, ipv6
	}
	return "", ""
}
