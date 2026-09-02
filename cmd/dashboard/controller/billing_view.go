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
