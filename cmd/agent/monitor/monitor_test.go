package monitor

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/net"

	"github.com/r0n9/nodekeep/model"
)

func TestDetectedPlatformPrefersDistribution(t *testing.T) {
	tests := []struct {
		name string
		info *host.InfoStat
		want string
	}{
		{name: "ubuntu", info: &host.InfoStat{OS: "linux", Platform: "ubuntu"}, want: "ubuntu"},
		{name: "debian", info: &host.InfoStat{OS: "linux", Platform: "debian"}, want: "debian"},
		{name: "centos", info: &host.InfoStat{OS: "linux", Platform: "centos"}, want: "centos"},
		{name: "unknown distribution", info: &host.InfoStat{OS: "linux"}, want: "linux"},
		{name: "nil info", info: nil, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := detectedPlatform(tt.info); got != tt.want {
				t.Fatalf("detectedPlatform() = %q, want %q", got, tt.want)
			}
		})
	}
}

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
	})

	if in != 400 || out != 600 {
		t.Fatalf("filtered counters = %d/%d, want 400/600", in, out)
	}
}

func TestTrackNetworkSpeedUsesElapsedTimeAndHandlesRollback(t *testing.T) {
	originalCounters := netIOCounters
	originalNow := nowFunc
	defer func() {
		netIOCounters = originalCounters
		nowFunc = originalNow
		resetNetworkStats()
	}()

	resetNetworkStats()

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

func TestCollectNICCountersReportsAllNICsWithVirtualFlag(t *testing.T) {
	counters := []net.IOCountersStat{
		{Name: "eth0", BytesRecv: 100, BytesSent: 200},
		{Name: "lo", BytesRecv: 1000, BytesSent: 1000},
		{Name: "docker0", BytesRecv: 2000, BytesSent: 3000},
		{Name: "wlo1", BytesRecv: 300, BytesSent: 400},
	}

	got := collectNICCounters(counters)
	if len(got) != len(counters) {
		t.Fatalf("collectNICCounters returned %d entries, want %d", len(got), len(counters))
	}

	want := map[string]struct {
		recv, sent uint64
		virtual    bool
	}{
		"eth0":    {100, 200, false},
		"lo":      {1000, 1000, true},
		"docker0": {2000, 3000, true},
		"wlo1":    {300, 400, false},
	}
	for _, nic := range got {
		expected, ok := want[nic.Name]
		if !ok {
			t.Fatalf("unexpected NIC %q", nic.Name)
		}
		if nic.BytesRecv != expected.recv || nic.BytesSent != expected.sent {
			t.Fatalf("NIC %q counters = %d/%d, want %d/%d", nic.Name, nic.BytesRecv, nic.BytesSent, expected.recv, expected.sent)
		}
		if nic.Virtual != expected.virtual {
			t.Fatalf("NIC %q virtual = %v, want %v", nic.Name, nic.Virtual, expected.virtual)
		}
	}
}

// 默认口径下明细求和必须与聚合值一致，否则服务端按网卡求和会与老口径对不上。
func TestCollectNICCountersSumMatchesAggregate(t *testing.T) {
	counters := []net.IOCountersStat{
		{Name: "eth0", BytesRecv: 100, BytesSent: 200},
		{Name: "wlo1", BytesRecv: 300, BytesSent: 400},
		{Name: "lo", BytesRecv: 1000, BytesSent: 1000},
		{Name: "docker0", BytesRecv: 2000, BytesSent: 2000},
	}

	sumIn, sumOut := sumNetworkCounters(counters)
	var nicIn, nicOut uint64
	for _, nic := range collectNICCounters(counters) {
		if nic.Virtual {
			continue
		}
		nicIn += nic.BytesRecv
		nicOut += nic.BytesSent
	}
	if nicIn != sumIn || nicOut != sumOut {
		t.Fatalf("physical NIC sum = %d/%d, aggregate = %d/%d", nicIn, nicOut, sumIn, sumOut)
	}
}

func TestTrackNetworkSpeedPublishesNICSnapshot(t *testing.T) {
	originalCounters := netIOCounters
	originalNow := nowFunc
	defer func() {
		netIOCounters = originalCounters
		nowFunc = originalNow
		resetNetworkStats()
		nicSnapshot = atomic.Value{}
	}()

	resetNetworkStats()
	nicSnapshot = atomic.Value{}

	netIOCounters = func() ([]net.IOCountersStat, error) {
		return []net.IOCountersStat{
			{Name: "eth0", BytesRecv: 1000, BytesSent: 2000},
			{Name: "lo", BytesRecv: 10, BytesSent: 20},
		}, nil
	}
	nowFunc = func() time.Time { return time.Unix(10, 0) }

	TrackNetworkSpeed()

	// 直接读快照而不走 GetState：GetState 依赖 host.Info()，
	// 在受限环境（无权限读取 boot time）下会返回 nil 而 panic。
	nics, ok := nicSnapshot.Load().([]model.NICCounter)
	if !ok {
		t.Fatal("NIC snapshot was not published")
	}
	if len(nics) != 2 {
		t.Fatalf("published NICs = %d, want 2", len(nics))
	}
	if nics[0].Name != "eth0" || nics[0].Virtual {
		t.Fatalf("first NIC = %#v, want eth0 non-virtual", nics[0])
	}
	if nics[1].Name != "lo" || !nics[1].Virtual {
		t.Fatalf("second NIC = %#v, want lo virtual", nics[1])
	}
}

func resetNetworkStats() {
	atomic.StoreUint64(&netInSpeed, 0)
	atomic.StoreUint64(&netOutSpeed, 0)
	atomic.StoreUint64(&netInTransfer, 0)
	atomic.StoreUint64(&netOutTransfer, 0)
	atomic.StoreUint64(&lastUpdateNano, 0)
}
