package dao

import (
	"sort"
	"time"

	"github.com/r0n9/nodekeep/model"
)

// CurrencySpend 是单一币种的支出汇总。不做跨币种折算，按币种分行展示。
//
// 先算年度再除出月均，而不是反过来：AmountCents/月数 会在每台机器上各丢一次
// 余数，20 台机器累计能差出好几块。年付 4999 分 /12 得 416，×12 只剩 4992。
type CurrencySpend struct {
	Currency     string
	AnnualCents  int64 // 周期性支出的年度折算
	MonthlyCents int64 // = AnnualCents / 12，仅用于展示
	OnetimeCents int64 // 一次性支出，不进月均和年度折算
	Count        int   // 计入周期性支出的服务器数
	OnetimeCount int
}

// BillingSpend 按币种汇总当前仍在计费的订阅。
// 已退订和已过终止日的不计入当前支出，但它们的历史流水仍在 PaymentYearSpend 里。
func BillingSpend(billings []model.ServerBilling, now time.Time) []CurrencySpend {
	spends := make(map[string]*CurrencySpend)
	get := func(currency string) *CurrencySpend {
		if spends[currency] == nil {
			spends[currency] = &CurrencySpend{Currency: currency}
		}
		return spends[currency]
	}

	for i := range billings {
		billing := billings[i]
		if !billing.Active() || billing.Ended(now) || billing.AmountCents == 0 {
			continue
		}
		if billing.Cycle == model.BillingCycleOnetime {
			spend := get(billing.Currency)
			spend.OnetimeCents += billing.AmountCents
			spend.OnetimeCount++
			continue
		}
		months := model.BillingCycleMonths(billing.Cycle)
		if months == 0 {
			continue
		}
		count := billing.CycleCount
		if count < 1 {
			count = 1
		}
		spend := get(billing.Currency)
		spend.AnnualCents += billing.AmountCents * 12 / int64(months*count)
		spend.Count++
	}

	result := make([]CurrencySpend, 0, len(spends))
	for _, spend := range spends {
		spend.MonthlyCents = spend.AnnualCents / 12
		result = append(result, *spend)
	}
	sortCurrencySpend(result)
	return result
}

// YearSpend 是某一年某一币种的实付合计，直接来自付费流水。
// 不依赖订阅表，所以已经退订的机器当年花的钱也算得进去。
type YearSpend struct {
	Year     int
	Currency string
	Cents    int64
	Count    int
}

// PaymentYearSpend 按年份和币种汇总付费流水，年份降序。
func PaymentYearSpend(payments []model.ServerPayment) []YearSpend {
	type key struct {
		year     int
		currency string
	}
	totals := make(map[key]*YearSpend)
	for i := range payments {
		payment := payments[i]
		k := key{year: payment.PaidAt.In(time.Local).Year(), currency: payment.Currency}
		if totals[k] == nil {
			totals[k] = &YearSpend{Year: k.year, Currency: k.currency}
		}
		totals[k].Cents += payment.AmountCents
		totals[k].Count++
	}

	result := make([]YearSpend, 0, len(totals))
	for _, spend := range totals {
		result = append(result, *spend)
	}
	sort.Slice(result, func(a, b int) bool {
		if result[a].Year != result[b].Year {
			return result[a].Year > result[b].Year
		}
		return result[a].Currency < result[b].Currency
	})
	return result
}

// AllServerPayments 返回全部付费流水，按付款日倒序。
func AllServerPayments() []model.ServerPayment {
	var payments []model.ServerPayment
	DB.Order("paid_at DESC").Order("id DESC").Find(&payments)
	return payments
}

// AllServerBillings 返回全部订阅信息。
func AllServerBillings() []model.ServerBilling {
	var billings []model.ServerBilling
	DB.Find(&billings)
	return billings
}

func sortCurrencySpend(spends []CurrencySpend) {
	sort.Slice(spends, func(a, b int) bool {
		// 未设置币种的排最后，提醒去补数据
		if (spends[a].Currency == "") != (spends[b].Currency == "") {
			return spends[b].Currency == ""
		}
		return spends[a].Currency < spends[b].Currency
	})
}
