package controller

import (
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

func TestNewBillingView(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.Local)
	dueAt := func(days int) *time.Time {
		d := time.Date(2026, 3, 1, 0, 0, 0, 0, time.Local).AddDate(0, 0, days)
		return &d
	}

	cases := []struct {
		name      string
		billing   *model.ServerBilling
		wantLevel string
		wantText  string
	}{
		{
			name:      "未录入计费信息",
			billing:   nil,
			wantLevel: dueLevelNone,
			wantText:  "-",
		},
		{
			name:      "录入了但没填到期日",
			billing:   &model.ServerBilling{Provider: "BandwagonHost"},
			wantLevel: dueLevelNone,
			wantText:  "-",
		},
		{
			name:      "还有很久",
			billing:   &model.ServerBilling{NextDueDate: dueAt(90)},
			wantLevel: dueLevelNormal,
			wantText:  "90 天",
		},
		{
			name:      "刚好 30 天算临近",
			billing:   &model.ServerBilling{NextDueDate: dueAt(30)},
			wantLevel: dueLevelSoon,
			wantText:  "30 天",
		},
		{
			name:      "31 天还不算临近",
			billing:   &model.ServerBilling{NextDueDate: dueAt(31)},
			wantLevel: dueLevelNormal,
			wantText:  "31 天",
		},
		{
			name:      "7 天内紧急",
			billing:   &model.ServerBilling{NextDueDate: dueAt(3)},
			wantLevel: dueLevelUrgent,
			wantText:  "3 天",
		},
		{
			name:      "今天到期",
			billing:   &model.ServerBilling{NextDueDate: dueAt(0)},
			wantLevel: dueLevelUrgent,
			wantText:  "今天到期",
		},
		{
			name:      "已过期",
			billing:   &model.ServerBilling{NextDueDate: dueAt(-5)},
			wantLevel: dueLevelExpired,
			wantText:  "已过期 5 天",
		},
		{
			name: "已退订不看到期日",
			billing: &model.ServerBilling{
				NextDueDate: dueAt(-100),
				Status:      model.BillingStatusCancelled,
			},
			wantLevel: dueLevelCancelled,
			wantText:  "已退订",
		},
	}

	for _, tc := range cases {
		view := newBillingView(tc.billing, now)
		if view.Level != tc.wantLevel {
			t.Errorf("%s：Level = %q, 期望 %q", tc.name, view.Level, tc.wantLevel)
		}
		if view.Text != tc.wantText {
			t.Errorf("%s：Text = %q, 期望 %q", tc.name, view.Text, tc.wantText)
		}
	}
}

func TestBillingTooltip(t *testing.T) {
	due := time.Date(2026, 1, 2, 0, 0, 0, 0, time.Local)
	billing := &model.ServerBilling{
		Provider:    "BandwagonHost",
		ProductPlan: "THE DUCK PLAN",
		Currency:    "USD",
		AmountCents: 4999,
		Cycle:       model.BillingCycleAnnually,
		CycleCount:  1,
		NextDueDate: &due,
		AutoRenew:   true,
	}
	want := "BandwagonHost · THE DUCK PLAN · USD 49.99 / 年付 · 到期 2026-01-02 · 自动续费"
	if got := billingTooltip(billing); got != want {
		t.Errorf("billingTooltip = %q, 期望 %q", got, want)
	}

	// 缺项应自动省略，不留下连续的分隔符
	if got := billingTooltip(&model.ServerBilling{Provider: "RackNerd"}); got != "RackNerd" {
		t.Errorf("billingTooltip = %q, 期望 %q", got, "RackNerd")
	}
}

func TestBillingViewsForServersCoversEveryServer(t *testing.T) {
	servers := []*model.ServerRuntime{
		{Server: model.Server{Common: model.Common{ID: 1}}},
		{Server: model.Server{Common: model.Common{ID: 2}}},
		nil,
	}
	due := time.Now().AddDate(0, 0, 10)
	billings := map[uint64]*model.ServerBilling{
		1: {NextDueDate: &due},
	}
	views := billingViewsForServers(servers, billings, time.Now())
	if len(views) != 2 {
		t.Fatalf("期望 2 条视图，实际 %d", len(views))
	}
	// 没有计费信息的服务器也要有占位记录，否则模板里 index 会取到零值
	if views[2].Level != dueLevelNone || views[2].Text != "-" {
		t.Errorf("未录入计费的服务器视图 = %+v", views[2])
	}
	if views[1].Level != dueLevelSoon {
		t.Errorf("10 天后到期应为 soon，实际 %q", views[1].Level)
	}
}

