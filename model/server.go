package model

import (
	"encoding/json"
	"fmt"
	"html/template"
	"strings"
	"time"
)

// 流量统计网卡选择的上限。网卡名由 Agent 上报，属于不可信输入，
// 落库前必须限长限量，避免配置被塞成超长字符串。
const (
	// MaxTrafficNICs 是单台服务器可选的网卡数量上限。
	MaxTrafficNICs = 16
	// MaxNICNameLength 是单个网卡名的长度上限。
	MaxNICNameLength = 64
)

type Server struct {
	Common
	Name         string
	Tag          string // 分组名
	Secret       string `gorm:"uniqueIndex" json:"-"`
	Note         string `json:"-"` // 管理员可见备注
	DisplayIndex int    // 展示排序，越大越靠前
	// TrafficNICs 是参与流量统计的网卡名，逗号分隔；空串表示自动
	// （统计所有非虚拟网卡）。属于监控配置而非订阅信息，所以不放进 ServerBilling。
	TrafficNICs string
}

// ParseNICSelection 解析逗号分隔的网卡选择配置，去空白、去重并保持原顺序。
func ParseNICSelection(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	seen := make(map[string]struct{})
	names := make([]string, 0, 4)
	for _, item := range strings.Split(raw, ",") {
		name := strings.TrimSpace(item)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
}

// FormatNICSelection 把网卡名列表序列化成存储格式。
func FormatNICSelection(names []string) string {
	return strings.Join(names, ",")
}

type ServerRuntime struct {
	Server
	Host       *Host                  `gorm:"-"`
	State      *HostState             `gorm:"-"`
	LastActive time.Time              `gorm:"-"`
	Traffic    *ServerTrafficSnapshot `gorm:"-"`
	// Billing 是订阅信息的只读快照，由 service/dao 在计费信息变更后刷新。
	// 挂在内存态上是因为告警规则的 Snapshot 在 model 包内取值，不能反向依赖 dao 查库。
	Billing *ServerBilling `gorm:"-"`
}

type PublicHost struct {
	Platform        string
	PlatformVersion string
	CPU             []string
	MemTotal        uint64
	DiskTotal       uint64
	SwapTotal       uint64
	Arch            string
	Virtualization  string
	BootTime        uint64
	CountryCode     string
}

type PublicServerRuntime struct {
	ID         uint64
	Name       string
	Tag        string
	Host       *PublicHost
	State      *HostState
	LastActive time.Time
	Traffic    *ServerTrafficSnapshot `json:"Traffic,omitempty"`
}

func (s Server) Marshal() template.JS {
	name, _ := json.Marshal(s.Name)
	tag, _ := json.Marshal(s.Tag)
	note, _ := json.Marshal(s.Note)
	secret, _ := json.Marshal(s.Secret)
	// 网卡选择按存储原样（逗号分隔字符串）下发，保持 Marshal 输出与 Server
	// 字段一一对应、可原样反序列化；前端自行 split。
	trafficNICs, _ := json.Marshal(s.TrafficNICs)
	return template.JS(fmt.Sprintf(`{"ID":%d,"Name":%s,"Secret":%s,"DisplayIndex":%d,"Tag":%s,"Note":%s,"TrafficNICs":%s}`,
		s.ID, name, secret, s.DisplayIndex, tag, note, trafficNICs))
}
