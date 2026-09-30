package dao

import (
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/r0n9/nodekeep/model"
)

type trafficBillingConfig struct {
	TrafficReset    int
	TrafficVol      string
	TrafficType     uint8
	trafficVolBytes uint64
}

type serverTrafficTracker struct {
	serverID    uint64
	todayDate   string // YYYY-MM-DD
	cycleStart  string // YYYY-MM-DD
	cycleEnd    string // YYYY-MM-DD
	todayNetIn  uint64
	todayNetOut uint64
	cycleNetIn  uint64
	cycleNetOut uint64
	totalNetIn  uint64
	totalNetOut uint64
	pendingIn   uint64
	pendingOut  uint64
	lastRawIn   uint64
	lastRawOut  uint64
	hasLastRaw  bool
	initialized bool
}

var (
	trafficLock      sync.RWMutex
	trafficTrackers  = make(map[uint64]*serverTrafficTracker)
	trafficSettings  = make(map[uint64]trafficBillingConfig)
	trafficInitMutex sync.Mutex
)

// InitTrafficState 初始化或重置内存中的流量跟踪状态。
func InitTrafficState() {
	trafficLock.Lock()
	defer trafficLock.Unlock()
	trafficTrackers = make(map[uint64]*serverTrafficTracker)
	trafficSettings = make(map[uint64]trafficBillingConfig)
}

// SetTrafficBillingConfig 设置或更新某台服务器的计费周期配置。
func SetTrafficBillingConfig(serverID uint64, resetDay int, vol string, trafficType uint8) {
	if resetDay <= 0 || resetDay > 31 {
		resetDay = 1
	}
	volBytes, _ := model.ParseTrafficBytes(vol)

	trafficLock.Lock()
	defer trafficLock.Unlock()
	trafficSettings[serverID] = trafficBillingConfig{
		TrafficReset:    resetDay,
		TrafficVol:      vol,
		TrafficType:     trafficType,
		trafficVolBytes: volBytes,
	}

	// 如果该服务器已有 tracker，更新其当前周期的起止范围
	if tr := trafficTrackers[serverID]; tr != nil && tr.initialized {
		now := time.Now()
		cStart, cEnd := model.BillingCycleRange(now, resetDay)
		if tr.cycleStart != cStart || tr.cycleEnd != cEnd {
			tr.cycleStart = cStart
			tr.cycleEnd = cEnd
			// 重新从数据库汇总新周期的历史天流量
			recalcCycleTrafficLocked(tr)
		}
	}
}

// RemoveTrafficBillingConfig 移除某台服务器的计费配置。
func RemoveTrafficBillingConfig(serverID uint64) {
	trafficLock.Lock()
	defer trafficLock.Unlock()
	delete(trafficSettings, serverID)
}

// LoadAllTrafficBillingConfigs 从数据库加载全部服务器的订阅与流量配置。
func LoadAllTrafficBillingConfigs() {
	if DB == nil {
		return
	}
	var billings []model.ServerBilling
	if err := DB.Find(&billings).Error; err != nil {
		return
	}
	for _, b := range billings {
		SetTrafficBillingConfig(b.ServerID, b.Extra.TrafficReset, b.Extra.TrafficVol, b.Extra.TrafficType)
	}
}

func recalcCycleTrafficLocked(tr *serverTrafficTracker) {
	if DB == nil || tr == nil {
		return
	}
	var result struct {
		SumIn  uint64
		SumOut uint64
	}
	_ = DB.Model(&model.ServerTrafficDaily{}).
		Select("COALESCE(SUM(net_in_bytes), 0) as sum_in, COALESCE(SUM(net_out_bytes), 0) as sum_out").
		Where("server_id = ? AND date >= ? AND date < ?", tr.serverID, tr.cycleStart, tr.cycleEnd).
		Scan(&result).Error

	tr.cycleNetIn = result.SumIn + tr.pendingIn
	tr.cycleNetOut = result.SumOut + tr.pendingOut
}

