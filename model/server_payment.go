package model

import "time"

// ServerPayment 是一条付费流水，与 Server 一对多。
//
// PeriodStart/PeriodEnd 记录本次付费覆盖的区间，续费时应与推进后的
// ServerBilling.NextDueDate 保持一致。有了区间就能直接统计「某台机器一共花了多少」
// 和「某年总支出」，不必从周期反推，退订过的机器也算得进去。
type ServerPayment struct {
	Common
	ServerID    uint64    `gorm:"index"`
	PaidAt      time.Time `gorm:"index"` // 实付日期
	AmountCents int64
	Currency    string
	Cycle       string    // 本次购买的周期，可能与订阅默认周期不同
	PeriodStart time.Time // 本次覆盖区间起
	PeriodEnd   time.Time // 本次覆盖区间止
	Method      string    // PayPal / 支付宝 / 信用卡
	InvoiceNo   string
	Note        string
}

// AmountText 把金额格式化成两位小数。
func (p *ServerPayment) AmountText() string {
	if p == nil {
		return ""
	}
	return FormatAmountCents(p.AmountCents)
}
