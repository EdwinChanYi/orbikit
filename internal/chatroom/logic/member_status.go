package logic

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"go.uber.org/zap"

	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
	"github.com/EdwinChanYi/orbikit/internal/chatroom/model"
	"github.com/EdwinChanYi/orbikit/internal/chatroom/rediskey"
	"github.com/EdwinChanYi/orbikit/internal/chatroom/reqctx"
	"github.com/EdwinChanYi/orbikit/pkg/log"
)

const upsertMemberStatus = `INSERT chatroom_member_status_memory_list
	(app_id, chatroom_id, uid, is_block, is_muted, is_kicked,
	muted_begin_at, muted_finish_at, kicked_begin_at, kicked_finish_at,
	is_online, created_at, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON DUPLICATE KEY UPDATE `

func execMemberStatus(name, onDup string, args ...any) error {
	res := db.Exec(upsertMemberStatus+onDup, args...)
	if res.Error != nil {
		log.L().Error(name+" chatroom_member_status_memory_list exec err", zap.Error(res.Error))
		return res.Error
	}
	if res.RowsAffected == 0 {
		log.L().Error(name + " chatroom_member_status_memory_list 0 row!")
		return fmt.Errorf("%s chatroom_member_status_memory_list affected 0 row", name)
	}
	return nil
}

// resetUserInfo applies the member info staged by ApplyEnterChatroomToken:
// role cache, search index and (asynchronously) the role table.
func resetUserInfo(ctx context.Context, appID string, roomID, uid int64) error {
	logFields := []zap.Field{zap.String("appID", appID), zap.Int64("roomID", roomID), zap.Int64("uid", uid)}

	data, err := rdb.Get(ctx, rediskey.ChatroomEnterInfo(appID, roomID, uid)).Result()
	if err != nil {
		log.L().Error("ResetUserLogicRole get userInfo err", append(logFields, zap.Error(err))...)
		return err
	}
	memberInfo := &servicepb.ChatroomMemberInfo{}
	if err := json.Unmarshal([]byte(data), memberInfo); err != nil {
		log.L().Error("ResetUserLogicRole Unmarshal memberInfo err", append(logFields, zap.Error(err))...)
		return err
	}

	roleIDs := make([]int64, 0, len(memberInfo.RoleId))
	for _, id := range memberInfo.RoleId {
		roleIDs = append(roleIDs, int64(id))
	}
	setRoleCache(ctx, appID, roomID, uid, roleIDs)

	if err := db.Where("app_id = ? AND chatroom_id = ? AND uid = ?", appID, roomID, uid).
		Delete(&model.ChatroomMemberSearchInfo{}).Error; err != nil {
		log.L().Error("ResetMemberInfo del ChatroomMemberSearchInfo err", append(logFields, zap.Error(err))...)
		return err
	}
	if rows := searchRows(appID, roomID, uid, memberInfo.Nickname, memberInfo.SearchKeys); len(rows) > 0 {
		if err := db.Create(&rows).Error; err != nil {
			log.L().Error("ResetMemberInfo insert ChatroomMemberSearchInfo err", append(logFields, zap.Error(err))...)
			return err
		}
	}

	go func() {
		tx := db.Begin()
		if tx.Error != nil {
			log.L().Error("ResetUserLogicRole begin err", zap.Error(tx.Error))
			return
		}
		defer tx.Rollback()

		if err := tx.Where("app_id = ? AND chatroom_id = ? AND uid = ?", appID, roomID, uid).
			Delete(&model.UserLogicRole{}).Error; err != nil {
			log.L().Error("ResetUserLogicRole del UserLogicRole err", append(logFields, zap.Error(err))...)
			return
		}
		if rows := userRoleRows(appID, roomID, uid, roleIDs); len(rows) > 0 {
			if err := tx.Create(&rows).Error; err != nil {
				log.L().Error("ResetUserLogicRole Insert UserLogicRole err", append(logFields, zap.Error(err))...)
				return
			}
		}
		if err := tx.Commit().Error; err != nil {
			log.L().Error("ResetUserLogicRole commit err", append(logFields, zap.Error(err))...)
		}
	}()
	return nil
}

func searchRows(appID string, roomID, uid int64, nickname string, keys map[string]string) []model.ChatroomMemberSearchInfo {
	now := model.Now()
	rows := make([]model.ChatroomMemberSearchInfo, 0, len(keys))
	for k, v := range keys {
		rows = append(rows, model.ChatroomMemberSearchInfo{
			AppId: appID, ChatroomId: roomID, Uid: uid,
			Nickname: nickname, Key: k, Value: v,
			CreatedAt: now, UpdatedAt: now,
		})
	}
	return rows
}

