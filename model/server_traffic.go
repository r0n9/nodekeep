package model

import (
	"strconv"
	"strings"
	"time"
	"unicode"
)

// ServerTraffic 记录服务器的全局总累计流量（跨重启、持久化）。
type ServerTraffic struct {
	Common
	ServerID    uint64 `gorm:"unique;index"`
	TotalNetIn  uint64
	TotalNetOut uint64
}

func (ServerTraffic) TableName() string {
	return "server_traffics"
}

// ServerTrafficDaily 记录单台服务器每日的累计进出流量。
type ServerTrafficDaily struct {
	Common
	ServerID    uint64 `gorm:"uniqueIndex:idx_server_traffic_daily;index"`
	Date        string `gorm:"uniqueIndex:idx_server_traffic_daily;type:varchar(10);index"` // YYYY-MM-DD
	NetInBytes  uint64
	NetOutBytes uint64
}

func (ServerTrafficDaily) TableName() string {
	return "server_traffic_dailies"
}

// ServerTrafficSnapshot 包含提供给前端展示和告警评估的实时流量快照。
type ServerTrafficSnapshot struct {
	TodayNetIn       uint64  `json:"TodayNetIn"`
	TodayNetOut      uint64  `json:"TodayNetOut"`
	CycleNetIn       uint64  `json:"CycleNetIn"`
	CycleNetOut      uint64  `json:"CycleNetOut"`
	TotalNetIn       uint64  `json:"TotalNetIn"`
	TotalNetOut      uint64  `json:"TotalNetOut"`
	CycleTrafficVol  uint64  `json:"CycleTrafficVol,omitempty"`  // 字节额度，0 表示未设置
	CycleTrafficType uint8   `json:"CycleTrafficType,omitempty"` // 1 单向 / 2 双向
	CycleUsed        uint64  `json:"CycleUsed"`                  // 本周期已用字节
	CyclePercent     float64 `json:"CyclePercent"`               // 已用百分比 (0..100)
	CycleResetDay    int     `json:"CycleResetDay"`              // 每月重置日 (1..31，默认 1)
}

// BillingCycleRange 计算包含 now 时刻的当前账单周期范围：[startDate, endDate)
// resetDay 为每月重置日（1~31），若 <= 0 或 > 31 则默认为 1。
// 返回的日期字符串格式为 "YYYY-MM-DD"（本地时区），用于对每日流量表进行范围查询。
func BillingCycleRange(now time.Time, resetDay int) (string, string) {
	if resetDay <= 0 || resetDay > 31 {
		resetDay = 1
	}

	loc := now.Location()
	year, month, _ := now.Date()

	clampedDay := resetDay
	if last := daysInMonth(year, month); clampedDay > last {
		clampedDay = last
	}
	thisMonthReset := time.Date(year, month, clampedDay, 0, 0, 0, 0, loc)

	var start, end time.Time
	if now.Before(thisMonthReset) {
		// 当前时间早于本月重置日，说明当前周期从上月重置日开始，到本月重置日结束
		prev := addMonthsClamped(thisMonthReset, -1, resetDay)
		start = prev
		end = thisMonthReset
	} else {
		// 当前时间处于本月重置日或之后，当前周期从本月重置日开始，到下月重置日结束
		next := addMonthsClamped(thisMonthReset, 1, resetDay)
		start = thisMonthReset
		end = next
	}

	return start.Format("2006-01-02"), end.Format("2006-01-02")
}

// ParseTrafficBytes 解析常见的人性化流量配额字符串（如 "1TB/月"、"500GB"、"1000G"、"2.5T"），
// 转换为对应的 uint64 字节数。若无法解析或为空，返回 0, false。
func ParseTrafficBytes(s string) (uint64, bool) {
	clean := strings.TrimSpace(strings.ToLower(s))
	if clean == "" || clean == "无限" || clean == "unlimited" {
		return 0, false
	}

	// 去除斜杠及其后面的周期描述（如 "/月"、"/ month"、"/季"、"/年" 等）
	if slashIdx := strings.Index(clean, "/"); slashIdx >= 0 {
		clean = clean[:slashIdx]
	}
	clean = strings.TrimSpace(clean)

	if clean == "" {
		return 0, false
	}

	// 分离数字部分与单位部分
	idx := 0
	for idx < len(clean) && (unicode.IsDigit(rune(clean[idx])) || clean[idx] == '.') {
		idx++
	}
	if idx == 0 {
		return 0, false
	}

	numStr := strings.TrimSpace(clean[:idx])
	unitStr := strings.TrimSpace(clean[idx:])

	val, err := strconv.ParseFloat(numStr, 64)
	if err != nil || val <= 0 {
		return 0, false
	}

	var multiplier float64
	switch unitStr {
	case "b", "bytes", "":
		multiplier = 1
	case "k", "kb":
		multiplier = 1024
	case "m", "mb":
		multiplier = 1024 * 1024
	case "g", "gb":
		multiplier = 1024 * 1024 * 1024
	case "t", "tb":
		multiplier = 1024 * 1024 * 1024 * 1024
	case "p", "pb":
		multiplier = 1024 * 1024 * 1024 * 1024 * 1024
	default:
		return 0, false
	}

	total := val * multiplier
	if total < 0 {
		return 0, false
	}
	return uint64(total), true
}

// CalculateCycleUsed 计算账单周期内实际消耗的流量（字节）。
// trafficType: 1 为单向（取流入和流出中较大者），2 为双向（流入 + 流出），默认为双向。
func CalculateCycleUsed(trafficType uint8, netIn, netOut uint64) uint64 {
	if trafficType == TrafficTypeSingle {
		if netOut > netIn {
			return netOut
		}
		return netIn
	}
	return netIn + netOut
}
