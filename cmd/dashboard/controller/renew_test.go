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

func billingDate(y int, m time.Month, d int) *time.Time {
	t := time.Date(y, m, d, 0, 0, 0, 0, time.Local)
	return &t
}

func TestNewRenewDefaults(t *testing.T) {
	now := time.Date(2026, 3, 5, 10, 0, 0, 0, time.Local)

	t.Run("未录入计费信息", func(t *testing.T) {
		got := newRenewDefaults(nil, now)
		if got.Renewable || got.Reason == "" {
			t.Errorf("应不可续费并给出原因，实际 %+v", got)
		}
	})

	t.Run("没设周期", func(t *testing.T) {
		got := newRenewDefaults(&model.ServerBilling{}, now)
		if got.Renewable || !strings.Contains(got.Reason, "计费周期") {
			t.Errorf("应提示设置计费周期，实际 %+v", got)
		}
	})

	t.Run("一次性付费", func(t *testing.T) {
		got := newRenewDefaults(&model.ServerBilling{Cycle: model.BillingCycleOnetime}, now)
		if got.Renewable {
			t.Error("一次性付费不应可续费")
		}
	})

	t.Run("锚点是旧到期日而不是今天", func(t *testing.T) {
		// 3/1 到期、3/5 才付款，下一期仍应从 4/1 算起，
		// 否则账单日会随着每次晚付逐期往后漂
		billing := &model.ServerBilling{
			Cycle:       model.BillingCycleMonthly,
			CycleCount:  1,
			Currency:    "USD",
			AmountCents: 500,
			NextDueDate: billingDate(2026, 3, 1),
		}
		got := newRenewDefaults(billing, now)
		if !got.Renewable {
			t.Fatalf("应可续费：%+v", got)
		}
		if got.PeriodStart != "2026-03-01" {
			t.Errorf("PeriodStart = %q, 期望 2026-03-01", got.PeriodStart)
		}
		if got.PeriodEnd != "2026-04-01" {
			t.Errorf("PeriodEnd = %q, 期望 2026-04-01", got.PeriodEnd)
		}
		if got.PaidAt != "2026-03-05" {
			t.Errorf("PaidAt = %q, 期望今天 2026-03-05", got.PaidAt)
		}
		if got.Amount != "5.00" || got.Currency != "USD" {
			t.Errorf("金额预填错误：%+v", got)
		}
	})

	t.Run("没有到期日时从今天算", func(t *testing.T) {
		billing := &model.ServerBilling{Cycle: model.BillingCycleAnnually, CycleCount: 1}
		got := newRenewDefaults(billing, now)
		if got.PeriodStart != "2026-03-05" || got.PeriodEnd != "2027-03-05" {
			t.Errorf("区间 = %s ~ %s", got.PeriodStart, got.PeriodEnd)
		}
	})

	t.Run("开通日作为锚点参与推进", func(t *testing.T) {
		billing := &model.ServerBilling{
			Cycle:       model.BillingCycleMonthly,
			CycleCount:  1,
			StartDate:   billingDate(2025, 1, 31),
			NextDueDate: billingDate(2026, 2, 28),
		}
		got := newRenewDefaults(billing, now)
		if got.PeriodEnd != "2026-03-31" {
			t.Errorf("PeriodEnd = %q, 期望 2026-03-31（锚点 31 号）", got.PeriodEnd)
		}
	})
}

func TestBuildRenewal(t *testing.T) {
	now := time.Date(2026, 3, 5, 10, 0, 0, 0, time.Local)
	billing := &model.ServerBilling{
		ServerID:    9,
		Cycle:       model.BillingCycleMonthly,
		CycleCount:  1,
		Currency:    "USD",
		AmountCents: 500,
		NextDueDate: billingDate(2026, 3, 1),
	}

	t.Run("空表单全部走默认值", func(t *testing.T) {
		payment, nextDue, err := buildRenewal(billing, &renewForm{}, now)
		if err != nil {
			t.Fatalf("意外报错：%v", err)
		}
		if payment.AmountCents != 500 || payment.Currency != "USD" {
			t.Errorf("金额未回落到订阅默认值：%+v", payment)
		}
		// PeriodEnd 必须与推进后的到期日一致，否则流水和订阅会各说各话
		if !nextDue.Equal(payment.PeriodEnd) {
			t.Errorf("到期日 %v 与 PeriodEnd %v 不一致", nextDue, payment.PeriodEnd)
		}
		if model.FormatBillingDate(&nextDue) != "2026-04-01" {
			t.Errorf("新到期日 = %s", model.FormatBillingDate(&nextDue))
		}
	})

	t.Run("表单值覆盖默认值", func(t *testing.T) {
		payment, nextDue, err := buildRenewal(billing, &renewForm{
			Amount:    "12.34",
			Currency:  "cny",
			Cycle:     model.BillingCycleAnnually,
			PeriodEnd: "2027-03-01",
			Method:    " PayPal ",
		}, now)
		if err != nil {
			t.Fatalf("意外报错：%v", err)
		}
		if payment.AmountCents != 1234 {
			t.Errorf("AmountCents = %d, 期望 1234", payment.AmountCents)
		}
		if payment.Currency != "CNY" {
			t.Errorf("货币应规范成大写，实际 %q", payment.Currency)
		}
		if payment.Method != "PayPal" {
			t.Errorf("Method 应去空白，实际 %q", payment.Method)
		}
		if model.FormatBillingDate(&nextDue) != "2027-03-01" {
			t.Errorf("新到期日 = %s", model.FormatBillingDate(&nextDue))
		}
	})

	t.Run("区间颠倒被拒绝", func(t *testing.T) {
		_, _, err := buildRenewal(billing, &renewForm{
			PeriodStart: "2026-05-01",
			PeriodEnd:   "2026-04-01",
		}, now)
		if err == nil {
			t.Error("结束早于开始应报错")
		}
	})

	t.Run("非法金额被拒绝", func(t *testing.T) {
		if _, _, err := buildRenewal(billing, &renewForm{Amount: "abc"}, now); err == nil {
			t.Error("非法金额应报错")
		}
	})

	t.Run("非法周期被拒绝", func(t *testing.T) {
		if _, _, err := buildRenewal(billing, &renewForm{Cycle: "forever"}, now); err == nil {
			t.Error("非法周期应报错")
		}
	})

	t.Run("一次性付费没有可推的区间", func(t *testing.T) {
		onetime := &model.ServerBilling{ServerID: 9, Cycle: model.BillingCycleOnetime}
		if _, _, err := buildRenewal(onetime, &renewForm{}, now); err == nil {
			t.Error("一次性付费缺少区间时应报错")
		}
	})
}

