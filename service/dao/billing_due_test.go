package dao

import (
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/r0n9/nodekeep/model"
)

func dueDate(y int, m time.Month, d int) *time.Time {
	t := time.Date(y, m, d, 0, 0, 0, 0, time.Local)
	return &t
}

func TestShouldRemindBillingDue(t *testing.T) {
	tiers := []int{30, 7, 3, 1}
	cases := []struct {
		days int
		want bool
	}{
		{days: 45, want: false},
		{days: 31, want: false},
		{days: 30, want: true},
		{days: 29, want: false}, // 档位是精确匹配，29 天不提醒
		{days: 7, want: true},
		{days: 3, want: true},
		{days: 2, want: false},
		{days: 1, want: true},
		{days: 0, want: false}, // 0 不在默认档位里
		{days: -1, want: true}, // 已过期每天提醒
		{days: -30, want: true},
	}
	for _, tc := range cases {
		if got := shouldRemindBillingDue(tc.days, tiers); got != tc.want {
			t.Errorf("shouldRemindBillingDue(%d) = %v, 期望 %v", tc.days, got, tc.want)
		}
	}

	// 自定义档位应当生效
	if !shouldRemindBillingDue(14, []int{14}) {
		t.Error("自定义档位 14 未命中")
	}
	if shouldRemindBillingDue(30, []int{14}) {
		t.Error("自定义档位下不应命中默认的 30")
	}
}

func TestCollectBillingDue(t *testing.T) {
	now := time.Date(2026, 3, 1, 9, 3, 0, 0, time.Local)
	names := map[uint64]string{1: "node-a", 2: "node-b", 3: "node-c", 4: "node-d", 5: "node-e", 6: "node-f"}

	yesterday := time.Date(2026, 2, 28, 9, 3, 0, 0, time.Local)
	today := time.Date(2026, 3, 1, 0, 30, 0, 0, time.Local)

	billings := []model.ServerBilling{
		// 命中 3 天档
		{Common: model.Common{ID: 1}, ServerID: 1, Status: model.BillingStatusActive, NextDueDate: dueDate(2026, 3, 4)},
		// 已过期，每天提醒
		{Common: model.Common{ID: 2}, ServerID: 2, Status: model.BillingStatusActive, NextDueDate: dueDate(2026, 2, 20)},
		// 未命中任何档位
		{Common: model.Common{ID: 3}, ServerID: 3, Status: model.BillingStatusActive, NextDueDate: dueDate(2026, 4, 15)},
		// 已退订
		{Common: model.Common{ID: 4}, ServerID: 4, Status: model.BillingStatusCancelled, NextDueDate: dueDate(2026, 3, 2)},
		// 静音
		{Common: model.Common{ID: 5}, ServerID: 5, Status: model.BillingStatusActive, Muted: true, NextDueDate: dueDate(2026, 3, 2)},
		// 没填到期日
		{Common: model.Common{ID: 6}, ServerID: 6, Status: model.BillingStatusActive},
	}

	items := collectBillingDue(billings, names, now)
	if len(items) != 2 {
		t.Fatalf("期望命中 2 台，实际 %d：%+v", len(items), items)
	}
	// 已过期的排在最前
	if items[0].billing.ServerID != 2 || items[1].billing.ServerID != 1 {
		t.Errorf("应按紧迫程度排序，实际顺序 %d, %d", items[0].billing.ServerID, items[1].billing.ServerID)
	}
	if items[0].days != -9 {
		t.Errorf("已过期天数 = %d, 期望 -9", items[0].days)
	}
	if items[1].name != "node-a" {
		t.Errorf("名称未关联，实际 %q", items[1].name)
	}

	t.Run("当天已提醒过的跳过", func(t *testing.T) {
		list := []model.ServerBilling{{
			Common: model.Common{ID: 1}, ServerID: 1,
			Status: model.BillingStatusActive, NextDueDate: dueDate(2026, 3, 4),
			LastRemindedOn: &today,
		}}
		if got := collectBillingDue(list, names, now); len(got) != 0 {
			t.Errorf("当天已提醒过不应再次命中，实际 %+v", got)
		}
	})

	t.Run("昨天提醒过的今天照常提醒", func(t *testing.T) {
		list := []model.ServerBilling{{
			Common: model.Common{ID: 1}, ServerID: 1,
			Status: model.BillingStatusActive, NextDueDate: dueDate(2026, 2, 20),
			LastRemindedOn: &yesterday,
		}}
		if got := collectBillingDue(list, names, now); len(got) != 1 {
			t.Errorf("已过期的应每天提醒，实际 %+v", got)
		}
	})

	t.Run("已到终止日的不再提醒", func(t *testing.T) {
		list := []model.ServerBilling{{
			Common: model.Common{ID: 1}, ServerID: 1,
			Status:  model.BillingStatusActive,
			EndDate: dueDate(2026, 2, 1), NextDueDate: dueDate(2026, 2, 20),
		}}
		if got := collectBillingDue(list, names, now); len(got) != 0 {
			t.Errorf("已终止的机器不应提醒，实际 %+v", got)
		}
	})

	t.Run("没有名字时回落到 ID", func(t *testing.T) {
		list := []model.ServerBilling{{
			Common: model.Common{ID: 1}, ServerID: 99,
			Status: model.BillingStatusActive, NextDueDate: dueDate(2026, 3, 4),
		}}
		got := collectBillingDue(list, names, now)
		if len(got) != 1 || got[0].name != "ID:99" {
			t.Errorf("名称回落错误：%+v", got)
		}
	})
}