func userRoleRows(appID string, roomID, uid int64, roleIDs []int64) []model.UserLogicRole {
	now := model.Now()
	rows := make([]model.UserLogicRole, 0, len(roleIDs))
	for _, id := range roleIDs {
		rows = append(rows, model.UserLogicRole{
			AppId: appID, ChatroomId: roomID, Uid: uid, RoleId: id,
			CreatedAt: now, UpdatedAt: now,
		})
	}
	return rows
}

// setRoleCache replaces the cached role set of a member.
func setRoleCache(ctx context.Context, appID string, roomID, uid int64, roleIDs []int64) {
	key := rediskey.ChatroomUserLogicRole(appID, roomID, uid)
	pipe := rdb.TxPipeline()
	pipe.Del(ctx, key)
	if len(roleIDs) > 0 {
		members := make([]any, 0, len(roleIDs))
		for _, id := range roleIDs {
			members = append(members, id)
		}
		pipe.SAdd(ctx, key, members...)
	}
	_, _ = pipe.Exec(ctx)
}

func muteMember(ctx context.Context, appID string, roomID, uid, timeout int64) error {
	if timeout <= 0 {
		return nil
	}
	now := time.Now()
	sNow := model.FormatTime(now)
	sFinish := model.FormatTime(now.Add(time.Duration(timeout) * time.Second))
	if err := execMemberStatus("MuteChatroomMember",
		"updated_at = ?, is_muted = ?, muted_begin_at = ?, muted_finish_at = ?",
		appID, roomID, uid, 0, 1, 0, sNow, sFinish, sNow, sNow, 0, sNow, sNow,
		sNow, 1, sNow, sFinish); err != nil {
		return err
	}
	return rdb.Set(ctx, rediskey.ChatroomMutedUser(appID, roomID, uid), 1, time.Duration(timeout)*time.Second).Err()
}

func unmuteMember(ctx context.Context, appID string, roomID, uid int64) error {
	if err := db.Model(&model.ChatroomMemberStatusMemoryList{}).
		Where("app_id = ? AND chatroom_id = ? AND uid = ?", appID, roomID, uid).
		Updates(map[string]any{"is_muted": false, "updated_at": model.Now()}).Error; err != nil {
		return err
	}
	return rdb.Del(ctx, rediskey.ChatroomMutedUser(appID, roomID, uid)).Err()
}

func blockMember(appID string, roomID, uid int64) error {
	sNow := model.FormatTime(time.Now())
	return execMemberStatus("BlockChatroomMember", "updated_at = ?, is_block = ?",
		appID, roomID, uid, 1, 0, 0, sNow, sNow, sNow, sNow, 0, sNow, sNow,
		sNow, 1)
}

func unblockMember(appID string, roomID, uid int64) error {
	return db.Model(&model.ChatroomMemberStatusMemoryList{}).
		Where("app_id = ? AND chatroom_id = ? AND uid = ?", appID, roomID, uid).
		Updates(map[string]any{"is_block": false, "updated_at": model.Now()}).Error
}

func isBlockedMember(appID string, roomID, uid int64) (bool, error) {
	return exists(db.Where("app_id = ? AND chatroom_id = ? AND uid = ? AND is_block = ?", appID, roomID, uid, true),
		&model.ChatroomMemberStatusMemoryList{})
}

func kickMember(ctx context.Context, appID string, roomID, uid, timeout int64) error {
	if timeout <= 0 {
		return nil
	}
	now := time.Now()
	sNow := model.FormatTime(now)
	sFinish := model.FormatTime(now.Add(time.Duration(timeout) * time.Second))
	if err := execMemberStatus("KickChatroomMember",
		"updated_at = ?, is_kicked = ?, kicked_begin_at = ?, kicked_finish_at = ?",
		appID, roomID, uid, 0, 0, 1, sNow, sNow, sNow, sFinish, 0, sNow, sNow,
		sNow, 1, sNow, sFinish); err != nil {
		return err
	}
	return rdb.Set(ctx, rediskey.ChatroomKickedUser(appID, roomID, uid), 1, time.Duration(timeout)*time.Second).Err()
}

