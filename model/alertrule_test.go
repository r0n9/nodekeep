package model

import (
	"testing"
	"time"
)

func TestAlertRuleTrafficMetrics(t *testing.T) {
	server := &ServerRuntime{
		Server: Server{Common: Common{ID: 1}, Name: "test-node"},
		State: &HostState{
			NetInTransfer:  1000,
			NetOutTransfer: 2000,
		},
		Traffic: &ServerTrafficSnapshot{
			TodayNetIn:   300,
			TodayNetOut:  500,
			CycleUsed:    1500,
			CyclePercent: 85.5,
		},
	}

	tests := []struct {
		name       string
		rule       Rule
		shouldFail bool // returns struct{}{} when metric condition is violated
	}{
		{
			name:       "transfer_today exceeds max",
			rule:       Rule{Type: "transfer_today", Max: 700},
			shouldFail: true, // 300+500 = 800 > 700
		},
		{
			name:       "transfer_today below max",
			rule:       Rule{Type: "transfer_today", Max: 900},
			shouldFail: false, // 800 <= 900
		},
		{
			name:       "transfer_cycle exceeds max",
			rule:       Rule{Type: "transfer_cycle", Max: 1200},
			shouldFail: true, // 1500 > 1200
		},
		{
			name:       "transfer_cycle below max",
			rule:       Rule{Type: "transfer_cycle", Max: 2000},
			shouldFail: false, // 1500 <= 2000
		},
		{
			name:       "transfer_cycle_percent exceeds max 80%",
			rule:       Rule{Type: "transfer_cycle_percent", Max: 80},
			shouldFail: true, // 85 > 80
		},
		{
			name:       "transfer_cycle_percent below max 90%",
			rule:       Rule{Type: "transfer_cycle_percent", Max: 90},
			shouldFail: false, // 85 <= 90
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := tt.rule.Snapshot(server)
			failed := res != nil
			if failed != tt.shouldFail {
				t.Errorf("Rule %s Snapshot() failed = %v, want %v", tt.name, failed, tt.shouldFail)
			}
		})
	}
}

func TestAlertRuleExpireDays(t *testing.T) {
	now := time.Now()
	dueIn := func(days int) *time.Time {
		t := now.AddDate(0, 0, days)
		return &t
	}
	build := func(billing *ServerBilling) *ServerRuntime {
		return &ServerRuntime{
			Server:  Server{Common: Common{ID: 1}, Name: "test-node"},
			Billing: billing,
		}
	}
	active := func(due *time.Time) *ServerBilling {
		return &ServerBilling{ServerID: 1, Status: BillingStatusActive, NextDueDate: due}
	}

	tests := []struct {
		name       string
		rule       Rule
		server     *ServerRuntime
		shouldFail bool
	}{
		{
			name:       "未录入订阅不参与告警",
			rule:       Rule{Type: "expire_days", Min: 8},
			server:     build(nil),
			shouldFail: false,
		},
		{
			name:       "未填到期日不参与告警",
			rule:       Rule{Type: "expire_days", Min: 8},
			server:     build(active(nil)),
			shouldFail: false,
		},
		{
			name:       "剩余不足阈值触发",
			rule:       Rule{Type: "expire_days", Min: 8},
			server:     build(active(dueIn(5))),
			shouldFail: true,
		},
		{
			name:       "剩余充足不触发",
			rule:       Rule{Type: "expire_days", Min: 8},
			server:     build(active(dueIn(10))),
			shouldFail: false,
		},
		{
			// min 是严格小于：min:7 时剩余 7 天不触发
			name:       "min为严格小于边界",
			rule:       Rule{Type: "expire_days", Min: 7},
			server:     build(active(dueIn(7))),
			shouldFail: false,
		},
		{
			name:       "已过期触发",
			rule:       Rule{Type: "expire_days", Min: 8},
			server:     build(active(dueIn(-3))),
			shouldFail: true,
		},
		{
			name:       "max阈值触发",
			rule:       Rule{Type: "expire_days", Max: 5},
			server:     build(active(dueIn(10))),
			shouldFail: true,
		},
		{
			name:       "已退订不参与告警",
			rule:       Rule{Type: "expire_days", Min: 8},
			server:     build(&ServerBilling{ServerID: 1, Status: BillingStatusCancelled, NextDueDate: dueIn(3)}),
			shouldFail: false,
		},
		{
			name:       "已静音不参与告警",
			rule:       Rule{Type: "expire_days", Min: 8},
			server:     build(&ServerBilling{ServerID: 1, Status: BillingStatusActive, Muted: true, NextDueDate: dueIn(3)}),
			shouldFail: false,
		},
		{
			name: "已到终止日不参与告警",
			rule: Rule{Type: "expire_days", Min: 8},
			server: build(&ServerBilling{
				ServerID: 1, Status: BillingStatusActive, NextDueDate: dueIn(3), EndDate: dueIn(-1),
			}),
			shouldFail: false,
		},
		{
			name:       "ignore列表内不参与告警",
			rule:       Rule{Type: "expire_days", Min: 8, Ignore: map[uint64]bool{1: true}},
			server:     build(active(dueIn(1))),
			shouldFail: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			failed := tt.rule.Snapshot(tt.server) != nil
			if failed != tt.shouldFail {
				t.Errorf("Rule %s Snapshot() failed = %v, want %v", tt.name, failed, tt.shouldFail)
			}
		})
	}
}

