package model

import "time"

type ServerMetric struct {
	Common
	ServerID       uint64    `gorm:"uniqueIndex:idx_server_metric_bucket;index"`
	BucketAt       time.Time `gorm:"uniqueIndex:idx_server_metric_bucket;index"`
	SampleCount    uint32
	CPUAvg         float64
	CPUMax         float64
	MemUsedAvg     uint64
	MemTotal       uint64
	SwapUsedAvg    uint64
	SwapTotal      uint64
	DiskUsedAvg    uint64
	DiskTotal      uint64
	NetInSpeedAvg  uint64
	NetOutSpeedAvg uint64
	NetInSpeedMax  uint64
	NetOutSpeedMax uint64
	NetInBytes     uint64
	NetOutBytes    uint64
	Uptime         uint64
}
