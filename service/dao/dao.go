package dao

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/patrickmn/go-cache"
	"github.com/robfig/cron/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/r0n9/nodekeep/model"
	"github.com/r0n9/nodekeep/pkg/geoip"
	pb "github.com/r0n9/nodekeep/proto"
)

const (
	SnapshotDelay = 3
	ReportDelay   = 2
)

var (
	Conf  *model.Config
	Cache *cache.Cache
	DB    *gorm.DB

	serverList map[uint64]*serverSession
	secretToID map[string]uint64
	serverLock sync.RWMutex

	sortedServerList []*serverSession
	sortedServerLock sync.RWMutex

	serverMetricBuckets map[uint64]*serverMetricBucket
	serverMetricLock    sync.Mutex
)

var errTaskStreamReplaced = errors.New("task stream replaced")
var errTaskStreamUnavailable = errors.New("task stream unavailable")

type serverSession struct {
	runtime    model.ServerRuntime
	taskClose  chan error
	taskStream pb.ProbeService_RequestTaskServer
	sendLock   sync.Mutex
}

type TaskTarget struct {
	ServerID uint64
	stream   pb.ProbeService_RequestTaskServer
	sendLock *sync.Mutex
}

type serverMetricBucket struct {
	metric          model.ServerMetric
	lastInTransfer  uint64
	lastOutTransfer uint64
	hasLastTransfer bool
}

func (t TaskTarget) Send(task *pb.Task) error {
	if t.stream == nil || t.sendLock == nil {
		return errTaskStreamUnavailable
	}
	t.sendLock.Lock()
	defer t.sendLock.Unlock()
	if err := t.stream.Send(task); err != nil {
		DetachTaskStream(t.ServerID, t.stream)
		return err
	}
	return nil
}

func InitServerRuntimeState() {
	serverLock.Lock()
	defer serverLock.Unlock()

	serverList = make(map[uint64]*serverSession)
	secretToID = make(map[string]uint64)
	sortedServerLock.Lock()
	sortedServerList = nil
	sortedServerLock.Unlock()
	serverMetricLock.Lock()
	serverMetricBuckets = make(map[uint64]*serverMetricBucket)
	serverMetricLock.Unlock()
	InitTrafficState()
}

func cloneHost(h *model.Host) *model.Host {
	if h == nil {
		return nil
	}
	clone := *h
	if h.CPU != nil {
		clone.CPU = append([]string(nil), h.CPU...)
	}
	return &clone
}

func cloneState(s *model.HostState) *model.HostState {
	if s == nil {
		return nil
	}
	clone := *s
	return &clone
}

func cloneTrafficSnapshot(t *model.ServerTrafficSnapshot) *model.ServerTrafficSnapshot {
	if t == nil {
		return nil
	}
	clone := *t
	return &clone
}

func cloneServerRuntime(s *model.ServerRuntime) *model.ServerRuntime {
	if s == nil {
		return nil
	}
	clone := *s
	clone.Host = cloneHost(s.Host)
	clone.State = cloneState(s.State)
	clone.Traffic = cloneTrafficSnapshot(s.Traffic)
	return &clone
}

func publicHostSnapshot(h *model.Host) *model.PublicHost {
	if h == nil {
		return nil
	}
	host := &model.PublicHost{
		Platform:        h.Platform,
		PlatformVersion: h.PlatformVersion,
		MemTotal:        h.MemTotal,
		DiskTotal:       h.DiskTotal,
		SwapTotal:       h.SwapTotal,
		Arch:            h.Arch,
		Virtualization:  h.Virtualization,
		BootTime:        h.BootTime,
		CountryCode:     h.CountryCode,
	}
	if h.CPU != nil {
		host.CPU = append([]string(nil), h.CPU...)
	}
	return host
}

func publicServerRuntimeSnapshot(s *model.ServerRuntime) *model.PublicServerRuntime {
	if s == nil {
		return nil
	}
	return &model.PublicServerRuntime{
		ID:         s.ID,
		Name:       s.Name,
		Tag:        s.Tag,
		Host:       publicHostSnapshot(s.Host),
		State:      cloneState(s.State),
		LastActive: s.LastActive,
		Traffic:    cloneTrafficSnapshot(s.Traffic),
	}
}

func PublicServerSnapshot(id uint64) (*model.PublicServerRuntime, bool) {
	serverLock.RLock()
	defer serverLock.RUnlock()
	s := serverList[id]
	if s == nil {
		return nil, false
	}
	return publicServerRuntimeSnapshot(&s.runtime), true
}

