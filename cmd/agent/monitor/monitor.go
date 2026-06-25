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
var nicAllowlist atomic.Value

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

func SetNICAllowlist(value string) {
	allowlist := make(map[string]struct{})
	for _, item := range strings.Split(value, ",") {
		name := strings.TrimSpace(item)
		if name != "" {
			allowlist[name] = struct{}{}
		}
	}
	nicAllowlist.Store(allowlist)
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
		Platform:        hi.OS,
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

	return &model.HostState{
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

	innerNetInTransfer, innerNetOutTransfer := sumNetworkCounters(nc, currentNICAllowlist())
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

func currentNICAllowlist() map[string]struct{} {
	value := nicAllowlist.Load()
	if value == nil {
		return nil
	}
	return value.(map[string]struct{})
}

func sumNetworkCounters(counters []net.IOCountersStat, allowlist map[string]struct{}) (uint64, uint64) {
	var inTransfer, outTransfer uint64
	for _, counter := range counters {
		if !shouldIncludeNIC(counter.Name, allowlist) {
			continue
		}
		inTransfer += counter.BytesRecv
		outTransfer += counter.BytesSent
	}
	return inTransfer, outTransfer
}

func shouldIncludeNIC(name string, allowlist map[string]struct{}) bool {
	if len(allowlist) > 0 {
		_, ok := allowlist[name]
		return ok
	}

	name = strings.ToLower(name)
	for _, prefix := range excludedNICPrefixes {
		if strings.HasPrefix(name, prefix) {
			return false
		}
	}
	return true
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
