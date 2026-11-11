package logic

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/yourname/roomkit/api/gen/servicepb"
	"github.com/yourname/roomkit/internal/chatroom/lock"
	"github.com/yourname/roomkit/internal/chatroom/model"
	"github.com/yourname/roomkit/internal/chatroom/rediskey"
	"github.com/yourname/roomkit/pkg/log"
)

const appRoleCacheTTL = 5 * time.Minute

var (
	appRoleIDs   sync.Map
	appRoleLocks sync.Map
)

// appRoles returns the role IDs defined for an app, cached for five minutes.
func appRoles(appID string) ([]int64, error) {
	if v, ok := appRoleIDs.Load(appID); ok {
		return v.([]int64), nil
	}
	l, _ := appRoleLocks.LoadOrStore(appID, &sync.Mutex{})
	mu := l.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	if v, ok := appRoleIDs.Load(appID); ok {
		return v.([]int64), nil
	}

	var roles []model.Role
	if err := db.Select("role_id").Where("app_id = ?", appID).Find(&roles).Error; err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(roles))
	for _, r := range roles {
		ids = append(ids, r.RoleId)
	}
	appRoleIDs.Store(appID, ids)
	time.AfterFunc(appRoleCacheTTL, func() { appRoleIDs.Delete(appID) })
	return ids, nil
}

