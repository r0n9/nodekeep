package controller

import (
	"sort"
	"time"

	"github.com/r0n9/nodekeep/model"
	"github.com/r0n9/nodekeep/service/dao"
)

// billingRow 是汇总页总览表的一行。
type billingRow struct {
	ServerID  uint64
	Name      string
	Tag       string
	Due       billingView
	Provider  string
	Plan      string
	Price     string
	NextDue   string
	AutoRenew bool
	Cancelled bool
	PanelURL  string
}

// billingOverview 是汇总页顶部卡片和总览表的数据。
type billingOverview struct {
	Rows      []billingRow
	DueSoon   int // 30 天内到期（含今天）
	Expired   int
	NoBilling int
}

// newBillingOverview 按到期日升序排列服务器，没有到期日的排在最后。
func newBillingOverview(servers []*model.ServerRuntime, billings map[uint64]*model.ServerBilling, now time.Time) billingOverview {
	overview := billingOverview{Rows: make([]billingRow, 0, len(servers))}
	for _, server := range servers {
		if server == nil {
			continue
		}
		billing := billings[server.ID]
		row := billingRow{
			ServerID: server.ID,
			Name:     server.Name,
			Tag:      server.Tag,
			Due:      newBillingView(billing, now),
		}
		if billing == nil {
			overview.NoBilling++
			overview.Rows = append(overview.Rows, row)
			continue
		}
		row.Provider = billing.Provider
		row.Plan = billing.ProductPlan
		row.Price = billing.PriceText()
		row.NextDue = model.FormatBillingDate(billing.NextDueDate)
		row.AutoRenew = billing.AutoRenew
		row.Cancelled = !billing.Active()
		row.PanelURL = billing.PanelURL

		if !row.Cancelled {
			if days, ok := billing.DaysUntilDue(now); ok {
				switch {
				case days < 0:
					overview.Expired++
				case days <= 30:
					overview.DueSoon++
				}
			}
		}
		overview.Rows = append(overview.Rows, row)
	}

	sort.SliceStable(overview.Rows, func(a, b int) bool {
		left, right := overview.Rows[a], overview.Rows[b]
		// 没到期日的沉底，其余按剩余天数升序，最紧迫的在最上面
		if left.Due.HasDue != right.Due.HasDue {
			return left.Due.HasDue
		}
		if !left.Due.HasDue {
			return left.ServerID < right.ServerID
		}
		if left.Due.DueDays != right.Due.DueDays {
			return left.Due.DueDays < right.Due.DueDays
		}
		return left.ServerID < right.ServerID
	})
	return overview
}

// spendRow 是按币种分行的支出汇总，金额已格式化好。
type spendRow struct {
	Currency     string
	Monthly      string
	Annual       string
	Onetime      string
	Count        int
	OnetimeCount int
}

func newSpendRows(spends []dao.CurrencySpend) []spendRow {
	rows := make([]spendRow, 0, len(spends))
	for _, spend := range spends {
		row := spendRow{
			Currency:     currencyLabel(spend.Currency),
			Monthly:      model.FormatAmountCents(spend.MonthlyCents),
			Annual:       model.FormatAmountCents(spend.AnnualCents),
			Count:        spend.Count,
			OnetimeCount: spend.OnetimeCount,
		}
		if spend.OnetimeCents > 0 {
			row.Onetime = model.FormatAmountCents(spend.OnetimeCents)
		}
		rows = append(rows, row)
	}
	return rows
}

// yearSpendRow 是按年份和币种的实付合计。
type yearSpendRow struct {
	Year     int
	Currency string
	Amount   string
	Count    int
}

func newYearSpendRows(spends []dao.YearSpend) []yearSpendRow {
	rows := make([]yearSpendRow, 0, len(spends))
	for _, spend := range spends {
		rows = append(rows, yearSpendRow{
			Year:     spend.Year,
			Currency: currencyLabel(spend.Currency),
			Amount:   model.FormatAmountCents(spend.Cents),
			Count:    spend.Count,
		})
	}
	return rows
}

// paymentRow 是全局流水表的一行，带上服务器名和年份供前端筛选。
type paymentRow struct {
	ID       uint64
	ServerID uint64
	Server   string
	Year     int
	PaidAt   string
	Amount   string
	Currency string
	Cycle    string
	Period   string
	Method   string
	Note     string
}

func newPaymentRows(payments []model.ServerPayment, names map[uint64]string) []paymentRow {
	rows := make([]paymentRow, 0, len(payments))
	for i := range payments {
		payment := payments[i]
		name := names[payment.ServerID]
		if name == "" {
			name = "已删除的服务器"
		}
		rows = append(rows, paymentRow{
			ID:       payment.ID,
			ServerID: payment.ServerID,
			Server:   name,
			Year:     payment.PaidAt.In(time.Local).Year(),
			PaidAt:   model.FormatBillingDate(&payment.PaidAt),
			Amount:   payment.AmountText(),
			Currency: currencyLabel(payment.Currency),
			Cycle:    model.BillingCycleName(payment.Cycle),
			Period:   paymentPeriodText(payment),
			Method:   payment.Method,
			Note:     payment.Note,
		})
	}
	return rows
}

// paymentYears 返回流水里出现过的年份，降序，供筛选下拉使用。
func paymentYears(rows []paymentRow) []int {
	seen := make(map[int]bool)
	var years []int
	for _, row := range rows {
		if !seen[row.Year] {
			seen[row.Year] = true
			years = append(years, row.Year)
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(years)))
	return years
}

func currencyLabel(currency string) string {
	if currency == "" {
		return "未设置"
	}
	return currency
}
