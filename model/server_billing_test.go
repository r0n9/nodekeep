package model

import (
	"strings"
	"testing"
	"time"
)

func TestParseAmount(t *testing.T) {
	cases := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{in: "", want: 0},
		{in: "12", want: 1200},
		{in: "12.5", want: 1250},
		{in: "12.50", want: 1250},
		{in: "0.99", want: 99},
		{in: ".99", want: 99},
		{in: "$49.99", want: 4999},
		{in: "¥1,299.00", want: 129900},
		{in: " 10 ", want: 1000},
		{in: "-5.00", want: -500},
		{in: "12.345", wantErr: true},
		{in: "abc", wantErr: true},
	}
	for _, tc := range cases {
		got, err := ParseAmount(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseAmount(%q) 期望报错，实际得到 %d", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseAmount(%q) 意外报错：%v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseAmount(%q) = %d, 期望 %d", tc.in, got, tc.want)
		}
	}
}

func TestFormatAmountCents(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{in: 0, want: "0.00"},
		{in: 5, want: "0.05"},
		{in: 99, want: "0.99"},
		{in: 1250, want: "12.50"},
		{in: 129900, want: "1299.00"},
		{in: -500, want: "-5.00"},
	}
	for _, tc := range cases {
		if got := FormatAmountCents(tc.in); got != tc.want {
			t.Errorf("FormatAmountCents(%d) = %q, 期望 %q", tc.in, got, tc.want)
		}
	}
}

func TestParseAmountRoundTrip(t *testing.T) {
	for _, raw := range []string{"0.01", "9.99", "1299.00", "49.50"} {
		cents, err := ParseAmount(raw)
		if err != nil {
			t.Fatalf("ParseAmount(%q) 报错：%v", raw, err)
		}
		if got := FormatAmountCents(cents); got != raw {
			t.Errorf("往返 %q -> %d -> %q", raw, cents, got)
		}
	}
}

func TestParseBillingDate(t *testing.T) {
	got, err := ParseBillingDate("2026-03-01")
	if err != nil {
		t.Fatalf("解析日期报错：%v", err)
	}
	if got == nil {
		t.Fatal("期望得到日期，实际为 nil")
	}
	if got.Year() != 2026 || got.Month() != time.March || got.Day() != 1 {
		t.Errorf("解析结果 = %v，期望 2026-03-01", got)
	}
	if h, m, s := got.Clock(); h != 0 || m != 0 || s != 0 {
		t.Errorf("期望截断到零点，实际 %v", got)
	}

	empty, err := ParseBillingDate("  ")
	if err != nil || empty != nil {
		t.Errorf("空串应返回 nil, nil，实际 %v, %v", empty, err)
	}

	if _, err := ParseBillingDate("2026/03/01"); err == nil {
		t.Error("非法格式应报错")
	}
}

func TestDaysUntilDue(t *testing.T) {
	due := time.Date(2026, 3, 10, 0, 0, 0, 0, time.Local)
	billing := &ServerBilling{NextDueDate: &due}

	cases := []struct {
		name string
		now  time.Time
		want int
	}{
		// 同一天内的不同时刻必须给出相同天数：这正是不能用 Sub()/24h 的原因
		{name: "当天凌晨", now: time.Date(2026, 3, 1, 0, 30, 0, 0, time.Local), want: 9},
		{name: "当天深夜", now: time.Date(2026, 3, 1, 23, 30, 0, 0, time.Local), want: 9},
		{name: "到期当天", now: time.Date(2026, 3, 10, 18, 0, 0, 0, time.Local), want: 0},
		{name: "已过期", now: time.Date(2026, 3, 15, 1, 0, 0, 0, time.Local), want: -5},
	}
	for _, tc := range cases {
		got, ok := billing.DaysUntilDue(tc.now)
		if !ok {
			t.Errorf("%s：期望有到期日", tc.name)
			continue
		}
		if got != tc.want {
			t.Errorf("%s：DaysUntilDue = %d, 期望 %d", tc.name, got, tc.want)
		}
	}

	if _, ok := (&ServerBilling{}).DaysUntilDue(time.Now()); ok {
		t.Error("未设置到期日时应返回 false")
	}
	if _, ok := (*ServerBilling)(nil).DaysUntilDue(time.Now()); ok {
		t.Error("nil 订阅应返回 false")
	}
}