func UpsertServerRuntime(s model.Server, isEdit bool) {
	serverLock.Lock()
	if isEdit {
		if old := serverList[s.ID]; old != nil {
			delete(secretToID, old.runtime.Secret)
			old.runtime.Server = s
			if old.runtime.Host == nil {
				old.runtime.Host = &model.Host{}
			}
			if old.runtime.State == nil {
				old.runtime.State = &model.HostState{}
			}
			secretToID[s.Secret] = s.ID
			serverLock.Unlock()
			ReSortServer()
			return
		}
	}
	secretToID[s.Secret] = s.ID
	serverList[s.ID] = &serverSession{
		runtime: model.ServerRuntime{
			Server: s,
			Host:   &model.Host{},
			State:  &model.HostState{},
		},
	}
	serverLock.Unlock()
	ReSortServer()
}

func DeleteServerRuntime(id uint64) {
	serverLock.Lock()
	if s := serverList[id]; s != nil {
		delete(secretToID, s.runtime.Secret)
		if s.taskClose != nil {
			select {
			case s.taskClose <- errTaskStreamReplaced:
			default:
			}
		}
	}
	delete(serverList, id)
	serverLock.Unlock()
	ReSortServer()
}

func ResolveClientID(clientSecret string) (uint64, bool) {
	serverLock.RLock()
	defer serverLock.RUnlock()
	clientID, hasID := secretToID[clientSecret]
	_, hasServer := serverList[clientID]
	return clientID, hasID && hasServer
}

func AttachTaskStream(clientID uint64, stream pb.ProbeService_RequestTaskServer, closeCh chan error) {
	serverLock.Lock()
	defer serverLock.Unlock()
	s := serverList[clientID]
	if s == nil {
		return
	}
	if s.taskClose != nil && s.taskClose != closeCh {
		select {
		case s.taskClose <- errTaskStreamReplaced:
		default:
		}
	}
	s.taskStream = stream
	s.taskClose = closeCh
}

func DetachTaskStream(clientID uint64, stream pb.ProbeService_RequestTaskServer) {
	serverLock.Lock()
	defer serverLock.Unlock()
	s := serverList[clientID]
	if s == nil || s.taskStream != stream {
		return
	}
	s.taskStream = nil
	s.taskClose = nil
}

func UpdateServerState(clientID uint64, state model.HostState, now time.Time) {
	serverLock.Lock()
	var host *model.Host
	var found bool
	if s := serverList[clientID]; s != nil {
		s.runtime.LastActive = now
		s.runtime.State = &state
		host = cloneHost(s.runtime.Host)
		found = true
	}
	serverLock.Unlock()
	if !found {
		return
	}
	ObserveServerMetric(clientID, state, host, now)
}

func UpdateServerHost(clientID uint64, host model.Host, enableIPChangeNotification bool) (name, oldIP, newIP string, changed bool) {
	host.CountryCode = resolveCountryCode(host)

	serverLock.Lock()
	defer serverLock.Unlock()
	s := serverList[clientID]
	if s == nil {
		return "", "", "", false
	}
	if enableIPChangeNotification &&
		s.runtime.Host != nil &&
		s.runtime.Host.IP != "" &&
		host.IP != "" &&
		s.runtime.Host.IP != host.IP {
		name = s.runtime.Name
		oldIP = s.runtime.Host.IP
		newIP = host.IP
		changed = true
	}
	s.runtime.Host = &host
	return
}

func resolveCountryCode(host model.Host) string {
	if code, err := geoip.LookupCountryCode(host.IP); err == nil && code != "" {
		return code
	}
	return strings.ToLower(strings.TrimSpace(host.CountryCode))
}

func SortedServerSnapshot() []*model.ServerRuntime {
	serverLock.RLock()
	defer serverLock.RUnlock()
	sortedServerLock.RLock()
	defer sortedServerLock.RUnlock()

	servers := make([]*model.ServerRuntime, 0, len(sortedServerList))
	for _, s := range sortedServerList {
		servers = append(servers, cloneServerRuntime(&s.runtime))
	}
	sort.SliceStable(servers, func(i, j int) bool {
		return lessServerRuntimeByGroupOrderID(servers[i], servers[j])
	})
	return servers
}

