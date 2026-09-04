package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/r0n9/nodekeep/model"
	"github.com/r0n9/nodekeep/service/dao"
)

// TestPublicBillingWhitelist 是这一整块的核心保证：
// 金额、订单编号、面板地址不管怎么配置都不能出现在给游客的数据里。
func TestPublicBillingWhitelist(t *testing.T) {
	now := time.Now()
	due := now.AddDate(0, 0, 10)
	billing := &model.ServerBilling{
		Provider:    "BandwagonHost",
		ProductPlan: "THE DUCK PLAN",
		PanelURL:    "https://panel.example.com/secret",
		OrderNo:     "ORDER-42",
		Currency:    "USD",
		AmountCents: 4999,
		Cycle:       model.BillingCycleAnnually,
		AutoRenew:   true,
		NextDueDate: &due,
		Extra: model.BillingExtra{
			Location:     "洛杉矶 DC9",
			Bandwidth:    "1Gbps",
			TrafficVol:   "1TB/月",
			TrafficType:  model.TrafficTypeDouble,
			NetworkRoute: "CN2GIA",
			CPUSpec:      "2 vCPU",
		},
	}

	public := billing.PublicSnapshot(now)
	if public == nil {
		t.Fatal("默认应当公开")
	}
	if public.Provider != "BandwagonHost" || public.Location != "洛杉矶 DC9" {
		t.Errorf("公开字段缺失：%+v", public)
	}
	if public.TrafficType != "双向" {
		t.Errorf("流量计算方式 = %q, 期望 双向", public.TrafficType)
	}
	if !public.HasDue || public.DueDays != 10 {
		t.Errorf("到期信息错误：%+v", public)
	}

	// 序列化后逐一检查敏感值没有泄漏
	payload := string(publicBillingJSON(map[uint64]*model.PublicBilling{1: public}))
	for _, leaked := range []string{"4999", "49.99", "USD", "ORDER-42", "panel.example.com", "AutoRenew"} {
		if strings.Contains(payload, leaked) {
			t.Errorf("公开数据泄漏了 %q：%s", leaked, payload)
		}
	}
}

func TestPublicBillingHidden(t *testing.T) {
	now := time.Now()
	if got := (&model.ServerBilling{HideBilling: true, Provider: "X"}).PublicSnapshot(now); got != nil {
		t.Errorf("单机器隐藏时应返回 nil，实际 %+v", got)
	}
	if got := (*model.ServerBilling)(nil).PublicSnapshot(now); got != nil {
		t.Error("nil 订阅应返回 nil")
	}

	// 已退订的仍展示规格，但不给倒计时
	due := now.AddDate(0, 0, -5)
	cancelled := &model.ServerBilling{
		Status:      model.BillingStatusCancelled,
		NextDueDate: &due,
		Extra:       model.BillingExtra{Location: "东京"},
	}
	public := cancelled.PublicSnapshot(now)
	if public == nil || public.Location != "东京" {
		t.Fatalf("已退订仍应展示规格：%+v", public)
	}
	if public.HasDue || public.NextDueDate != "" {
		t.Errorf("已退订不应给出到期倒计时：%+v", public)
	}
}

func TestPublicBillingJSONEmpty(t *testing.T) {
	if got := string(publicBillingJSON(nil)); got != "{}" {
		t.Errorf("空表应序列化成 {}，实际 %s", got)
	}
}

func setupBillingPageDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.Server{},
		&model.ServerBilling{}, &model.ServerPayment{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

func TestBillingSummaryPage(t *testing.T) {
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

	db := setupBillingPageDB(t)
	dao.DB = db
	dao.InitServerRuntimeState()
	dao.UpsertServerRuntime(model.Server{Common: model.Common{ID: 1}, Name: "node-a", Secret: "s1"}, false)
	dao.UpsertServerRuntime(model.Server{Common: model.Common{ID: 2}, Name: "node-b", Secret: "s2"}, false)

	admin := model.User{Common: model.Common{ID: 1}, Login: "admin", Token: "token", TokenExpired: time.Now().AddDate(0, 1, 0)}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatalf("create admin: %v", err)
	}

	soon := time.Now().AddDate(0, 0, 5)
	if err := dao.SaveServerBilling(&model.ServerBilling{
		ServerID: 1, Provider: "BandwagonHost", Status: model.BillingStatusActive,
		Currency: "USD", AmountCents: 4999, Cycle: model.BillingCycleAnnually, CycleCount: 1,
		NextDueDate: &soon,
	}); err != nil {
		t.Fatalf("save billing: %v", err)
	}
	if err := db.Create(&model.ServerPayment{
		ServerID: 1, PaidAt: time.Date(2025, 6, 1, 0, 0, 0, 0, time.Local),
		AmountCents: 4999, Currency: "USD", Cycle: model.BillingCycleAnnually,
	}).Error; err != nil {
		t.Fatalf("create payment: %v", err)
	}

	r := ServeWeb()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/billing", nil)
	req.AddCookie(&http.Cookie{Name: "nodekeep", Value: "token"})
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("/billing status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		"计费总览",
		"BandwagonHost",
		"49.99",  // 年度折算
		"5 天",    // 到期倒计时
		"2025",   // 年度实付
		"node-b", // 未录入计费的机器也要出现在总览里
	} {
		if !strings.Contains(body, want) {
			t.Errorf("汇总页缺少 %q", want)
		}
	}
}