func TestBillingDueMessage(t *testing.T) {
	items := []billingDueItem{
		{
			billing: model.ServerBilling{
				Provider: "RackNerd", NextDueDate: dueDate(2026, 2, 20),
			},
			name: "node-b", days: -9,
		},
		{
			billing: model.ServerBilling{
				Provider: "BandwagonHost", AutoRenew: true,
				Currency: "USD", AmountCents: 4999, Cycle: model.BillingCycleAnnually, CycleCount: 1,
				NextDueDate: dueDate(2026, 3, 4),
			},
			name: "node-a", days: 3,
		},
		{
			billing: model.ServerBilling{NextDueDate: dueDate(2026, 3, 1)},
			name:    "node-c", days: 0,
		},
	}

	message := billingDueMessage(items)
	for _, want := range []string{
		"[到期提醒]",
		"共 3 台服务器需要处理",
		"node-b（RackNerd） 已过期 9 天：2026-02-20，请及时续费",
		"node-a（BandwagonHost） 3 天后到期：2026-03-04，USD 49.99 / 年付，将自动续费，请确认余额",
		"node-c 今天到期：2026-03-01，请及时续费",
	} {
		if !strings.Contains(message, want) {
			t.Errorf("通知文案缺少 %q：\n%s", want, message)
		}
	}
}

func TestCheckBillingDueMarksReminded(t *testing.T) {
	previousDB := DB
	defer func() { DB = previousDB }()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.Server{}, &model.ServerBilling{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	DB = db

	if err := db.Create(&model.Server{Common: model.Common{ID: 1}, Name: "node-a", Secret: "s1"}).Error; err != nil {
		t.Fatalf("create server: %v", err)
	}
	if err := db.Create(&model.Server{Common: model.Common{ID: 2}, Name: "node-b", Secret: "s2"}).Error; err != nil {
		t.Fatalf("create server: %v", err)
	}

	now := time.Now()
	soon := now.AddDate(0, 0, 3)
	far := now.AddDate(0, 0, 200)
	if err := SaveServerBilling(&model.ServerBilling{
		ServerID: 1, Status: model.BillingStatusActive, NextDueDate: &soon,
	}); err != nil {
		t.Fatalf("save billing: %v", err)
	}
	if err := SaveServerBilling(&model.ServerBilling{
		ServerID: 2, Status: model.BillingStatusActive, NextDueDate: &far,
	}); err != nil {
		t.Fatalf("save billing: %v", err)
	}

	checkBillingDueAt(now)

	hit, _ := ServerBillingOf(1)
	if hit.LastRemindedOn == nil {
		t.Error("命中的订阅应写入 LastRemindedOn")
	} else if !model.SameLocalDate(*hit.LastRemindedOn, now) {
		t.Errorf("LastRemindedOn 应为今天，实际 %v", hit.LastRemindedOn)
	}

	// 未命中的不应被标记，否则真到期那天会被去重吞掉
	miss, _ := ServerBillingOf(2)
	if miss.LastRemindedOn != nil {
		t.Error("未命中的订阅不应写入 LastRemindedOn")
	}

	// 同一天再跑一次不应重复提醒
	items := collectBillingDue([]model.ServerBilling{*hit}, map[uint64]string{1: "node-a"}, now)
	if len(items) != 0 {
		t.Error("同一天不应重复提醒")
	}
}
