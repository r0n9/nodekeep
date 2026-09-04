package dao

import (
	"testing"
	"time"

	"github.com/r0n9/nodekeep/model"
)

func TestBillingSpend(t *testing.T) {
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.Local)
	past := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)

	billings := []model.ServerBilling{
		// 年付 49.99 USD -> 年度 4999
		{ServerID: 1, Status: model.BillingStatusActive, Currency: "USD", AmountCents: 4999, Cycle: model.BillingCycleAnnually, CycleCount: 1},
		// 月付 5.00 USD -> 年度 6000
		{ServerID: 2, Status: model.BillingStatusActive, Currency: "USD", AmountCents: 500, Cycle: model.BillingCycleMonthly, CycleCount: 1},
		// 季付 30.00 CNY -> 年度 12000
		{ServerID: 3, Status: model.BillingStatusActive, Currency: "CNY", AmountCents: 3000, Cycle: model.BillingCycleQuarterly, CycleCount: 1},
		// 一次性 99.00 USD，单独统计
		{ServerID: 4, Status: model.BillingStatusActive, Currency: "USD", AmountCents: 9900, Cycle: model.BillingCycleOnetime},
		// 已退订，不计入当前支出
		{ServerID: 5, Status: model.BillingStatusCancelled, Currency: "USD", AmountCents: 10000, Cycle: model.BillingCycleAnnually},
		// 已过终止日，不计入
		{ServerID: 6, Status: model.BillingStatusActive, Currency: "USD", AmountCents: 10000, Cycle: model.BillingCycleAnnually, EndDate: &past},
		// 免费机器
		{ServerID: 7, Status: model.BillingStatusActive, Currency: "USD", AmountCents: 0, Cycle: model.BillingCycleAnnually},
	}

	spends := BillingSpend(billings, now)
	if len(spends) != 2 {
		t.Fatalf("期望 2 个币种，实际 %d：%+v", len(spends), spends)
	}

	byCurrency := map[string]CurrencySpend{}
	for _, spend := range spends {
		byCurrency[spend.Currency] = spend
	}

	usd := byCurrency["USD"]
	if usd.AnnualCents != 4999+6000 {
		t.Errorf("USD 年度 = %d, 期望 %d", usd.AnnualCents, 4999+6000)
	}
	if usd.MonthlyCents != (4999+6000)/12 {
		t.Errorf("USD 月均 = %d, 期望 %d", usd.MonthlyCents, (4999+6000)/12)
	}
	if usd.OnetimeCents != 9900 || usd.OnetimeCount != 1 {
		t.Errorf("一次性支出应单列：%+v", usd)
	}
	if usd.Count != 2 {
		t.Errorf("USD 周期性机器数 = %d, 期望 2", usd.Count)
	}

	cny := byCurrency["CNY"]
	if cny.AnnualCents != 12000 {
		t.Errorf("CNY 年度 = %d, 期望 12000", cny.AnnualCents)
	}
}

// TestBillingSpendNoRoundingLoss 说明为什么先算年度再除月均：
// 反过来做，年付 49.99 会先变成月均 416 分，×12 只剩 49.92。
func TestBillingSpendNoRoundingLoss(t *testing.T) {
	now := time.Now()
	var billings []model.ServerBilling
	for i := 0; i < 20; i++ {
		billings = append(billings, model.ServerBilling{
			ServerID: uint64(i + 1), Status: model.BillingStatusActive,
			Currency: "USD", AmountCents: 4999,
			Cycle: model.BillingCycleAnnually, CycleCount: 1,
		})
	}
	spends := BillingSpend(billings, now)
	if len(spends) != 1 {
		t.Fatalf("期望 1 个币种，实际 %d", len(spends))
	}
	if got, want := spends[0].AnnualCents, int64(4999*20); got != want {
		t.Errorf("年度合计 = %d, 期望 %d（每台各丢一次余数就会少 %d）", got, want, want-got)
	}
}

func TestBillingSpendCycleCount(t *testing.T) {
	now := time.Now()
	// 每 2 年付 240.00 -> 年度 12000
	billings := []model.ServerBilling{{
		ServerID: 1, Status: model.BillingStatusActive,
		Currency: "USD", AmountCents: 24000,
		Cycle: model.BillingCycleAnnually, CycleCount: 2,
	}}
	spends := BillingSpend(billings, now)
	if spends[0].AnnualCents != 12000 {
		t.Errorf("年度 = %d, 期望 12000", spends[0].AnnualCents)
	}

	// 两年付常量与「年付 × 2」应当一致
	billings[0].Cycle = model.BillingCycleBiennially
	billings[0].CycleCount = 1
	if got := BillingSpend(billings, now)[0].AnnualCents; got != 12000 {
		t.Errorf("两年付年度 = %d, 期望 12000", got)
	}
}

func TestBillingSpendSortsUnsetCurrencyLast(t *testing.T) {
	now := time.Now()
	billings := []model.ServerBilling{
		{ServerID: 1, Status: model.BillingStatusActive, Currency: "", AmountCents: 100, Cycle: model.BillingCycleMonthly},
		{ServerID: 2, Status: model.BillingStatusActive, Currency: "USD", AmountCents: 100, Cycle: model.BillingCycleMonthly},
		{ServerID: 3, Status: model.BillingStatusActive, Currency: "CNY", AmountCents: 100, Cycle: model.BillingCycleMonthly},
	}
	spends := BillingSpend(billings, now)
	if len(spends) != 3 {
		t.Fatalf("期望 3 个币种，实际 %d", len(spends))
	}
	if spends[0].Currency != "CNY" || spends[1].Currency != "USD" || spends[2].Currency != "" {
		t.Errorf("排序错误：%+v", spends)
	}
}

func TestPaymentYearSpend(t *testing.T) {
	at := func(y int, m time.Month, d int) time.Time {
		return time.Date(y, m, d, 0, 0, 0, 0, time.Local)
	}
	payments := []model.ServerPayment{
		{ServerID: 1, PaidAt: at(2025, 3, 1), AmountCents: 1000, Currency: "USD"},
		{ServerID: 2, PaidAt: at(2025, 8, 1), AmountCents: 2000, Currency: "USD"},
		{ServerID: 3, PaidAt: at(2025, 8, 1), AmountCents: 5000, Currency: "CNY"},
		{ServerID: 4, PaidAt: at(2026, 1, 1), AmountCents: 3000, Currency: "USD"},
	}

	spends := PaymentYearSpend(payments)
	if len(spends) != 3 {
		t.Fatalf("期望 3 组，实际 %d：%+v", len(spends), spends)
	}
	// 年份降序
	if spends[0].Year != 2026 {
		t.Errorf("应按年份降序，首行 = %d", spends[0].Year)
	}
	if spends[0].Cents != 3000 || spends[0].Currency != "USD" {
		t.Errorf("2026 USD 合计错误：%+v", spends[0])
	}
	for _, spend := range spends[1:] {
		if spend.Year != 2025 {
			continue
		}
		switch spend.Currency {
		case "USD":
			if spend.Cents != 3000 || spend.Count != 2 {
				t.Errorf("2025 USD 合计错误：%+v", spend)
			}
		case "CNY":
			if spend.Cents != 5000 || spend.Count != 1 {
				t.Errorf("2025 CNY 合计错误：%+v", spend)
			}
		}
	}

	if got := PaymentYearSpend(nil); len(got) != 0 {
		t.Errorf("没有流水时应返回空，实际 %+v", got)
	}
}
