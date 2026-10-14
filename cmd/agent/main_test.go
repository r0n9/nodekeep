package main

import (
	"errors"
	"testing"
)

func TestHTTPTaskOutcome(t *testing.T) {
	reqErr := errors.New("dial tcp: connection refused")
	statusErr := errors.New("\n应用错误：500 Internal Server Error")

	tests := []struct {
		name           string
		reqErr         error
		statusErr      error
		certData       string
		wantData       string
		wantSuccessful bool
	}{
		{
			name:           "传输层失败时不查证书，上报错误",
			reqErr:         reqErr,
			certData:       "",
			wantData:       reqErr.Error(),
			wantSuccessful: false,
		},
		{
			name:           "站点5xx但证书正常：仍上报证书并标记不可用",
			statusErr:      statusErr,
			certData:       "Let's Encrypt|2026-11-01 12:00:00 +0000 UTC",
			wantData:       "Let's Encrypt|2026-11-01 12:00:00 +0000 UTC",
			wantSuccessful: false,
		},
		{
			name:           "站点5xx且证书取失败：上报证书错误",
			statusErr:      statusErr,
			certData:       "SSL证书错误：timeout",
			wantData:       "SSL证书错误：timeout",
			wantSuccessful: false,
		},
		{
			name:           "站点正常且有证书",
			certData:       "Let's Encrypt|2026-11-01 12:00:00 +0000 UTC",
			wantData:       "Let's Encrypt|2026-11-01 12:00:00 +0000 UTC",
			wantSuccessful: true,
		},
		{
			name:           "非HTTPS且站点5xx：回落到HTTP错误",
			statusErr:      statusErr,
			certData:       "",
			wantData:       statusErr.Error(),
			wantSuccessful: false,
		},
		{
			name:           "非HTTPS且站点正常",
			certData:       "",
			wantData:       "",
			wantSuccessful: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, successful := httpTaskOutcome(tt.reqErr, tt.statusErr, tt.certData)
			if data != tt.wantData {
				t.Errorf("data = %q, want %q", data, tt.wantData)
			}
			if successful != tt.wantSuccessful {
				t.Errorf("successful = %v, want %v", successful, tt.wantSuccessful)
			}
		})
	}
}

func TestAgentDefaultVersion(t *testing.T) {
	if version != "develop" {
		t.Fatalf("version = %q, want develop", version)
	}
}

func TestShouldSelfUpdate(t *testing.T) {
	oldVersion := version
	t.Cleanup(func() {
		version = oldVersion
	})

	tests := []struct {
		name    string
		version string
		want    bool
	}{
		{name: "develop", version: "develop", want: false},
		{name: "empty", version: "", want: false},
		{name: "tag version", version: "v1.2.3", want: true},
		{name: "plain semver", version: "1.2.3", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version = tt.version
			if got := shouldSelfUpdate(); got != tt.want {
				t.Fatalf("shouldSelfUpdate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseCertTarget(t *testing.T) {
	tests := []struct {
		name         string
		target       string
		wantHostPort string
		wantHTTPS    bool
		wantErr      bool
	}{
		{
			name:         "https with custom port and trailing slash",
			target:       "https://oec.880508.xyz:15668/",
			wantHostPort: "oec.880508.xyz:15668",
			wantHTTPS:    true,
			wantErr:      false,
		},
		{
			name:         "https with custom port no slash",
			target:       "https://oec.880508.xyz:15668",
			wantHostPort: "oec.880508.xyz:15668",
			wantHTTPS:    true,
			wantErr:      false,
		},
		{
			name:         "https with custom port and path query fragment",
			target:       "https://oec.880508.xyz:15668/api/v1/health?token=abc#live",
			wantHostPort: "oec.880508.xyz:15668",
			wantHTTPS:    true,
			wantErr:      false,
		},
		{
			name:         "https default port with trailing slash",
			target:       "https://example.com/",
			wantHostPort: "example.com:443",
			wantHTTPS:    true,
			wantErr:      false,
		},
		{
			name:         "https default port no slash",
			target:       "https://example.com",
			wantHostPort: "example.com:443",
			wantHTTPS:    true,
			wantErr:      false,
		},
		{
			name:         "https explicit 443 with slash",
			target:       "https://example.com:443/",
			wantHostPort: "example.com:443",
			wantHTTPS:    true,
			wantErr:      false,
		},
		{
			name:         "https default port with path",
			target:       "https://example.com/login",
			wantHostPort: "example.com:443",
			wantHTTPS:    true,
			wantErr:      false,
		},
		{
			name:         "uppercase HTTPS scheme",
			target:       "HTTPS://OEC.880508.XYZ:15668/",
			wantHostPort: "oec.880508.xyz:15668",
			wantHTTPS:    true,
			wantErr:      false,
		},
		{
			name:         "leading and trailing spaces",
			target:       "   https://oec.880508.xyz:15668/   ",
			wantHostPort: "oec.880508.xyz:15668",
			wantHTTPS:    true,
			wantErr:      false,
		},
		{
			name:         "url with userinfo",
			target:       "https://user:password@oec.880508.xyz:15668/",
			wantHostPort: "oec.880508.xyz:15668",
			wantHTTPS:    true,
			wantErr:      false,
		},
		{
			name:         "http scheme without port",
			target:       "http://example.com/",
			wantHostPort: "",
			wantHTTPS:    false,
			wantErr:      false,
		},
		{
			name:         "http scheme with port",
			target:       "http://oec.880508.xyz:15668/",
			wantHostPort: "",
			wantHTTPS:    false,
			wantErr:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotHostPort, gotHTTPS, err := parseCertTarget(tt.target)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseCertTarget(%q) error = %v, wantErr %v", tt.target, err, tt.wantErr)
			}
			if gotHTTPS != tt.wantHTTPS {
				t.Errorf("parseCertTarget(%q) isHTTPS = %v, want %v", tt.target, gotHTTPS, tt.wantHTTPS)
			}
			if gotHostPort != tt.wantHostPort {
				t.Errorf("parseCertTarget(%q) hostport = %q, want %q", tt.target, gotHostPort, tt.wantHostPort)
			}
		})
	}
}
