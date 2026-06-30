package dao

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/r0n9/nodekeep/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSortedPublicServerSnapshotRedactsSensitiveFields(t *testing.T) {
	InitServerRuntimeState()
	UpsertServerRuntime(model.Server{
		Common:       model.Common{ID: 1},
		Name:         "public-node",
		Tag:          "edge",
		Secret:       "secret-token",
		Note:         "private-note",
		DisplayIndex: 10,
	}, false)
	UpdateServerHost(1, model.Host{
		Platform:        "linux",
		PlatformVersion: "6.8",
		CPU:             []string{"secret-cpu"},
		MemTotal:        1024,
		DiskTotal:       2048,
		SwapTotal:       512,
		Arch:            "amd64",
		Virtualization:  "kvm",
		BootTime:        123,
		IP:              "203.0.113.10",
		CountryCode:     "US",
		Version:         "1.0.0",
	}, false)
	UpdateServerState(1, model.HostState{CPU: 12.5, Uptime: 99}, time.Unix(100, 0))

	servers := SortedPublicServerSnapshot()
	if len(servers) != 1 {
		t.Fatalf("public snapshot length = %d, want 1", len(servers))
	}
	if servers[0].Host == nil {
		t.Fatal("public snapshot host is nil")
	}

	data, err := json.Marshal(servers)
	if err != nil {
		t.Fatalf("marshal public snapshot: %v", err)
	}
	payload := string(data)
	for _, sensitive := range []string{"203.0.113.10", "secret-token", "private-note", "1.0.0", `"IP"`, `"Secret"`, `"Note"`, `"Version"`} {
		if strings.Contains(payload, sensitive) {
			t.Fatalf("public snapshot JSON leaked %q: %s", sensitive, payload)
		}
	}
	for _, expected := range []string{"public-node", "edge", "linux", "amd64"} {
		if !strings.Contains(payload, expected) {
			t.Fatalf("public snapshot JSON missing %q: %s", expected, payload)
		}
	}
}

func TestSortedPublicServerSnapshotIsDeepCopy(t *testing.T) {
	InitServerRuntimeState()
	UpsertServerRuntime(model.Server{
		Common: model.Common{ID: 1},
		Name:   "node",
	}, false)
	UpdateServerHost(1, model.Host{CPU: []string{"cpu-a"}}, false)
	UpdateServerState(1, model.HostState{Uptime: 1}, time.Unix(100, 0))

	servers := SortedPublicServerSnapshot()
	servers[0].Name = "changed"
	servers[0].Host.CPU[0] = "changed-cpu"
	servers[0].State.Uptime = 999

	fresh := SortedPublicServerSnapshot()
	if fresh[0].Name != "node" {
		t.Fatalf("public snapshot mutation changed runtime name: %q", fresh[0].Name)
	}
	if fresh[0].Host.CPU[0] != "cpu-a" {
		t.Fatalf("public snapshot mutation changed runtime CPU: %q", fresh[0].Host.CPU[0])
	}
	if fresh[0].State.Uptime != 1 {
		t.Fatalf("public snapshot mutation changed runtime state uptime: %d", fresh[0].State.Uptime)
	}
}

func TestSortedServerSnapshotOrdersByGroupDisplayIndexAndID(t *testing.T) {
	InitServerRuntimeState()
	for _, server := range []model.Server{
		{Common: model.Common{ID: 1}, Name: "beta-high", Tag: "beta", DisplayIndex: 100},
		{Common: model.Common{ID: 2}, Name: "alpha-low", Tag: "alpha", DisplayIndex: 20},
		{Common: model.Common{ID: 3}, Name: "alpha-high-a", Tag: "alpha", DisplayIndex: 50},
		{Common: model.Common{ID: 4}, Name: "alpha-high-b", Tag: "alpha", DisplayIndex: 50},
	} {
		UpsertServerRuntime(server, false)
	}

	servers := SortedServerSnapshot()
	got := make([]uint64, 0, len(servers))
	for _, server := range servers {
		got = append(got, server.ID)
	}
	want := []uint64{1, 4, 3, 2}
	if len(got) != len(want) {
		t.Fatalf("sorted server count = %d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sorted server ids = %v, want %v", got, want)
		}
	}
}

