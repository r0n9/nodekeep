package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/robfig/cron/v3"
	"gorm.io/gorm"

	"github.com/r0n9/nodekeep/model"
	"github.com/r0n9/nodekeep/pkg/mygin"
	"github.com/r0n9/nodekeep/pkg/utils"
	"github.com/r0n9/nodekeep/service/dao"
)

type memberAPI struct {
	r gin.IRouter
}

func (ma *memberAPI) serve() {
	mr := ma.r.Group("")
	mr.Use(mygin.Authorize(mygin.AuthorizeOption{
		Member:   true,
		IsPage:   false,
		Msg:      "访问此接口需要登录",
		Btn:      "点此登录",
		Redirect: "/login",
	}))
	mr.Use(mygin.RequireSameOriginForUnsafeRequests())

	mr.GET("/search-server", ma.searchServer)
	mr.POST("/server", ma.addOrEditServer)
	mr.POST("/monitor", ma.addOrEditMonitor)
	mr.POST("/cron", ma.addOrEditCron)
	mr.POST("/cron/:id/manual", ma.manualTrigger)
	mr.POST("/notification", ma.addOrEditNotification)
	mr.POST("/alert-rule", ma.addOrEditAlertRule)
	mr.POST("/setting", ma.updateSetting)
	mr.DELETE("/:model/:id", ma.delete)
	mr.POST("/logout", ma.logout)
}

func (ma *memberAPI) delete(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	if id < 1 {
		c.JSON(http.StatusOK, model.Response{
			Code:    http.StatusBadRequest,
			Message: "错误的 Server ID",
		})
		return
	}

	var err error
	switch c.Param("model") {
	case "server":
		err = dao.DB.Delete(&model.Server{}, "id = ?", id).Error
		if err == nil {
			err = dao.DeleteServerBillingData(id)
		}
		if err == nil {
			dao.DeleteServerRuntime(id)
		}
	case "notification":
		err = dao.DB.Delete(&model.Notification{}, "id = ?", id).Error
		if err == nil {
			dao.OnDeleteNotification(id)
		}
	case "monitor":
		err = dao.DB.Delete(&model.Monitor{}, "id = ?", id).Error
		if err == nil {
			err = dao.DB.Delete(&model.MonitorHistory{}, "monitor_id = ?", id).Error
		}
	case "cron":
		err = dao.DB.Delete(&model.Cron{}, "id = ?", id).Error
		if err == nil {
			dao.CronLock.Lock()
			defer dao.CronLock.Unlock()
			cr := dao.Crons[id]
			if cr != nil && cr.CronID != 0 {
				dao.Cron.Remove(cr.CronID)
			}
			delete(dao.Crons, id)
		}
	case "alert-rule":
		err = dao.DB.Delete(&model.AlertRule{}, "id = ?", id).Error
		if err == nil {
			dao.OnDeleteAlert(id)
		}
	}
	if err != nil {
		c.JSON(http.StatusOK, model.Response{
			Code:    http.StatusBadRequest,
			Message: fmt.Sprintf("数据库错误：%s", err),
		})
		return
	}
	c.JSON(http.StatusOK, model.Response{
		Code: http.StatusOK,
	})
}

type searchResult struct {
	Name  string `json:"name,omitempty"`
	Value uint64 `json:"value,omitempty"`
	Text  string `json:"text,omitempty"`
}

func (ma *memberAPI) searchServer(c *gin.Context) {
	var servers []model.Server
	likeWord := "%" + c.Query("word") + "%"
	dao.DB.Select("id,name").Where("id = ? OR name LIKE ? OR tag LIKE ? OR note LIKE ?",
		c.Query("word"), likeWord, likeWord, likeWord).
		Order("tag DESC").
		Order("display_index DESC").
		Order("id DESC").
		Find(&servers)

	var resp []searchResult
	for i := 0; i < len(servers); i++ {
		resp = append(resp, searchResult{
			Value: servers[i].ID,
			Name:  servers[i].Name,
			Text:  servers[i].Name,
		})
	}

	c.JSON(http.StatusOK, map[string]interface{}{
		"success": true,
		"results": resp,
	})
}

