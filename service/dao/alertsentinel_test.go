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