func lessServerRuntimeByGroupOrderID(a, b *model.ServerRuntime) bool {
	if a == nil || b == nil {
		return b != nil
	}
	if a.Tag != b.Tag {
		return a.Tag > b.Tag
	}
	if a.DisplayIndex != b.DisplayIndex {
		return a.DisplayIndex > b.DisplayIndex
	}
	return a.ID > b.ID
}

func SortedPublicServerSnapshot() []*model.PublicServerRuntime {
	serverLock.RLock()
	defer serverLock.RUnlock()
	sortedServerLock.RLock()
	defer sortedServerLock.RUnlock()

	servers := make([]*model.PublicServerRuntime, 0, len(sortedServerList))
	for _, s := range sortedServerList {
		servers = append(servers, publicServerRuntimeSnapshot(&s.runtime))
	}
	return servers
}

func ServerSnapshot() []*model.ServerRuntime {
	serverLock.RLock()
	defer serverLock.RUnlock()

	servers := make([]*model.ServerRuntime, 0, len(serverList))
	for _, s := range serverList {
		servers = append(servers, cloneServerRuntime(&s.runtime))
	}
	return servers
}

func ObserveServerMetric(serverID uint64, state model.HostState, host *model.Host, now time.Time) {
	if serverID == 0 || now.IsZero() {
		return
	}
	bucketAt := now.Truncate(time.Minute)
	serverMetricLock.Lock()
	defer serverMetricLock.Unlock()
	if serverMetricBuckets == nil {
		serverMetricBuckets = make(map[uint64]*serverMetricBucket)
	}

	bucket := serverMetricBuckets[serverID]
	if bucket == nil {
		bucket = newServerMetricBucket(serverID, bucketAt)
		serverMetricBuckets[serverID] = bucket
	}
	if !bucket.metric.BucketAt.Equal(bucketAt) {
		flushServerMetricLocked(bucket.metric)
		FlushServerTraffic(serverID, now)
		next := newServerMetricBucket(serverID, bucketAt)
		next.lastInTransfer = bucket.lastInTransfer
		next.lastOutTransfer = bucket.lastOutTransfer
		next.hasLastTransfer = bucket.hasLastTransfer
		bucket = next
		serverMetricBuckets[serverID] = bucket
	}

	netInBytes, netOutBytes := uint64(0), uint64(0)
	if bucket.hasLastTransfer {
		netInBytes = positiveCounterDelta(state.NetInTransfer, bucket.lastInTransfer)
		netOutBytes = positiveCounterDelta(state.NetOutTransfer, bucket.lastOutTransfer)
	}
	addServerMetricSample(&bucket.metric, state, host, netInBytes, netOutBytes)
	bucket.lastInTransfer = state.NetInTransfer
	bucket.lastOutTransfer = state.NetOutTransfer
	bucket.hasLastTransfer = true

	totalIn, totalOut, trafficSnapshot := ProcessServerTraffic(serverID, state.NetInTransfer, state.NetOutTransfer, now)
	serverLock.Lock()
	if s := serverList[serverID]; s != nil {
		if s.runtime.State != nil {
			s.runtime.State.NetInTransfer = totalIn
			s.runtime.State.NetOutTransfer = totalOut
		}
		s.runtime.Traffic = trafficSnapshot
	}
	serverLock.Unlock()
}

func ServerMetricSnapshot(serverID uint64, since time.Time) []model.ServerMetric {
	return serverMetricSnapshot(serverID, since, 0)
}