func marshalString(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

// ResetMemberInfo stages the member info used when uid enters the room and
// upserts the member row. With noLocked the caller already holds the user lock.
func (c *ChatroomContainer) ResetMemberInfo(uid int64, memberInfo *servicepb.ChatroomMemberInfo, noLocked bool) (servicepb.ErrorCode, error) {
	if !noLocked {
		if !lock.AcquireUser(c.ctx, c.appId, c.roomId, uid, true) {
			return servicepb.ErrorCode_CHATROOM_ROOM_USER_LOCKED, nil
		}
		defer lock.ReleaseUser(c.appId, c.roomId, uid)
	}
	logFields := []zap.Field{zap.Int64("roomID", c.roomId), zap.Int64("uid", uid)}

	roleIDs, err := appRoles(c.appId)
	if err != nil {
		c.ctx.Error("ResetUserLogicRole getRoleIds err", zap.Error(err))
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	for _, want := range memberInfo.RoleId {
		found := false
		for _, id := range roleIDs {
			if id == int64(want) {
				found = true
				break
			}
		}
		if !found {
			return servicepb.ErrorCode_CHATROOM_ROLE_NOT_EXISTS_ERROR, nil
		}
	}

	staged, err := marshalString(memberInfo)
	if err != nil {
		c.ctx.Error("ResetMemberInfo marshal memberInfo err", append(logFields, zap.Error(err))...)
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if err := rdb.Set(c.ctx, rediskey.ChatroomEnterInfo(c.appId, c.roomId, uid), staged, settings.EnterTokenTTL).Err(); err != nil {
		c.ctx.Error("ResetMemberInfo Set userInfo err", append(logFields, zap.Error(err))...)
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}

	customInfo, _ := model.StringMap(memberInfo.CustomInfo).Value()
	remoteExt, _ := model.StringMap(memberInfo.ChatroomRemoteExt).Value()
	searchData, _ := model.StringMap(memberInfo.SearchKeys).Value()
	isRobot := 0
	if memberInfo.IsReboot {
		isRobot = 1
	}
	now := time.Now()
	sNow := model.FormatTime(now)
	if err := db.Exec(`INSERT chatroom_member_info
		(app_id, chatroom_id, uid, nickname, avatar, custom_info, chatroom_remote_ext, search_data, is_robot, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
		nickname = ?, avatar = ?,
		custom_info = ?, chatroom_remote_ext = ?, search_data = ?,
		is_robot = ?, updated_at = ?`,
		c.appId, c.roomId, uid, memberInfo.Nickname, memberInfo.Avatar,
		customInfo, remoteExt, searchData, isRobot, sNow, sNow,
		memberInfo.Nickname, memberInfo.Avatar,
		customInfo, remoteExt, searchData,
		isRobot, sNow).Error; err != nil {
		c.ctx.Error("ResetMemberInfo insert/update chatroom_member_info err", append(logFields, zap.Error(err))...)
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, nil
	}

	if err := syncMemberInfoToRedis(c.ctx, c.appId, c.roomId, uid, &model.ChatroomMemberInfo{
		AppId:             c.appId,
		ChatroomId:        c.roomId,
		Uid:               uid,
		Nickname:          memberInfo.Nickname,
		Avatar:            memberInfo.Avatar,
		CustomInfo:        memberInfo.CustomInfo,
		ChatroomRemoteExt: memberInfo.ChatroomRemoteExt,
		SearchData:        memberInfo.SearchKeys,
		IsRobot:           memberInfo.IsReboot,
		CreatedAt:         model.At(now),
		UpdatedAt:         model.At(now),
	}); err != nil {
		c.ctx.Error("ResetMemberInfo syncMemberInfoToRedis err", zap.Error(err))
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	return servicepb.ErrorCode_SUCCESS, nil
}

// FlushAllMemberInfo clears member data when a room closes. Members holding a
// role with keep_on_closed, and members that are blocked, muted or kicked,
// keep their rows.
func (c *ChatroomContainer) FlushAllMemberInfo() (servicepb.ErrorCode, error) {
	tx := db.Begin()
	if tx.Error != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, tx.Error
	}
	defer tx.Rollback()

	if err := tx.Where("app_id = ? AND chatroom_id = ?", c.appId, c.roomId).
		Delete(&model.ChatroomMemberSearchInfo{}).Error; err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}

	const pageSize = 100
	var roleKeys []string
	for offset := 0; ; offset += pageSize {
		rows, err := queryStrings(tx, "SELECT uid FROM user_logic_role WHERE app_id = ? AND chatroom_id = ? LIMIT ?, ?",
			c.appId, c.roomId, offset, pageSize)
		if err != nil {
			return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
		}
		for _, row := range rows {
			uid, err := strconv.ParseInt(row["uid"], 10, 64)
			if err != nil {
				continue
			}
			roleKeys = append(roleKeys, rediskey.ChatroomUserLogicRole(c.appId, c.roomId, uid))
		}
		if len(rows) < pageSize {
			break
		}
	}

	if err := tx.Exec(`DELETE FROM user_logic_role WHERE app_id = ? AND chatroom_id = ? AND role_id IN (
		SELECT DISTINCT role_id FROM (
			SELECT role_id FROM role WHERE app_id = ? AND keep_on_closed = 0) t)`,
		c.appId, c.roomId, c.appId).Error; err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	for _, key := range roleKeys {
		rdb.Del(c.ctx, key)
	}

	if err := tx.Exec(`DELETE FROM chatroom_member_status_memory_list
		WHERE app_id = ? AND chatroom_id = ?
		AND is_block = 0 AND is_muted = 0 AND is_kicked = 0
		AND uid NOT IN (
		SELECT DISTINCT uid FROM (
			SELECT uid FROM user_logic_role WHERE app_id = ? AND chatroom_id = ?) t)`,
		c.appId, c.roomId, c.appId, c.roomId).Error; err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}

	if err := tx.Model(&model.ChatroomMemberStatusMemoryList{}).
		Where("app_id = ? AND chatroom_id = ?", c.appId, c.roomId).
		Updates(map[string]any{"is_online": false, "updated_at": model.Now()}).Error; err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}

	if err := tx.Exec(`DELETE FROM chatroom_member_info WHERE app_id = ? AND chatroom_id = ? AND uid NOT IN (
		SELECT DISTINCT uid FROM (
			SELECT uid FROM chatroom_member_status_memory_list WHERE app_id = ? AND chatroom_id = ? UNION
			SELECT uid FROM user_logic_role WHERE app_id = ? AND chatroom_id = ?) t)`,
		c.appId, c.roomId, c.appId, c.roomId, c.appId, c.roomId).Error; err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}

	if err := tx.Commit().Error; err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	return servicepb.ErrorCode_SUCCESS, nil
}

// RemoveChatroomMemberInfo drops the search index and the cached member info
// of uid. The MySQL member row is kept so admins can still list kicked and
// blocked members.
func (c *ChatroomContainer) RemoveChatroomMemberInfo(uid int64) (servicepb.ErrorCode, error) {
	tx := db.Begin()
	if tx.Error != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, tx.Error
	}
	defer tx.Rollback()

	if err := tx.Where("app_id = ? AND chatroom_id = ? AND uid = ?", c.appId, c.roomId, uid).
		Delete(&model.ChatroomMemberSearchInfo{}).Error; err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}

	key := rediskey.ChatroomMemberInfo(c.appId, c.roomId, uid)
	c.ctx.Info("user status trace",
		zap.String("target", "user leave chatroom"),
		zap.Int64("uid", uid), zap.Int64("chatroom id", c.roomId), zap.String("op redis key", key))
	if err := rdb.Del(c.ctx, key).Err(); err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}

	if err := tx.Commit().Error; err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	return servicepb.ErrorCode_SUCCESS, nil
}

