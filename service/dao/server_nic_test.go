package dao

import (
	"testing"
	"time"

	"github.com/r0n9/nodekeep/model"
)

func TestSyncServerNICsPersistsAndRefreshes(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.ServerNIC{}); err != nil {
		t.Fatalf("auto migrate server nic: %v", err)
	}

	serverID := uint64(7)
	now := time.Date(2026, 9, 20, 10, 0, 0, 0, time.Local)
	counters := []model.NICCounter{
		{Name: "eth0", BytesRecv: 1, BytesSent: 2},
		{Name: "docker0", BytesRecv: 3, BytesSent: 4, Virtual: true},
	}

	syncServerNICs(serverID, counters, now)

	var stored []model.ServerNIC
	if err := db.Where("server_id = ?", serverID).Order("name ASC").Find(&stored).Error; err != nil {
		t.Fatalf("load server nics: %v", err)
	}
	if len(stored) != 2 {
		t.Fatalf("stored NICs = %d, want 2", len(stored))
	}
	if stored[0].Name != "docker0" || !stored[0].Virtual {
		t.Fatalf("first NIC = %#v, want docker0 virtual", stored[0])
	}
	if stored[1].Name != "eth0" || stored[1].Virtual {
		t.Fatalf("second NIC = %#v, want eth0 non-virtual", stored[1])
	}

	// 集合未变化且未到刷新间隔：不应重复写库。
	syncServerNICs(serverID, counters, now.Add(time.Minute))
	var count int64
	db.Model(&model.ServerNIC{}).Where("server_id = ?", serverID).Count(&count)
	if count != 2 {
		t.Fatalf("NIC rows after unchanged sync = %d, want 2", count)
	}

	// 集合变化：应新增记录。
	syncServerNICs(serverID, append(counters, model.NICCounter{Name: "wlo1"}), now.Add(2*time.Minute))
	db.Model(&model.ServerNIC{}).Where("server_id = ?", serverID).Count(&count)
	if count != 3 {
		t.Fatalf("NIC rows after set changed = %d, want 3", count)
	}
}

// 网卡集合长期不变时，last_seen 仍需定期推进，否则后台会把在用网卡误判为已消失。
func TestSyncServerNICsRefreshesLastSeenAfterInterval(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.ServerNIC{}); err != nil {
		t.Fatalf("auto migrate server nic: %v", err)
	}

	serverID := uint64(8)
	now := time.Date(2026, 9, 20, 10, 0, 0, 0, time.Local)
	counters := []model.NICCounter{{Name: "eth0", BytesRecv: 1, BytesSent: 2}}

	syncServerNICs(serverID, counters, now)
	syncServerNICs(serverID, counters, now.Add(serverNICRefreshInterval+time.Minute))

	var stored model.ServerNIC
	if err := db.First(&stored, "server_id = ? AND name = ?", serverID, "eth0").Error; err != nil {
		t.Fatalf("load server nic: %v", err)
	}
	if !stored.LastSeen.After(now) {
		t.Fatalf("last_seen = %v, want after %v", stored.LastSeen, now)
	}
}

func TestDeleteServerNICs(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.ServerNIC{}); err != nil {
		t.Fatalf("auto migrate server nic: %v", err)
	}

	syncServerNICs(9, []model.NICCounter{{Name: "eth0"}}, time.Now())
	DeleteServerNICs(9)

	var count int64
	db.Model(&model.ServerNIC{}).Where("server_id = ?", 9).Count(&count)
	if count != 0 {
		t.Fatalf("NIC rows after delete = %d, want 0", count)
	}
}
