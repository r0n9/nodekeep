package model

import (
	"testing"
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