type serverForm struct {
	ID           uint64
	Name         string `binding:"required"`
	DisplayIndex int
	Secret       string
	Tag          string
	Note         string

	// 计费信息
	Provider      string
	ProductPlan   string
	PanelURL      string
	OrderNo       string
	Currency      string
	Amount        string // 十进制金额，如 12.50；服务端转成最小单位存储
	Cycle         string
	CycleCount    int
	AutoRenew     string // checkbox，选中时为 "on"
	StartDate     string // YYYY-MM-DD
	NextDueDate   string
	EndDate       string
	Status        uint8
	RemindDaysRaw string
	Muted         string
	HideBilling   string

	// 套餐规格
	Bandwidth    string
	TrafficVol   string
	TrafficType  uint8
	TrafficReset int
	IPv4Count    int
	IPv6Count    int
	NetworkRoute string
	Location     string
	CPUSpec      string
	MemSpec      string
	DiskSpec     string
}

// hasBillingData 判断计费表单是否被填写过。整体留空表示这台机器没有计费信息，
// 保存时会清掉已有的订阅记录（付费流水属于历史，不删）。
func (sf *serverForm) hasBillingData() bool {
	texts := []string{
		sf.Provider, sf.ProductPlan, sf.PanelURL, sf.OrderNo,
		sf.Amount, sf.Cycle, sf.StartDate, sf.NextDueDate, sf.EndDate,
		sf.RemindDaysRaw, sf.Bandwidth, sf.TrafficVol, sf.NetworkRoute,
		sf.Location, sf.CPUSpec, sf.MemSpec, sf.DiskSpec,
	}
	for _, text := range texts {
		if strings.TrimSpace(text) != "" {
			return true
		}
	}
	if sf.TrafficType != 0 || sf.TrafficReset != 0 || sf.IPv4Count != 0 || sf.IPv6Count != 0 {
		return true
	}
	return sf.AutoRenew == "on" || sf.Muted == "on" || sf.HideBilling == "on"
}

var validBillingCycles = map[string]bool{
	"":                             true,
	model.BillingCycleMonthly:      true,
	model.BillingCycleQuarterly:    true,
	model.BillingCycleSemiannually: true,
	model.BillingCycleAnnually:     true,
	model.BillingCycleBiennially:   true,
	model.BillingCycleTriennially:  true,
	model.BillingCycleOnetime:      true,
}

// buildServerBilling 校验并组装订阅信息。所有校验都在写库之前完成，
// 这样事务里只剩下真正的写操作。
func (sf *serverForm) buildServerBilling() (*model.ServerBilling, error) {
	if !validBillingCycles[sf.Cycle] {
		return nil, fmt.Errorf("未知的计费周期：%s", sf.Cycle)
	}
	amount, err := model.ParseAmount(sf.Amount)
	if err != nil {
		return nil, err
	}
	startDate, err := model.ParseBillingDate(sf.StartDate)
	if err != nil {
		return nil, err
	}
	nextDueDate, err := model.ParseBillingDate(sf.NextDueDate)
	if err != nil {
		return nil, err
	}
	endDate, err := model.ParseBillingDate(sf.EndDate)
	if err != nil {
		return nil, err
	}
	status := sf.Status
	if status != model.BillingStatusCancelled {
		status = model.BillingStatusActive
	}
	cycleCount := sf.CycleCount
	if cycleCount < 1 {
		cycleCount = 1
	}
	return &model.ServerBilling{
		Provider:      strings.TrimSpace(sf.Provider),
		ProductPlan:   strings.TrimSpace(sf.ProductPlan),
		PanelURL:      strings.TrimSpace(sf.PanelURL),
		OrderNo:       strings.TrimSpace(sf.OrderNo),
		Currency:      strings.ToUpper(strings.TrimSpace(sf.Currency)),
		AmountCents:   amount,
		Cycle:         sf.Cycle,
		CycleCount:    cycleCount,
		AutoRenew:     sf.AutoRenew == "on",
		StartDate:     startDate,
		NextDueDate:   nextDueDate,
		EndDate:       endDate,
		Status:        status,
		RemindDaysRaw: strings.TrimSpace(sf.RemindDaysRaw),
		Muted:         sf.Muted == "on",
		HideBilling:   sf.HideBilling == "on",
		Extra: model.BillingExtra{
			Bandwidth:    strings.TrimSpace(sf.Bandwidth),
			TrafficVol:   strings.TrimSpace(sf.TrafficVol),
			TrafficType:  sf.TrafficType,
			TrafficReset: sf.TrafficReset,
			IPv4Count:    sf.IPv4Count,
			IPv6Count:    sf.IPv6Count,
			NetworkRoute: strings.TrimSpace(sf.NetworkRoute),
			Location:     strings.TrimSpace(sf.Location),
			CPUSpec:      strings.TrimSpace(sf.CPUSpec),
			MemSpec:      strings.TrimSpace(sf.MemSpec),
			DiskSpec:     strings.TrimSpace(sf.DiskSpec),
		},
	}, nil
}

