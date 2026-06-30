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

func cloneServerRuntime(s *model.ServerRuntime) *model.ServerRuntime {
	if s == nil {
		return nil
	}
	clone := *s
	clone.Host = cloneHost(s.Host)
	clone.State = cloneState(s.State)
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
}

func ServerMetricSnapshot(serverID uint64, since time.Time) []model.ServerMetric {
	byBucket := make(map[int64]model.ServerMetric)
	if DB != nil {
		var metrics []model.ServerMetric
		DB.Where("server_id = ? AND bucket_at >= ?", serverID, since).
			Order("bucket_at ASC").
			Find(&metrics)
		for _, metric := range metrics {
			byBucket[metric.BucketAt.Unix()] = metric
		}
	}

	serverMetricLock.Lock()
	if bucket := serverMetricBuckets[serverID]; bucket != nil &&
		!bucket.metric.BucketAt.Before(since) &&
		bucket.metric.SampleCount > 0 {
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
	if current <= previous {
		return 0
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
