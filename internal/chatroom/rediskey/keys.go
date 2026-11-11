// Package rediskey builds the Redis keys used by the chatroom service. The
// formats are shared with other services and must not change.
package rediskey

import (
	"fmt"
	"strconv"
	"strings"
)

// ChatroomInfo 房间信息（JSON）。
func ChatroomInfo(appID string, roomID int64) string {
	return fmt.Sprintf("CHATROOM_SERVICE:CHATROOM_INFO:APP_ID:%s:ROOM_ID:%d", appID, roomID)
}

// ChatroomIsOpened 房间是否开启（"0"/"1"）。
func ChatroomIsOpened(appID string, roomID int64) string {
	return fmt.Sprintf("CHATROOM_SERVICE:CHATROOM_STATUS:APP_ID:%s:ROOM_ID:%d", appID, roomID)
}

// ChatroomOnlineMember set，房间所有在线用户（包括机器人）。
func ChatroomOnlineMember(appID string, roomID int64) string {
	return fmt.Sprintf("CHATROOM_SERVICE:CHATROOM_ONLINE_MEMBER:APP_ID:%s:ROOM_ID:%d", appID, roomID)
}

// MemberInChatroomSet set，用户所在房间列表。
func MemberInChatroomSet(appID string, uid int64) string {
	return fmt.Sprintf("CHATROOM_SERVICE:MEMBER_IN_CHATROOM_SET:APP_ID:%s:UID:%d", appID, uid)
}

// ChatroomKickedReason list，被踢出原因（最近 10 条）。
func ChatroomKickedReason(appID string, roomID, uid int64) string {
	return fmt.Sprintf("CHATROOM_SERVICE:CHATROOM_KICKED_REASON:APP_ID:%s:ROOM_ID:%d:UID:%d", appID, roomID, uid)
}

// ChatroomMemberOptExt 对房间成员操作时附带的 ext（JSON）。
func ChatroomMemberOptExt(appID string, roomID, uid int64) string {
	return fmt.Sprintf("CHATROOM_SERVICE:CHATROOMMEMBEROPTEXT:APP_ID:%s:ROOM_ID:%d:UID:%d", appID, roomID, uid)
}

// ChatroomEnterToken 进房令牌。
func ChatroomEnterToken(appID string, roomID, uid int64) string {
	return fmt.Sprintf("CHATROOM_SERVICE:CHATROOM_ENTER_TOKEN:APP_ID:%s:ROOM_ID:%d:UID:%d", appID, roomID, uid)
}

// ChatroomMemberInfo 房间成员信息缓存（JSON）。
func ChatroomMemberInfo(appID string, roomID, uid int64) string {
	return fmt.Sprintf("CHATROOM_SERVICE:CHATROOM_MEMBER_INFO:APP_ID:%s:ROOM_ID:%d:UID:%d", appID, roomID, uid)
}

// MicSpaceInfo hash，麦位信息。
func MicSpaceInfo(appID string, roomID int64) string {
	return fmt.Sprintf("CHATROOM_SERVICE:MIC_SPACE_INFO:APP_ID:%s:ROOM_ID:%d", appID, roomID)
}

// ChatroomMutedUser 成员禁言标记，过期即自动解除禁言。
func ChatroomMutedUser(appID string, roomID, uid int64) string {
	return fmt.Sprintf("CHATROOM_SERVICE:MUTED_USER:APP_ID:%s:ROOM_ID:%d:UID:%d", appID, roomID, uid)
}

// ChatroomKickedUser 成员被踢标记，过期即自动解除踢出状态。
func ChatroomKickedUser(appID string, roomID, uid int64) string {
	return fmt.Sprintf("CHATROOM_SERVICE:KICKED_USER:APP_ID:%s:ROOM_ID:%d:UID:%d", appID, roomID, uid)
}

// ChatroomUserLogicRole set，用户在房间内的角色 ID。
func ChatroomUserLogicRole(appID string, roomID, uid int64) string {
	return fmt.Sprintf("CHATROOM_SERVICE:USER_LOGIC_ROLE:%s:%d:%d", appID, roomID, uid)
}

// ChatroomEnterInfo 申请进房令牌时暂存的成员信息（JSON）。
func ChatroomEnterInfo(appID string, roomID, uid int64) string {
	return fmt.Sprintf("CHATROOM_SERVICE:ENTER_INFO:%s:%d:%d", appID, roomID, uid)
}

// ChatroomOnlineCount 房间在线人数（包括机器人）。
func ChatroomOnlineCount(appID string, roomID int64) string {
	return fmt.Sprintf("CHATROOM_SERVICE:CHATROOM_ONLINE_COUNT:APP_ID:%s:ROOM_ID:%d", appID, roomID)
}

// ChatroomRobotCount 房间机器人数量。
func ChatroomRobotCount(appID string, roomID int64) string {
	return fmt.Sprintf("CHATROOM_SERVICE:CHATROOM_ROBOT_COUNT:APP_ID:%s:ROOM_ID:%d", appID, roomID)
}

// ChatroomOnlineMemberRank zset，按在线人数排序的房间。
func ChatroomOnlineMemberRank(appID string) string {
	return fmt.Sprintf("CHATROOM_SERVICE:CHATROOM_ONLINE_MEMBER_RANK:APP_ID:%s", appID)
}

// ChatroomUsedMicSpaceRank zset，按麦上人数排序的房间。
func ChatroomUsedMicSpaceRank(appID string) string {
	return fmt.Sprintf("CHATROOM_SERVICE:CHATROOM_USED_MIC_SPACE_RANK:APP_ID:%s", appID)
}

