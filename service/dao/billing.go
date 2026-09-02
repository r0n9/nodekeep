package dao

import (
	"errors"

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