// GetChatroomMemberInfo reads member info from the cache and falls back to
// MySQL. Leaving or closing deletes the cache but may keep the MySQL row.
func (c *ChatroomContainer) GetChatroomMemberInfo(uid int64) (servicepb.ErrorCode, *servicepb.ChatroomMemberInfo, error) {
	info := &model.ChatroomMemberInfo{}
	data, err := rdb.Get(c.ctx, rediskey.ChatroomMemberInfo(c.appId, c.roomId, uid)).Result()
	switch {
	case isNil(err):
		has, err := first(db.Where("app_id = ? AND chatroom_id = ? AND uid = ?", c.appId, c.roomId, uid), info)
		if err != nil {
			return servicepb.ErrorCode_SERVER_INTERNAL_ERROR, nil, err
		}
		if !has {
			return servicepb.ErrorCode_CHATROOM_NOT_IN_ROOM, nil, nil
		}
	case err != nil:
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, nil, err
	default:
		if err := json.Unmarshal([]byte(data), info); err != nil {
			return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, nil, err
		}
	}

	return servicepb.ErrorCode_SUCCESS, &servicepb.ChatroomMemberInfo{
		Uid:               uid,
		Nickname:          info.Nickname,
		Avatar:            info.Avatar,
		CustomInfo:        info.CustomInfo,
		ChatroomRemoteExt: info.ChatroomRemoteExt,
		SearchKeys:        info.SearchData,
		IsReboot:          info.IsRobot,
		EnterTime:         info.CreatedAt.UnixNano() / int64(time.Millisecond),
	}, nil
}

// GetChatRoomMemberInfoMore adds status flags and roles to GetChatroomMemberInfo.
func (c *ChatroomContainer) GetChatRoomMemberInfoMore(uid int64) (servicepb.ErrorCode, *servicepb.ChatroomMemberInfo, error) {
	errCode, info, err := c.GetChatroomMemberInfo(uid)
	if err != nil || errCode != servicepb.ErrorCode_SUCCESS {
		return errCode, nil, err
	}

	st := &model.ChatroomMemberStatusMemoryList{}
	if _, err := first(db.Select("is_online", "is_muted", "is_kicked", "is_block",
		"muted_begin_at", "muted_finish_at", "kicked_begin_at", "kicked_finish_at").
		Where("app_id = ? AND chatroom_id = ? AND uid = ?", c.appId, c.roomId, uid), st); err != nil {
		return servicepb.ErrorCode_SERVER_INTERNAL_ERROR, nil, err
	}

	info.IsOnline = st.IsOnline
	info.IsInMuteList = st.IsMuted
	info.IsInKickList = st.IsKicked
	info.IsInBlockList = st.IsBlock
	info.MuteBegin = st.MutedBeginAt.UnixNano()
	info.MuteFinish = st.MutedFinishAt.UnixNano()
	info.KickBegin = st.KickedBeginAt.UnixNano()
	info.KickFinish = st.KickedFinishAt.UnixNano()
	info.RoleId = memberRoleIDsFromDB(c.appId, c.roomId, uid)
	return servicepb.ErrorCode_SUCCESS, info, nil
}

// IsUserInChatroom reports whether uid is in the online member set.
func (c *ChatroomContainer) IsUserInChatroom(uid int64) (servicepb.ErrorCode, error) {
	ok, err := rdb.SIsMember(c.ctx, rediskey.ChatroomOnlineMember(c.appId, c.roomId), uid).Result()
	if err != nil {
		return servicepb.ErrorCode_SERVER_INTERNAL_ERROR, err
	}
	if !ok {
		return servicepb.ErrorCode_CHATROOM_NOT_IN_ROOM, nil
	}
	return servicepb.ErrorCode_SUCCESS, nil
}

func syncMemberInfoToRedis(ctx context.Context, appID string, roomID, uid int64, info *model.ChatroomMemberInfo) error {
	key := rediskey.ChatroomMemberInfo(appID, roomID, uid)
	rdb.Del(ctx, key)
	b, err := json.Marshal(info)
	if err != nil {
		return err
	}
	return rdb.Set(ctx, key, b, 0).Err()
}

