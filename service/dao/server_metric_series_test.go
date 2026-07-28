package dao

import (
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/r0n9/nodekeep/model"
)

func TestServerMetricSeriesSnapshotDownsamplesAndIncrements(t *testing.T) {
	previousDB := DB
	defer func() { DB = previousDB }()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.ServerMetric{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	DB = db
	InitServerRuntimeState()

	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.Local)
	// 12 minutes of raw data spanning two 5-minute buckets.
	for i := 0; i < 12; i++ {
		metric := model.ServerMetric{
			ServerID:       9,
			BucketAt:       base.Add(time.Duration(i) * time.Minute),
			SampleCount:    1,
			CPUAvg:         float64(i),
			MemUsedAvg:     uint64(100 + i),
			MemTotal:       1000,
			DiskUsedAvg:    uint64(200 + i),
			DiskTotal:      2000,
			NetInSpeedAvg:  uint64(10 + i),
			NetOutSpeedAvg: uint64(20 + i),
		}
		if err := db.Create(&metric).Error; err != nil {
			t.Fatalf("create metric %d: %v", i, err)
		}
	}

	full := ServerMetricSeriesSnapshot(9, "3d", base, 0, 1000)
	if full.StepSec != 5*60 {
		t.Fatalf("stepSec = %d, want %d", full.StepSec, 5*60)
	}
	if full.Count != 3 {
		// minutes 0-4, 5-9, 10-11
		t.Fatalf("downsampled count = %d, want 3: %#v", full.Count, full)
	}
	limited := ServerMetricSeriesSnapshot(9, "3d", base, 0, 100)
	if limited.StepSec <= 5*60 {
		t.Fatalf("limited max should increase step, got %d", limited.StepSec)
	}
	if full.T[0] != base.Unix() {
		t.Fatalf("first bucket = %d, want %d", full.T[0], base.Unix())
	}
	if full.CPU[0] < 1.9 || full.CPU[0] > 2.1 {
		t.Fatalf("first cpu avg = %v, want ~2", full.CPU[0])
	}
	if full.Mem[0] < 10 || full.Mem[0] > 11 {
		t.Fatalf("first mem percent = %v, want ~10.2", full.Mem[0])
	}

	// Incremental should refresh the open bucket and omit older finished buckets.
	delta := ServerMetricSeriesSnapshot(9, "3d", base, full.T[1], 1000)
	if delta.Count < 1 {
		t.Fatalf("incremental count = %d, want >= 1", delta.Count)
	}
	if delta.T[0] < full.T[1] {
		t.Fatalf("incremental first ts %d < after %d", delta.T[0], full.T[1])
	}

	today := ServerMetricSeriesSnapshot(9, "today", base, 0, 2000)
	if today.StepSec != 60 {
		t.Fatalf("today stepSec = %d, want 60", today.StepSec)
	}
	if today.Count != 12 {
		t.Fatalf("today count = %d, want 12", today.Count)
	}
}

func TestDownsampleServerMetricsEmpty(t *testing.T) {
	series := downsampleServerMetrics(nil, 300)
	if series.Count != 0 && len(series.T) != 0 {
		t.Fatalf("empty downsample should be empty: %#v", series)
	}
}