// ServerMetricSeriesSnapshot returns a compact downsampled series for charts.
// afterUnix > 0 returns buckets at/after that unix second so the open step can refresh.
// maxPoints caps series length by increasing step when needed (default 720).
func ServerMetricSeriesSnapshot(serverID uint64, rangeKey string, since time.Time, afterUnix int64, maxPoints int) model.ServerMetricSeries {
	stepSec := serverMetricStepSec(rangeKey)
	if maxPoints <= 0 {
		maxPoints = 720
	}
	if maxPoints < 60 {
		maxPoints = 60
	}
	if maxPoints > 2000 {
		maxPoints = 2000
	}

	windowSec := serverMetricWindowSec(rangeKey)
	if windowSec < stepSec {
		windowSec = stepSec
	}
	dynamicStep := windowSec / int64(maxPoints)
	if dynamicStep > stepSec {
		stepSec = ((dynamicStep + 59) / 60) * 60
	}
	if stepSec < 60 {
		stepSec = 60
	}

	// For incremental polls, read raw minutes after cursor then downsample so the
	// latest partial step bucket can still update.
	queryAfter := int64(0)
	if afterUnix > 0 {
		queryAfter = afterUnix - stepSec
		if queryAfter < 0 {
			queryAfter = 0
		}
	}
	metrics := serverMetricSnapshot(serverID, since, queryAfter)
	series := downsampleServerMetrics(metrics, stepSec)
	series.Range = normalizeMetricRangeKey(rangeKey)
	series.StepSec = stepSec

	if afterUnix > 0 {
		filtered := emptyMetricSeries(series.Range, stepSec, len(series.T))
		for i, ts := range series.T {
			// Keep the open bucket (ts == afterUnix) so the latest step can refresh.
			if ts < afterUnix {
				continue
			}
			filtered.T = append(filtered.T, ts)
			filtered.CPU = append(filtered.CPU, series.CPU[i])
			filtered.Mem = append(filtered.Mem, series.Mem[i])
			filtered.MemUsed = append(filtered.MemUsed, series.MemUsed[i])
			filtered.Disk = append(filtered.Disk, series.Disk[i])
			filtered.DiskUsed = append(filtered.DiskUsed, series.DiskUsed[i])
			filtered.NetIn = append(filtered.NetIn, series.NetIn[i])
			filtered.NetOut = append(filtered.NetOut, series.NetOut[i])
		}
		series = filtered
	}

	series.Count = len(series.T)
	if series.Count > 0 {
		series.From = series.T[0]
		series.To = series.T[series.Count-1]
	}
	return series
}

func normalizeMetricRangeKey(rangeKey string) string {
	switch strings.ToLower(strings.TrimSpace(rangeKey)) {
	case "3", "3d", "3day", "3days":
		return "3d"
	case "7", "7d", "7day", "7days":
		return "7d"
	default:
		return "today"
	}
}

func serverMetricStepSec(rangeKey string) int64 {
	switch normalizeMetricRangeKey(rangeKey) {
	case "3d":
		return 5 * 60
	case "7d":
		return 15 * 60
	default:
		return 60
	}
}

func serverMetricWindowSec(rangeKey string) int64 {
	switch normalizeMetricRangeKey(rangeKey) {
	case "3d":
		return 3 * 24 * 60 * 60
	case "7d":
		return 7 * 24 * 60 * 60
	default:
		return 24 * 60 * 60
	}
}

func emptyMetricSeries(rangeKey string, stepSec int64, capacity int) model.ServerMetricSeries {
	if capacity < 0 {
		capacity = 0
	}
	return model.ServerMetricSeries{
		Range:    rangeKey,
		StepSec:  stepSec,
		T:        make([]int64, 0, capacity),
		CPU:      make([]float64, 0, capacity),
		Mem:      make([]float64, 0, capacity),
		MemUsed:  make([]uint64, 0, capacity),
		Disk:     make([]float64, 0, capacity),
		DiskUsed: make([]uint64, 0, capacity),
		NetIn:    make([]float64, 0, capacity),
		NetOut:   make([]float64, 0, capacity),
	}
}

func serverMetricSnapshot(serverID uint64, since time.Time, afterUnix int64) []model.ServerMetric {
	byBucket := make(map[int64]model.ServerMetric)
	if DB != nil {
		var metrics []model.ServerMetric
		query := DB.Select(
			"bucket_at",
			"sample_count",
			"cpu_avg",
			"cpu_max",
			"mem_used_avg",
			"mem_total",
			"disk_used_avg",
			"disk_total",
			"net_in_speed_avg",
			"net_out_speed_avg",
		).Where("server_id = ? AND bucket_at >= ?", serverID, since)
		if afterUnix > 0 {
			query = query.Where("bucket_at > ?", time.Unix(afterUnix, 0))
		}
		query.Order("bucket_at ASC").Find(&metrics)
		for _, metric := range metrics {
			byBucket[metric.BucketAt.Unix()] = metric
		}
	}

	serverMetricLock.Lock()
	if bucket := serverMetricBuckets[serverID]; bucket != nil &&
		!bucket.metric.BucketAt.Before(since) &&
		bucket.metric.SampleCount > 0 &&
		(afterUnix <= 0 || bucket.metric.BucketAt.Unix() > afterUnix) {
		byBucket[bucket.metric.BucketAt.Unix()] = bucket.metric
	}
	serverMetricLock.Unlock()

	keys := make([]int64, 0, len(byBucket))
	for key := range byBucket {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return keys[i] < keys[j]
	})
	metrics := make([]model.ServerMetric, 0, len(keys))
	for _, key := range keys {
		metrics = append(metrics, byBucket[key])
	}
	return metrics
}

