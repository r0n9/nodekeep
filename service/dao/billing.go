package dao

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/r0n9/nodekeep/model"
)

// ServerBillingOf 读取单台服务器的订阅信息，未录入时返回 false。
func ServerBillingOf(serverID uint64) (*model.ServerBilling, bool) {
	if serverID == 0 {
		return nil, false
	}
	// 用 Find 而非 First：这里「查不到」是正常情况，
	// First 会返回 ErrRecordNotFound，被 gorm 的默认 logger 当成错误打日志。
	var list []model.ServerBilling
	if err := DB.Where("server_id = ?", serverID).Limit(1).Find(&list).Error; err != nil || len(list) == 0 {
		return nil, false
	}
	return &list[0], true
}

// ServerBillingMap 一次性取回全部订阅信息，按 ServerID 建索引。
// 服务器列表页用它避免在模板 range 里逐台查库（N+1）。
func ServerBillingMap() map[uint64]*model.ServerBilling {
	var list []model.ServerBilling
	DB.Find(&list)
	result := make(map[uint64]*model.ServerBilling, len(list))
	for i := range list {
		billing := list[i]
		result[billing.ServerID] = &billing
	}
	return result
}

// SaveServerBilling 按 ServerID 写入订阅信息，已存在则更新。
func SaveServerBilling(billing *model.ServerBilling) error {
	return SaveServerBillingWith(DB, billing)
}

// SaveServerBillingWith 是 SaveServerBilling 的事务版本，供「保存服务器 + 保存计费」
// 这类需要一起成败的场景使用。
//
// LastRemindedOn 由到期提醒任务维护，后台编辑不应把它清空，否则当天会重复提醒。
func SaveServerBillingWith(tx *gorm.DB, billing *model.ServerBilling) error {
	if billing == nil || billing.ServerID == 0 {
		return errors.New("缺少服务器 ID")
	}
	var found []model.ServerBilling
	if err := tx.Where("server_id = ?", billing.ServerID).Limit(1).Find(&found).Error; err != nil {
		return err
	}
	if len(found) == 0 {
		return tx.Create(billing).Error
	}
	existing := found[0]
	billing.ID = existing.ID
	billing.CreatedAt = existing.CreatedAt
	if billing.LastRemindedOn == nil {
		billing.LastRemindedOn = existing.LastRemindedOn
	}
	return tx.Save(billing).Error
}

// DeleteServerBillingWith 删除一台服务器的订阅信息，保留付费流水（那是历史记录）。
// 后台把计费表单整体清空时调用。
func DeleteServerBillingWith(tx *gorm.DB, serverID uint64) error {
	if serverID == 0 {
		return nil
	}
	return tx.Delete(&model.ServerBilling{}, "server_id = ?", serverID).Error
}

// DeleteServerBillingData 删除一台服务器的订阅信息和全部付费流水。
// 删除服务器时调用，两张表放在同一个事务里，避免留下孤儿流水。
func DeleteServerBillingData(serverID uint64) error {
	if serverID == 0 {
		return nil
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&model.ServerBilling{}, "server_id = ?", serverID).Error; err != nil {
			return err
		}
		return tx.Delete(&model.ServerPayment{}, "server_id = ?", serverID).Error
	})
}

// ServerPaymentsOf 按付费时间倒序返回一台服务器的付费流水。
func ServerPaymentsOf(serverID uint64) []model.ServerPayment {
	var payments []model.ServerPayment
	if serverID == 0 {
		return payments
	}
	DB.Where("server_id = ?", serverID).Order("paid_at DESC").Order("id DESC").Find(&payments)
	return payments
}

// RenewServerBilling 记一笔续费：插入付费流水，同时把订阅的到期日推进到本期期末。
//
// 两件事必须一起成败。只记流水不推到期日，会当天再次触发到期提醒；
// 只推到期日不记流水，这笔钱就从支出统计里消失了。
//
// 同时清掉 LastRemindedOn：它是「今天已提醒过」的去重标记，续费后应当作废，
// 否则新周期里当天的提醒会被误判为重复而吞掉。
func RenewServerBilling(payment *model.ServerPayment, nextDueDate time.Time) error {
	if payment == nil || payment.ServerID == 0 {
		return errors.New("缺少服务器 ID")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(payment).Error; err != nil {
			return err
		}
		result := tx.Model(&model.ServerBilling{}).
			Where("server_id = ?", payment.ServerID).
			Updates(map[string]interface{}{
				"next_due_date":    nextDueDate,
				"last_reminded_on": nil,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errors.New("该服务器还没有计费信息")
		}
		return nil
	})
}

