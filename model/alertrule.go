package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
)

const (
	RuleCheckPass = 1
	RuleCheckFail = 0
)

type Rule struct {
	// 指标类型，cpu、memory、swap、disk、net_in_speed、net_out_speed
	// net_all_speed、transfer_in、transfer_out、transfer_all、offline
	Type     string          `json:"type,omitempty"`
	Min      uint64          `json:"min,omitempty"`      // 最小阈值 (百分比、字节 kb ÷ 1024)
	Max      uint64          `json:"max,omitempty"`      // 最大阈值 (百分比、字节 kb ÷ 1024)
	Duration uint64          `json:"duration,omitempty"` // 持续时间 (秒)
	Ignore   map[uint64]bool `json:"ignore,omitempty"`   //忽略此规则的ID列表
}

func percentage(used, total uint64) uint64 {
	if total == 0 {
		return 0
	}
	return used * 100 / total
}

// Snapshot 未通过规则返回 struct{}{}, 通过返回 nil
func (u *Rule) Snapshot(server *ServerRuntime) interface{} {
	if u.Ignore[server.ID] {
		return nil
	}
	state := server.State
	if state == nil {
		state = &HostState{}
	}
	host := server.Host
	if host == nil {
		host = &Host{}
	}
	var src uint64
	switch u.Type {
	case "cpu":
		src = uint64(state.CPU)
	case "memory":
		src = percentage(state.MemUsed, host.MemTotal)
	case "swap":
		src = percentage(state.SwapUsed, host.SwapTotal)
	case "disk":
		src = percentage(state.DiskUsed, host.DiskTotal)
	case "net_in_speed":
		src = state.NetInSpeed
	case "net_out_speed":
		src = state.NetOutSpeed
	case "net_all_speed":
		src = state.NetInSpeed + state.NetOutSpeed
	case "transfer_in":
		src = state.NetInTransfer
	case "transfer_out":
		src = state.NetOutTransfer
	case "transfer_all":
		src = state.NetOutTransfer + state.NetInTransfer
	case "offline":
		if server.LastActive.IsZero() {
			src = 0
		} else {
			src = uint64(server.LastActive.Unix())
		}
	}

	if u.Type == "offline" && uint64(time.Now().Unix())-src > 6 {
		return struct{}{}
	} else if (u.Max > 0 && src > u.Max) || (u.Min > 0 && src < u.Min) {
		return struct{}{}
	}
	return nil
}

type AlertRule struct {
	Common
	Name     string
	RulesRaw string
	Enable   *bool
	Rules    []Rule `gorm:"-" json:"-"`
}

func (r *AlertRule) BeforeSave(tx *gorm.DB) error {
	data, err := json.Marshal(r.Rules)
	if err != nil {
		return err
	}
	r.RulesRaw = string(data)
	return nil
}

func (r *AlertRule) AfterFind(tx *gorm.DB) error {
	return json.Unmarshal([]byte(r.RulesRaw), &r.Rules)
}

func (r *AlertRule) Snapshot(server *ServerRuntime) []interface{} {
	var point []interface{}
	for i := 0; i < len(r.Rules); i++ {
		point = append(point, r.Rules[i].Snapshot(server))
	}
	return point
}

func (r *AlertRule) Check(points [][]interface{}) (int, string) {
	var dist bytes.Buffer
	var max int
	var count int
	for i := 0; i < len(r.Rules); i++ {
		total := 0.0
		fail := 0.0
		num := int(r.Rules[i].Duration / 2) // SnapshotDelay
		if num > max {
			max = num
		}
		if len(points) < num {
			continue
		}
		for j := len(points) - 1; j >= 0 && len(points)-num <= j; j-- {
			total++
			if points[j][i] != nil {
				fail++
			}
		}
		if fail/total > 0.7 {
			count++
			dist.WriteString(fmt.Sprintf("%+v\n", r.Rules[i]))
		}
	}
	if count == len(r.Rules) {
		return max, dist.String()
	}
	return max, ""
}
