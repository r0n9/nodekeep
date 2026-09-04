package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

// 计费周期。取值与主流主机计费系统保持一致，便于以后导入导出。
const (
	BillingCycleMonthly      = "monthly"
	BillingCycleQuarterly    = "quarterly"
	BillingCycleSemiannually = "semiannually"
	BillingCycleAnnually     = "annually"
	BillingCycleBiennially   = "biennially"
	BillingCycleTriennially  = "triennially"
	BillingCycleOnetime      = "onetime"
)

// 订阅状态。「已过期」由 NextDueDate 推导，不落库，避免存储状态与实际日期漂移。
const (
	BillingStatusActive    uint8 = 1
	BillingStatusCancelled uint8 = 2
)

// 流量计费方式
const (
	TrafficTypeSingle uint8 = 1 // 单向
	TrafficTypeDouble uint8 = 2 // 双向
)

// DefaultBillingRemindDays 未单独配置时的到期提醒档位（天）
var DefaultBillingRemindDays = []int{30, 7, 3, 1}

var billingCycleMonths = map[string]int{
	BillingCycleMonthly:      1,
	BillingCycleQuarterly:    3,
	BillingCycleSemiannually: 6,
	BillingCycleAnnually:     12,
	BillingCycleBiennially:   24,
	BillingCycleTriennially:  36,
	BillingCycleOnetime:      0,
}

var billingCycleNames = map[string]string{
	BillingCycleMonthly:      "月付",
	BillingCycleQuarterly:    "季付",
	BillingCycleSemiannually: "半年付",
	BillingCycleAnnually:     "年付",
	BillingCycleBiennially:   "两年付",
	BillingCycleTriennially:  "三年付",
	BillingCycleOnetime:      "一次性",
}

// BillingCycleMonths 返回一个计费周期包含的月数，一次性付费返回 0。
func BillingCycleMonths(cycle string) int {
	return billingCycleMonths[cycle]
}

// BillingCycleName 返回计费周期的中文名，未知周期原样返回。
func BillingCycleName(cycle string) string {
	if name, ok := billingCycleNames[cycle]; ok {
		return name
	}
	return cycle
}

// BillingExtra 套餐规格，仅用于展示，不参与查询和汇总。
type BillingExtra struct {
	Bandwidth    string `json:"bandwidth,omitempty"`    // 端口带宽，如 1Gbps
	TrafficVol   string `json:"trafficVol,omitempty"`   // 流量额度，如 1TB/月
	TrafficType  uint8  `json:"trafficType,omitempty"`  // 1 单向 / 2 双向
	TrafficReset int    `json:"trafficReset,omitempty"` // 流量重置日，常与账单日不同
	IPv4Count    int    `json:"ipv4,omitempty"`
	IPv6Count    int    `json:"ipv6,omitempty"`
	NetworkRoute string `json:"route,omitempty"` // 线路，如 CN2GIA
	Location     string `json:"location,omitempty"`
	CPUSpec      string `json:"cpu,omitempty"`
	MemSpec      string `json:"mem,omitempty"`
	DiskSpec     string `json:"disk,omitempty"`
}

// ServerBilling 是服务器的订阅信息，与 Server 一一对应。
// 独立于 Server 存储：计费信息一年才变一次，不进内存态的 ServerRuntime；
// 金额等敏感字段也因此天然不会出现在公开快照和模糊搜索里。
type ServerBilling struct {
	Common
	ServerID uint64 `gorm:"uniqueIndex"`

	// 服务商
	Provider    string // 服务商，如 BandwagonHost
	ProductPlan string // 套餐名
	PanelURL    string // 服务商管理面板地址
	OrderNo     string // 订单/服务编号，用于对账

	// 计费
	Currency    string     // CNY / USD / EUR，统一大写
	AmountCents int64      // 每周期金额，按最小单位存储，避免浮点累加误差
	Cycle       string     // 见 BillingCycle* 常量
	CycleCount  int        // 每 N 个周期结算一次，支持「每 2 年」这类非标准周期，默认 1
	AutoRenew   bool       // 是否自动续费
	StartDate   *time.Time // 首次开通日
	NextDueDate *time.Time // 下次到期日，到期提醒和排序均以此为准
	EndDate     *time.Time // 已决定不再续费的终止日
	Status      uint8      // 见 BillingStatus* 常量

	// 提醒
	RemindDaysRaw  string     // "30,7,3,1"，为空时用 DefaultBillingRemindDays
	Muted          bool       // 静音此节点的到期提醒
	LastRemindedOn *time.Time // 当天去重，落库以免重启后重复提醒

	// 展示。零值即公开，与「到期信息默认对游客公开」的默认一致。
	HideBilling bool

	RemindDays []int        `gorm:"-" json:"-"`
	ExtraRaw   string       `gorm:"type:longtext"`
	Extra      BillingExtra `gorm:"-"`
}