// ModifyChatroomUserInfo updates the profile and search keys of a member and
// notifies the room.
func (c *ChatroomContainer) ModifyChatroomUserInfo(uid int64, userInfo *servicepb.ChatroomMemberInfo) (servicepb.ErrorCode, error) {
	old := &model.ChatroomMemberInfo{}
	has, err := first(db.Where("app_id = ? AND chatroom_id = ? AND uid = ?", c.appId, c.roomId, uid), old)
	if err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if !has {
		return servicepb.ErrorCode_CHATROOM_USER_NOT_IN_TARGET_ROOM, nil
	}

	needUpdateBaseInfo := false
	if userInfo.Nickname != "" {
		old.Nickname = userInfo.Nickname
		needUpdateBaseInfo = true
	}
	if userInfo.Avatar != "" {
		old.Avatar = userInfo.Avatar
		needUpdateBaseInfo = true
	}
	if userInfo.CustomInfo != nil {
		old.CustomInfo = userInfo.CustomInfo
		needUpdateBaseInfo = true
	}
	if userInfo.ChatroomRemoteExt != nil {
		old.ChatroomRemoteExt = userInfo.ChatroomRemoteExt
		needUpdateBaseInfo = true
	}
	old.UpdatedAt = model.Now()

	tx := db.Begin()
	if tx.Error != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, tx.Error
	}
	defer tx.Rollback()

	if userInfo.SearchKeys != nil {
		if err := tx.Where("app_id = ? AND chatroom_id = ? AND uid = ?", c.appId, c.roomId, uid).
			Delete(&model.ChatroomMemberSearchInfo{}).Error; err != nil {
			return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
		}
		if rows := searchRows(c.appId, c.roomId, uid, old.Nickname, userInfo.SearchKeys); len(rows) > 0 {
			if err := tx.Create(&rows).Error; err != nil {
				return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
			}
		}
	}

	if needUpdateBaseInfo {
		customInfo, _ := old.CustomInfo.Value()
		remoteExt, _ := old.ChatroomRemoteExt.Value()
		searchData, _ := old.SearchData.Value()
		createdAt, _ := old.CreatedAt.Value()
		updatedAt, _ := old.UpdatedAt.Value()
		if err := tx.Exec(`INSERT INTO chatroom_member_info(
			app_id, chatroom_id, uid,
			nickname, avatar, custom_info, chatroom_remote_ext,
			search_data, is_robot, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE avatar=?, nickname=?, custom_info=?, chatroom_remote_ext=?`,
			old.AppId, old.ChatroomId, old.Uid, old.Nickname, old.Avatar,
			customInfo, remoteExt, searchData, old.IsRobot, createdAt, updatedAt,
			old.Avatar, old.Nickname, customInfo, remoteExt).Error; err != nil {
			return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
		}
	}

	if err := tx.Commit().Error; err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if needUpdateBaseInfo {
		if err := syncMemberInfoToRedis(c.ctx, c.appId, c.roomId, uid, old); err != nil {
			return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
		}
	}

	errCode, toInfo, err := c.GetChatroomMemberInfo(uid)
	if err != nil || errCode != servicepb.ErrorCode_SUCCESS {
		c.ctx.Error("get user info failed", zap.Error(err), zap.String("eCode", errCode.String()))
	}
	_, _ = broadcastEvent(c.ctx, evMemberUpdate, c.appId, c.roomId, c.ctx.MsgID(), 0, uid, nil, toInfo, nil)
	return servicepb.ErrorCode_SUCCESS, nil
}

// memberRoleIDsFromDB returns the role IDs of uid in ascending order.
func memberRoleIDsFromDB(appID string, roomID, uid int64) []int32 {
	rows, err := queryStrings(db, "SELECT role_id FROM user_logic_role WHERE app_id = ? AND chatroom_id = ? AND uid = ? ORDER BY role_id",
		appID, roomID, uid)
	if err != nil {
		log.L().Error("query member role ids err", zap.Error(err))
		return nil
	}
	var ids []int32
	for _, row := range rows {
		if id, err := strconv.ParseInt(row["role_id"], 10, 32); err == nil {
			ids = append(ids, int32(id))
		}
	}
	return ids
}

// memberRoleIDsBatch returns the role IDs of each uid.
func memberRoleIDsBatch(appID string, roomID int64, uids []int64) map[int64][]int32 {
	out := map[int64][]int32{}
	if len(uids) == 0 {
		return out
	}
	rows, err := queryStrings(db, "SELECT uid, role_id FROM user_logic_role WHERE app_id = ? AND chatroom_id = ? AND uid IN ?",
		appID, roomID, uids)
	if err != nil {
		log.L().Error("query member role ids err", zap.Error(err))
		return nil
	}
	for _, row := range rows {
		uid, err := strconv.ParseInt(row["uid"], 10, 64)
		if err != nil {
			continue
		}
		id, err := strconv.ParseInt(row["role_id"], 10, 32)
		if err != nil {
			continue
		}
		out[uid] = append(out[uid], int32(id))
	}
	return out
}
