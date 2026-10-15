package dao

import (
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/r0n9/nodekeep/model"
)

func setupNICSelectionTest(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&model.Server{}, &model.ServerNIC{}, &model.ServerMetric{},
		&model.ServerTraffic{}, &model.ServerTrafficDaily{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	DB = db
	InitServerRuntimeState()
	return db
}

func TestSumSelectedNICs(t *testing.T) {
	counters := []model.NICCounter{
		{Name: "eth0", BytesRecv: 100, BytesSent: 200},
		{Name: "eth1", BytesRecv: 300, BytesSent: 400},
		{Name: "docker0", BytesRecv: 2000, BytesSent: 3000, Virtual: true},
	}

	t.Run("自动口径只统计物理网卡", func(t *testing.T) {
		in, out, key, ok := sumSelectedNICs(counters, nil)
		if !ok {
			t.Fatal("hasNICs = false, want true")
		}
		if in != 400 || out != 600 {
			t.Fatalf("sum = %d/%d, want 400/600", in, out)
		}
		if key != "nics:eth0,eth1" {
			t.Fatalf("key = %q, want %q", key, "nics:eth0,eth1")
		}
	})

	t.Run("选中集合只统计指定网卡", func(t *testing.T) {
		in, out, key, ok := sumSelectedNICs(counters, []string{"eth1"})
		if !ok {
			t.Fatal("hasNICs = false, want true")
		}
		if in != 300 || out != 400 {
			t.Fatalf("sum = %d/%d, want 300/400", in, out)
		}
		if key != "nics:eth1" {
			t.Fatalf("key = %q, want %q", key, "nics:eth1")
		}
	})

	t.Run("显式选中虚拟网卡也统计", func(t *testing.T) {
		in, out, _, _ := sumSelectedNICs(counters, []string{"docker0"})
		if in != 2000 || out != 3000 {
			t.Fatalf("sum = %d/%d, want 2000/3000", in, out)
		}
	})

	t.Run("老 Agent 无明细时回落", func(t *testing.T) {
		_, _, key, ok := sumSelectedNICs(nil, []string{"eth0"})
		if ok {
			t.Fatal("hasNICs = true, want false")
		}
		if key != legacyNICKey {
			t.Fatalf("key = %q, want %q", key, legacyNICKey)
		}
	})

	t.Run("指纹与网卡顺序无关", func(t *testing.T) {
		reordered := []model.NICCounter{
			{Name: "eth1", BytesRecv: 300, BytesSent: 400},
			{Name: "eth0", BytesRecv: 100, BytesSent: 200},
		}
		_, _, key, _ := sumSelectedNICs(reordered, nil)
		if key != "nics:eth0,eth1" {
			t.Fatalf("key = %q, want %q", key, "nics:eth0,eth1")
		}
	})
}

// 统计口径变化时原始计数会阶跃。若不重设基线，positiveCounterDelta 会把
// 整个阶跃当成流量，缩小白名单就会一次性灌入几十 GB 假流量。
func TestProcessServerTrafficResetsBaselineOnNICKeyChange(t *testing.T) {
	setupNICSelectionTest(t)
	serverID := uint64(40)
	now := time.Date(2026, 9, 20, 10, 0, 0, 0, time.Local)

	// 自动口径：eth0 + eth1 = 1000 + 5000 = 6000
	first := ProcessServerTraffic(serverID, 6000, 8000, "nics:eth0,eth1", now)
	if first.TotalIn != 6000 || first.TotalOut != 8000 {
		t.Fatalf("first tick totals = %d/%d, want 6000/8000", first.TotalIn, first.TotalOut)
	}

	// 正常增长 +1000/+2000
	second := ProcessServerTraffic(serverID, 7000, 10000, "nics:eth0,eth1", now.Add(10*time.Second))
	if second.TotalIn != 7000 || second.TotalOut != 10000 {
		t.Fatalf("second tick totals = %d/%d, want 7000/10000", second.TotalIn, second.TotalOut)
	}
	if second.Snapshot.TodayNetIn != 1000 || second.Snapshot.TodayNetOut != 2000 {
		t.Fatalf("second tick today = %d/%d, want 1000/2000", second.Snapshot.TodayNetIn, second.Snapshot.TodayNetOut)
	}

	// 改为只统计 eth0：求和值从 7000 掉到 2000（阶跃下降）
	third := ProcessServerTraffic(serverID, 2000, 3000, "nics:eth0", now.Add(20*time.Second))
	// 阶跃不能被计成流量：今日/累计都应保持上一轮的值
	if third.TotalIn != 7000 || third.TotalOut != 10000 {
		t.Fatalf("after key change totals = %d/%d, want unchanged 7000/10000", third.TotalIn, third.TotalOut)
	}
	if third.Snapshot.TodayNetIn != 1000 || third.Snapshot.TodayNetOut != 2000 {
		t.Fatalf("after key change today = %d/%d, want unchanged 1000/2000",
			third.Snapshot.TodayNetIn, third.Snapshot.TodayNetOut)
	}

	// 新口径下继续增长 +300/+400，应正常累加
	fourth := ProcessServerTraffic(serverID, 2300, 3400, "nics:eth0", now.Add(30*time.Second))
	if fourth.TotalIn != 7300 || fourth.TotalOut != 10400 {
		t.Fatalf("fourth tick totals = %d/%d, want 7300/10400", fourth.TotalIn, fourth.TotalOut)
	}
	if fourth.Snapshot.TodayNetIn != 1300 || fourth.Snapshot.TodayNetOut != 2400 {
		t.Fatalf("fourth tick today = %d/%d, want 1300/2400", fourth.Snapshot.TodayNetIn, fourth.Snapshot.TodayNetOut)
	}
}

// 虚拟网卡中途出现/消失同样会让求和结果阶跃，走的是同一条重设基线路径。
func TestProcessServerTrafficResetsBaselineWhenNICAppears(t *testing.T) {
	setupNICSelectionTest(t)
	serverID := uint64(41)
	now := time.Date(2026, 9, 20, 10, 0, 0, 0, time.Local)

	ProcessServerTraffic(serverID, 1000, 2000, "nics:eth0", now)
	second := ProcessServerTraffic(serverID, 1500, 2500, "nics:eth0", now.Add(10*time.Second))
	if second.Snapshot.TodayNetIn != 500 {
		t.Fatalf("second tick today in = %d, want 500", second.Snapshot.TodayNetIn)
	}

	// docker0 出现，参与求和的集合变成 eth0 + docker0，求和值跳到 10000
	third := ProcessServerTraffic(serverID, 10000, 12000, "nics:docker0,eth0", now.Add(20*time.Second))
	if third.Snapshot.TodayNetIn != 500 || third.Snapshot.TodayNetOut != 500 {
		t.Fatalf("after NIC appears today = %d/%d, want unchanged 500/500",
			third.Snapshot.TodayNetIn, third.Snapshot.TodayNetOut)
	}
}

// 速率按服务端重算，且不采信 Agent 上报值。
func TestObserveServerMetricRecomputesSpeedFromSelectedNICs(t *testing.T) {
	db := setupNICSelectionTest(t)
	serverID := uint64(42)
	UpsertServerRuntime(model.Server{
		Common:      model.Common{ID: serverID},
		Name:        "node",
		Secret:      "s",
		TrafficNICs: "eth0",
	}, false)

	start := time.Date(2026, 9, 20, 10, 0, 0, 0, time.Local)
	host := &model.Host{MemTotal: 1000}

	// 第一次采样：只有 eth0，无基线
	ObserveServerMetric(serverID, model.HostState{
		NetInTransfer:  999999,
		NetOutTransfer: 999999,
		NetInSpeed:     12345,
		NetOutSpeed:    67890,
		NICs: []model.NICCounter{
			{Name: "eth0", BytesRecv: 1000, BytesSent: 2000},
			{Name: "eth1", BytesRecv: 500000, BytesSent: 500000},
		},
	}, host, []string{"eth0"}, start)

	// 第二次采样：eth0 增长 +1000/+2000，历时 10 秒 => 100 B/s、200 B/s
	ObserveServerMetric(serverID, model.HostState{
		NetInTransfer:  999999,
		NetOutTransfer: 999999,
		NetInSpeed:     12345,
		NetOutSpeed:    67890,
		NICs: []model.NICCounter{
			{Name: "eth0", BytesRecv: 2000, BytesSent: 4000},
			{Name: "eth1", BytesRecv: 500000, BytesSent: 500000},
		},
	}, host, []string{"eth0"}, start.Add(10*time.Second))

	runtime, ok := ServerRuntimeOf(serverID)
	if !ok {
		t.Fatal("server runtime missing")
	}
	if runtime.State.NetInSpeed != 100 || runtime.State.NetOutSpeed != 200 {
		t.Fatalf("recomputed speed = %d/%d, want 100/200", runtime.State.NetInSpeed, runtime.State.NetOutSpeed)
	}
	// 累计值也应按选中集合走，而不是 Agent 上报的聚合值。
	if runtime.State.NetInTransfer != 2000 || runtime.State.NetOutTransfer != 4000 {
		t.Fatalf("totals = %d/%d, want 2000/4000", runtime.State.NetInTransfer, runtime.State.NetOutTransfer)
	}

	// 跨到下一分钟触发分钟桶落盘（桶只在翻分钟时 flush）。
	ObserveServerMetric(serverID, model.HostState{
		NICs: []model.NICCounter{
			{Name: "eth0", BytesRecv: 3000, BytesSent: 6000},
		},
	}, host, []string{"eth0"}, start.Add(61*time.Second))

	// 分钟指标桶也应记录重算后的速率。
	var stored model.ServerMetric
	if err := db.First(&stored, "server_id = ? AND bucket_at = ?", serverID, start.Truncate(time.Minute)).Error; err != nil {
		t.Fatalf("query metric: %v", err)
	}
	if stored.NetInSpeedMax != 100 || stored.NetOutSpeedMax != 200 {
		t.Fatalf("metric speed max = %d/%d, want 100/200", stored.NetInSpeedMax, stored.NetOutSpeedMax)
	}
}

// 老 Agent 不上报网卡明细时，回落行为必须与改造前完全一致。
func TestObserveServerMetricFallsBackForLegacyAgent(t *testing.T) {
	setupNICSelectionTest(t)
	serverID := uint64(43)
	UpsertServerRuntime(model.Server{
		Common:      model.Common{ID: serverID},
		Name:        "legacy",
		Secret:      "s",
		TrafficNICs: "eth0",
	}, false)

	start := time.Date(2026, 9, 20, 10, 0, 0, 0, time.Local)
	host := &model.Host{MemTotal: 1000}

	ObserveServerMetric(serverID, model.HostState{NetInTransfer: 1000, NetOutTransfer: 2000}, host, []string{"eth0"}, start)
	ObserveServerMetric(serverID, model.HostState{NetInTransfer: 1500, NetOutTransfer: 2600}, host, []string{"eth0"}, start.Add(10*time.Second))

	runtime, _ := ServerRuntimeOf(serverID)
	if runtime.State.NetInTransfer != 1500 || runtime.State.NetOutTransfer != 2600 {
		t.Fatalf("legacy totals = %d/%d, want 1500/2600", runtime.State.NetInTransfer, runtime.State.NetOutTransfer)
	}
	if runtime.State.NetInSpeed != 50 || runtime.State.NetOutSpeed != 60 {
		t.Fatalf("legacy speed = %d/%d, want 50/60", runtime.State.NetInSpeed, runtime.State.NetOutSpeed)
	}
}