func TestRenewServerEndpoint(t *testing.T) {
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

	reminded := time.Now()
	due := time.Now().AddDate(0, 0, 3)
	billing := model.ServerBilling{
		ServerID:       1,
		Currency:       "USD",
		AmountCents:    1000,
		Cycle:          model.BillingCycleMonthly,
		CycleCount:     1,
		NextDueDate:    &due,
		LastRemindedOn: &reminded,
	}
	if err := dao.SaveServerBilling(&billing); err != nil {
		t.Fatalf("save billing: %v", err)
	}

	r := ServeWeb()
	call := func(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-NodeKeep-Request", "1")
		req.AddCookie(&http.Cookie{Name: "nodekeep", Value: "token"})
		r.ServeHTTP(w, req)
		return w
	}

	// 打开弹窗时拿到的预填数据
	w := call(t, http.MethodGet, "/api/server/1/payments", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"Renewable":true`) {
		t.Fatalf("预填数据异常：%s", w.Body.String())
	}

	wantDue := model.FormatBillingDate(&due)
	expectedNext, _ := billing.NextDue(due)

	w = call(t, http.MethodPost, "/api/server/1/renew", `{"Method":"PayPal","InvoiceNo":"INV-1"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"code":200`) {
		t.Fatalf("续费失败：%s", w.Body.String())
	}

	stored, ok := dao.ServerBillingOf(1)
	if !ok {
		t.Fatal("订阅信息丢失")
	}
	if got := model.FormatBillingDate(stored.NextDueDate); got != model.FormatBillingDate(&expectedNext) {
		t.Errorf("到期日 = %s, 期望 %s", got, model.FormatBillingDate(&expectedNext))
	}
	// 续费后当天的提醒去重标记必须作废
	if stored.LastRemindedOn != nil {
		t.Error("续费后 LastRemindedOn 应被清空")
	}

	payments := dao.ServerPaymentsOf(1)
	if len(payments) != 1 {
		t.Fatalf("期望 1 条流水，实际 %d", len(payments))
	}
	payment := payments[0]
	if payment.AmountCents != 1000 || payment.Currency != "USD" {
		t.Errorf("流水金额错误：%+v", payment)
	}
	if model.FormatBillingDate(&payment.PeriodStart) != wantDue {
		t.Errorf("本期开始应为旧到期日 %s，实际 %s", wantDue, model.FormatBillingDate(&payment.PeriodStart))
	}
	if !payment.PeriodEnd.Equal(*stored.NextDueDate) {
		t.Error("PeriodEnd 必须与新的到期日一致")
	}
	if payment.Method != "PayPal" || payment.InvoiceNo != "INV-1" {
		t.Errorf("流水字段错误：%+v", payment)
	}

	// 删除流水不应回退到期日
	w = call(t, http.MethodDelete, "/api/payment/"+strconv.FormatUint(payment.ID, 10), "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"code":200`) {
		t.Fatalf("删除流水失败：%s", w.Body.String())
	}
	if len(dao.ServerPaymentsOf(1)) != 0 {
		t.Error("流水未删除")
	}
	after, _ := dao.ServerBillingOf(1)
	if model.FormatBillingDate(after.NextDueDate) != model.FormatBillingDate(&expectedNext) {
		t.Error("删除流水不应改动到期日")
	}
}

func TestRenewWithoutBillingIsRejected(t *testing.T) {
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
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/server/42/renew", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-NodeKeep-Request", "1")
	req.AddCookie(&http.Cookie{Name: "nodekeep", Value: "token"})
	r.ServeHTTP(w, req)

	if !strings.Contains(w.Body.String(), "还没有计费信息") {
		t.Errorf("没有计费信息时应明确拒绝，实际：%s", w.Body.String())
	}
	// 不能凭空造出一条流水
	if len(dao.ServerPaymentsOf(42)) != 0 {
		t.Error("拒绝的请求不应写入流水")
	}
}