// ProcessServerTraffic 处理 Agent 上报的网卡原始计数，返回跨重启持久累计值和实时流量快照。
func ProcessServerTraffic(serverID uint64, rawIn, rawOut uint64, now time.Time) (uint64, uint64, *model.ServerTrafficSnapshot) {
	if serverID == 0 {
		return rawIn, rawOut, nil
	}

	trafficLock.Lock()
	defer trafficLock.Unlock()

	tracker := trafficTrackers[serverID]
	if tracker == nil {
		tracker = &serverTrafficTracker{
			serverID: serverID,
		}
		trafficTrackers[serverID] = tracker
	}

	setting := trafficSettings[serverID]
	resetDay := setting.TrafficReset
	if resetDay <= 0 || resetDay > 31 {
		resetDay = 1
	}

	curDate := now.In(time.Local).Format("2006-01-02")
	cycleStart, cycleEnd := model.BillingCycleRange(now, resetDay)

	if !tracker.initialized {
		tracker.todayDate = curDate
		tracker.cycleStart = cycleStart
		tracker.cycleEnd = cycleEnd

		// 从持久化库读取累计历史总量
		if DB != nil {
			var st []model.ServerTraffic
			if err := DB.Where("server_id = ?", serverID).Limit(1).Find(&st).Error; err == nil && len(st) > 0 {
				tracker.totalNetIn = st[0].TotalNetIn
				tracker.totalNetOut = st[0].TotalNetOut
			} else {
				// 数据库尚无记录（首次接入），以当前 Agent 上报值作为基线
				tracker.totalNetIn = rawIn
				tracker.totalNetOut = rawOut
			}

			// 读取当日流量
			var todayRows []model.ServerTrafficDaily
			if err := DB.Where("server_id = ? AND date = ?", serverID, curDate).Limit(1).Find(&todayRows).Error; err == nil && len(todayRows) > 0 {
				tracker.todayNetIn = todayRows[0].NetInBytes
				tracker.todayNetOut = todayRows[0].NetOutBytes
			}

			// 汇总本周期已落库的流量
			var cycleRow struct {
				SumIn  uint64
				SumOut uint64
			}
			_ = DB.Model(&model.ServerTrafficDaily{}).
				Select("COALESCE(SUM(net_in_bytes), 0) as sum_in, COALESCE(SUM(net_out_bytes), 0) as sum_out").
				Where("server_id = ? AND date >= ? AND date < ?", serverID, cycleStart, cycleEnd).
				Scan(&cycleRow).Error
			tracker.cycleNetIn = cycleRow.SumIn
			tracker.cycleNetOut = cycleRow.SumOut
		} else {
			tracker.totalNetIn = rawIn
			tracker.totalNetOut = rawOut
		}

		tracker.lastRawIn = rawIn
		tracker.lastRawOut = rawOut
		tracker.hasLastRaw = true
		tracker.initialized = true
	} else {
		// 跨天检查
		if curDate != tracker.todayDate {
			flushPendingTrafficLocked(tracker, now)
			tracker.todayDate = curDate
			tracker.todayNetIn = 0
			tracker.todayNetOut = 0

			// 检查是否进入了新的账单周期
			if curDate >= tracker.cycleEnd || curDate < tracker.cycleStart {
				tracker.cycleStart = cycleStart
				tracker.cycleEnd = cycleEnd
				tracker.cycleNetIn = 0
				tracker.cycleNetOut = 0
			}
		}

		// 计算增量 Delta（能自动处理节点重启重置）
		deltaIn := positiveCounterDelta(rawIn, tracker.lastRawIn)
		deltaOut := positiveCounterDelta(rawOut, tracker.lastRawOut)

		tracker.todayNetIn += deltaIn
		tracker.todayNetOut += deltaOut
		tracker.cycleNetIn += deltaIn
		tracker.cycleNetOut += deltaOut
		tracker.totalNetIn += deltaIn
		tracker.totalNetOut += deltaOut

		tracker.pendingIn += deltaIn
		tracker.pendingOut += deltaOut

		tracker.lastRawIn = rawIn
		tracker.lastRawOut = rawOut
	}

	cycleUsed := model.CalculateCycleUsed(setting.TrafficType, tracker.cycleNetIn, tracker.cycleNetOut)
	var cyclePercent float64
	if setting.trafficVolBytes > 0 {
		cyclePercent = (float64(cycleUsed) / float64(setting.trafficVolBytes)) * 100.0
	}

	snapshot := &model.ServerTrafficSnapshot{
		TodayNetIn:       tracker.todayNetIn,
		TodayNetOut:      tracker.todayNetOut,
		CycleNetIn:       tracker.cycleNetIn,
		CycleNetOut:      tracker.cycleNetOut,
		TotalNetIn:       tracker.totalNetIn,
		TotalNetOut:      tracker.totalNetOut,
		CycleTrafficVol:  setting.trafficVolBytes,
		CycleTrafficType: setting.TrafficType,
		CycleUsed:        cycleUsed,
		CyclePercent:     cyclePercent,
		CycleResetDay:    resetDay,
	}

	return tracker.totalNetIn, tracker.totalNetOut, snapshot
}

