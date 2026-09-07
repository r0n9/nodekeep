package model

import (
	"testing"
	"time"
)

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.Local)
}

func TestAdvanceDue(t *testing.T) {
	cases := []struct {
		name      string
		from      time.Time
		cycle     string
		count     int
		anchorDay int
		want      time.Time
	}{
		{
			name:  "月付普通情况",
			from:  date(2026, 3, 1),
			cycle: BillingCycleMonthly,
			want:  date(2026, 4, 1),
		},
		{
			name:  "跨年",
			from:  date(2026, 12, 15),
			cycle: BillingCycleMonthly,
			want:  date(2027, 1, 15),
		},
		{
			// AddDate(0,1,0) 会得到 3/3，必须截断到 2 月末
			name:  "1月31日加一月截断到2月末",
			from:  date(2026, 1, 31),
			cycle: BillingCycleMonthly,
			want:  date(2026, 2, 28),
		},
		{
			name:  "闰年2月末",
			from:  date(2028, 1, 31),
			cycle: BillingCycleMonthly,
			want:  date(2028, 2, 29),
		},
		{
			name:  "3月31日加一月截断到4月30",
			from:  date(2026, 3, 31),
			cycle: BillingCycleMonthly,
			want:  date(2026, 4, 30),
		},
		{
			name:  "季付跨到小月",
			from:  date(2026, 1, 31),
			cycle: BillingCycleQuarterly,
			want:  date(2026, 4, 30),
		},
		{
			name:  "年付",
			from:  date(2026, 6, 1),
			cycle: BillingCycleAnnually,
			want:  date(2027, 6, 1),
		},
		{
			name:  "闰日年付落到平年",
			from:  date(2028, 2, 29),
			cycle: BillingCycleAnnually,
			want:  date(2029, 2, 28),
		},
		{
			name:  "两年付",
			from:  date(2026, 5, 10),
			cycle: BillingCycleBiennially,
			want:  date(2028, 5, 10),
		},
		{
			name:  "五年付",
			from:  date(2026, 5, 10),
			cycle: BillingCycleQuinquennially,
			want:  date(2031, 5, 10),
		},
		{
			name:  "周期数为2的年付等于加两年",
			from:  date(2026, 5, 10),
			cycle: BillingCycleAnnually,
			count: 2,
			want:  date(2028, 5, 10),
		},
		{
			// 有锚点时不受上一期被截断的影响
			name:      "锚点31从2月末推进回到3月31",
			from:      date(2026, 2, 28),
			cycle:     BillingCycleMonthly,
			anchorDay: 31,
			want:      date(2026, 3, 31),
		},
		{
			name:      "锚点30不会被抬到31",
			from:      date(2026, 4, 30),
			cycle:     BillingCycleMonthly,
			anchorDay: 30,
			want:      date(2026, 5, 30),
		},
		{
			name:      "锚点超出目标月长度仍截断",
			from:      date(2026, 1, 31),
			cycle:     BillingCycleMonthly,
			anchorDay: 31,
			want:      date(2026, 2, 28),
		},
	}

	for _, tc := range cases {
		got, ok := AdvanceDue(tc.from, tc.cycle, tc.count, tc.anchorDay)
		if !ok {
			t.Errorf("%s：期望可以推进", tc.name)
			continue
		}
		if !got.Equal(tc.want) {
			t.Errorf("%s：AdvanceDue(%s) = %s, 期望 %s",
				tc.name, tc.from.Format(billingDateLayout),
				got.Format(billingDateLayout), tc.want.Format(billingDateLayout))
		}
	}
}

func TestAdvanceDueNotRenewable(t *testing.T) {
	from := date(2026, 3, 1)
	for _, cycle := range []string{BillingCycleOnetime, "", "forever"} {
		got, ok := AdvanceDue(from, cycle, 1, 0)
		if ok {
			t.Errorf("周期 %q 不应支持推进", cycle)
		}
		if !got.Equal(from) {
			t.Errorf("周期 %q 不可推进时应原样返回", cycle)
		}
	}
}

// TestAdvanceDueNoDrift 是月末锚点最容易出问题的场景：
// 连续推进 12 期，有锚点时应当每次都回到 31 号（短月截断），
// 没有锚点时会一路前移，这也是 anchorDay 存在的理由。
func TestAdvanceDueNoDrift(t *testing.T) {
	current := date(2026, 1, 31)
	for i := 0; i < 12; i++ {
		next, ok := AdvanceDue(current, BillingCycleMonthly, 1, 31)
		if !ok {
			t.Fatal("月付应支持推进")
		}
		last := daysInMonth(next.Year(), next.Month())
		wantDay := 31
		if last < 31 {
			wantDay = last
		}
		if next.Day() != wantDay {
			t.Fatalf("第 %d 期 = %s，期望日为 %d", i+1, next.Format(billingDateLayout), wantDay)
		}
		current = next
	}
	if got, want := current.Format(billingDateLayout), "2027-01-31"; got != want {
		t.Errorf("12 期后 = %s, 期望 %s", got, want)
	}

	// 没有锚点时到期日会被短月永久拉前，记录这个已知行为
	noAnchor := date(2026, 1, 31)
	for i := 0; i < 3; i++ {
		noAnchor, _ = AdvanceDue(noAnchor, BillingCycleMonthly, 1, 0)
	}
	if got, want := noAnchor.Format(billingDateLayout), "2026-04-28"; got != want {
		t.Errorf("无锚点推进 3 期 = %s, 期望 %s", got, want)
	}
}

func TestServerBillingNextDue(t *testing.T) {
	start := date(2025, 1, 31)
	billing := &ServerBilling{
		Cycle:      BillingCycleMonthly,
		CycleCount: 1,
		StartDate:  &start,
	}
	if got := billing.AnchorDay(); got != 31 {
		t.Errorf("AnchorDay = %d, 期望 31", got)
	}

	next, ok := billing.NextDue(date(2026, 2, 28))
	if !ok {
		t.Fatal("月付应支持推进")
	}
	if want := date(2026, 3, 31); !next.Equal(want) {
		t.Errorf("NextDue = %s, 期望 %s", next.Format(billingDateLayout), want.Format(billingDateLayout))
	}

	// 未填开通日时没有锚点
	if got := (&ServerBilling{}).AnchorDay(); got != 0 {
		t.Errorf("未填开通日时 AnchorDay 应为 0，实际 %d", got)
	}
	if _, ok := (*ServerBilling)(nil).NextDue(time.Now()); ok {
		t.Error("nil 订阅不应支持推进")
	}
}

func TestDaysInMonth(t *testing.T) {
	cases := []struct {
		year  int
		month time.Month
		want  int
	}{
		{2026, time.January, 31},
		{2026, time.February, 28},
		{2028, time.February, 29},
		{2000, time.February, 29}, // 400 年闰
		{1900, time.February, 28}, // 100 年不闰
		{2026, time.April, 30},
		{2026, time.December, 31},
	}
	for _, tc := range cases {
		if got := daysInMonth(tc.year, tc.month); got != tc.want {
			t.Errorf("daysInMonth(%d, %s) = %d, 期望 %d", tc.year, tc.month, got, tc.want)
		}
	}
}