func (ma *memberAPI) addOrEditServer(c *gin.Context) {
	admin := c.MustGet(model.CtxKeyAuthorizedUser).(*model.User)
	var sf serverForm
	var s model.Server
	var billing *model.ServerBilling
	var isEdit bool
	err := c.ShouldBindJSON(&sf)
	if err == nil && sf.hasBillingData() {
		billing, err = sf.buildServerBilling()
	}
	if err == nil {
		s.Name = sf.Name
		s.Secret = sf.Secret
		s.DisplayIndex = sf.DisplayIndex
		s.ID = sf.ID
		s.Tag = sf.Tag
		s.Note = sf.Note
		if sf.ID == 0 {
			s.Secret = utils.MD5(fmt.Sprintf("%s%s%d", time.Now(), sf.Name, admin.ID))
			s.Secret = s.Secret[:18]
		} else {
			isEdit = true
		}
		// 服务器和计费信息一起成败，避免出现只写了一半的记录。
		err = dao.DB.Transaction(func(tx *gorm.DB) error {
			var txErr error
			if isEdit {
				txErr = tx.Save(&s).Error
			} else {
				txErr = tx.Create(&s).Error
			}
			if txErr != nil {
				return txErr
			}
			if billing == nil {
				return dao.DeleteServerBillingWith(tx, s.ID)
			}
			billing.ServerID = s.ID
			return dao.SaveServerBillingWith(tx, billing)
		})
	}
	if err != nil {
		c.JSON(http.StatusOK, model.Response{
			Code:    http.StatusBadRequest,
			Message: fmt.Sprintf("请求错误：%s", err),
		})
		return
	}
	dao.UpsertServerRuntime(s, isEdit)
	c.JSON(http.StatusOK, model.Response{
		Code: http.StatusOK,
	})
}

type monitorForm struct {
	ID     uint64
	Name   string
	Target string
	Type   uint8
}

func (ma *memberAPI) addOrEditMonitor(c *gin.Context) {
	var mf monitorForm
	var m model.Monitor
	err := c.ShouldBindJSON(&mf)
	if err == nil {
		m.Name = mf.Name
		m.Target = mf.Target
		m.Type = mf.Type
		m.ID = mf.ID
	}
	if err == nil {
		if m.ID == 0 {
			err = dao.DB.Create(&m).Error
		} else {
			err = dao.DB.Save(&m).Error
		}
	}
	if err != nil {
		c.JSON(http.StatusOK, model.Response{
			Code:    http.StatusBadRequest,
			Message: fmt.Sprintf("请求错误：%s", err),
		})
		return
	}
	c.JSON(http.StatusOK, model.Response{
		Code: http.StatusOK,
	})
}

type cronForm struct {
	ID             uint64
	Name           string
	Scheduler      string
	Command        string
	ServersRaw     string
	PushSuccessful string
}

