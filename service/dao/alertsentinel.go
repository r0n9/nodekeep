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
var alertsClusterOutage map[uint64]bool
var alertsIndividuallyAlerted map[uint64]map[uint64]bool

const (
	alertStateUnknown uint8 = iota
	alertStateFail
	alertStatePass
)

const (
	ClusterOutageMinServers = 3   // 触发集群离线熔断的最小受控服务器总数
	ClusterOutageRatio      = 0.6 // 触发集群离线熔断的失联比例阈值 (>= 60%)
	ClusterOutageMinOffline = 2   // 触发集群离线熔断的最小失联节点数
)

type NotificationHistory struct {
	Duration time.Duration
	Until    time.Time
}

type serverCheckResult struct {
	server *model.ServerRuntime
	max    int
	desc   string
}

func AlertSentinelStart() {
	alertsStore = make(map[uint64]map[uint64][][]interface{})
	alertsPrevState = make(map[uint64]map[uint64]uint8)
	alertsClusterOutage = make(map[uint64]bool)
	alertsIndividuallyAlerted = make(map[uint64]map[uint64]bool)
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
	delete(alertsClusterOutage, alert.ID)
	delete(alertsIndividuallyAlerted, alert.ID)
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
	alertsIndividuallyAlerted[alert.ID] = make(map[uint64]bool)
}

func OnDeleteAlert(id uint64) {
	alertsLock.Lock()
	defer alertsLock.Unlock()
	delete(alertsStore, id)
	delete(alertsPrevState, id)
	delete(alertsClusterOutage, id)
	delete(alertsIndividuallyAlerted, id)
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

		results := make([]serverCheckResult, 0, len(servers))
		for _, server := range servers {
			// 监测点
			alertsStore[alert.ID][server.ID] = append(alertsStore[alert.
				ID][server.ID], alert.Snapshot(server))
			// 规则检查
			max, desc := alert.Check(alertsStore[alert.ID][server.ID])
			results = append(results, serverCheckResult{server: server, max: max, desc: desc})

			// 清理旧数据
			if max > 0 && max < len(alertsStore[alert.ID][server.ID]) {
				alertsStore[alert.ID][server.ID] = alertsStore[alert.ID][server.ID][len(alertsStore[alert.ID][server.ID])-max:]
			}
		}

		// 检查是否包含 offline 规则
		var offlineRule *model.Rule
		for i := range alert.Rules {
			if alert.Rules[i].Type == "offline" {
				offlineRule = &alert.Rules[i]
				break
			}
		}

		// 统计该规则下有效参与检测的节点与失联节点
		var totalEligible int
		var failingResults []serverCheckResult
		for _, res := range results {
			if offlineRule != nil && offlineRule.Ignore[res.server.ID] {
				continue
			}
			totalEligible++
			if res.desc != "" {
				failingResults = append(failingResults, res)
			}
		}

		isClusterOutage := offlineRule != nil &&
			totalEligible >= ClusterOutageMinServers &&
			len(failingResults) >= ClusterOutageMinOffline &&
			float64(len(failingResults))/float64(totalEligible) >= ClusterOutageRatio

		wasClusterOutage := alertsClusterOutage[alert.ID]

		// 触发或持续处于大面积失联熔断状态
		if isClusterOutage {
			alertsClusterOutage[alert.ID] = true
			if alert.AllowedNotificationTime(time.Now()) {
				message := alertClusterOutageMessage(&alert, len(failingResults), totalEligible, failingResults)
				go SendNotification(message, true)
			}
			// 熔断期间：抑制单机离线告警，仅维护内部失败状态
			for _, res := range results {
				if res.desc != "" {
					updateAlertCheckStateLocked(alert.ID, res.server.ID, true)
				}
			}
			continue
		}

		// 大面积失联恢复（失联比例降至阈值以下）
		if wasClusterOutage {
			alertsClusterOutage[alert.ID] = false
			reconnectedNames := make([]string, 0, len(results))
			for _, res := range results {
				if res.desc == "" && alertsPrevState[alert.ID][res.server.ID] == alertStateFail && !alertsIndividuallyAlerted[alert.ID][res.server.ID] {
					reconnectedNames = append(reconnectedNames, serverNotificationLabel(res.server))
				}
			}
			message := alertClusterRecoveryMessage(&alert, len(failingResults), totalEligible, reconnectedNames)
			go SendNotification(message, false)
		}

		// 正常单机告警处理（未熔断或熔断已解除）
		for _, res := range results {
			if res.desc != "" {
				if alert.AllowedNotificationTime(time.Now()) {
					message := alertTriggerMessage(&alert, res.server, res.desc)
					go SendNotification(message, true)
					updateAlertCheckStateLocked(alert.ID, res.server.ID, true)
					alertsIndividuallyAlerted[alert.ID][res.server.ID] = true
				}
			} else {
				wasAlerted := alertsIndividuallyAlerted[alert.ID][res.server.ID]
				recovered := updateAlertCheckStateLocked(alert.ID, res.server.ID, false)
				if wasAlerted && recovered {
					message := alertRecoveryMessage(&alert, res.server)
					go SendNotification(message, false)
					alertsIndividuallyAlerted[alert.ID][res.server.ID] = false
				}
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
	if alertsClusterOutage == nil {
		alertsClusterOutage = make(map[uint64]bool)
	}
	if alertsIndividuallyAlerted == nil {
		alertsIndividuallyAlerted = make(map[uint64]map[uint64]bool)
	}
	if alertsIndividuallyAlerted[alertID] == nil {
		alertsIndividuallyAlerted[alertID] = make(map[uint64]bool)
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
		if alert.Rules[i].Type == "expire_days" {
			status = "到期日已更新"
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

func alertClusterOutageMessage(alert *model.AlertRule, offlineCount, totalCount int, failingServers []serverCheckResult) string {
	names := make([]string, 0, len(failingServers))
	for _, res := range failingServers {
		names = append(names, serverNotificationLabel(res.server))
	}
	ratio := float64(offlineCount) / float64(totalCount) * 100
	listText := formatServerNameList(names, 15)

	return fmt.Sprintf("[系统告警] 检测到大量节点同时失联\n规则：%s\n失联节点：%d/%d 台（占比 %.0f%%）\n状态：疑似监控面板入站网络异常或机房故障\n处理措施：已自动触发集群离线熔断保护，暂停发送单机离线告警\n失联列表：\n%s",
		alert.Name, offlineCount, totalCount, ratio, listText)
}

func alertClusterRecoveryMessage(alert *model.AlertRule, currentOffline, totalCount int, reconnectedNames []string) string {
	var detail string
	if len(reconnectedNames) > 0 {
		detail = fmt.Sprintf("\n已恢复上线节点：\n%s", formatServerNameList(reconnectedNames, 15))
	}
	return fmt.Sprintf("[系统恢复] 批量失联节点已重新连接\n规则：%s\n状态：集群离线熔断已解除（当前失联降至 %d/%d 台）%s",
		alert.Name, currentOffline, totalCount, detail)
}

func formatServerNameList(names []string, maxDisplay int) string {
	if len(names) == 0 {
		return ""
	}
	if len(names) <= maxDisplay {
		return "- " + strings.Join(names, "\n- ")
	}
	displayed := names[:maxDisplay]
	return fmt.Sprintf("- %s\n... 等共 %d 台服务器", strings.Join(displayed, "\n- "), len(names))
}