func TestRuleAllowedHour(t *testing.T) {
	atHour := func(hour int) time.Time {
		return time.Date(2026, 10, 26, hour, 30, 0, 0, time.Local)
	}

	intPtr := func(v int) *int {
		return &v
	}

	// 1. 普通性能规则（如 cpu）全天允许
	cpuRule := Rule{Type: "cpu", Max: 90}
	if !cpuRule.AllowedHour(atHour(0)) || !cpuRule.AllowedHour(atHour(12)) || !cpuRule.AllowedHour(atHour(23)) {
		t.Errorf("cpu 规则应全天允许发送提醒")
	}

	// 2. expire_days 默认只在 09:00 ~ 21:00 允许
	expireRule := Rule{Type: "expire_days", Min: 8}
	if expireRule.AllowedHour(atHour(0)) {
		t.Errorf("expire_days 默认在 00:30 不应允许发送")
	}
	if expireRule.AllowedHour(atHour(8)) {
		t.Errorf("expire_days 默认在 08:30 不应允许发送")
	}
	if !expireRule.AllowedHour(atHour(9)) {
		t.Errorf("expire_days 默认在 09:30 应允许发送")
	}
	if !expireRule.AllowedHour(atHour(15)) {
		t.Errorf("expire_days 默认在 15:30 应允许发送")
	}
	if !expireRule.AllowedHour(atHour(20)) {
		t.Errorf("expire_days 默认在 20:30 应允许发送")
	}
	if expireRule.AllowedHour(atHour(21)) {
		t.Errorf("expire_days 默认在 21:30 不应允许发送")
	}

	// 3. 自定义 start_hour 和 end_hour
	customRule := Rule{Type: "expire_days", Min: 8, StartHour: intPtr(10), EndHour: intPtr(18)}
	if customRule.AllowedHour(atHour(9)) {
		t.Errorf("customRule 在 09:30 不应允许发送")
	}
	if !customRule.AllowedHour(atHour(10)) {
		t.Errorf("customRule 在 10:30 应允许发送")
	}
	if customRule.AllowedHour(atHour(18)) {
		t.Errorf("customRule 在 18:30 不应允许发送")
	}

	// 4. AlertRule 聚合检测
	alert := AlertRule{
		Rules: []Rule{expireRule},
	}
	if alert.AllowedNotificationTime(atHour(0)) {
		t.Errorf("包含 expire_days 的 alert 在 00:30 不应允许发送")
	}
	if !alert.AllowedNotificationTime(atHour(10)) {
		t.Errorf("包含 expire_days 的 alert 在 10:30 应允许发送")
	}
}
