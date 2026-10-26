package dao

import (
	"crypto/md5"
	"encoding/hex"
	"sync"
	"time"

	"github.com/r0n9/nodekeep/model"
)

const (
	firstNotificationDelay = time.Minute * 15
	notificationCacheTTL   = time.Hour * 48
)

// 通知方式
var notifications []model.Notification
var notificationsLock sync.RWMutex

func OnRefreshOrAddNotification(n model.Notification) {
	notificationsLock.Lock()
	defer notificationsLock.Unlock()
	var isEdit bool
	for i := 0; i < len(notifications); i++ {
		if notifications[i].ID == n.ID {
			notifications[i] = n
			isEdit = true
		}
	}
	if !isEdit {
		notifications = append(notifications, n)
	}
}

func OnDeleteNotification(id uint64) {
	notificationsLock.Lock()
	defer notificationsLock.Unlock()
	for i := 0; i < len(notifications); i++ {
		if notifications[i].ID == id {
			notifications = append(notifications[:i], notifications[i+1:]...)
			i--
		}
	}
}

func SendNotification(desc string, muteable bool) {
	if muteable {
		// 通知防骚扰策略
		nID := hex.EncodeToString(md5.New().Sum([]byte(desc)))
		var flag bool
		if cacheN, has := Cache.Get(nID); has {
			nHistory := cacheN.(NotificationHistory)
			// 每次提醒都增加一倍等待时间，最后每天最多提醒一次
			if time.Now().After(nHistory.Until) {
				flag = true
				nHistory.Duration *= 2
				if nHistory.Duration > time.Hour*24 {
					nHistory.Duration = time.Hour * 24
				}
				nHistory.Until = time.Now().Add(nHistory.Duration)
				// 缓存保留足够长时间，避免夜间免打扰时段过后退避进度在次日被清空重置
				Cache.Set(nID, nHistory, notificationCacheTTL)
			}
		} else {
			// 新提醒直接通知
			flag = true
			Cache.Set(nID, NotificationHistory{
				Duration: firstNotificationDelay,
				Until:    time.Now().Add(firstNotificationDelay),
			}, notificationCacheTTL)
		}

		if !flag {
			return
		}
	}
	// 发出通知
	notificationsLock.RLock()
	defer notificationsLock.RUnlock()
	for i := 0; i < len(notifications); i++ {
		notifications[i].Send(desc)
	}
}
