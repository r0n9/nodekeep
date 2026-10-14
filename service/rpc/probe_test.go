package rpc

import (
	"context"
	"testing"
	"time"

	"github.com/r0n9/nodekeep/model"
	"google.golang.org/grpc/metadata"
)

func TestParseCertNotAfter(t *testing.T) {
	tests := []struct {
		name  string
		parts []string
		want  time.Time
		ok    bool
	}{
		{
			name:  "正常证书字段",
			parts: []string{"Let's Encrypt R3", "2026-11-01 12:00:00 +0000 UTC"},
			want:  time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC),
			ok:    true,
		},
		{
			// agent 用 time.Time.String()，可能带纳秒和本地时区
			name:  "带纳秒与本地时区",
			parts: []string{"Issuer", "2026-11-01 20:00:00.123456789 +0800 CST"},
			want:  time.Date(2026, 11, 1, 20, 0, 0, 123456789, time.FixedZone("CST", 8*3600)),
			ok:    true,
		},
		{
			name:  "缺少到期时间字段",
			parts: []string{"Let's Encrypt R3"},
			ok:    false,
		},
		{
			name:  "空数据",
			parts: []string{""},
			ok:    false,
		},
		{
			// 旧版 agent 或字段被截断，解析失败必须返回 false，
			// 否则零值时间会被当成「已过期」而误报
			name:  "格式不符",
			parts: []string{"Issuer", "not-a-time"},
			ok:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseCertNotAfter(tt.parts)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if ok && !got.Equal(tt.want) {
				t.Errorf("time = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestFillClientIPFromContextUsesForwardedForWhenHostIPMissing(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		"x-forwarded-for", "198.51.100.10, 198.51.100.20",
	))
	host := &model.Host{IP: "IPs[IPv4:,IPv6:]"}

	fillClientIPFromContext(ctx, host)

	if host.IP != "198.51.100.10" {
		t.Fatalf("host IP = %q, want 198.51.100.10", host.IP)
	}
}

func TestFillClientIPFromContextKeepsReportedIP(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		"x-forwarded-for", "198.51.100.10",
	))
	host := &model.Host{IP: "IPs[IPv4:203.0.113.10,IPv6:]"}

	fillClientIPFromContext(ctx, host)

	if host.IP != "IPs[IPv4:203.0.113.10,IPv6:]" {
		t.Fatalf("host IP = %q, want reported IP", host.IP)
	}
}