// TestServerAdminPageRendersBilling 真正渲染一次后台服务器页面。
// TestServeWebTemplateParse 只做模板解析，抓不到 index $.BillingViews 这类渲染期错误。
func TestServerAdminPageRendersBilling(t *testing.T) {
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
	if err := db.AutoMigrate(&model.User{}, &model.ServerBilling{}, &model.ServerPayment{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	dao.DB = db
	dao.InitServerRuntimeState()
	dao.UpsertServerRuntime(model.Server{
		Common: model.Common{ID: 1},
		Name:   "node-a",
		Secret: "secret-a",
	}, false)
	dao.UpsertServerRuntime(model.Server{
		Common: model.Common{ID: 2},
		Name:   "node-b",
		Secret: "secret-b",
	}, false)

	admin := model.User{Common: model.Common{ID: 1}, Login: "admin", Token: "token", TokenExpired: time.Now().AddDate(0, 1, 0)}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatalf("create admin: %v", err)
	}

	due := time.Now().AddDate(0, 0, 3)
	billing := model.ServerBilling{
		ServerID:    1,
		Provider:    "BandwagonHost",
		Currency:    "USD",
		AmountCents: 4999,
		Cycle:       model.BillingCycleAnnually,
		NextDueDate: &due,
		Extra:       model.BillingExtra{Bandwidth: "1Gbps"},
	}
	if err := dao.SaveServerBilling(&billing); err != nil {
		t.Fatalf("save billing: %v", err)
	}

	r := ServeWeb()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/server", nil)
	req.AddCookie(&http.Cookie{Name: "nodekeep", Value: "token"})
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("/server status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "3 天") {
		t.Errorf("页面缺少到期天数：%s", body)
	}
	if !strings.Contains(body, "BandwagonHost") {
		t.Errorf("页面缺少服务商 tooltip")
	}
	// 未录入计费信息的节点应该渲染成占位符而不是空白
	if !strings.Contains(body, `<span class="nk-muted-text">-</span>`) {
		t.Errorf("未录入计费的节点缺少占位符")
	}
}

func TestSaveServerBillingKeepsLastRemindedOn(t *testing.T) {
	previousDB := dao.DB
	defer func() { dao.DB = previousDB }()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.ServerBilling{}, &model.ServerPayment{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	dao.DB = db

	reminded := time.Now().AddDate(0, 0, -1)
	first := model.ServerBilling{ServerID: 7, Provider: "A", LastRemindedOn: &reminded}
	if err := dao.SaveServerBilling(&first); err != nil {
		t.Fatalf("first save: %v", err)
	}

	// 后台编辑不会提交 LastRemindedOn，保存时必须保留原值，否则当天会重复提醒
	second := model.ServerBilling{ServerID: 7, Provider: "B"}
	if err := dao.SaveServerBilling(&second); err != nil {
		t.Fatalf("second save: %v", err)
	}

	stored, ok := dao.ServerBillingOf(7)
	if !ok {
		t.Fatal("订阅信息丢失")
	}
	if stored.Provider != "B" {
		t.Errorf("Provider = %q, 期望 B", stored.Provider)
	}
	if stored.LastRemindedOn == nil {
		t.Error("LastRemindedOn 被清空了")
	}
	if stored.ID != first.ID {
		t.Errorf("应更新同一行，ID 从 %d 变成了 %d", first.ID, stored.ID)
	}

	// 删除服务器时订阅信息和流水一起清掉
	if err := db.Create(&model.ServerPayment{ServerID: 7, AmountCents: 100}).Error; err != nil {
		t.Fatalf("create payment: %v", err)
	}
	if err := dao.DeleteServerBillingData(7); err != nil {
		t.Fatalf("delete billing data: %v", err)
	}
	if _, ok := dao.ServerBillingOf(7); ok {
		t.Error("订阅信息未删除")
	}
	if payments := dao.ServerPaymentsOf(7); len(payments) != 0 {
		t.Errorf("付费流水未删除，剩余 %d 条", len(payments))
	}
}

// TestAddOrEditServerBilling 走一遍 POST /api/server 的完整链路：
// 表单校验 -> 事务写入 -> 回读，并验证「计费表单整体留空 = 清除订阅信息」。
func TestAddOrEditServerBilling(t *testing.T) {
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
	if err := db.AutoMigrate(&model.User{}, &model.Server{}, &model.ServerBilling{}, &model.ServerPayment{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	dao.DB = db
	dao.InitServerRuntimeState()

	admin := model.User{Common: model.Common{ID: 1}, Login: "admin", Token: "token", TokenExpired: time.Now().AddDate(0, 1, 0)}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatalf("create admin: %v", err)
	}

	r := ServeWeb()
	post := func(t *testing.T, body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/server", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-NodeKeep-Request", "1")
		req.AddCookie(&http.Cookie{Name: "nodekeep", Value: "token"})
		r.ServeHTTP(w, req)
		return w
	}

	// 新建服务器并录入计费信息
	w := post(t, `{"name":"node-a","Tag":"edge","Provider":"BandwagonHost","Currency":"usd",
		"Amount":"49.99","Cycle":"annually","CycleCount":1,"NextDueDate":"2026-12-01",
		"AutoRenew":"on","Status":1,"Bandwidth":"1Gbps","IPv4Count":1}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"code":200`) {
		t.Fatalf("新建失败：%d %s", w.Code, w.Body.String())
	}

	var created model.Server
	if err := db.Where("name = ?", "node-a").First(&created).Error; err != nil {
		t.Fatalf("服务器未创建：%v", err)
	}
	billing, ok := dao.ServerBillingOf(created.ID)
	if !ok {
		t.Fatal("计费信息未写入")
	}
	if billing.AmountCents != 4999 {
		t.Errorf("AmountCents = %d, 期望 4999", billing.AmountCents)
	}
	if billing.Currency != "USD" {
		t.Errorf("货币应规范成大写，实际 %q", billing.Currency)
	}
	if !billing.AutoRenew {
		t.Error("AutoRenew 未生效")
	}
	if model.FormatBillingDate(billing.NextDueDate) != "2026-12-01" {
		t.Errorf("到期日 = %q", model.FormatBillingDate(billing.NextDueDate))
	}
	if billing.Extra.Bandwidth != "1Gbps" || billing.Extra.IPv4Count != 1 {
		t.Errorf("套餐规格未写入：%+v", billing.Extra)
	}

	// 非法金额应被拦在写库之前
	w = post(t, `{"ID":`+itoa(created.ID)+`,"name":"node-a","Amount":"12.345","Cycle":"annually"}`)
	if !strings.Contains(w.Body.String(), "金额") {
		t.Errorf("非法金额应报错，实际：%s", w.Body.String())
	}
	if reread, _ := dao.ServerBillingOf(created.ID); reread == nil || reread.AmountCents != 4999 {
		t.Error("校验失败时不应改动已有数据")
	}

	// 非法计费周期同样应被拒绝
	w = post(t, `{"ID":`+itoa(created.ID)+`,"name":"node-a","Cycle":"forever"}`)
	if !strings.Contains(w.Body.String(), "未知的计费周期") {
		t.Errorf("非法周期应报错，实际：%s", w.Body.String())
	}

	// 计费表单整体留空 = 清除订阅信息，但服务器本身保留
	w = post(t, `{"ID":`+itoa(created.ID)+`,"name":"node-a","Tag":"edge"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"code":200`) {
		t.Fatalf("清空计费失败：%s", w.Body.String())
	}
	if _, ok := dao.ServerBillingOf(created.ID); ok {
		t.Error("计费表单留空时应清除订阅信息")
	}
	if err := db.Where("id = ?", created.ID).First(&model.Server{}).Error; err != nil {
		t.Errorf("服务器不应被删除：%v", err)
	}
}

func itoa(v uint64) string {
	return strconv.FormatUint(v, 10)
}
