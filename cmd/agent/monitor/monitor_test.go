package monitor

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/net"
)

func TestGetHostCPUIsNonNil(t *testing.T) {
	host := GetHost()
	if host.CPU == nil {
		t.Fatal("expected CPU info slice to be non-nil")
	}
}

func TestSumNetworkCountersFiltersVirtualInterfaces(t *testing.T) {
	in, out := sumNetworkCounters([]net.IOCountersStat{
		{Name: "eth0", BytesRecv: 100, BytesSent: 200},
		{Name: "wlo1", BytesRecv: 300, BytesSent: 400},
		{Name: "lo", BytesRecv: 1000, BytesSent: 1000},
		{Name: "docker0", BytesRecv: 2000, BytesSent: 2000},
		{Name: "vethabc", BytesRecv: 3000, BytesSent: 3000},
		{Name: "utun1", BytesRecv: 4000, BytesSent: 4000},
	}, nil)

	if in != 400 || out != 600 {
		t.Fatalf("filtered counters = %d/%d, want 400/600", in, out)
	}
}

func TestSumNetworkCountersUsesAllowlist(t *testing.T) {
	in, out := sumNetworkCounters([]net.IOCountersStat{
		{Name: "eth0", BytesRecv: 100, BytesSent: 200},
		{Name: "docker0", BytesRecv: 2000, BytesSent: 3000},
	}, map[string]struct{}{"docker0": {}})

	if in != 2000 || out != 3000 {
		t.Fatalf("allowlisted counters = %d/%d, want 2000/3000", in, out)
	}
}

func TestTrackNetworkSpeedUsesElapsedTimeAndHandlesRollback(t *testing.T) {
	originalCounters := netIOCounters
	originalNow := nowFunc
	defer func() {
		netIOCounters = originalCounters
		nowFunc = originalNow
		SetNICAllowlist("")
		resetNetworkStats()
	}()

	resetNetworkStats()
	SetNICAllowlist("")

	samples := [][]net.IOCountersStat{
		{{Name: "eth0", BytesRecv: 1000, BytesSent: 2000}},
		{{Name: "eth0", BytesRecv: 2500, BytesSent: 2600}},
		{{Name: "eth0", BytesRecv: 100, BytesSent: 100}},
	}
	times := []time.Time{
		time.Unix(10, 0),
		time.Unix(11, int64(500*time.Millisecond)),
		time.Unix(12, int64(500*time.Millisecond)),
	}
	index := 0

	netIOCounters = func() ([]net.IOCountersStat, error) {
		return samples[index], nil
	}
	nowFunc = func() time.Time {
		return times[index]
	}

	TrackNetworkSpeed()
	if got := atomic.LoadUint64(&netInSpeed); got != 0 {
		t.Fatalf("initial net in speed = %d, want 0", got)
	}

	index = 1
	TrackNetworkSpeed()
	if got := atomic.LoadUint64(&netInSpeed); got != 1000 {
		t.Fatalf("net in speed = %d, want 1000", got)
	}
	if got := atomic.LoadUint64(&netOutSpeed); got != 400 {
		t.Fatalf("net out speed = %d, want 400", got)
	}

	index = 2
	TrackNetworkSpeed()
	if got := atomic.LoadUint64(&netInSpeed); got != 0 {
		t.Fatalf("rollback net in speed = %d, want 0", got)
	}
	if got := atomic.LoadUint64(&netOutSpeed); got != 0 {
		t.Fatalf("rollback net out speed = %d, want 0", got)
	}
}

func resetNetworkStats() {
	atomic.StoreUint64(&netInSpeed, 0)
	atomic.StoreUint64(&netOutSpeed, 0)
	atomic.StoreUint64(&netInTransfer, 0)
	atomic.StoreUint64(&netOutTransfer, 0)
	atomic.StoreUint64(&lastUpdateNano, 0)
}