func TestBillingOverviewSorting(t *testing.T) {
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.Local)
	due := func(days int) *time.Time {
		d := now.AddDate(0, 0, days)
		return &d
	}
	servers := []*model.ServerRuntime{
		{Server: model.Server{Common: model.Common{ID: 1}, Name: "far"}},
		{Server: model.Server{Common: model.Common{ID: 2}, Name: "expired"}},
		{Server: model.Server{Common: model.Common{ID: 3}, Name: "none"}},
		{Server: model.Server{Common: model.Common{ID: 4}, Name: "soon"}},
	}
	billings := map[uint64]*model.ServerBilling{
		1: {ServerID: 1, Status: model.BillingStatusActive, NextDueDate: due(90)},
		2: {ServerID: 2, Status: model.BillingStatusActive, NextDueDate: due(-5)},
		4: {ServerID: 4, Status: model.BillingStatusActive, NextDueDate: due(5)},
	}

	overview := newBillingOverview(servers, billings, now)
	gotOrder := make([]string, 0, len(overview.Rows))
	for _, row := range overview.Rows {
		gotOrder = append(gotOrder, row.Name)
	}
	want := []string{"expired", "soon", "far", "none"}
	for i := range want {
		if gotOrder[i] != want[i] {
			t.Fatalf("排序 = %v, 期望 %v", gotOrder, want)
		}
	}

	if overview.Expired != 1 {
		t.Errorf("已过期计数 = %d, 期望 1", overview.Expired)
	}
	if overview.DueSoon != 1 {
		t.Errorf("30 天内到期计数 = %d, 期望 1", overview.DueSoon)
	}
	if overview.NoBilling != 1 {
		t.Errorf("未录入计费计数 = %d, 期望 1", overview.NoBilling)
	}
}

func TestPublicServerBillingMapRespectsGlobalSwitch(t *testing.T) {
	previousConf := dao.Conf
	previousDB := dao.DB
	defer func() {
		dao.Conf = previousConf
		dao.DB = previousDB
	}()

	db := setupBillingPageDB(t)
	dao.DB = db
	dao.Conf = &model.Config{}

	due := time.Now().AddDate(0, 0, 10)
	if err := dao.SaveServerBilling(&model.ServerBilling{
		ServerID: 1, Provider: "RackNerd", Status: model.BillingStatusActive, NextDueDate: &due,
	}); err != nil {
		t.Fatalf("save billing: %v", err)
	}

	if got := dao.PublicServerBillingMap(time.Now()); len(got) != 1 {
		t.Errorf("默认应公开，实际 %+v", got)
	}

	dao.Conf.Site.HideBillingToGuest = true
	if got := dao.PublicServerBillingMap(time.Now()); len(got) != 0 {
		t.Errorf("全局关闭后不应返回任何数据，实际 %+v", got)
	}
}

// TestGuestHomeMergesBilling 盯住方案 B 的接线：计费数据必须渲染进首屏，
// 并且 ws 回调里要按 ID 合并回来，否则 2 秒后这些字段就被整体替换掉了。
func TestGuestHomeMergesBilling(t *testing.T) {
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

	db := setupBillingPageDB(t)
	dao.DB = db
	dao.InitServerRuntimeState()
	dao.UpsertServerRuntime(model.Server{Common: model.Common{ID: 1}, Name: "node-a", Secret: "s1"}, false)

	due := time.Now().AddDate(0, 0, 10)
	if err := dao.SaveServerBilling(&model.ServerBilling{
		ServerID: 1, Provider: "RackNerd", Status: model.BillingStatusActive,
		Currency: "USD", AmountCents: 4999, Cycle: model.BillingCycleAnnually,
		OrderNo: "ORDER-42", PanelURL: "https://panel.example.com/secret",
		NextDueDate: &due,
	}); err != nil {
		t.Fatalf("save billing: %v", err)
	}

	r := ServeWeb()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("/ status = %d, want 200", w.Code)
	}
	body := w.Body.String()

	if !strings.Contains(body, "RackNerd") {
		t.Error("首屏没有渲染计费数据")
	}
	if !strings.Contains(body, "billingData[server.ID]") {
		t.Error("ws 回调里缺少按 ID 合并，推送会覆盖掉计费信息")
	}
	// 游客页面绝不能出现金额和订单信息
	for _, leaked := range []string{"ORDER-42", "panel.example.com", "4999", "49.99"} {
		if strings.Contains(body, leaked) {
			t.Errorf("游客首页泄漏了 %q", leaked)
		}
	}

	// 全局关闭后首屏也不该有计费数据
	dao.Conf.Site.HideBillingToGuest = true
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if strings.Contains(w.Body.String(), "RackNerd") {
		t.Error("全局关闭后首屏仍有计费数据")
	}
}