// ChatroomUsedMicSpaceCount 房间麦上人数。
func ChatroomUsedMicSpaceCount(appID string, roomID int64) string {
	return fmt.Sprintf("CHATROOM_SERVICE:CHATROOM_USED_MIC_SPACE_RANK:APP_ID:%s:ROOM_ID:%d", appID, roomID)
}

// UserMicToken 用户麦位 token。
func UserMicToken(appID string, roomID, uid int64) string {
	return fmt.Sprintf("CHATROOM_SERVICE:USER_MIC_TOKEN:%s:%d:%d", appID, roomID, uid)
}

// UserOfflineChatroom 用户断线标记，过期后将用户踢出所有房间。
func UserOfflineChatroom(appID string, uid int64) string {
	return fmt.Sprintf("CHATROOM_SERVICE:OFFLINEONLINECHATROOM:APP_ID:%s:UID:%d", appID, uid)
}

// UserConnID 用户当前长连接 ID。
func UserConnID(appID string, uid int64) string {
	return fmt.Sprintf("CHATROOM_SERVICE:USERCONNID:APP_ID:%s:UID:%d", appID, uid)
}

// ChatroomOnlineRealMember set，房间真实用户列表。
func ChatroomOnlineRealMember(appID string, roomID int64) string {
	return fmt.Sprintf("CROnlineRMem%s:%d", appID, roomID)
}

// ChatroomAfterDealQueue set，待刷新排行数据的房间（"<appID>_<roomID>"）。
const ChatroomAfterDealQueue = "crAfterDealQueue"

// UserAfterDealQueue zset，断线待离房的用户（"<appID>_<uid>"，score 为断线时间纳秒）。
const UserAfterDealQueue = "usAfterDealQueue"

// UserLock 用户操作锁。
func UserLock(appID string, roomID, uid int64) string {
	return fmt.Sprintf("CRULock:%s:%d:%d", appID, roomID, uid)
}

// ChatroomInfoLock 房间信息修改锁。
func ChatroomInfoLock(appID string, roomID int64) string {
	return fmt.Sprintf("CRCILock:%s:%d", appID, roomID)
}

// ChatroomProcessLock 房间处理锁。
func ChatroomProcessLock(appID string, roomID int64) string {
	return fmt.Sprintf("CHATROOM_TEMP_LOCKER:%s:%d", appID, roomID)
}

// ChatroomMicLock 麦位扩展信息修改锁。
func ChatroomMicLock(appID string, roomID int64) string {
	return fmt.Sprintf("CHATROOM_MIC_LOCK:%s:%d", appID, roomID)
}

// RoomMember is the identity encoded in a per-member key.
type RoomMember struct {
	AppID  string
	RoomID int64
	UID    int64
}

const (
	mutedUserPrefix       = "CHATROOM_SERVICE:MUTED_USER:"
	kickedUserPrefix      = "CHATROOM_SERVICE:KICKED_USER:"
	offlineChatroomPrefix = "CHATROOM_SERVICE:OFFLINEONLINECHATROOM:"
)

// ParseMutedUser parses a key built by ChatroomMutedUser.
func ParseMutedUser(key string) (RoomMember, bool) {
	return parseRoomMember(key, mutedUserPrefix)
}

// ParseKickedUser parses a key built by ChatroomKickedUser.
func ParseKickedUser(key string) (RoomMember, bool) {
	return parseRoomMember(key, kickedUserPrefix)
}

// ParseUserOfflineChatroom parses a key built by UserOfflineChatroom.
func ParseUserOfflineChatroom(key string) (appID string, uid int64, ok bool) {
	rest, found := strings.CutPrefix(key, offlineChatroomPrefix)
	if !found {
		return "", 0, false
	}
	parts := strings.Split(rest, ":")
	if len(parts) != 4 || parts[0] != "APP_ID" || parts[1] == "" || parts[2] != "UID" {
		return "", 0, false
	}
	uid, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return "", 0, false
	}
	return parts[1], uid, true
}

func parseRoomMember(key, prefix string) (RoomMember, bool) {
	rest, found := strings.CutPrefix(key, prefix)
	if !found {
		return RoomMember{}, false
	}
	parts := strings.Split(rest, ":")
	if len(parts) != 6 || parts[0] != "APP_ID" || parts[1] == "" || parts[2] != "ROOM_ID" || parts[4] != "UID" {
		return RoomMember{}, false
	}
	roomID, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return RoomMember{}, false
	}
	uid, err := strconv.ParseInt(parts[5], 10, 64)
	if err != nil {
		return RoomMember{}, false
	}
	return RoomMember{AppID: parts[1], RoomID: roomID, UID: uid}, true
}

// QueueMember encodes an "<appID>_<id>" after-deal queue member.
func QueueMember(appID string, id int64) string {
	return appID + "_" + strconv.FormatInt(id, 10)
}

// ParseQueueMember decodes a value built by QueueMember. The ID is taken
// after the last underscore so app IDs may themselves contain underscores.
func ParseQueueMember(v string) (appID string, id int64, ok bool) {
	i := strings.LastIndexByte(v, '_')
	if i <= 0 || i == len(v)-1 {
		return "", 0, false
	}
	id, err := strconv.ParseInt(v[i+1:], 10, 64)
	if err != nil {
		return "", 0, false
	}
	return v[:i], id, true
}