// DeleteServerPayment 删除一条付费流水，用于订正录错的记录。
// 不改动到期日：改错金额和改到期日是两件事，后者在编辑弹窗里改。
func DeleteServerPayment(id uint64) error {
	if id == 0 {
		return errors.New("错误的流水 ID")
	}
	return DB.Delete(&model.ServerPayment{}, "id = ?", id).Error
}

// billingDueItem 是一条待提醒的到期记录。
type billingDueItem struct {
	billing model.ServerBilling
	name    string
	days    int
}

// CheckBillingDue 是每日到期检查的 cron 入口。
func CheckBillingDue() {
	checkBillingDueAt(time.Now())
}

func checkBillingDueAt(now time.Time) {
	var billings []model.ServerBilling
	if err := DB.Find(&billings).Error; err != nil {
		return
	}
	items := collectBillingDue(billings, serverNameMap(), now)
	if len(items) == 0 {
		return
	}

	// 聚合成一条：20 台机器同一天到期时，20 条消息会把通知渠道刷屏
	SendNotification(billingDueMessage(items), false)

	ids := make([]uint64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.billing.ID)
	}
	DB.Model(&model.ServerBilling{}).Where("id IN ?", ids).
		Update("last_reminded_on", now)
}

// collectBillingDue 挑出今天需要提醒的订阅，按紧迫程度排序。
func collectBillingDue(billings []model.ServerBilling, names map[uint64]string, now time.Time) []billingDueItem {
	var items []billingDueItem
	for i := range billings {
		billing := billings[i]
		if !billing.Active() || billing.Muted || billing.Ended(now) {
			continue
		}
		days, ok := billing.DaysUntilDue(now)
		if !ok || !shouldRemindBillingDue(days, billing.EffectiveRemindDays()) {
			continue
		}
		if billing.RemindedOn(now) {
			continue
		}
		name := names[billing.ServerID]
		if name == "" {
			name = fmt.Sprintf("ID:%d", billing.ServerID)
		}
		items = append(items, billingDueItem{billing: billing, name: name, days: days})
	}
	sort.Slice(items, func(a, b int) bool {
		if items[a].days != items[b].days {
			return items[a].days < items[b].days
		}
		return items[a].billing.ServerID < items[b].billing.ServerID
	})
	return items
}

// shouldRemindBillingDue 判断剩余天数是否命中提醒档位。
// 已过期的每天提醒一次，直到续费或标记退订。
//
// 档位是精确匹配：面板整天没运行会漏掉那一档，下一档仍会提醒。
func shouldRemindBillingDue(days int, remindDays []int) bool {
	if days < 0 {
		return true
	}
	for _, remindDay := range remindDays {
		if remindDay == days {
			return true
		}
	}
	return false
}

func billingDueMessage(items []billingDueItem) string {
	var buf strings.Builder
	fmt.Fprintf(&buf, "[到期提醒]\n共 %d 台服务器需要处理\n", len(items))
	for _, item := range items {
		buf.WriteString("\n")
		buf.WriteString(billingDueLine(item))
	}
	return buf.String()
}

func billingDueLine(item billingDueItem) string {
	label := item.name
	if provider := strings.TrimSpace(item.billing.Provider); provider != "" {
		label += "（" + provider + "）"
	}

	var when string
	switch {
	case item.days < 0:
		when = fmt.Sprintf("已过期 %d 天", -item.days)
	case item.days == 0:
		when = "今天到期"
	default:
		when = fmt.Sprintf("%d 天后到期", item.days)
	}

	// 自动续费的风险是余额不足而不是忘记续费，提示语要不一样
	action := "请及时续费"
	if item.billing.AutoRenew {
		action = "将自动续费，请确认余额"
	}
	if price := item.billing.PriceText(); price != "" {
		action = price + "，" + action
	}
	return fmt.Sprintf("%s %s：%s，%s", label, when,
		model.FormatBillingDate(item.billing.NextDueDate), action)
}

func serverNameMap() map[uint64]string {
	var servers []model.Server
	DB.Select("id, name").Find(&servers)
	names := make(map[uint64]string, len(servers))
	for _, server := range servers {
		names[server.ID] = server.Name
	}
	return names
}
