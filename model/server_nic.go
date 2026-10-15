package model

import "time"

// ServerNIC 记录某台服务器上报过的网卡。持久化是为了让后台在节点离线、
// 或面板重启之后仍能配置「用哪些网卡统计流量」。
type ServerNIC struct {
	Common
	ServerID uint64 `gorm:"uniqueIndex:idx_server_nic;index"`
	Name     string `gorm:"uniqueIndex:idx_server_nic"`
	Virtual  bool
	LastSeen time.Time
}

func (ServerNIC) TableName() string {
	return "server_nics"
}
