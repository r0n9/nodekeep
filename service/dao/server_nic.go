package dao

import (
	"sort"
	"strings"
	"time"

	"gorm.io/gorm/clause"

	"github.com/r0n9/nodekeep/model"
)

// legacyNICKey 表示该 Agent 未上报网卡明细、只能使用聚合字段的口径。
const legacyNICKey = "legacy"

// sumSelectedNICs 按选中集合对网卡明细求和。
//
// selected 为空表示「自动」：统计所有非虚拟网卡，与 Agent 侧的默认口径一致。
// 返回的 nicKey 是实际参与求和的网卡名指纹，调用方用它识别统计口径变化；
// hasNICs 为 false 表示该 Agent 未上报网卡明细，调用方应回落到聚合字段。
//
// 指纹取「实际参与求和的网卡名」而不是选中配置本身，是因为虚拟网卡中途
// 出现/消失同样会让求和结果阶跃，那种情况也必须重设基线。
func sumSelectedNICs(counters []model.NICCounter, selected []string) (in, out uint64, nicKey string, hasNICs bool) {
	if len(counters) == 0 {
		return 0, 0, legacyNICKey, false
	}

	var allow map[string]struct{}
	if len(selected) > 0 {
		allow = make(map[string]struct{}, len(selected))
		for _, name := range selected {
			allow[name] = struct{}{}
		}
	}

	names := make([]string, 0, len(counters))
	for _, counter := range counters {
		if counter.Name == "" {
			continue
		}
		if allow != nil {
			if _, ok := allow[counter.Name]; !ok {
				continue
			}
		} else if counter.Virtual {
			continue
		}
		names = append(names, counter.Name)
		in += counter.BytesRecv
		out += counter.BytesSent
	}
	sort.Strings(names)
	return in, out, "nics:" + strings.Join(names, ","), true
}

// serverNICRefreshInterval 是网卡清单未变化时刷新 last_seen 的间隔。
// 清单本身只在变化时写库，但 last_seen 需要定期推进，
// 否则长期稳定的网卡会被误判成「已消失」而在后台被置灰。
const serverNICRefreshInterval = 10 * time.Minute

type serverNICSeenState struct {
	names     map[string]struct{}
	writtenAt time.Time
}

// syncServerNICs 在网卡集合变化（或 last_seen 过期）时更新网卡清单。
// Agent 每 2 秒上报一次，这里必须做去重，否则会把库写爆。
func syncServerNICs(serverID uint64, counters []model.NICCounter, now time.Time) {
	if serverID == 0 || len(counters) == 0 {
		return
	}
	names := make(map[string]struct{}, len(counters))
	for _, counter := range counters {
		if counter.Name != "" {
			names[counter.Name] = struct{}{}
		}
	}
	if len(names) == 0 {
		return
	}

	serverNICSeenLock.Lock()
	seen := serverNICSeen[serverID]
	if seen != nil && sameNICSet(seen.names, names) && now.Sub(seen.writtenAt) < serverNICRefreshInterval {
		serverNICSeenLock.Unlock()
		return
	}
	serverNICSeen[serverID] = &serverNICSeenState{names: names, writtenAt: now}
	serverNICSeenLock.Unlock()

	UpsertServerNICs(serverID, counters, now)
}

func sameNICSet(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for name := range a {
		if _, ok := b[name]; !ok {
			return false
		}
	}
	return true
}

// UpsertServerNICs 记录某台服务器本次上报的网卡清单。
// 由调用方保证仅在网卡集合发生变化时调用，避免每次上报都写库。
func UpsertServerNICs(serverID uint64, counters []model.NICCounter, now time.Time) {
	if DB == nil || serverID == 0 || len(counters) == 0 {
		return
	}
	for _, counter := range counters {
		if counter.Name == "" {
			continue
		}
		row := model.ServerNIC{
			ServerID: serverID,
			Name:     counter.Name,
			Virtual:  counter.Virtual,
			LastSeen: now,
		}
		DB.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "server_id"}, {Name: "name"}},
			DoUpdates: clause.AssignmentColumns([]string{"virtual", "last_seen", "updated_at"}),
		}).Create(&row)
	}
}

// ServerNICList 返回某台服务器已知的网卡清单，按名称升序。
func ServerNICList(serverID uint64) []model.ServerNIC {
	if DB == nil || serverID == 0 {
		return make([]model.ServerNIC, 0)
	}
	var nics []model.ServerNIC
	_ = DB.Where("server_id = ?", serverID).Order("name ASC").Find(&nics).Error
	if nics == nil {
		nics = make([]model.ServerNIC, 0)
	}
	return nics
}

// DeleteServerNICs 清除某台服务器的网卡记录，随服务器删除一起调用。
func DeleteServerNICs(serverID uint64) {
	if DB == nil || serverID == 0 {
		return
	}
	DB.Where("server_id = ?", serverID).Delete(&model.ServerNIC{})
}
