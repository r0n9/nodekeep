package dao

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/r0n9/nodekeep/model"
)

// 报警规则
var alertsLock sync.RWMutex
var alerts []model.AlertRule
var alertsStore map[uint64]map[uint64][][]interface{}
var alertsPrevState map[uint64]map[uint64]uint8

const (
	alertStateUnknown uint8 = iota
	alertStateFail
	alertStatePass
)

type NotificationHistory struct {
	Duration time.Duration
	Until    time.Time
}

func AlertSentinelStart() {
	alertsStore = make(map[uint64]map[uint64][][]interface{})
	alertsPrevState = make(map[uint64]map[uint64]uint8)
	notificationsLock.Lock()
	if err := DB.Find(&notifications).Error; err != nil {
		panic(err)
	}
	notificationsLock.Unlock()
	alertsLock.Lock()
	if err := DB.Find(&alerts).Error; err != nil {
		panic(err)
	}
	for i := 0; i < len(alerts); i++ {
		alertsStore[alerts[i].ID] = make(map[uint64][][]interface{})
		alertsPrevState[alerts[i].ID] = make(map[uint64]uint8)
	}
	alertsLock.Unlock()

	time.Sleep(time.Second * 10)
	var lastPrint time.Time
	var checkCount uint64
	for {
		startedAt := time.Now()
		checkStatus()
		checkCount++
		if lastPrint.Before(startedAt.Add(-1 * time.Hour)) {
			log.Println("报警规则检测每小时", checkCount, "次", startedAt, time.Now())
			checkCount = 0
			lastPrint = startedAt
		}
		time.Sleep(time.Until(startedAt.Add(time.Second * SnapshotDelay)))
	}
}

func OnRefreshOrAddAlert(alert model.AlertRule) {
	alertsLock.Lock()
	defer alertsLock.Unlock()
	ensureAlertStateStoreLocked(alert.ID)
	delete(alertsStore, alert.ID)
	delete(alertsPrevState, alert.ID)
	var isEdit bool
	for i := 0; i < len(alerts); i++ {
		if alerts[i].ID == alert.ID {
			alerts[i] = alert
			isEdit = true
		}
	}
	if !isEdit {
		alerts = append(alerts, alert)
	}
	alertsStore[alert.ID] = make(map[uint64][][]interface{})
	alertsPrevState[alert.ID] = make(map[uint64]uint8)
}

func OnDeleteAlert(id uint64) {
	alertsLock.Lock()
	defer alertsLock.Unlock()
	delete(alertsStore, id)
	delete(alertsPrevState, id)
	for i := 0; i < len(alerts); i++ {
		if alerts[i].ID == id {
			alerts = append(alerts[:i], alerts[i+1:]...)
			i--
		}
	}
}

func checkStatus() {
	alertsLock.Lock()
	defer alertsLock.Unlock()

	servers := ServerSnapshot()
	for _, alert := range alerts {
		// 跳过未启用
		if alert.Enable == nil || !*alert.Enable {
			continue
		}
		ensureAlertStateStoreLocked(alert.ID)
		for _, server := range servers {
			// 监测点
			alertsStore[alert.ID][server.ID] = append(alertsStore[alert.
				ID][server.ID], alert.Snapshot(server))
			// 发送通知
			max, desc := alert.Check(alertsStore[alert.ID][server.ID])
			if desc != "" {
				message := alertTriggerMessage(&alert, server, desc)
				go SendNotification(message, true)
				updateAlertCheckStateLocked(alert.ID, server.ID, true)
			} else if updateAlertCheckStateLocked(alert.ID, server.ID, false) {
				message := alertRecoveryMessage(&alert, server)
				go SendNotification(message, false)
			}
			// 清理旧数据
			if max > 0 && max < len(alertsStore[alert.ID][server.ID]) {
				alertsStore[alert.ID][server.ID] = alertsStore[alert.ID][server.ID][len(alertsStore[alert.ID][server.ID])-max:]
			}
		}
	}
}

func ensureAlertStateStoreLocked(alertID uint64) {
	if alertsStore == nil {
		alertsStore = make(map[uint64]map[uint64][][]interface{})
	}
	if alertsStore[alertID] == nil {
		alertsStore[alertID] = make(map[uint64][][]interface{})
	}
	if alertsPrevState == nil {
		alertsPrevState = make(map[uint64]map[uint64]uint8)
	}
	if alertsPrevState[alertID] == nil {
		alertsPrevState[alertID] = make(map[uint64]uint8)
	}
}

func updateAlertCheckStateLocked(alertID, serverID uint64, failed bool) bool {
	if alertsPrevState == nil {
		alertsPrevState = make(map[uint64]map[uint64]uint8)
	}
	if alertsPrevState[alertID] == nil {
		alertsPrevState[alertID] = make(map[uint64]uint8)
	}
	previousState := alertsPrevState[alertID][serverID]
	if failed {
		alertsPrevState[alertID][serverID] = alertStateFail
		return false
	}
	alertsPrevState[alertID][serverID] = alertStatePass
	return previousState == alertStateFail
}

func alertTriggerMessage(alert *model.AlertRule, server *model.ServerRuntime, detail string) string {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		detail = "规则检查未通过"
	}
	return fmt.Sprintf("[告警触发]\n规则：%s\n服务器：%s\n状态：异常\n详情：\n%s", alert.Name, serverNotificationLabel(server), detail)
}

func alertRecoveryMessage(alert *model.AlertRule, server *model.ServerRuntime) string {
	status := "已恢复正常"
	for i := 0; i < len(alert.Rules); i++ {
		if alert.Rules[i].Type == "offline" {
			status = "已恢复上线"
			break
		}
	}
	return fmt.Sprintf("[告警恢复]\n规则：%s\n服务器：%s\n状态：%s", alert.Name, serverNotificationLabel(server), status)
}

func serverNotificationLabel(server *model.ServerRuntime) string {
	if server == nil {
		return "未知服务器"
	}
	name := server.Name
	if name == "" {
		name = fmt.Sprintf("ID:%d", server.ID)
	}
	if ip := serverHostIP(server); ip != "" {
		return fmt.Sprintf("%s（%s）", name, ip)
	}
	return name
}

func serverHostIP(server *model.ServerRuntime) string {
	if server == nil || server.Host == nil {
		return ""
	}
	return server.Host.IP
}
