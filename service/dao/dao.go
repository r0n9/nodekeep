package dao

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/patrickmn/go-cache"
	"github.com/robfig/cron/v3"
	"gorm.io/gorm"

	"github.com/r0n9/nodekeep/model"
	pb "github.com/r0n9/nodekeep/proto"
)

var Version = "v1.0.0"

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
	defer serverLock.Unlock()
	if s := serverList[clientID]; s != nil {
		s.runtime.LastActive = now
		s.runtime.State = &state
	}
}

func UpdateServerHost(clientID uint64, host model.Host, enableIPChangeNotification bool) (name, oldIP, newIP string, changed bool) {
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

func SortedServerSnapshot() []*model.ServerRuntime {
	serverLock.RLock()
	defer serverLock.RUnlock()
	sortedServerLock.RLock()
	defer sortedServerLock.RUnlock()

	servers := make([]*model.ServerRuntime, 0, len(sortedServerList))
	for _, s := range sortedServerList {
		servers = append(servers, cloneServerRuntime(&s.runtime))
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