func TestExpired(t *testing.T) {
	due := time.Date(2026, 3, 10, 0, 0, 0, 0, time.Local)
	billing := &ServerBilling{NextDueDate: &due}

	if billing.Expired(time.Date(2026, 3, 10, 23, 59, 0, 0, time.Local)) {
		t.Error("到期当天不算过期")
	}
	if !billing.Expired(time.Date(2026, 3, 11, 0, 1, 0, 0, time.Local)) {
		t.Error("次日应算过期")
	}
	if (&ServerBilling{}).Expired(time.Now()) {
		t.Error("没有到期日不应算过期")
	}
}

func TestParseRemindDays(t *testing.T) {
	cases := []struct {
		in   string
		want []int
	}{
		{in: "", want: nil},
		{in: "  ", want: nil},
		{in: "30,7,3,1", want: []int{30, 7, 3, 1}},
		{in: "1,3,7,30", want: []int{30, 7, 3, 1}},
		{in: "7, 7, 30", want: []int{30, 7}},
		{in: "30，7", want: []int{30, 7}}, // 全角逗号
		{in: "30,abc,-5,7", want: []int{30, 7}},
	}
	for _, tc := range cases {
		got := ParseRemindDays(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("ParseRemindDays(%q) = %v, 期望 %v", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("ParseRemindDays(%q) = %v, 期望 %v", tc.in, got, tc.want)
				break
			}
		}
	}
}

func TestEffectiveRemindDays(t *testing.T) {
	billing := &ServerBilling{}
	if got := billing.EffectiveRemindDays(); len(got) != len(DefaultBillingRemindDays) {
		t.Errorf("未配置时应回落到默认值，实际 %v", got)
	}
	billing.RemindDaysRaw = "14,2"
	if got := billing.EffectiveRemindDays(); len(got) != 2 || got[0] != 14 || got[1] != 2 {
		t.Errorf("EffectiveRemindDays = %v, 期望 [14 2]", got)
	}
}

func TestBillingCycleMonths(t *testing.T) {
	cases := map[string]int{
		BillingCycleMonthly:        1,
		BillingCycleQuarterly:      3,
		BillingCycleSemiannually:   6,
		BillingCycleAnnually:       12,
		BillingCycleBiennially:     24,
		BillingCycleTriennially:    36,
		BillingCycleQuinquennially: 60,
		BillingCycleOnetime:        0,
		"unknown":                  0,
	}
	for cycle, want := range cases {
		if got := BillingCycleMonths(cycle); got != want {
			t.Errorf("BillingCycleMonths(%q) = %d, 期望 %d", cycle, got, want)
		}
	}
}

func TestPriceText(t *testing.T) {
	billing := &ServerBilling{Currency: "USD", AmountCents: 4999, Cycle: BillingCycleAnnually, CycleCount: 1}
	if got, want := billing.PriceText(), "USD 49.99 / 年付"; got != want {
		t.Errorf("PriceText = %q, 期望 %q", got, want)
	}

	billing.CycleCount = 2
	if got, want := billing.PriceText(), "USD 49.99 / 每 2 个年付周期"; got != want {
		t.Errorf("PriceText = %q, 期望 %q", got, want)
	}

	// 金额为 0 视为免费，不显示价格
	if got := (&ServerBilling{Currency: "USD", Cycle: BillingCycleAnnually}).PriceText(); got != "" {
		t.Errorf("免费机器 PriceText 应为空，实际 %q", got)
	}
}

func TestServerBillingMarshalDates(t *testing.T) {
	start := time.Date(2025, 1, 2, 0, 0, 0, 0, time.Local)
	due := time.Date(2026, 1, 2, 0, 0, 0, 0, time.Local)
	billing := &ServerBilling{
		Provider:    "BandwagonHost",
		Currency:    "USD",
		AmountCents: 4999,
		Cycle:       BillingCycleAnnually,
		CycleCount:  1,
		StartDate:   &start,
		NextDueDate: &due,
		Extra:       BillingExtra{Bandwidth: "1Gbps"},
	}
	payload := string(billing.Marshal())
	for _, want := range []string{
		`"StartDate":"2025-01-02"`,
		`"NextDueDate":"2026-01-02"`,
		`"EndDate":""`,
		`"Amount":"49.99"`,
		`"Provider":"BandwagonHost"`,
		// 套餐规格必须平铺，键名与后台表单的 input name 一一对应
		`"Bandwidth":"1Gbps"`,
	} {
		if !strings.Contains(payload, want) {
			t.Errorf("回填数据缺少 %s：%s", want, payload)
		}
	}

	if got := string((*ServerBilling)(nil).Marshal()); got != "null" {
		t.Errorf("nil 订阅应序列化成 null，实际 %s", got)
	}
}
