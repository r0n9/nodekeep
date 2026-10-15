package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/r0n9/nodekeep/model"
	"github.com/r0n9/nodekeep/service/dao"
)

func setupNICSelectionAPI(t *testing.T) (*gorm.DB, func(string) *httptest.ResponseRecorder) {
	t.Helper()
	restoreWorkingDir := chdirRepoRoot(t)
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
	if err := db.AutoMigrate(
		&model.User{}, &model.Server{}, &model.ServerBilling{},
		&model.ServerPayment{}, &model.ServerNIC{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	dao.DB = db
	dao.InitServerRuntimeState()

	admin := model.User{Common: model.Common{ID: 1}, Login: "admin", Token: "token", TokenExpired: time.Now().AddDate(0, 1, 0)}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatalf("create admin: %v", err)
	}

	r := ServeWeb()
	post := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/server", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-NodeKeep-Request", "1")
		req.AddCookie(&http.Cookie{Name: "nodekeep", Value: "token"})
		r.ServeHTTP(w, req)
		return w
	}
	return db, post
}

func TestNormalizeNICSelection(t *testing.T) {
	cases := []struct {
		name    string
		input   []string
		want    string
		wantErr bool
	}{
		{name: "空选择表示自动", input: nil, want: ""},
		{name: "去空白并去重", input: []string{" eth0 ", "eth1", "eth0", ""}, want: "eth0,eth1"},
		{name: "单个网卡", input: []string{"eth0"}, want: "eth0"},
		{name: "网卡名过长", input: []string{strings.Repeat("a", model.MaxNICNameLength+1)}, wantErr: true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeNICSelection(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("normalizeNICSelection(%v) 期望报错，实际 %q", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeNICSelection(%v) 报错：%v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("normalizeNICSelection(%v) = %q, 期望 %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNormalizeNICSelectionRejectsTooMany(t *testing.T) {
	names := make([]string, 0, model.MaxTrafficNICs+1)
	for i := 0; i <= model.MaxTrafficNICs; i++ {
		names = append(names, "eth"+string(rune('a'+i)))
	}
	if _, err := normalizeNICSelection(names); err == nil {
		t.Fatalf("超过 %d 个网卡应报错", model.MaxTrafficNICs)
	}
}

// 只选网卡不应被当成「填了计费信息」，否则会凭空建出一条空订阅。
func TestSaveServerPersistsTrafficNICsWithoutBilling(t *testing.T) {
	db, post := setupNICSelectionAPI(t)

	w := post(`{"name":"node-nic","Tag":"edge","TrafficNICs":["eth0","eth1"]}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"code":200`) {
		t.Fatalf("保存失败：%d %s", w.Code, w.Body.String())
	}

	var created model.Server
	if err := db.Where("name = ?", "node-nic").First(&created).Error; err != nil {
		t.Fatalf("服务器未创建：%v", err)
	}
	if created.TrafficNICs != "eth0,eth1" {
		t.Fatalf("TrafficNICs = %q, 期望 %q", created.TrafficNICs, "eth0,eth1")
	}
	if _, ok := dao.ServerBillingOf(created.ID); ok {
		t.Fatal("只选网卡不应创建订阅记录")
	}
}

func TestSaveServerClearsTrafficNICsWhenEmpty(t *testing.T) {
	db, post := setupNICSelectionAPI(t)

	post(`{"name":"node-clear","TrafficNICs":["eth0"]}`)
	var created model.Server
	if err := db.Where("name = ?", "node-clear").First(&created).Error; err != nil {
		t.Fatalf("服务器未创建：%v", err)
	}

	// 编辑时清空选择，应回到「自动」口径。
	body := `{"ID":` + strconv.FormatUint(created.ID, 10) + `,"name":"node-clear","Secret":"` + created.Secret + `","TrafficNICs":[]}`
	w := post(body)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"code":200`) {
		t.Fatalf("保存失败：%d %s", w.Code, w.Body.String())
	}

	var reloaded model.Server
	if err := db.First(&reloaded, created.ID).Error; err != nil {
		t.Fatalf("重新读取失败：%v", err)
	}
	if reloaded.TrafficNICs != "" {
		t.Fatalf("TrafficNICs = %q, 期望空串", reloaded.TrafficNICs)
	}
}

func TestServerNICsEndpointReturnsSelected(t *testing.T) {
	setupNICSelectionAPI(t)

	dao.UpsertServerRuntime(model.Server{
		Common:      model.Common{ID: 1},
		Name:        "node-a",
		Secret:      "secret-a",
		TrafficNICs: "eth0,eth1",
	}, false)
	dao.UpsertServerNICs(1, []model.NICCounter{
		{Name: "eth0"},
		{Name: "eth2"},
	}, time.Now())

	result := getServerNICs(t, "/api/server/1/nic")

	selected, ok := result["Selected"].([]interface{})
	if !ok {
		t.Fatalf("Selected 不是数组：%#v", result["Selected"])
	}
	if len(selected) != 2 || selected[0] != "eth0" || selected[1] != "eth1" {
		t.Fatalf("Selected = %#v, 期望 [eth0 eth1]", selected)
	}

	// 刚写入的网卡不应被判为失联。
	list, _ := result["List"].([]interface{})
	for _, item := range list {
		nic, _ := item.(map[string]interface{})
		if nic["Stale"] == true {
			t.Fatalf("网卡 %v 被误判为失联：%#v", nic["Name"], nic)
		}
	}
}

func TestServerNICsEndpointMarksStaleNICs(t *testing.T) {
	setupNICSelectionAPI(t)

	dao.UpsertServerNICs(1, []model.NICCounter{{Name: "eth0"}}, time.Now().Add(-time.Hour))

	result := getServerNICs(t, "/api/server/1/nic")
	list, _ := result["List"].([]interface{})
	if len(list) != 1 {
		t.Fatalf("List 长度 = %d, 期望 1", len(list))
	}
	nic, _ := list[0].(map[string]interface{})
	if nic["Stale"] != true {
		t.Fatalf("一小时未上报的网卡应标记为失联：%#v", nic)
	}
}

// Marshal 要带上网卡选择，编辑弹窗才能在打开瞬间回填（不依赖异步接口）。
func TestServerMarshalIncludesTrafficNICs(t *testing.T) {
	server := model.Server{
		Common:      model.Common{ID: 7},
		Name:        "node-a",
		Secret:      "s",
		TrafficNICs: "eth0,eth1",
	}
	payload := string(server.Marshal())
	if !strings.Contains(payload, `"TrafficNICs":"eth0,eth1"`) {
		t.Fatalf("Marshal 未包含网卡选择：%s", payload)
	}

	var decoded struct {
		TrafficNICs string `json:"TrafficNICs"`
	}
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatalf("Marshal 输出不是合法 JSON：%v (%s)", err, payload)
	}
	if decoded.TrafficNICs != "eth0,eth1" {
		t.Fatalf("TrafficNICs = %q, 期望 %q", decoded.TrafficNICs, "eth0,eth1")
	}
}

func TestServerMarshalTrafficNICsEmptyWhenAuto(t *testing.T) {
	server := model.Server{Common: model.Common{ID: 8}, Name: "node-b", Secret: "s"}
	payload := string(server.Marshal())
	if !strings.Contains(payload, `"TrafficNICs":""`) {
		t.Fatalf("自动口径应下发空串：%s", payload)
	}
}