// FlushServerTraffic 将指定服务器尚未落盘的流量增量持久化到 SQLite。
func FlushServerTraffic(serverID uint64, now time.Time) {
	if DB == nil || serverID == 0 {
		return
	}
	trafficLock.Lock()
	defer trafficLock.Unlock()

	tracker := trafficTrackers[serverID]
	if tracker == nil || !tracker.initialized {
		return
	}
	flushPendingTrafficLocked(tracker, now)
}

func flushPendingTrafficLocked(tracker *serverTrafficTracker, now time.Time) {
	if DB == nil || tracker == nil {
		return
	}
	if tracker.pendingIn == 0 && tracker.pendingOut == 0 {
		return
	}

	pendingIn := tracker.pendingIn
	pendingOut := tracker.pendingOut
	tracker.pendingIn = 0
	tracker.pendingOut = 0

	// 1. 原子累加到当天的 ServerTrafficDaily
	daily := model.ServerTrafficDaily{
		ServerID:    tracker.serverID,
		Date:        tracker.todayDate,
		NetInBytes:  pendingIn,
		NetOutBytes: pendingOut,
	}
	DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "server_id"}, {Name: "date"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"net_in_bytes":  gorm.Expr("server_traffic_dailies.net_in_bytes + excluded.net_in_bytes"),
			"net_out_bytes": gorm.Expr("server_traffic_dailies.net_out_bytes + excluded.net_out_bytes"),
			"updated_at":    now,
		}),
	}).Create(&daily)

	// 2. 更新 ServerTraffic 的总累计
	st := model.ServerTraffic{
		ServerID:    tracker.serverID,
		TotalNetIn:  tracker.totalNetIn,
		TotalNetOut: tracker.totalNetOut,
	}
	DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "server_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"total_net_in", "total_net_out", "updated_at"}),
	}).Create(&st)
}

// GetServerTrafficHistory 查询指定服务器近 N 天的每日流量数据（按日期升序排序）。
func GetServerTrafficHistory(serverID uint64, days int) []model.ServerTrafficDaily {
	if DB == nil || serverID == 0 {
		return make([]model.ServerTrafficDaily, 0)
	}
	if days <= 0 {
		days = 30
	}
	since := time.Now().In(time.Local).AddDate(0, 0, -days+1).Format("2006-01-02")
	var records []model.ServerTrafficDaily
	_ = DB.Where("server_id = ? AND date >= ?", serverID, since).
		Order("date ASC").
		Find(&records).Error

	trafficLock.Lock()
	defer trafficLock.Unlock()

	tracker := trafficTrackers[serverID]
	if tracker != nil && tracker.initialized {
		today := tracker.todayDate
		found := false
		for i := range records {
			if records[i].Date == today {
				records[i].NetInBytes = tracker.todayNetIn
				records[i].NetOutBytes = tracker.todayNetOut
				found = true
				break
			}
		}
		if !found && today >= since {
			records = append(records, model.ServerTrafficDaily{
				ServerID:    serverID,
				Date:        today,
				NetInBytes:  tracker.todayNetIn,
				NetOutBytes: tracker.todayNetOut,
			})
		}
	}

	if records == nil {
		records = make([]model.ServerTrafficDaily, 0)
	}
	return records
}
