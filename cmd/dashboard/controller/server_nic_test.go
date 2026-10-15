package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/r0n9/nodekeep/model"
	"github.com/r0n9/nodekeep/service/dao"
)

func setupServerNICTest(t *testing.T) {
	t.Helper()
	restoreWorkingDir := chdirRepoRoot(t)
	// 用 t.Cleanup 而不是 defer：helper 返回后测试体还要用仓库根目录加载模板。
	t.Cleanup(restoreWorkingDir)

	previousConf := dao.Conf
	previousDB := dao.DB
	t.Cleanup(func() {
		dao.Conf = previousConf
		dao.DB = previousDB
	})
	dao.Conf = &model.Config{}
	dao.Conf.Site.Brand = "nodekeep"
	dao.Conf.Site.CookieName = "nodekeep"

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.Server{}, &model.ServerNIC{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	dao.DB = db
	dao.InitServerRuntimeState()
	dao.UpsertServerRuntime(model.Server{
		Common: model.Common{ID: 1},
		Name:   "node-a",
		Secret: "secret-a",
	}, false)

	admin := model.User{Common: model.Common{ID: 1}, Login: "admin", Token: "token", TokenExpired: time.Now().AddDate(0, 1, 0)}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatalf("create admin: %v", err)
	}
}

func getServerNICs(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	r := ServeWeb()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(&http.Cookie{Name: "nodekeep", Value: "token"})
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("%s status = %d, want 200", path, w.Code)
	}
	var resp struct {
		Code   uint64                 `json:"code"`
		Result map[string]interface{} `json:"result"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (%s)", err, w.Body.String())
	}
	if resp.Code != http.StatusOK {
		t.Fatalf("response code = %d, want 200", resp.Code)
	}
	return resp.Result
}

func TestServerNICsEndpointListsReportedNICs(t *testing.T) {
	setupServerNICTest(t)

	now := time.Now()
	dao.UpsertServerNICs(1, []model.NICCounter{
		{Name: "eth0"},
		{Name: "docker0", Virtual: true},
	}, now)

	result := getServerNICs(t, "/api/server/1/nic")

	if supported, _ := result["Supported"].(bool); !supported {
		t.Fatalf("Supported = %v, want true", result["Supported"])
	}
	list, ok := result["List"].([]interface{})
	if !ok {
		t.Fatalf("List is not an array: %#v", result["List"])
	}
	if len(list) != 2 {
		t.Fatalf("List length = %d, want 2", len(list))
	}
	// 按名称升序：docker0 在前。
	first, _ := list[0].(map[string]interface{})
	if first["Name"] != "docker0" || first["Virtual"] != true {
		t.Fatalf("first NIC = %#v, want docker0 virtual", first)
	}
	second, _ := list[1].(map[string]interface{})
	if second["Name"] != "eth0" || second["Virtual"] != false {
		t.Fatalf("second NIC = %#v, want eth0 non-virtual", second)
	}
	if seen, _ := second["LastSeen"].(string); seen == "" {
		t.Fatalf("LastSeen is empty for eth0")
	}
}

// 老 Agent 不上报网卡明细，此时 Supported 必须为 false，前端据此禁用选择器。
func TestServerNICsEndpointReportsUnsupportedForLegacyAgent(t *testing.T) {
	setupServerNICTest(t)

	result := getServerNICs(t, "/api/server/1/nic")

	if supported, _ := result["Supported"].(bool); supported {
		t.Fatalf("Supported = true, want false for a server with no reported NICs")
	}
	list, ok := result["List"].([]interface{})
	if !ok {
		t.Fatalf("List is not an array: %#v", result["List"])
	}
	if len(list) != 0 {
		t.Fatalf("List length = %d, want 0", len(list))
	}
}

func TestServerNICsEndpointRejectsInvalidID(t *testing.T) {
	setupServerNICTest(t)

	r := ServeWeb()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/server/0/nic", nil)
	req.AddCookie(&http.Cookie{Name: "nodekeep", Value: "token"})
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Code uint64 `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("response code = %d, want %d", resp.Code, http.StatusBadRequest)
	}
}
