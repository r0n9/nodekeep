package model

import (
	"testing"
	"time"
)

func TestBillingCycleRange(t *testing.T) {
	loc := time.UTC
	tests := []struct {
		name      string
		now       time.Time
		resetDay  int
		wantStart string
		wantEnd   string
	}{
		{
			name:      "reset on 1st, middle of month",
			now:       time.Date(2026, 9, 15, 12, 0, 0, 0, loc),
			resetDay:  1,
			wantStart: "2026-09-01",
			wantEnd:   "2026-10-01",
		},
		{
			name:      "reset on 1st, exactly on 1st",
			now:       time.Date(2026, 9, 1, 0, 0, 0, 0, loc),
			resetDay:  1,
			wantStart: "2026-09-01",
			wantEnd:   "2026-10-01",
		},
		{
			name:      "reset on 1st, default when resetDay is 0",
			now:       time.Date(2026, 9, 30, 23, 59, 59, 0, loc),
			resetDay:  0,
			wantStart: "2026-09-01",
			wantEnd:   "2026-10-01",
		},
		{
			name:      "reset on 15th, before reset day",
			now:       time.Date(2026, 9, 10, 8, 0, 0, 0, loc),
			resetDay:  15,
			wantStart: "2026-08-15",
			wantEnd:   "2026-09-15",
		},
		{
			name:      "reset on 15th, on reset day",
			now:       time.Date(2026, 9, 15, 0, 0, 0, 0, loc),
			resetDay:  15,
			wantStart: "2026-09-15",
			wantEnd:   "2026-10-15",
		},
		{
			name:      "reset on 15th, after reset day",
			now:       time.Date(2026, 9, 25, 18, 0, 0, 0, loc),
			resetDay:  15,
			wantStart: "2026-09-15",
			wantEnd:   "2026-10-15",
		},
		{
			name:      "year boundary, early January before reset day",
			now:       time.Date(2026, 1, 5, 10, 0, 0, 0, loc),
			resetDay:  10,
			wantStart: "2025-12-10",
			wantEnd:   "2026-01-10",
		},
		{
			name:      "month end 31st clamped in Feb (2026 is non-leap, 28 days)",
			now:       time.Date(2026, 3, 5, 0, 0, 0, 0, loc),
			resetDay:  31,
			wantStart: "2026-02-28",
			wantEnd:   "2026-03-31",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStart, gotEnd := BillingCycleRange(tt.now, tt.resetDay)
			if gotStart != tt.wantStart || gotEnd != tt.wantEnd {
				t.Errorf("BillingCycleRange(%v, %d) = (%s, %s), want (%s, %s)",
					tt.now, tt.resetDay, gotStart, gotEnd, tt.wantStart, tt.wantEnd)
			}
		})
	}
}

func TestParseTrafficBytes(t *testing.T) {
	tests := []struct {
		input     string
		wantBytes uint64
		wantOK    bool
	}{
		{"1TB", 1024 * 1024 * 1024 * 1024, true},
		{"1TB/月", 1024 * 1024 * 1024 * 1024, true},
		{"1 tb / month", 1024 * 1024 * 1024 * 1024, true},
		{"500GB/月", 500 * 1024 * 1024 * 1024, true},
		{"500g", 500 * 1024 * 1024 * 1024, true},
		{"1024MB", 1024 * 1024 * 1024, true},
		{"2.5TB", uint64(2.5 * 1024 * 1024 * 1024 * 1024), true},
		{"100b", 100, true},
		{"", 0, false},
		{"无限", 0, false},
		{"unlimited", 0, false},
		{"invalid", 0, false},
		{"-50GB", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			gotBytes, gotOK := ParseTrafficBytes(tt.input)
			if gotOK != tt.wantOK || gotBytes != tt.wantBytes {
				t.Errorf("ParseTrafficBytes(%q) = (%d, %v), want (%d, %v)",
					tt.input, gotBytes, gotOK, tt.wantBytes, tt.wantOK)
			}
		})
	}
}

func TestCalculateCycleUsed(t *testing.T) {
	// Single direction: max(netIn, netOut)
	if got := CalculateCycleUsed(TrafficTypeSingle, 100, 200); got != 200 {
		t.Errorf("CalculateCycleUsed(single, 100, 200) = %d, want 200", got)
	}
	if got := CalculateCycleUsed(TrafficTypeSingle, 300, 150); got != 300 {
		t.Errorf("CalculateCycleUsed(single, 300, 150) = %d, want 300", got)
	}

	// Double direction: netIn + netOut
	if got := CalculateCycleUsed(TrafficTypeDouble, 100, 200); got != 300 {
		t.Errorf("CalculateCycleUsed(double, 100, 200) = %d, want 300", got)
	}
	// Default (0) defaults to double
	if got := CalculateCycleUsed(0, 100, 200); got != 300 {
		t.Errorf("CalculateCycleUsed(0, 100, 200) = %d, want 300", got)
	}
}