func (ma *memberAPI) addOrEditCron(c *gin.Context) {
	var cf cronForm
	var cr model.Cron
	err := c.ShouldBindJSON(&cf)
	if err == nil {
		cr.Name = cf.Name
		cr.Scheduler = cf.Scheduler
		cr.Command = cf.Command
		cr.ServersRaw = cf.ServersRaw
		cr.PushSuccessful = cf.PushSuccessful == "on"
		cr.ID = cf.ID
		err = json.Unmarshal([]byte(cf.ServersRaw), &cr.Servers)
	}
	if err == nil {
		_, err = cron.ParseStandard(cr.Scheduler)
	}
	if err == nil {
		if cf.ID == 0 {
			err = dao.DB.Create(&cr).Error
		} else {
			err = dao.DB.Save(&cr).Error
		}
	}

	if err != nil {
		c.JSON(http.StatusOK, model.Response{
			Code:    http.StatusBadRequest,
			Message: fmt.Sprintf("请求错误：%s", err),
		})
		return
	}

	dao.CronLock.Lock()
	defer dao.CronLock.Unlock()
	crOld := dao.Crons[cr.ID]
	if crOld != nil && crOld.CronID != 0 {
		dao.Cron.Remove(crOld.CronID)
	}

	cr.CronID, err = dao.Cron.AddFunc(cr.Scheduler, func() {
		dao.CronTrigger(&cr)
	})

	delete(dao.Crons, cr.ID)
	dao.Crons[cr.ID] = &cr

	c.JSON(http.StatusOK, model.Response{
		Code: http.StatusOK,
	})
}

func (ma *memberAPI) manualTrigger(c *gin.Context) {
	var cr model.Cron
	if err := dao.DB.First(&cr, "id = ?", c.Param("id")).Error; err != nil {
		c.JSON(http.StatusOK, model.Response{
			Code:    http.StatusBadRequest,
			Message: err.Error(),
		})
		return
	}

	dao.CronTrigger(&cr)

	c.JSON(http.StatusOK, model.Response{
		Code: http.StatusOK,
	})
}

type notificationForm struct {
	ID            uint64
	Name          string
	URL           string
	RequestMethod int
	RequestType   int
	RequestBody   string
	VerifySSL     string
}

func (ma *memberAPI) addOrEditNotification(c *gin.Context) {
	var nf notificationForm
	var n model.Notification
	err := c.ShouldBindJSON(&nf)
	if err == nil {
		n.Name = nf.Name
		n.RequestMethod = nf.RequestMethod
		n.RequestType = nf.RequestType
		n.RequestBody = nf.RequestBody
		n.URL = nf.URL
		verifySSL := nf.VerifySSL == "on"
		n.VerifySSL = &verifySSL
		n.ID = nf.ID
		err = n.Send("这是测试消息")
	}
	if err == nil {
		if n.ID == 0 {
			err = dao.DB.Create(&n).Error
		} else {
			err = dao.DB.Save(&n).Error
		}
	}
	if err != nil {
		c.JSON(http.StatusOK, model.Response{
			Code:    http.StatusBadRequest,
			Message: fmt.Sprintf("请求错误：%s", err),
		})
		return
	}
	dao.OnRefreshOrAddNotification(n)
	c.JSON(http.StatusOK, model.Response{
		Code: http.StatusOK,
	})
}

type alertRuleForm struct {
	ID       uint64
	Name     string
	RulesRaw string
	Enable   string
}

