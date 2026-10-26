package dao

import (
	"strings"
	"testing"

	"github.com/r0n9/nodekeep/model"
)

func TestUpdateAlertCheckStateLockedReturnsRecoveryOnlyAfterFailure(t *testing.T) {
	previousStates := alertsPrevState
	t.Cleanup(func() {
		alertsPrevState = previousStates
	})
	alertsPrevState = nil

	if updateAlertCheckStateLocked(1, 2, false) {
		t.Fatal("initial pass must not trigger recovery notification")
	}
	if got := alertsPrevState[1][2]; got != alertStatePass {
		t.Fatalf("initial pass stored state = %d, want %d", got, alertStatePass)
	}
	if updateAlertCheckStateLocked(1, 2, true) {
		t.Fatal("failure must not trigger recovery notification")
	}
	if got := alertsPrevState[1][2]; got != alertStateFail {
		t.Fatalf("failure stored state = %d, want %d", got, alertStateFail)
	}
	if !updateAlertCheckStateLocked(1, 2, false) {
		t.Fatal("first pass after failure must trigger recovery notification")
	}
	if updateAlertCheckStateLocked(1, 2, false) {
		t.Fatal("repeated pass must not trigger recovery notification")
	}
}

func TestAlertRecoveryMessageOfflineUsesOnlineTextAndNilHostSafe(t *testing.T) {
	alert := &model.AlertRule{
		Name:  "离线告警",
		Rules: []model.Rule{{Type: "offline"}},
	}
	server := &model.ServerRuntime{
		Server: model.Server{Name: "test-node"},
	}

	message := alertRecoveryMessage(alert, server)
	for _, want := range []string{"[告警恢复]", "规则：离线告警", "服务器：test-node", "状态：已恢复上线"} {
		if !strings.Contains(message, want) {
			t.Fatalf("recovery message %q does not contain %q", message, want)
		}
	}
	if strings.Contains(message, "()") {
		t.Fatalf("recovery message must not render empty IP parentheses: %q", message)
	}
}

func TestAlertTriggerMessageUsesProfessionalTemplate(t *testing.T) {
	alert := &model.AlertRule{Name: "CPU 告警"}
	server := &model.ServerRuntime{
		Server: model.Server{Name: "test-node"},
		Host:   &model.Host{IP: "192.0.2.1"},
	}

	message := alertTriggerMessage(alert, server, "cpu > 90")
	for _, want := range []string{"[告警触发]", "规则：CPU 告警", "服务器：test-node（192.0.2.1）", "状态：异常", "详情：\ncpu > 90"} {
		if !strings.Contains(message, want) {
			t.Fatalf("trigger message %q does not contain %q", message, want)
		}
	}
	for _, unwanted := range []string{"逮到咯", "快去看看"} {
		if strings.Contains(message, unwanted) {
			t.Fatalf("trigger message must not contain %q: %q", unwanted, message)
		}
	}
}

func TestAlertClusterOutageMessage(t *testing.T) {
	alert := &model.AlertRule{Name: "离线告警"}
	failing := []serverCheckResult{
		{server: &model.ServerRuntime{Server: model.Server{Name: "node-1"}}},
		{server: &model.ServerRuntime{Server: model.Server{Name: "node-2"}}},
		{server: &model.ServerRuntime{Server: model.Server{Name: "node-3"}}},
	}

	msg := alertClusterOutageMessage(alert, 3, 4, failing)
	for _, want := range []string{
		"[系统告警]",
		"检测到大量节点同时失联",
		"规则：离线告警",
		"失联节点：3/4 台（占比 75%）",
		"疑似监控面板入站网络异常",
		"已自动触发集群离线熔断保护",
		"- node-1",
		"- node-2",
		"- node-3",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("alertClusterOutageMessage missing %q in %q", want, msg)
		}
	}
}

func TestAlertClusterRecoveryMessage(t *testing.T) {
	alert := &model.AlertRule{Name: "离线告警"}
	reconnected := []string{"node-1", "node-2", "node-3"}

	msg := alertClusterRecoveryMessage(alert, 0, 4, reconnected)
	for _, want := range []string{
		"[系统恢复]",
		"批量失联节点已重新连接",
		"规则：离线告警",
		"集群离线熔断已解除（当前失联降至 0/4 台）",
		"已恢复上线节点：",
		"- node-1",
		"- node-2",
		"- node-3",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("alertClusterRecoveryMessage missing %q in %q", want, msg)
		}
	}
}

func TestFormatServerNameList(t *testing.T) {
	if got := formatServerNameList(nil, 5); got != "" {
		t.Errorf("expected empty string for nil list, got %q", got)
	}

	names := []string{"s1", "s2", "s3"}
	got := formatServerNameList(names, 5)
	if got != "- s1\n- s2\n- s3" {
		t.Errorf("unexpected format: %q", got)
	}

	many := []string{"s1", "s2", "s3", "s4", "s5"}
	gotTruncated := formatServerNameList(many, 2)
	if !strings.Contains(gotTruncated, "- s1\n- s2") || !strings.Contains(gotTruncated, "等共 5 台服务器") {
		t.Errorf("unexpected truncation format: %q", gotTruncated)
	}
}
