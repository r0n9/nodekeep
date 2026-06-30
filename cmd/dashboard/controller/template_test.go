package controller

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/r0n9/nodekeep/model"
	"github.com/r0n9/nodekeep/service/dao"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestServeWebTemplateParse(t *testing.T) {
	restoreWorkingDir := chdirRepoRoot(t)
	defer restoreWorkingDir()

	previousConf := dao.Conf
	defer func() {
		dao.Conf = previousConf
	}()
	dao.Conf = &model.Config{}
	dao.Conf.Site.Brand = "nodekeep"
	dao.Conf.Site.CookieName = "nodekeep"

	_ = ServeWeb()
}

func TestNodeDetailPageAndMetricsEndpoint(t *testing.T) {
	restoreWorkingDir := chdirRepoRoot(t)
	defer restoreWorkingDir()

	previousConf := dao.Conf
	previousDB := dao.DB
	defer func() {
		dao.Conf = previousConf
		dao.DB = previousDB
	}()
	dao.Conf = &model.Config{}
	dao.Conf.Site.Brand = "nodekeep"
	dao.Conf.Site.CookieName = "nodekeep"

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.ServerMetric{}); err != nil {
		t.Fatalf("auto migrate server metric: %v", err)
	}
	dao.DB = db
	dao.InitServerRuntimeState()
	dao.UpsertServerRuntime(model.Server{
		Common:       model.Common{ID: 1},
		Name:         "node-a",
		Tag:          "edge",
		DisplayIndex: 1,
	}, false)
	dao.UpdateServerHost(1, model.Host{
		Platform:        "linux",
		PlatformVersion: "6.8",
		CPU:             []string{"test cpu 4 Physical Core"},
		MemTotal:        1024,
		DiskTotal:       2048,
		SwapTotal:       512,
		Arch:            "amd64",
		Virtualization:  "kvm",
		BootTime:        100,
		CountryCode:     "US",
	}, false)
	dao.UpdateServerState(1, model.HostState{
		CPU:            12,
		MemUsed:        256,
		SwapUsed:       32,
		DiskUsed:       512,
		NetInTransfer:  1000,
		NetOutTransfer: 2000,
		NetInSpeed:     300,
		NetOutSpeed:    400,
		Uptime:         60,
	}, time.Now())
	if err := db.Create(&model.ServerMetric{
		ServerID:    1,
		BucketAt:    time.Now().AddDate(0, 0, -1).Truncate(time.Minute),
		SampleCount: 1,
		CPUAvg:      99,
	}).Error; err != nil {
		t.Fatalf("create old server metric: %v", err)
	}

	r := ServeWeb()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/node/1", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("node detail status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, expected := range []string{
		"node-detail-app",
		"node-a",
		"监控指标",
		"今天",
		"近 3 天",
		"近 7 天",
		"const initMetricRange = 'today';",
		"/static/vendor/uplot/uPlot.min.css",
		"/static/vendor/uplot/uPlot.iife.min.js",
		"new window.uPlot",
		"nk-chart-tooltip",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("node detail body missing %q", expected)
		}
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/node/1/metrics", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("node metrics status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"CPUAvg":12`) {
		t.Fatalf("node metrics body missing CPUAvg: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"CPUAvg":99`) {
		t.Fatalf("default node metrics should only include today: %s", w.Body.String())
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/node/1/metrics?range=3d", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("node metrics 3d status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"CPUAvg":99`) {
		t.Fatalf("node metrics 3d body missing older CPUAvg: %s", w.Body.String())
	}
}

func chdirRepoRoot(t *testing.T) func() {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	previousWorkingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working dir: %v", err)
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", ".."))
	if err := os.Chdir(repoRoot); err != nil {
		t.Fatalf("chdir repo root: %v", err)
	}
	return func() {
		if err := os.Chdir(previousWorkingDir); err != nil {
			t.Fatalf("restore working dir: %v", err)
		}
	}
}
