package dao

import (
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/r0n9/nodekeep/model"
)

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&model.Server{},
		&model.ServerBilling{},
		&model.ServerMetric{},
		&model.ServerTraffic{},
		&model.ServerTrafficDaily{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	DB = db
	InitServerRuntimeState()
	return db
}

func TestProcessServerTrafficHandlesRebootAndMonotonicIncrease(t *testing.T) {
	db := setupTestDB(t)

	serverID := uint64(10)
	SetTrafficBillingConfig(serverID, 1, "1TB", model.TrafficTypeDouble)

	now := time.Date(2026, 9, 20, 10, 0, 0, 0, time.Local)

	// 1. 首次接入：当前网卡值为 1000 入, 2000 出
	totalIn, totalOut, snap := ProcessServerTraffic(serverID, 1000, 2000, now)
	if totalIn != 1000 || totalOut != 2000 {
		t.Fatalf("first tick: totalIn=%d, totalOut=%d, want 1000, 2000", totalIn, totalOut)
	}
	if snap.TodayNetIn != 0 || snap.TodayNetOut != 0 {
		t.Fatalf("first tick today: in=%d, out=%d, want 0, 0", snap.TodayNetIn, snap.TodayNetOut)
	}

	// 2. 正常运行增加 500 入, 600 出
	totalIn, totalOut, snap = ProcessServerTraffic(serverID, 1500, 2600, now.Add(time.Second*10))
	if totalIn != 1500 || totalOut != 2600 {
		t.Fatalf("second tick: totalIn=%d, totalOut=%d, want 1500, 2600", totalIn, totalOut)
	}
	if snap.TodayNetIn != 500 || snap.TodayNetOut != 600 {
		t.Fatalf("second tick today: in=%d, out=%d, want 500, 600", snap.TodayNetIn, snap.TodayNetOut)
	}
	if snap.CycleUsed != 1100 {
		t.Fatalf("second tick cycleUsed: %d, want 1100", snap.CycleUsed)
	}

	// 3. 模拟节点重启：网卡计数重置为 50 入, 80 出（50 < 1500, 80 < 2600）
	totalIn, totalOut, snap = ProcessServerTraffic(serverID, 50, 80, now.Add(time.Minute*2))
	// 重启后增量应为 50, 80，累计总量应增长为 1500+50=1550, 2600+80=2680
	if totalIn != 1550 || totalOut != 2680 {
		t.Fatalf("reboot tick: totalIn=%d, totalOut=%d, want 1550, 2680", totalIn, totalOut)
	}
	if snap.TodayNetIn != 550 || snap.TodayNetOut != 680 {
		t.Fatalf("reboot tick today: in=%d, out=%d, want 550, 680", snap.TodayNetIn, snap.TodayNetOut)
	}
	if snap.CycleUsed != 1230 {
		t.Fatalf("reboot tick cycleUsed: %d, want 1230", snap.CycleUsed)
	}

	// 4. 重启后继续运行：增加 10 入, 20 出 (计数器变为 60, 100)
	totalIn, totalOut, snap = ProcessServerTraffic(serverID, 60, 100, now.Add(time.Minute*3))
	if totalIn != 1560 || totalOut != 2700 {
		t.Fatalf("post-reboot tick: totalIn=%d, totalOut=%d, want 1560, 2700", totalIn, totalOut)
	}
	if snap.TodayNetIn != 560 || snap.TodayNetOut != 700 {
		t.Fatalf("post-reboot tick today: in=%d, out=%d, want 560, 700", snap.TodayNetIn, snap.TodayNetOut)
	}

	// 5. 刷盘到数据库测试
	FlushServerTraffic(serverID, now.Add(time.Minute*3))

	var st model.ServerTraffic
	if err := db.First(&st, "server_id = ?", serverID).Error; err != nil {
		t.Fatalf("query ServerTraffic: %v", err)
	}
	if st.TotalNetIn != 1560 || st.TotalNetOut != 2700 {
		t.Fatalf("ServerTraffic in DB = (%d, %d), want (1560, 2700)", st.TotalNetIn, st.TotalNetOut)
	}

	var daily model.ServerTrafficDaily
	dateStr := now.In(time.Local).Format("2006-01-02")
	if err := db.First(&daily, "server_id = ? AND date = ?", serverID, dateStr).Error; err != nil {
		t.Fatalf("query ServerTrafficDaily: %v", err)
	}
	if daily.NetInBytes != 560 || daily.NetOutBytes != 700 {
		t.Fatalf("ServerTrafficDaily in DB = (%d, %d), want (560, 700)", daily.NetInBytes, daily.NetOutBytes)
	}
}

func TestProcessServerTrafficDayAndCycleRollover(t *testing.T) {
	setupTestDB(t)

	serverID := uint64(20)
	// 重置日设为 15 号
	SetTrafficBillingConfig(serverID, 15, "100GB", model.TrafficTypeDouble)

	// 9月14日（本周期的最后一天）
	day1 := time.Date(2026, 9, 14, 23, 50, 0, 0, time.Local)
	ProcessServerTraffic(serverID, 1000, 1000, day1)
	ProcessServerTraffic(serverID, 1200, 1300, day1.Add(time.Minute*5)) // +200, +300

	// 跨天跨周期到 9月15日 00:05（进入新账单周期）
	day2 := time.Date(2026, 9, 15, 0, 5, 0, 0, time.Local)
	_, _, snap := ProcessServerTraffic(serverID, 1250, 1380, day2) // +50, +80

	// 新的一天，今日流量应为 50, 80
	if snap.TodayNetIn != 50 || snap.TodayNetOut != 80 {
		t.Errorf("day2 today traffic: in=%d, out=%d, want 50, 80", snap.TodayNetIn, snap.TodayNetOut)
	}
	// 新账单周期，本期流量应为 50 + 80 = 130（不包含9月14日的上期数据）
	if snap.CycleNetIn != 50 || snap.CycleNetOut != 80 || snap.CycleUsed != 130 {
		t.Errorf("day2 cycle traffic: in=%d, out=%d, used=%d, want 50, 80, 130",
			snap.CycleNetIn, snap.CycleNetOut, snap.CycleUsed)
	}
	// 总累计流量依然单调累加：1000+(200+50)=1250, 1000+(300+80)=1380
	if snap.TotalNetIn != 1250 || snap.TotalNetOut != 1380 {
		t.Errorf("day2 total traffic: in=%d, out=%d, want 1250, 1380", snap.TotalNetIn, snap.TotalNetOut)
	}
}

func TestGetServerTrafficHistory(t *testing.T) {
	db := setupTestDB(t)
	serverID := uint64(30)

	today := time.Now().In(time.Local)
	d1 := today.AddDate(0, 0, -2).Format("2006-01-02")
	d2 := today.AddDate(0, 0, -1).Format("2006-01-02")
	d3 := today.Format("2006-01-02")

	db.Create(&model.ServerTrafficDaily{ServerID: serverID, Date: d1, NetInBytes: 100, NetOutBytes: 200})
	db.Create(&model.ServerTrafficDaily{ServerID: serverID, Date: d2, NetInBytes: 300, NetOutBytes: 400})
	db.Create(&model.ServerTrafficDaily{ServerID: serverID, Date: d3, NetInBytes: 500, NetOutBytes: 600})

	history := GetServerTrafficHistory(serverID, 7)
	if len(history) != 3 {
		t.Fatalf("history length = %d, want 3", len(history))
	}
	if history[0].Date != d1 || history[2].Date != d3 {
		t.Errorf("history dates = (%s, %s), want (%s, %s)", history[0].Date, history[2].Date, d1, d3)
	}
}