func (b *ServerBilling) BeforeSave(tx *gorm.DB) error {
	data, err := json.Marshal(b.Extra)
	if err != nil {
		return err
	}
	b.ExtraRaw = string(data)
	b.Currency = strings.ToUpper(strings.TrimSpace(b.Currency))
	if b.CycleCount < 1 {
		b.CycleCount = 1
	}
	if b.Status == 0 {
		b.Status = BillingStatusActive
	}
	return nil
}

func (b *ServerBilling) AfterFind(tx *gorm.DB) error {
	b.RemindDays = ParseRemindDays(b.RemindDaysRaw)
	if strings.TrimSpace(b.ExtraRaw) == "" {
		b.Extra = BillingExtra{}
		return nil
	}
	return json.Unmarshal([]byte(b.ExtraRaw), &b.Extra)
}

// EffectiveRemindDays 返回实际生效的提醒档位，未配置时回落到默认值。
func (b *ServerBilling) EffectiveRemindDays() []int {
	days := b.RemindDays
	if len(days) == 0 {
		days = ParseRemindDays(b.RemindDaysRaw)
	}
	if len(days) == 0 {
		return DefaultBillingRemindDays
	}
	return days
}

// DaysUntilDue 返回距离到期的天数，已过期为负数。没有设置到期日时返回 false。
//
// 按本地日期截断后相减，而不是 now.Sub(due)/24h：后者会因为两个时刻的时分秒不同
// 而在同一天内给出相差 1 天的结果。
func (b *ServerBilling) DaysUntilDue(now time.Time) (int, bool) {
	if b == nil || b.NextDueDate == nil || b.NextDueDate.IsZero() {
		return 0, false
	}
	today := truncateToLocalDate(now)
	due := truncateToLocalDate(*b.NextDueDate)
	return int(due.Sub(today).Hours() / 24), true
}

// Expired 判断是否已过期。没有到期日的视为未过期。
func (b *ServerBilling) Expired(now time.Time) bool {
	days, ok := b.DaysUntilDue(now)
	return ok && days < 0
}

// Active 判断订阅是否仍在计费中，已退订的不计入当前支出。
func (b *ServerBilling) Active() bool {
	return b != nil && b.Status != BillingStatusCancelled
}

// Ended 判断订阅是否已经走到终止日：决定不再续费的机器不必再提醒。
func (b *ServerBilling) Ended(now time.Time) bool {
	if b == nil || b.EndDate == nil || b.EndDate.IsZero() {
		return false
	}
	return !truncateToLocalDate(*b.EndDate).After(truncateToLocalDate(now))
}

// RemindedOn 判断给定日期当天是否已经提醒过，用于每日去重。
func (b *ServerBilling) RemindedOn(now time.Time) bool {
	return b != nil && b.LastRemindedOn != nil && SameLocalDate(*b.LastRemindedOn, now)
}

// SameLocalDate 判断两个时刻是否落在同一个本地日期。
func SameLocalDate(a, b time.Time) bool {
	ay, am, ad := a.In(time.Local).Date()
	by, bm, bd := b.In(time.Local).Date()
	return ay == by && am == bm && ad == bd
}