// UnkickMember clears the kicked state of a member.
func UnkickMember(ctx context.Context, appID string, roomID, uid int64) error {
	if err := db.Model(&model.ChatroomMemberStatusMemoryList{}).
		Where("app_id = ? AND chatroom_id = ? AND uid = ?", appID, roomID, uid).
		Updates(map[string]any{"is_kicked": false, "updated_at": model.Now()}).Error; err != nil {
		return err
	}
	return rdb.Del(ctx, rediskey.ChatroomKickedUser(appID, roomID, uid)).Err()
}

// UnmuteMember clears the muted state of a member.
func UnmuteMember(ctx context.Context, appID string, roomID, uid int64) error {
	return unmuteMember(ctx, appID, roomID, uid)
}

func isKickedMember(ctx context.Context, appID string, roomID, uid int64) (int64, error) {
	return rdb.Exists(ctx, rediskey.ChatroomKickedUser(appID, roomID, uid)).Result()
}

func setMemberOnline(appID string, roomID, uid int64, online bool) error {
	name, flag := "OfflineChatroomMember", 0
	if online {
		name, flag = "OnlineChatroomMember", 1
	}
	sNow := model.FormatTime(time.Now())
	return execMemberStatus(name, "updated_at = ?, is_online = ?",
		appID, roomID, uid, 0, 0, 0, sNow, sNow, sNow, sNow, flag, sNow, sNow,
		sNow, flag)
}

func memberRoleIDs(ctx context.Context, appID string, roomID, uid int64) ([]int64, error) {
	members, err := rdb.SMembers(ctx, rediskey.ChatroomUserLogicRole(appID, roomID, uid)).Result()
	if err != nil {
		var roles []model.UserLogicRole
		if err := db.Where("app_id = ? AND chatroom_id = ? AND uid = ?", appID, roomID, uid).Find(&roles).Error; err != nil {
			log.L().Error("getUserPermissionInfoInChatroom UserLogicRole not found",
				zap.String("appID", appID), zap.Int64("roomID", roomID), zap.Int64("uid", uid))
			return nil, err
		}
		ids := make([]int64, 0, len(roles))
		for _, r := range roles {
			ids = append(ids, r.RoleId)
		}
		return ids, nil
	}
	ids := make([]int64, 0, len(members))
	for _, v := range members {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// pickPermission selects the effective permission among a member's roles.
// Lower levels mean higher privilege.
func pickPermission(perms []model.Permission, initiative bool) *model.Permission {
	if len(perms) == 0 {
		return nil
	}
	index := 0
	for i, v := range perms {
		if i == index {
			continue
		}
		if initiative {
			if perms[index].InitiativeLevel > v.InitiativeLevel && perms[index].CanDoAction {
				index = i
			}
		} else if perms[index].PassivityLevel > v.PassivityLevel {
			index = i
		}
	}
	return &perms[index]
}

func memberPermission(ctx context.Context, appID string, uid, roomID int64, actionType int32, initiative bool) (*model.Permission, error) {
	roleIDs, err := memberRoleIDs(ctx, appID, roomID, uid)
	if err != nil {
		return nil, err
	}
	var perms []model.Permission
	if err := db.Where("app_id = ? AND action_type = ? AND role_id IN ?", appID, actionType, roleIDs).
		Find(&perms).Error; err != nil {
		return nil, err
	}
	return pickPermission(perms, initiative), nil
}

// checkUserPermission verifies that from may perform p, and when to is set,
// that from outranks to for p.
func checkUserPermission(ctx *reqctx.Context, appID string, roomID, from, to int64, p servicepb.Privilege,
	noPermission servicepb.ErrorCode) (servicepb.ErrorCode, error) {
	fromPerm, err := memberPermission(ctx, appID, from, roomID, int32(p), true)
	if err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if fromPerm == nil {
		return servicepb.ErrorCode_CHATROOM_USER_PERMISSION_ERROR, nil
	}
	if !fromPerm.CanDoAction {
		return servicepb.ErrorCode_CHATROOM_MEMBER_NOT_PERMISSION, nil
	}
	if to == 0 {
		return servicepb.ErrorCode_SUCCESS, nil
	}
	toPerm, err := memberPermission(ctx, appID, to, roomID, int32(p), false)
	if err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if toPerm != nil && fromPerm.InitiativeLevel >= toPerm.PassivityLevel {
		return noPermission, nil
	}
	return servicepb.ErrorCode_SUCCESS, nil
}
