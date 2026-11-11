// Package store holds the gateway's Redis state. Key formats are shared with
// other services and existing deployments and must not change.
package store

import (
	"fmt"
	"strconv"
	"time"
)

const dateLayout = "20060102"

// UserInfoKey is the hash of a gateway-issued auth token, read by the HTTP short link.
func UserInfoKey(authToken string) string {
	return fmt.Sprintf("MARS_AGENT:USER_INFO:APP_ID:%s:UID", authToken)
}

// AuthLockKey serializes AUTH and LOGOUT of one user across gateways.
func AuthLockKey(appID string, uid int64) string {
	return fmt.Sprintf("MARS_AGENT:AUTHLOCK:%s:%d", appID, uid)
}

// LinkStatusKey exists while the user holds a long-link connection on some gateway.
func LinkStatusKey(appID string, uid int64) string {
	return fmt.Sprintf("MARS_AGENT:LINK_STATUS:APP_ID:%s:UID:%d", appID, uid)
}

// NewConnectionChannel is the pub/sub channel that tells gateways to kick an older connection.
const NewConnectionChannel = "MARS_AGENT:NEW_CONNECTION_EVENT"

// NewConnectionEvent is the payload published on NewConnectionChannel.
func NewConnectionEvent(machineID uint16, connID uint64, appID string, uid int64) string {
	return fmt.Sprintf("MARS_AGENT:NEW_CONNECTION_EVENT:%d:%d:%s:%d", machineID, connID, appID, uid)
}

// DailyActiveLockKey guards the midnight roll-over of daily-active sets.
const DailyActiveLockKey = "MARS_AGENT:DAILY_ACTIVATED_LOCK"

// DailyActiveAppIDsKey lists apps with at least one login on date (YYYYMMDD).
func DailyActiveAppIDsKey(date string) string {
	return fmt.Sprintf("MARS_AGENT:DAILY_ACTIVATED_APP_IDS:%s", date)
}

// DailyActiveUIDsKey is the set of uids active on t's date.
func DailyActiveUIDsKey(appID string, t time.Time) string {
	return fmt.Sprintf("{MARS_AGENT:%s}:DAILY_ACTIVATED:%s", appID, t.Format(dateLayout))
}

// DailyActiveDevicesKey is the set of device ids active on t's date.
func DailyActiveDevicesKey(appID string, t time.Time) string {
	return fmt.Sprintf("{MARS_AGENT:%s}:DAILY_ACTIVATED_DEVICE:%s", appID, t.Format(dateLayout))
}

// OnlineUIDsKey is the set of uids currently online.
func OnlineUIDsKey(appID string) string {
	return fmt.Sprintf("{MARS_AGENT:%s}:ONLINE_UIDS", appID)
}

// OnlineDevicesKey is the set of device ids currently online.
func OnlineDevicesKey(appID string) string {
	return fmt.Sprintf("{MARS_AGENT:%s}:ONLINE_DEVICE_IDS", appID)
}

// DailyTextMsgKey counts chatroom messages sent on t's date.
func DailyTextMsgKey(appID string, t time.Time) string {
	return fmt.Sprintf("CHATROOM_SERVICE:DAILY_TEXT_MSG:%s:%s", appID, t.Format(dateLayout))
}

// DailyModeratedMsgKey counts chatroom messages that went through moderation on t's date.
func DailyModeratedMsgKey(appID string, t time.Time) string {
	return fmt.Sprintf("CHATROOM_SERVICE:DAILY_TEXT_MSG_NO_GARBEGE:%s:%s", appID, t.Format(dateLayout))
}

// LastLoginTokenKey remembers the login token of the user's latest AUTH, to
// tell a reconnect from a fresh login.
func LastLoginTokenKey(appID string, uid int64) string {
	return "MARS_AUTH:" + appID + ":" + strconv.FormatInt(uid, 10)
}
