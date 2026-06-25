package monitor

import (
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
	"https://ip4.seeip.org/geoip",
}

var ipv6Servers = []string{
	"https://ip6.seeip.org/geoip",
	"https://api-ipv6.ip.sb/geoip",
}

var (
	ipHTTPClient  = &http.Client{Timeout: 3 * time.Second}
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
		ipv4 = fetchGeoIP(ipv4Servers)
	}()
	go func() {
		defer wg.Done()
		ipv6 = fetchGeoIP(ipv6Servers)
	}()
	wg.Wait()

	country := ipv4.CountryCode
	if country == "" {
		country = ipv6.CountryCode
	}
	if ipv4.IP == "" && ipv6.IP == "" {
		return
	}

	cachedIPMu.Lock()
	cachedIP = fmt.Sprintf("IPs[IPv4:%s,IPv6:%s]", ipv4.IP, ipv6.IP)
	cachedCountry = strings.ToLower(country)
	cachedIPMu.Unlock()
}

func CachedIP() (string, string) {
	cachedIPMu.RLock()
	defer cachedIPMu.RUnlock()
	return cachedIP, cachedCountry
}

func fetchGeoIP(servers []string) geoIP {
	for i := 0; i < len(servers); i++ {
		var ip geoIP
		resp, err := ipHTTPClient.Get(servers[i])
		if err != nil {
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			continue
		}
		if err := json.Unmarshal(body, &ip); err != nil {
			continue
		}
		ip.IP = strings.TrimSpace(ip.IP)
		ip.CountryCode = strings.ToLower(strings.TrimSpace(ip.CountryCode))
		if net.ParseIP(ip.IP) == nil {
			continue
		}
		return ip
	}
	return geoIP{}
}
