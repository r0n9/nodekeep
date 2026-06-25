package geoip

import (
	_ "embed"
	"errors"
	"net"
	"strings"
	"sync"

	maxminddb "github.com/oschwald/maxminddb-golang"
)

//go:embed geoip.db
var db []byte

var (
	dbOnce = sync.OnceValues(func() (*maxminddb.Reader, error) {
		return maxminddb.FromBytes(db)
	})
)

var ErrIPNotFound = errors.New("geoip: ip not found")

type IPInfo struct {
	Country       string `maxminddb:"country"`
	CountryName   string `maxminddb:"country_name"`
	Continent     string `maxminddb:"continent"`
	ContinentName string `maxminddb:"continent_name"`
}

func ExtractIPs(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
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

	if strings.Contains(raw, "/") {
		var ipv4, ipv6 string
		parts := strings.SplitN(raw, "/", 2)
		if len(parts) > 0 {
			ipv4 = normalizeIP(parts[0])
		}
		if len(parts) > 1 {
			ipv6 = normalizeIP(parts[1])
		}
		if ipv4 != "" || ipv6 != "" {
			return ipv4, ipv6
		}
	}

	ip := normalizeIP(raw)
	if ip == "" {
		return "", ""
	}
	if strings.Contains(ip, ":") {
		return "", ip
	}
	return ip, ""
}

func LookupCountryCode(rawIP string) (string, error) {
	ipv4, ipv6 := ExtractIPs(rawIP)
	ipStr := ipv4
	if ipStr == "" {
		ipStr = ipv6
	}
	if ipStr == "" {
		return "", ErrIPNotFound
	}

	ip := net.ParseIP(ipStr)
	if ip == nil {
		return "", ErrIPNotFound
	}

	reader, err := dbOnce()
	if err != nil {
		return "", err
	}

	var record IPInfo
	if err := reader.Lookup(ip, &record); err != nil {
		return "", err
	}

	if record.Country != "" {
		return strings.ToLower(record.Country), nil
	}
	if record.Continent != "" {
		return strings.ToLower(record.Continent), nil
	}
	return "", ErrIPNotFound
}

func normalizeIP(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if ip := net.ParseIP(raw); ip != nil {
		return raw
	}
	return ""
}
