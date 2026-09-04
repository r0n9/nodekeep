package main

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/patrickmn/go-cache"
	"github.com/robfig/cron/v3"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/r0n9/nodekeep/cmd/dashboard/controller"
	"github.com/r0n9/nodekeep/cmd/dashboard/rpc"
	"github.com/r0n9/nodekeep/model"
	"github.com/r0n9/nodekeep/service/dao"
)

func init() {
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		panic(err)
	}

	// 初始化 dao 包
	dao.Conf = &model.Config{}
	dao.Cron = cron.New(cron.WithLocation(shanghai))
	dao.Crons = make(map[uint64]*model.Cron)
	dao.InitServerRuntimeState()

	err = dao.Conf.Read("data/config.yaml")
	if err != nil {
		panic(err)
	}
	dao.DB, err = gorm.Open(sqlite.Open("data/sqlite.db"), &gorm.Config{})
	if err != nil {
		panic(err)
	}
	if dao.Conf.Debug {
		dao.DB = dao.DB.Debug()
	}
	dao.Cache = cache.New(5*time.Minute, 10*time.Minute)

	initSystem()
}

func initSystem() {
	dao.DB.AutoMigrate(model.Server{}, model.User{},
		model.Notification{}, model.AlertRule{}, model.Monitor{},
		model.MonitorHistory{}, model.Cron{}, model.ServerMetric{},
		model.ServerBilling{}, model.ServerPayment{})

	loadServers() //加载服务器列表
	loadCrons()   //加载计划任务

	// 清理旧数据
	dao.Cron.AddFunc("0 3 * * *", cleanMonitorHistory)
	// 每天早上检查订阅到期，聚合成一条通知发出
	dao.Cron.AddFunc("3 9 * * *", dao.CheckBillingDue)
}

func cleanMonitorHistory() {
	dao.DB.Delete(&model.MonitorHistory{}, "created_at < ?", time.Now().AddDate(0, 0, -30))
	dao.DB.Delete(&model.ServerMetric{}, "bucket_at < ?", time.Now().AddDate(0, 0, -7))
}

func loadServers() {
	var servers []model.Server
	dao.DB.Find(&servers)
	for _, s := range servers {
		dao.UpsertServerRuntime(s, false)
	}
}

func loadCrons() {
	var crons []model.Cron
	dao.DB.Find(&crons)
	var err error
	for i := 0; i < len(crons); i++ {
		cr := crons[i]
		cr.CronID, err = dao.Cron.AddFunc(cr.Scheduler, func() {
			dao.CronTrigger(&cr)
		})
		if err != nil {
			panic(err)
		}
		dao.Crons[cr.ID] = &cr
	}
	dao.Cron.Start()
}

func main() {
	logViewPassword()

	webHandler := controller.ServeWeb()
	grpcHandler := rpc.ServeRPC()

	go rpc.DispatchTask(time.Minute * 3)
	go dao.AlertSentinelStart()

	handler := h2c.NewHandler(httpAndGRPCMux(webHandler, grpcHandler), &http2.Server{})
	if err := http.ListenAndServe(fmt.Sprintf(":%d", dao.Conf.HTTPPort), handler); err != nil {
		panic(err)
	}
}

// logViewPassword 启动时打印前台查看密码，方便自建部署时直接从容器日志里翻出来。
// 未设置时明确提示前台是公开的，避免以为有密码保护。
func logViewPassword() {
	if password := strings.TrimSpace(dao.Conf.Site.ViewPassword); password != "" {
		log.Printf("前台查看密码：%s", password)
		return
	}
	log.Println("前台查看密码未设置，任何人都能查看节点状态")
}

func httpAndGRPCMux(webHandler http.Handler, grpcHandler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
			grpcHandler.ServeHTTP(w, r)
			return
		}
		webHandler.ServeHTTP(w, r)
	})
}
