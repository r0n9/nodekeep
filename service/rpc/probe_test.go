package rpc

import (
	"context"
	"testing"

	"github.com/r0n9/nodekeep/model"
	"google.golang.org/grpc/metadata"
)

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
