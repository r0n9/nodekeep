package controller

import (
	"fmt"
	"html/template"
	"strings"
	"time"

	"github.com/r0n9/nodekeep/model"
)

// 到期紧迫程度，决定服务器列表里到期标签的颜色。
const (
	dueLevelNone      = "none"      // 未录入到期日
	dueLevelNormal    = "normal"    // 30 天以上
	dueLevelSoon      = "soon"      // 7 ~ 30 天
	dueLevelUrgent    = "urgent"    // 7 天以内
	dueLevelExpired   = "expired"   // 已过期
	dueLevelCancelled = "cancelled" // 已退订
)

// billingView 是服务器列表里「到期」列的渲染数据。
// 在 handler 里预先算好，避免把日期计算和文案拼接散进模板。
type billingView struct {
	Billing    *model.ServerBilling
	DueDays    int
	HasDue     bool
	Level      string
	LabelClass string
	Text       string
	Tooltip    string
	Payload    template.JS
}

func newBillingView(billing *model.ServerBilling, now time.Time) billingView {
	view := billingView{
		Billing: billing,
		Level:   dueLevelNone,
		Text:    "-",
		Payload: billing.Marshal(),
	}
	if billing == nil {
		return view
	}
	view.Tooltip = billingTooltip(billing)

	days, ok := billing.DaysUntilDue(now)
	view.DueDays, view.HasDue = days, ok

	switch {
	case billing.Status == model.BillingStatusCancelled:
		view.Level = dueLevelCancelled
		view.Text = "已退订"
	case !ok:
		view.Level = dueLevelNone
		view.Text = "-"
	case days < 0:
		view.Level = dueLevelExpired
		view.Text = fmt.Sprintf("已过期 %d 天", -days)
	case days == 0:
		view.Level = dueLevelUrgent
		view.Text = "今天到期"
	case days < 7:
		view.Level = dueLevelUrgent
		view.Text = fmt.Sprintf("%d 天", days)
	case days <= 30:
		view.Level = dueLevelSoon
		view.Text = fmt.Sprintf("%d 天", days)
	default:
		view.Level = dueLevelNormal
		view.Text = fmt.Sprintf("%d 天", days)
	}
	view.LabelClass = dueLabelClass(view.Level)
	return view
}

func dueLabelClass(level string) string {
	switch level {
	case dueLevelExpired:
		return "ui tiny red label nk-due-expired"
	case dueLevelUrgent:
		return "ui tiny red label"
	case dueLevelSoon:
		return "ui tiny orange label"
	case dueLevelNormal:
		return "ui tiny basic label"
	case dueLevelCancelled:
		return "ui tiny grey label"
	default:
		return ""
	}
}

// billingTooltip 拼出「服务商 · 套餐 · $10.00 / 年付」，缺项自动省略。
func billingTooltip(billing *model.ServerBilling) string {
	var parts []string
	for _, part := range []string{billing.Provider, billing.ProductPlan, billing.PriceText()} {
		if strings.TrimSpace(part) != "" {
			parts = append(parts, part)
		}
	}
	if billing.NextDueDate != nil {
		parts = append(parts, "到期 "+model.FormatBillingDate(billing.NextDueDate))
	}
	if billing.AutoRenew {
		parts = append(parts, "自动续费")
	}
	return strings.Join(parts, " · ")
}

// billingViewsForServers 为列表里的每台服务器预渲染到期列数据，
// 未录入计费信息的也会有一条占位记录，模板里不必再判空。
func billingViewsForServers(servers []*model.ServerRuntime, billings map[uint64]*model.ServerBilling, now time.Time) map[uint64]billingView {
	views := make(map[uint64]billingView, len(servers))
	for _, server := range servers {
		if server == nil {
			continue
		}
		views[server.ID] = newBillingView(billings[server.ID], now)
	}
	return views
}

// renewDefaults 是续费弹窗的预填数据。日期推进放在服务端算，
// 前端不重复实现一遍月末截断的规则。
type renewDefaults struct {
	Renewable   bool   `json:"Renewable"`
	Reason      string `json:"Reason"`
	Currency    string `json:"Currency"`
	Amount      string `json:"Amount"`
	Cycle       string `json:"Cycle"`
	CycleName   string `json:"CycleName"`
	PaidAt      string `json:"PaidAt"`
	PeriodStart string `json:"PeriodStart"`
	PeriodEnd   string `json:"PeriodEnd"`
}

func newRenewDefaults(billing *model.ServerBilling, now time.Time) renewDefaults {
	defaults := renewDefaults{PaidAt: model.FormatBillingDate(&now)}
	if billing == nil {
		defaults.Reason = "请先在服务器编辑里录入计费信息"
		return defaults
	}
	defaults.Currency = billing.Currency
	defaults.Amount = billing.AmountText()
	defaults.Cycle = billing.Cycle
	defaults.CycleName = billing.CycleText()

	switch billing.Cycle {
	case "":
		defaults.Reason = "请先设置计费周期"
		return defaults
	case model.BillingCycleOnetime:
		defaults.Reason = "一次性付费没有续费周期"
		return defaults
	}

	// 锚点是上一次的到期日，不是今天：3/1 到期、3/5 才付款，
	// 下一期仍应从 4/1 算起，否则账单日会逐期往后漂。
	periodStart := now
	if billing.NextDueDate != nil && !billing.NextDueDate.IsZero() {
		periodStart = *billing.NextDueDate
	}
	periodEnd, ok := billing.NextDue(periodStart)
	if !ok {
		defaults.Reason = "无法根据当前周期推算到期日"
		return defaults
	}

	defaults.Renewable = true
	defaults.PeriodStart = model.FormatBillingDate(&periodStart)
	defaults.PeriodEnd = model.FormatBillingDate(&periodEnd)
	return defaults
}

// paymentView 是付费流水在弹窗里的展示形状。
type paymentView struct {
	ID        uint64 `json:"ID"`
	PaidAt    string `json:"PaidAt"`
	Amount    string `json:"Amount"`
	Currency  string `json:"Currency"`
	CycleName string `json:"CycleName"`
	Period    string `json:"Period"`
	Method    string `json:"Method"`
	InvoiceNo string `json:"InvoiceNo"`
	Note      string `json:"Note"`
}

func newPaymentViews(payments []model.ServerPayment) []paymentView {
	views := make([]paymentView, 0, len(payments))
	for i := range payments {
		payment := payments[i]
		views = append(views, paymentView{
			ID:        payment.ID,
			PaidAt:    model.FormatBillingDate(&payment.PaidAt),
			Amount:    payment.AmountText(),
			Currency:  payment.Currency,
			CycleName: model.BillingCycleName(payment.Cycle),
			Period:    paymentPeriodText(payment),
			Method:    payment.Method,
			InvoiceNo: payment.InvoiceNo,
			Note:      payment.Note,
		})
	}
	return views
}

func paymentPeriodText(payment model.ServerPayment) string {
	start := model.FormatBillingDate(&payment.PeriodStart)
	end := model.FormatBillingDate(&payment.PeriodEnd)
	if start == "" && end == "" {
		return ""
	}
	return start + " ~ " + end
}