// AmountText 把金额格式化成两位小数，金额为 0 时返回空串（视为免费）。
func (b *ServerBilling) AmountText() string {
	if b == nil || b.AmountCents == 0 {
		return ""
	}
	return FormatAmountCents(b.AmountCents)
}

// CycleText 返回带周期数的中文周期名，如「每 2 年付」。
func (b *ServerBilling) CycleText() string {
	if b == nil || b.Cycle == "" {
		return ""
	}
	name := BillingCycleName(b.Cycle)
	if b.CycleCount > 1 {
		return fmt.Sprintf("每 %d 个%s周期", b.CycleCount, name)
	}
	return name
}

// PriceText 返回「$10.00 / 年付」形式的价格描述。
func (b *ServerBilling) PriceText() string {
	amount := b.AmountText()
	if amount == "" {
		return ""
	}
	text := strings.TrimSpace(b.Currency + " " + amount)
	if cycle := b.CycleText(); cycle != "" {
		return text + " / " + cycle
	}
	return text
}

// billingPayload 是回填后台编辑表单用的形状：日期为 YYYY-MM-DD、金额为十进制字符串，
// 套餐规格平铺到顶层，键名与表单 input 的 name 一一对应，前端按名字回填即可。
type billingPayload struct {
	Provider      string `json:"Provider"`
	ProductPlan   string `json:"ProductPlan"`
	PanelURL      string `json:"PanelURL"`
	OrderNo       string `json:"OrderNo"`
	Currency      string `json:"Currency"`
	Amount        string `json:"Amount"`
	Cycle         string `json:"Cycle"`
	CycleCount    int    `json:"CycleCount"`
	AutoRenew     bool   `json:"AutoRenew"`
	StartDate     string `json:"StartDate"`
	NextDueDate   string `json:"NextDueDate"`
	EndDate       string `json:"EndDate"`
	Status        uint8  `json:"Status"`
	RemindDaysRaw string `json:"RemindDaysRaw"`
	Muted         bool   `json:"Muted"`
	HideBilling   bool   `json:"HideBilling"`

	Bandwidth    string `json:"Bandwidth"`
	TrafficVol   string `json:"TrafficVol"`
	TrafficType  uint8  `json:"TrafficType"`
	TrafficReset int    `json:"TrafficReset"`
	IPv4Count    int    `json:"IPv4Count"`
	IPv6Count    int    `json:"IPv6Count"`
	NetworkRoute string `json:"NetworkRoute"`
	Location     string `json:"Location"`
	CPUSpec      string `json:"CPUSpec"`
	MemSpec      string `json:"MemSpec"`
	DiskSpec     string `json:"DiskSpec"`
}

// Marshal 生成后台编辑弹窗的回填数据。仅在管理页面渲染，包含金额等敏感字段。
func (b *ServerBilling) Marshal() template.JS {
	if b == nil {
		return template.JS("null")
	}
	payload := billingPayload{
		Provider:      b.Provider,
		ProductPlan:   b.ProductPlan,
		PanelURL:      b.PanelURL,
		OrderNo:       b.OrderNo,
		Currency:      b.Currency,
		Amount:        b.AmountText(),
		Cycle:         b.Cycle,
		CycleCount:    b.CycleCount,
		AutoRenew:     b.AutoRenew,
		StartDate:     FormatBillingDate(b.StartDate),
		NextDueDate:   FormatBillingDate(b.NextDueDate),
		EndDate:       FormatBillingDate(b.EndDate),
		Status:        b.Status,
		RemindDaysRaw: b.RemindDaysRaw,
		Muted:         b.Muted,
		HideBilling:   b.HideBilling,

		Bandwidth:    b.Extra.Bandwidth,
		TrafficVol:   b.Extra.TrafficVol,
		TrafficType:  b.Extra.TrafficType,
		TrafficReset: b.Extra.TrafficReset,
		IPv4Count:    b.Extra.IPv4Count,
		IPv6Count:    b.Extra.IPv6Count,
		NetworkRoute: b.Extra.NetworkRoute,
		Location:     b.Extra.Location,
		CPUSpec:      b.Extra.CPUSpec,
		MemSpec:      b.Extra.MemSpec,
		DiskSpec:     b.Extra.DiskSpec,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return template.JS("null")
	}
	return template.JS(data)
}

