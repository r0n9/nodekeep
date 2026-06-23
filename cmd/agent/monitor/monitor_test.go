package monitor

import "testing"

func TestGetHostCPUIsNonNil(t *testing.T) {
	host := GetHost()
	if host.CPU == nil {
		t.Fatal("expected CPU info slice to be non-nil")
	}
}