func downsampleServerMetrics(metrics []model.ServerMetric, stepSec int64) model.ServerMetricSeries {
	series := emptyMetricSeries("", stepSec, len(metrics))
	if len(metrics) == 0 {
		return series
	}
	if stepSec <= 60 {
		for _, metric := range metrics {
			appendMetricPoint(&series, metric.BucketAt.Unix(), metric)
		}
		return series
	}

	type agg struct {
		bucket    int64
		count     float64
		cpu       float64
		memUsed   float64
		memTotal  uint64
		diskUsed  float64
		diskTotal uint64
		netIn     float64
		netOut    float64
	}
	var current *agg
	flush := func() {
		if current == nil || current.count == 0 {
			return
		}
		point := model.ServerMetric{
			CPUAvg:         current.cpu / current.count,
			MemUsedAvg:     uint64(current.memUsed/current.count + 0.5),
			MemTotal:       current.memTotal,
			DiskUsedAvg:    uint64(current.diskUsed/current.count + 0.5),
			DiskTotal:      current.diskTotal,
			NetInSpeedAvg:  uint64(current.netIn/current.count + 0.5),
			NetOutSpeedAvg: uint64(current.netOut/current.count + 0.5),
		}
		appendMetricPoint(&series, current.bucket, point)
		current = nil
	}
	for _, metric := range metrics {
		ts := metric.BucketAt.Unix()
		bucket := ts - (ts % stepSec)
		if current == nil || current.bucket != bucket {
			flush()
			current = &agg{bucket: bucket}
		}
		weight := float64(metric.SampleCount)
		if weight <= 0 {
			weight = 1
		}
		current.count += weight
		current.cpu += metric.CPUAvg * weight
		current.memUsed += float64(metric.MemUsedAvg) * weight
		if metric.MemTotal > 0 {
			current.memTotal = metric.MemTotal
		}
		current.diskUsed += float64(metric.DiskUsedAvg) * weight
		if metric.DiskTotal > 0 {
			current.diskTotal = metric.DiskTotal
		}
		current.netIn += float64(metric.NetInSpeedAvg) * weight
		current.netOut += float64(metric.NetOutSpeedAvg) * weight
	}
	flush()
	return series
}

func appendMetricPoint(series *model.ServerMetricSeries, ts int64, metric model.ServerMetric) {
	series.T = append(series.T, ts)
	series.CPU = append(series.CPU, metric.CPUAvg)
	series.Mem = append(series.Mem, usagePercent(metric.MemUsedAvg, metric.MemTotal))
	series.MemUsed = append(series.MemUsed, metric.MemUsedAvg)
	series.Disk = append(series.Disk, usagePercent(metric.DiskUsedAvg, metric.DiskTotal))
	series.DiskUsed = append(series.DiskUsed, metric.DiskUsedAvg)
	series.NetIn = append(series.NetIn, float64(metric.NetInSpeedAvg))
	series.NetOut = append(series.NetOut, float64(metric.NetOutSpeedAvg))
}

func usagePercent(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	percent := float64(used) / float64(total) * 100
	if percent < 0 {
		return 0
	}
	if percent > 100 {
		return 100
	}
	return percent
}

func newServerMetricBucket(serverID uint64, bucketAt time.Time) *serverMetricBucket {
	return &serverMetricBucket{
		metric: model.ServerMetric{
			ServerID: serverID,
			BucketAt: bucketAt,
		},
	}
}

func addServerMetricSample(metric *model.ServerMetric, state model.HostState, host *model.Host, netInBytes, netOutBytes uint64) {
	sampleCount := metric.SampleCount
	metric.CPUAvg = avgFloat64(metric.CPUAvg, sampleCount, state.CPU)
	if state.CPU > metric.CPUMax {
		metric.CPUMax = state.CPU
	}
	metric.MemUsedAvg = avgUint64(metric.MemUsedAvg, sampleCount, state.MemUsed)
	metric.SwapUsedAvg = avgUint64(metric.SwapUsedAvg, sampleCount, state.SwapUsed)
	metric.DiskUsedAvg = avgUint64(metric.DiskUsedAvg, sampleCount, state.DiskUsed)
	metric.NetInSpeedAvg = avgUint64(metric.NetInSpeedAvg, sampleCount, state.NetInSpeed)
	metric.NetOutSpeedAvg = avgUint64(metric.NetOutSpeedAvg, sampleCount, state.NetOutSpeed)
	if state.NetInSpeed > metric.NetInSpeedMax {
		metric.NetInSpeedMax = state.NetInSpeed
	}
	if state.NetOutSpeed > metric.NetOutSpeedMax {
		metric.NetOutSpeedMax = state.NetOutSpeed
	}
	metric.NetInBytes += netInBytes
	metric.NetOutBytes += netOutBytes
	metric.Uptime = state.Uptime
	if host != nil {
		metric.MemTotal = host.MemTotal
		metric.SwapTotal = host.SwapTotal
		metric.DiskTotal = host.DiskTotal
	}
	metric.SampleCount++
}