func (ma *memberAPI) addOrEditAlertRule(c *gin.Context) {
	var arf alertRuleForm
	var r model.AlertRule
	err := c.ShouldBindJSON(&arf)
	if err == nil {
		err = json.Unmarshal([]byte(arf.RulesRaw), &r.Rules)
	}
	if err == nil {
		if len(r.Rules) == 0 {
			err = errors.New("至少定义一条规则")
		} else {
			for i := 0; i < len(r.Rules); i++ {
				if r.Rules[i].Duration < 3 {
					err = errors.New("Duration 至少为 3")
					break
				}
			}
		}
	}
	if err == nil {
		r.Name = arf.Name
		r.RulesRaw = arf.RulesRaw
		enable := arf.Enable == "on"
		r.Enable = &enable
		r.ID = arf.ID
		if r.ID == 0 {
			err = dao.DB.Create(&r).Error
		} else {
			err = dao.DB.Save(&r).Error
		}
	}
	if err != nil {
		c.JSON(http.StatusOK, model.Response{
			Code:    http.StatusBadRequest,
			Message: fmt.Sprintf("请求错误：%s", err),
		})
		return
	}
	dao.OnRefreshOrAddAlert(r)
	c.JSON(http.StatusOK, model.Response{
		Code: http.StatusOK,
	})
}

type logoutForm struct {
	ID uint64
}

func (ma *memberAPI) logout(c *gin.Context) {
	admin := c.MustGet(model.CtxKeyAuthorizedUser).(*model.User)
	var lf logoutForm
	if err := c.ShouldBindJSON(&lf); err != nil {
		c.JSON(http.StatusOK, model.Response{
			Code:    http.StatusBadRequest,
			Message: fmt.Sprintf("请求错误：%s", err),
		})
		return
	}
	if lf.ID != admin.ID {
		c.JSON(http.StatusOK, model.Response{
			Code:    http.StatusBadRequest,
			Message: fmt.Sprintf("请求错误：%s", "用户ID不匹配"),
		})
		return
	}
	dao.DB.Model(admin).UpdateColumns(model.User{
		Token:        "",
		TokenExpired: time.Now(),
	})
	mygin.ClearSecureCookie(c, dao.Conf.Site.CookieName)
	c.JSON(http.StatusOK, model.Response{
		Code: http.StatusOK,
	})
}

type settingForm struct {
	Title                      string
	Admin                      string
	CustomCode                 string
	ViewPassword               string
	EnableIPChangeNotification string
	Oauth2Type                 string
	LocalAuthEnabled           string
	LocalAuthUsername          string
	LocalAuthPassword          string
	AgentInstallHost           string
	AgentTLS                   string
}

func (ma *memberAPI) updateSetting(c *gin.Context) {
	var sf settingForm
	if err := c.ShouldBind(&sf); err != nil {
		c.JSON(http.StatusOK, model.Response{
			Code:    http.StatusBadRequest,
			Message: fmt.Sprintf("请求错误：%s", err),
		})
		return
	}
	dao.Conf.EnableIPChangeNotification = sf.EnableIPChangeNotification == "on"
	dao.Conf.Site.Brand = sf.Title
	dao.Conf.Site.CustomCode = sf.CustomCode
	dao.Conf.Site.ViewPassword = sf.ViewPassword
	dao.Conf.Oauth2.Type = sf.Oauth2Type
	dao.Conf.Oauth2.Admin = sf.Admin
	dao.Conf.Auth.Local.Enabled = sf.LocalAuthEnabled == "on"
	dao.Conf.Auth.Local.Username = sf.LocalAuthUsername
	dao.Conf.Agent.InstallHost = sf.AgentInstallHost
	dao.Conf.Agent.TLS = sf.AgentTLS == "on"
	if sf.LocalAuthPassword != "" {
		dao.Conf.Auth.Local.Password = sf.LocalAuthPassword
	}
	if dao.Conf.Auth.Local.Enabled && (dao.Conf.Auth.Local.Username == "" || dao.Conf.Auth.Local.Password == "") {
		c.JSON(http.StatusOK, model.Response{
			Code:    http.StatusBadRequest,
			Message: "启用本地账号登录时，用户名和密码不能为空",
		})
		return
	}
	if err := dao.Conf.Save(); err != nil {
		c.JSON(http.StatusOK, model.Response{
			Code:    http.StatusBadRequest,
			Message: fmt.Sprintf("请求错误：%s", err),
		})
		return
	}
	c.JSON(http.StatusOK, model.Response{
		Code: http.StatusOK,
	})
}