// PublicBilling 是允许游客看到的计费信息子集。
//
// 这是一份白名单，不是「排除列表」：金额、货币、订单编号、面板地址、
// 自动续费状态和付费流水永远不出现在这里，也不提供开关。
// 新增 ServerBilling 字段时默认不会流到前台，要公开必须显式加进来。
type PublicBilling struct {
	Provider     string `json:"Provider,omitempty"`
	ProductPlan  string `json:"ProductPlan,omitempty"`
	NextDueDate  string `json:"NextDueDate,omitempty"`
	DueDays      int    `json:"DueDays"`
	HasDue       bool   `json:"HasDue"`
	Lifetime     bool   `json:"Lifetime"`
	Location     string `json:"Location,omitempty"`
	Bandwidth    string `json:"Bandwidth,omitempty"`
	TrafficVol   string `json:"TrafficVol,omitempty"`
	TrafficType  string `json:"TrafficType,omitempty"`
	NetworkRoute string `json:"NetworkRoute,omitempty"`
	CPUSpec      string `json:"CPUSpec,omitempty"`
	MemSpec      string `json:"MemSpec,omitempty"`
	DiskSpec     string `json:"DiskSpec,omitempty"`
}

// TrafficTypeName 返回流量计算方式的中文名。
func TrafficTypeName(trafficType uint8) string {
	switch trafficType {
	case TrafficTypeSingle:
		return "单向"
	case TrafficTypeDouble:
		return "双向"
	default:
		return ""
	}
}

// PublicSnapshot 生成给游客看的计费信息。单独标记隐藏的返回 nil。
// 已退订的仍展示套餐规格，但不给出到期倒计时——那会让人以为机器要没了。
func (b *ServerBilling) PublicSnapshot(now time.Time) *PublicBilling {
	if b == nil || b.HideBilling {
		return nil
	}
	public := &PublicBilling{
		Provider:     b.Provider,
		ProductPlan:  b.ProductPlan,
		Location:     b.Extra.Location,
		Bandwidth:    b.Extra.Bandwidth,
		TrafficVol:   b.Extra.TrafficVol,
		TrafficType:  TrafficTypeName(b.Extra.TrafficType),
		NetworkRoute: b.Extra.NetworkRoute,
		CPUSpec:      b.Extra.CPUSpec,
		MemSpec:      b.Extra.MemSpec,
		DiskSpec:     b.Extra.DiskSpec,
	}
	if b.Active() {
		if days, ok := b.DaysUntilDue(now); ok {
			public.DueDays = days
			public.HasDue = true
			public.NextDueDate = FormatBillingDate(b.NextDueDate)
		} else if b.Cycle == BillingCycleOnetime {
			// 买断且没填到期日：前台该显示「永久」而不是什么都不显示。
			// 填了到期日的一次性付费是录错了，那时按到期倒计时展示。
			public.Lifetime = true
		}
	}
	return public
}

const billingDateLayout = "2006-01-02"

// AdvanceDue 从 from 推进 count 个 cycle 周期，返回新的到期日。
// 一次性付费和未知周期无法推进，返回 (from, false)。
//
// anchorDay 是账单锚定的「日」，通常取开通日的 Day()。给出后按它定位，
// 而不是按 from 自身的日，否则经过 2 月这类短月后到期日会永久前移：
// 1/31 -> 2/28 -> 3/28 -> ...，而正确结果是 1/31 -> 2/28 -> 3/31。
// 传 0 表示没有锚点，退回用 from 的日。
//
// 目标月没有这一天时取该月最后一天：Go 的 AddDate 是规范化而不是截断，
// 直接 AddDate(0,1,0) 会把 1/31 变成 3/3。
func AdvanceDue(from time.Time, cycle string, count int, anchorDay int) (time.Time, bool) {
	months := BillingCycleMonths(cycle)
	if months == 0 {
		return from, false
	}
	if count < 1 {
		count = 1
	}
	return addMonthsClamped(from, months*count, anchorDay), true
}