func flushServerMetricLocked(metric model.ServerMetric) {
	if DB == nil || metric.SampleCount == 0 {
		return
	}
	DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "server_id"}, {Name: "bucket_at"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"sample_count",
			"cpu_avg",
			"cpu_max",
			"mem_used_avg",
			"mem_total",
			"swap_used_avg",
			"swap_total",
			"disk_used_avg",
			"disk_total",
			"net_in_speed_avg",
			"net_out_speed_avg",
			"net_in_speed_max",
			"net_out_speed_max",
			"net_in_bytes",
			"net_out_bytes",
			"uptime",
			"updated_at",
		}),
	}).Create(&metric)
}

func positiveCounterDelta(current, previous uint64) uint64 {
	if current < previous {
		return current
	}
	return current - previous
}

func avgFloat64(current float64, sampleCount uint32, next float64) float64 {
	if sampleCount == 0 {
		return next
	}
	return (current*float64(sampleCount) + next) / float64(sampleCount+1)
}

func avgUint64(current uint64, sampleCount uint32, next uint64) uint64 {
	if sampleCount == 0 {
		return next
	}
	return uint64((float64(current)*float64(sampleCount) + float64(next)) / float64(sampleCount+1))
}

func SortedTaskTargetsSnapshot() []TaskTarget {
	serverLock.RLock()
	defer serverLock.RUnlock()
	sortedServerLock.RLock()
	defer sortedServerLock.RUnlock()

	targets := make([]TaskTarget, 0, len(sortedServerList))
	for _, s := range sortedServerList {
		if s.taskStream == nil {
			continue
		}
		targets = append(targets, TaskTarget{
			ServerID: s.runtime.ID,
			stream:   s.taskStream,
			sendLock: &s.sendLock,
		})
	}
	return targets
}

func TaskTargetsForServers(ids []uint64) (targets []TaskTarget, offline []uint64) {
	serverLock.RLock()
	defer serverLock.RUnlock()

	for _, id := range ids {
		s := serverList[id]
		if s == nil || s.taskStream == nil {
			offline = append(offline, id)
			continue
		}
		targets = append(targets, TaskTarget{
			ServerID: id,
			stream:   s.taskStream,
			sendLock: &s.sendLock,
		})
	}
	return targets, offline
}

func ReSortServer() {
	serverLock.RLock()
	defer serverLock.RUnlock()
	sortedServerLock.Lock()
	defer sortedServerLock.Unlock()

	sortedServerList = []*serverSession{}
	for _, s := range serverList {
		sortedServerList = append(sortedServerList, s)
	}

	sort.SliceStable(sortedServerList, func(i, j int) bool {
		if sortedServerList[i].runtime.DisplayIndex == sortedServerList[j].runtime.DisplayIndex {
			return sortedServerList[i].runtime.ID < sortedServerList[j].runtime.ID
		}
		return sortedServerList[i].runtime.DisplayIndex > sortedServerList[j].runtime.DisplayIndex
	})
}

// =============== Cron Mixin ===============

var CronLock sync.RWMutex
var Crons map[uint64]*model.Cron
var Cron *cron.Cron

func CronTrigger(c *model.Cron) {
	targets, offline := TaskTargetsForServers(c.Servers)
	task := &pb.Task{
		Id:   c.ID,
		Data: c.Command,
		Type: model.TaskTypeCommand,
	}
	for _, target := range targets {
		if err := target.Send(task); err != nil {
			SendNotification(fmt.Sprintf("计划任务：%s，服务器：%d 发送失败：%s。", c.Name, target.ServerID, err), false)
		}
	}
	for _, id := range offline {
		SendNotification(fmt.Sprintf("计划任务：%s，服务器：%d 离线，无法执行。", c.Name, id), false)
	}
}
