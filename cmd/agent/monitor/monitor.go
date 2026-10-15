package monitor

import (
	"fmt"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"

	"github.com/r0n9/nodekeep/model"
	"github.com/r0n9/nodekeep/service/dao"
)

var netInSpeed, netOutSpeed, netInTransfer, netOutTransfer, lastUpdateNano uint64
var nicSnapshot atomic.Value

var (
	netIOCounters = func() ([]net.IOCountersStat, error) {
		return net.IOCounters(true)
	}
	nowFunc = time.Now
)

var excludedNICPrefixes = []string{
	"lo", "tun", "utun", "tap", "docker", "veth", "br-", "bridge", "vmbr", "vnet",
	"kube", "meta", "tailscale", "fw", "awdl", "llw", "anpi", "gif", "stf", "ipsec",
}

func GetHost() *model.Host {
	hi, _ := host.Info()
	var cpuType string
	if hi.VirtualizationSystem != "" {
		cpuType = "Virtual"
	} else {
		cpuType = "Physical"
	}
	mv, _ := mem.VirtualMemory()
	ms, _ := mem.SwapMemory()
	u, _ := disk.Usage("/")
	ip, country := CachedIP()

	return &model.Host{
		Platform:        detectedPlatform(hi),
		PlatformVersion: hi.PlatformVersion,
		CPU:             getCPUInfo(cpuType),
		MemTotal:        mv.Total,
		DiskTotal:       u.Total,
		SwapTotal:       ms.Total,
		Arch:            hi.KernelArch,
		Virtualization:  hi.VirtualizationSystem,
		BootTime:        hi.BootTime,
		IP:              ip,
		CountryCode:     strings.ToLower(country),
		Version:         dao.Version,
	}
}

func detectedPlatform(info *host.InfoStat) string {
	if info == nil {
		return ""
	}
	if platform := strings.TrimSpace(info.Platform); platform != "" {
		return platform
	}
	return strings.TrimSpace(info.OS)
}

func GetState(delay int64) *model.HostState {
	hi, _ := host.Info()
	// Memory
	mv, _ := mem.VirtualMemory()
	ms, _ := mem.SwapMemory()
	// CPU
	var cpuPercent float64
	cp, err := cpu.Percent(time.Second*time.Duration(delay), false)
	if err == nil && len(cp) > 0 {
		cpuPercent = cp[0]
	}
	// Disk
	u, _ := disk.Usage("/")

	state := &model.HostState{
		CPU:            cpuPercent,
		MemUsed:        mv.Used,
		SwapUsed:       ms.Used,
		DiskUsed:       u.Used,
		NetInTransfer:  atomic.LoadUint64(&netInTransfer),
		NetOutTransfer: atomic.LoadUint64(&netOutTransfer),
		NetInSpeed:     atomic.LoadUint64(&netInSpeed),
		NetOutSpeed:    atomic.LoadUint64(&netOutSpeed),
		Uptime:         hi.Uptime,
	}
	if nics, ok := nicSnapshot.Load().([]model.NICCounter); ok {
		state.NICs = nics
	}
	return state
}

func getCPUInfo(cpuType string) []string {
	cpuModelCount := make(map[string]int)
	ci, err := cpu.Info()
	if err == nil {
		for _, info := range ci {
			modelName := strings.TrimSpace(info.ModelName)
			if modelName == "" {
				continue
			}
			cores := int(info.Cores)
			if cores <= 0 {
				cores = 1
			}
			cpuModelCount[modelName] += cores
		}
	}

	if len(cpuModelCount) == 0 {
		if modelName := fallbackCPUModel(); modelName != "" {
			cpuModelCount[modelName] = fallbackCPUCoreCount()
		}
	}

	if len(cpuModelCount) == 0 {
		return []string{}
	}

	models := make([]string, 0, len(cpuModelCount))
	for modelName := range cpuModelCount {
		models = append(models, modelName)
	}
	sort.Strings(models)

	cpus := make([]string, 0, len(models))
	for _, modelName := range models {
		count := cpuModelCount[modelName]
		if count <= 0 {
			count = 1
		}
		cpus = append(cpus, fmt.Sprintf("%s %d %s Core", modelName, count, cpuType))
	}
	return cpus
}

func fallbackCPUCoreCount() int {
	cores, err := cpu.Counts(false)
	if err != nil || cores <= 0 {
		cores = runtime.NumCPU()
	}
	if cores <= 0 {
		return 1
	}
	return cores
}

func TrackNetworkSpeed() {
	nc, err := netIOCounters()
	if err != nil || len(nc) == 0 {
		return
	}

	innerNetInTransfer, innerNetOutTransfer := sumNetworkCounters(nc)
	nicSnapshot.Store(collectNICCounters(nc))
	nowNano := uint64(nowFunc().UnixNano())
	prevUpdateNano := atomic.LoadUint64(&lastUpdateNano)
	prevIn := atomic.LoadUint64(&netInTransfer)
	prevOut := atomic.LoadUint64(&netOutTransfer)

	if prevUpdateNano > 0 && nowNano > prevUpdateNano {
		diffNano := nowNano - prevUpdateNano
		atomic.StoreUint64(&netInSpeed, bytesPerSecond(subUint64(innerNetInTransfer, prevIn), diffNano))
		atomic.StoreUint64(&netOutSpeed, bytesPerSecond(subUint64(innerNetOutTransfer, prevOut), diffNano))
	} else if prevUpdateNano == 0 {
		atomic.StoreUint64(&netInSpeed, 0)
		atomic.StoreUint64(&netOutSpeed, 0)
	}
	atomic.StoreUint64(&netInTransfer, innerNetInTransfer)
	atomic.StoreUint64(&netOutTransfer, innerNetOutTransfer)
	atomic.StoreUint64(&lastUpdateNano, nowNano)
}

// sumNetworkCounters 汇总所有物理网卡的原始计数，用于兼容老面板的聚合字段。
// 统计口径由服务端按管理员选中的网卡决定，Agent 不再做过滤。
func sumNetworkCounters(counters []net.IOCountersStat) (uint64, uint64) {
	var inTransfer, outTransfer uint64
	for _, counter := range counters {
		if isVirtualNIC(counter.Name) {
			continue
		}
		inTransfer += counter.BytesRecv
		outTransfer += counter.BytesSent
	}
	return inTransfer, outTransfer
}

// isVirtualNIC 按网卡名前缀判断是否为虚拟网卡。服务端据此实现
// 「默认只统计物理网卡」，所以这个判定必须与聚合口径保持一致。
func isVirtualNIC(name string) bool {
	name = strings.ToLower(name)
	for _, prefix := range excludedNICPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// collectNICCounters 汇总每张网卡的原始计数，供服务端按选中集合求和。
// 上报全部网卡（含虚拟网卡）并标注 virtual，由服务端决定统计口径——
// 这样在后台切换网卡无需重新下发配置给 Agent。
func collectNICCounters(counters []net.IOCountersStat) []model.NICCounter {
	nics := make([]model.NICCounter, 0, len(counters))
	for _, counter := range counters {
		nics = append(nics, model.NICCounter{
			Name:      counter.Name,
			BytesRecv: counter.BytesRecv,
			BytesSent: counter.BytesSent,
			Virtual:   isVirtualNIC(counter.Name),
		})
	}
	return nics
}

func bytesPerSecond(delta, elapsedNano uint64) uint64 {
	if delta == 0 || elapsedNano == 0 {
		return 0
	}
	return uint64(float64(delta) * float64(time.Second) / float64(elapsedNano))
}

func subUint64(current, previous uint64) uint64 {
	if current < previous {
		return 0
	}
	return current - previous
}