func addMonthsClamped(t time.Time, months int, anchorDay int) time.Time {
	year, month, day := t.Date()
	hour, minute, sec := t.Clock()
	if anchorDay > 0 {
		day = anchorDay
	}

	// 先定位到目标月的 1 号，让 time.Date 处理跨年进位，再决定「日」
	target := time.Date(year, month+time.Month(months), 1, hour, minute, sec, t.Nanosecond(), t.Location())
	if last := daysInMonth(target.Year(), target.Month()); day > last {
		day = last
	}
	return time.Date(target.Year(), target.Month(), day, hour, minute, sec, t.Nanosecond(), t.Location())
}

// daysInMonth 返回某年某月的天数。下个月的第 0 天就是本月最后一天。
func daysInMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// AnchorDay 返回账单锚定的日，未填开通日时返回 0。
func (b *ServerBilling) AnchorDay() int {
	if b == nil || b.StartDate == nil || b.StartDate.IsZero() {
		return 0
	}
	return b.StartDate.In(time.Local).Day()
}

// NextDue 按本订阅的周期从 from 推进一期。
func (b *ServerBilling) NextDue(from time.Time) (time.Time, bool) {
	if b == nil {
		return from, false
	}
	return AdvanceDue(from, b.Cycle, b.CycleCount, b.AnchorDay())
}

// ParseBillingDate 解析 YYYY-MM-DD，空串返回 nil 表示未设置。
// 按本地时区解析成当天零点，与 DaysUntilDue 的日期口径一致。
func ParseBillingDate(s string) (*time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	t, err := time.ParseInLocation(billingDateLayout, s, time.Local)
	if err != nil {
		return nil, fmt.Errorf("日期格式应为 YYYY-MM-DD：%s", s)
	}
	return &t, nil
}

// FormatBillingDate 把日期格式化成 YYYY-MM-DD，未设置返回空串。
func FormatBillingDate(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.In(time.Local).Format(billingDateLayout)
}

// FormatAmountCents 把最小单位金额格式化成两位小数，全程整数运算不经过浮点。
func FormatAmountCents(cents int64) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	return fmt.Sprintf("%s%d.%02d", sign, cents/100, cents%100)
}

// ParseAmount 把用户输入的十进制金额转成最小单位。容忍货币符号、千分位和空白。
func ParseAmount(s string) (int64, error) {
	s = strings.TrimSpace(s)
	for _, symbol := range []string{"$", "¥", "￥", "€", "£", ",", " "} {
		s = strings.ReplaceAll(s, symbol, "")
	}
	if s == "" {
		return 0, nil
	}
	negative := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")

	whole, frac, hasFrac := strings.Cut(s, ".")
	if whole == "" {
		whole = "0"
	}
	value, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, errors.New("金额格式错误：" + s)
	}
	cents := value * 100
	if hasFrac {
		switch len(frac) {
		case 0:
		case 1:
			frac += "0"
			fallthrough
		case 2:
			part, err := strconv.ParseInt(frac, 10, 64)
			if err != nil {
				return 0, errors.New("金额格式错误：" + s)
			}
			cents += part
		default:
			return 0, errors.New("金额最多保留两位小数：" + s)
		}
	}
	if negative {
		cents = -cents
	}
	return cents, nil
}

// ParseRemindDays 解析 "30,7,3,1" 形式的提醒档位，去重后按从大到小排序。
func ParseRemindDays(raw string) []int {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	seen := make(map[int]bool)
	var days []int
	for _, item := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '，' || r == ' ' || r == '\n' || r == '\t'
	}) {
		day, err := strconv.Atoi(strings.TrimSpace(item))
		if err != nil || day < 0 || seen[day] {
			continue
		}
		seen[day] = true
		days = append(days, day)
	}
	for i := 0; i < len(days); i++ {
		for j := i + 1; j < len(days); j++ {
			if days[j] > days[i] {
				days[i], days[j] = days[j], days[i]
			}
		}
	}
	return days
}

func truncateToLocalDate(t time.Time) time.Time {
	local := t.In(time.Local)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.Local)
}