func TestObserveServerMetricAggregatesMinuteBuckets(t *testing.T) {
	previousDB := DB
	defer func() {
		DB = previousDB
	}()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.ServerMetric{}); err != nil {
		t.Fatalf("auto migrate server metric: %v", err)
	}
	DB = db
	InitServerRuntimeState()

	host := &model.Host{
		MemTotal:  1000,
		SwapTotal: 2000,
		DiskTotal: 3000,
	}
	start := time.Date(2026, 6, 30, 12, 0, 2, 0, time.UTC)
	ObserveServerMetric(1, model.HostState{
		CPU:            10,
		MemUsed:        100,
		SwapUsed:       200,
		DiskUsed:       300,
		NetInTransfer:  1000,
		NetOutTransfer: 2000,
		NetInSpeed:     100,
		NetOutSpeed:    200,
		Uptime:         10,
	}, host, start)
	ObserveServerMetric(1, model.HostState{
		CPU:            20,
		MemUsed:        300,
		SwapUsed:       400,
		DiskUsed:       500,
		NetInTransfer:  1500,
		NetOutTransfer: 2600,
		NetInSpeed:     300,
		NetOutSpeed:    500,
		Uptime:         38,
	}, host, start.Add(28*time.Second))
	ObserveServerMetric(1, model.HostState{
		CPU:            40,
		MemUsed:        700,
		SwapUsed:       800,
		DiskUsed:       900,
		NetInTransfer:  2100,
		NetOutTransfer: 3300,
		NetInSpeed:     700,
		NetOutSpeed:    900,
		Uptime:         61,
	}, host, start.Add(61*time.Second))

	var stored model.ServerMetric
	if err := db.First(&stored, "server_id = ? AND bucket_at = ?", 1, start.Truncate(time.Minute)).Error; err != nil {
		t.Fatalf("first flushed metric: %v", err)
	}
	if stored.SampleCount != 2 {
		t.Fatalf("sample count = %d, want 2", stored.SampleCount)
	}
	if stored.CPUAvg != 15 || stored.CPUMax != 20 {
		t.Fatalf("cpu avg/max = %.2f/%.2f, want 15/20", stored.CPUAvg, stored.CPUMax)
	}
	if stored.MemUsedAvg != 200 || stored.SwapUsedAvg != 300 || stored.DiskUsedAvg != 400 {
		t.Fatalf("usage avg = mem %d swap %d disk %d, want 200/300/400", stored.MemUsedAvg, stored.SwapUsedAvg, stored.DiskUsedAvg)
	}
	if stored.MemTotal != 1000 || stored.SwapTotal != 2000 || stored.DiskTotal != 3000 {
		t.Fatalf("totals = mem %d swap %d disk %d, want 1000/2000/3000", stored.MemTotal, stored.SwapTotal, stored.DiskTotal)
	}
	if stored.NetInSpeedAvg != 200 || stored.NetOutSpeedAvg != 350 ||
		stored.NetInSpeedMax != 300 || stored.NetOutSpeedMax != 500 {
		t.Fatalf("network speed = in avg/max %d/%d out avg/max %d/%d, want 200/300 350/500",
			stored.NetInSpeedAvg, stored.NetInSpeedMax, stored.NetOutSpeedAvg, stored.NetOutSpeedMax)
	}
	if stored.NetInBytes != 500 || stored.NetOutBytes != 600 {
		t.Fatalf("network bytes = %d/%d, want 500/600", stored.NetInBytes, stored.NetOutBytes)
	}

	snapshot := ServerMetricSnapshot(1, start.Add(-time.Minute))
	if len(snapshot) != 2 {
		t.Fatalf("metric snapshot length = %d, want 2: %#v", len(snapshot), snapshot)
	}
	if !snapshot[1].BucketAt.Equal(start.Add(61 * time.Second).Truncate(time.Minute)) {
		t.Fatalf("current bucket time = %s, want %s", snapshot[1].BucketAt, start.Add(61*time.Second).Truncate(time.Minute))
	}
	if snapshot[1].SampleCount != 1 || snapshot[1].NetInBytes != 600 || snapshot[1].NetOutBytes != 700 {
		t.Fatalf("current bucket sample/bytes = %d %d/%d, want 1 600/700",
			snapshot[1].SampleCount, snapshot[1].NetInBytes, snapshot[1].NetOutBytes)
	}
}

func TestUpdateServerHostKeepsAgentCountryCodeWhenDashboardGeoIPUnavailable(t *testing.T) {
	InitServerRuntimeState()
	UpsertServerRuntime(model.Server{
		Common: model.Common{ID: 1},
		Name:   "node",
	}, false)

	UpdateServerHost(1, model.Host{
		IP:          "IPs[IPv4:203.0.113.10,IPv6:]",
		CountryCode: "US",
	}, false)

	servers := SortedPublicServerSnapshot()
	if len(servers) != 1 || servers[0].Host == nil {
		t.Fatalf("server host missing after update: %#v", servers)
	}
	if servers[0].Host.CountryCode != "us" {
		t.Fatalf("country code = %q, want us", servers[0].Host.CountryCode)
	}
}
