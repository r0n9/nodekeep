package model

import (
	pb "github.com/r0n9/nodekeep/proto"
)

const (
	_ = iota

	MTReportHostState
)

// NICCounter 是单张网卡自开机以来的原始累计计数。
// Agent 上报全部网卡的原始值，由服务端按管理员选中的网卡集合求和，
// 这样切换统计口径无需重新下发配置给 Agent。
type NICCounter struct {
	Name      string
	BytesRecv uint64
	BytesSent uint64
	Virtual   bool
}

type HostState struct {
	CPU            float64
	MemUsed        uint64
	SwapUsed       uint64
	DiskUsed       uint64
	NetInTransfer  uint64
	NetOutTransfer uint64
	NetInSpeed     uint64
	NetOutSpeed    uint64
	Uptime         uint64
	// NICs 是每张网卡的原始计数明细，仅服务端统计使用。
	// 必须对 JSON 隐藏：PublicServerRuntime.State 会直接序列化给游客页面，
	// 暴露网卡名等于泄露节点网络拓扑。
	NICs []NICCounter `json:"-"`
}

func (s *HostState) PB() *pb.State {
	state := &pb.State{
		Cpu:            s.CPU,
		MemUsed:        s.MemUsed,
		SwapUsed:       s.SwapUsed,
		DiskUsed:       s.DiskUsed,
		NetInTransfer:  s.NetInTransfer,
		NetOutTransfer: s.NetOutTransfer,
		NetInSpeed:     s.NetInSpeed,
		NetOutSpeed:    s.NetOutSpeed,
		Uptime:         s.Uptime,
	}
	if len(s.NICs) > 0 {
		state.NicCounters = make([]*pb.NICCounter, 0, len(s.NICs))
		for _, nic := range s.NICs {
			state.NicCounters = append(state.NicCounters, &pb.NICCounter{
				Name:      nic.Name,
				BytesRecv: nic.BytesRecv,
				BytesSent: nic.BytesSent,
				Virtual:   nic.Virtual,
			})
		}
	}
	return state
}

func PB2State(s *pb.State) HostState {
	state := HostState{
		CPU:            s.GetCpu(),
		MemUsed:        s.GetMemUsed(),
		SwapUsed:       s.GetSwapUsed(),
		DiskUsed:       s.GetDiskUsed(),
		NetInTransfer:  s.GetNetInTransfer(),
		NetOutTransfer: s.GetNetOutTransfer(),
		NetInSpeed:     s.GetNetInSpeed(),
		NetOutSpeed:    s.GetNetOutSpeed(),
		Uptime:         s.GetUptime(),
	}
	if counters := s.GetNicCounters(); len(counters) > 0 {
		state.NICs = make([]NICCounter, 0, len(counters))
		for _, counter := range counters {
			state.NICs = append(state.NICs, NICCounter{
				Name:      counter.GetName(),
				BytesRecv: counter.GetBytesRecv(),
				BytesSent: counter.GetBytesSent(),
				Virtual:   counter.GetVirtual(),
			})
		}
	}
	return state
}

type Host struct {
	Platform        string
	PlatformVersion string
	CPU             []string
	MemTotal        uint64
	DiskTotal       uint64
	SwapTotal       uint64
	Arch            string
	Virtualization  string
	BootTime        uint64
	IP              string `json:"-"`
	CountryCode     string
	Version         string
}

func (h *Host) PB() *pb.Host {
	return &pb.Host{
		Platform:        h.Platform,
		PlatformVersion: h.PlatformVersion,
		Cpu:             h.CPU,
		MemTotal:        h.MemTotal,
		DiskTotal:       h.DiskTotal,
		SwapTotal:       h.SwapTotal,
		Arch:            h.Arch,
		Virtualization:  h.Virtualization,
		BootTime:        h.BootTime,
		Ip:              h.IP,
		CountryCode:     h.CountryCode,
		Version:         h.Version,
	}
}

func PB2Host(h *pb.Host) Host {
	return Host{
		Platform:        h.GetPlatform(),
		PlatformVersion: h.GetPlatformVersion(),
		CPU:             h.GetCpu(),
		MemTotal:        h.GetMemTotal(),
		DiskTotal:       h.GetDiskTotal(),
		SwapTotal:       h.GetSwapTotal(),
		Arch:            h.GetArch(),
		Virtualization:  h.GetVirtualization(),
		BootTime:        h.GetBootTime(),
		IP:              h.GetIp(),
		CountryCode:     h.GetCountryCode(),
		Version:         h.GetVersion(),
	}
}
