package logic

import (
	"context"
	"database/sql"
	"sort"
	"strconv"
	"strings"

	"go.uber.org/zap"

	"github.com/yourname/roomkit/api/gen/servicepb"
	"github.com/yourname/roomkit/internal/chatroom/model"
	"github.com/yourname/roomkit/internal/chatroom/rediskey"
)

var readCommitted = &sql.TxOptions{Isolation: sql.LevelReadCommitted}

// joinRoleIDs renders role IDs as a sorted comma separated list.
func joinRoleIDs(roles []int64) string {
	sorted := append([]int64(nil), roles...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	parts := make([]string, len(sorted))
	for i, r := range sorted {
		parts[i] = strconv.FormatInt(r, 10)
	}
	return strings.Join(parts, ",")
}

func roleIDsToInt32(ids []int64) []int32 {
	out := make([]int32, 0, len(ids))
	for _, id := range ids {
		out = append(out, int32(id))
	}
	return out
}

// SetChatRoomUserRole replaces the roles of uid in the room. When the room
// is open a role change event is broadcast.
func (c *ChatroomContainer) SetChatRoomUserRole(uid int64, roleIDs []int64, roomOpen bool) (servicepb.ErrorCode, error) {
	tx := db.Begin(readCommitted)
	if tx.Error != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, tx.Error
	}
	defer tx.Rollback()

	var old []model.UserLogicRole
	if err := tx.Select("role_id").Where("app_id = ? AND chatroom_id = ? AND uid = ?", c.appId, c.roomId, uid).
		Find(&old).Error; err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if err := tx.Where("app_id = ? AND chatroom_id = ? AND uid = ?", c.appId, c.roomId, uid).
		Delete(&model.UserLogicRole{}).Error; err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	for _, id := range roleIDs {
		has, err := exists(tx.Where("app_id = ? AND role_id = ?", c.appId, id), &model.Role{})
		if err != nil {
			return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
		}
		if !has {
			return servicepb.ErrorCode_CHATROOM_ROLE_NOT_EXISTS_ERROR, nil
		}
	}
	if rows := userRoleRows(c.appId, c.roomId, uid, roleIDs); len(rows) > 0 {
		if err := tx.Create(&rows).Error; err != nil {
			return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
		}
	}
	if err := tx.Commit().Error; err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	setRoleCache(c.ctx, c.appId, c.roomId, uid, roleIDs)

	if !roomOpen {
		return servicepb.ErrorCode_SUCCESS, nil
	}
	errCode, toInfo, err := c.GetChatroomMemberInfo(uid)
	if err != nil || errCode != servicepb.ErrorCode_SUCCESS {
		return errCode, err
	}
	oldIDs := make([]int64, 0, len(old))
	for _, r := range old {
		oldIDs = append(oldIDs, r.RoleId)
	}
	toInfo.RoleId = append(toInfo.RoleId, roleIDsToInt32(roleIDs)...)
	_ = broadcastRoleChange(c.ctx, c.appId, c.roomId, c.ctx.MsgID(), uid, toInfo, map[string]string{
		"old_role_id": joinRoleIDs(oldIDs),
		"new_role_id": joinRoleIDs(roleIDs),
	})
	return servicepb.ErrorCode_SUCCESS, nil
}

// RemoveChatRoomUserRole removes one role of uid in the room.
func (c *ChatroomContainer) RemoveChatRoomUserRole(uid, roleID int64, roomOpen bool) (servicepb.ErrorCode, error) {
	errCode, toInfo, err := c.GetChatroomMemberInfo(uid)
	if err != nil || errCode == servicepb.ErrorCode_SERVER_INTERNAL_ERROR {
		c.ctx.Error("RemoveChatRoomUserRole failed", zap.Error(err), zap.String("eCode", errCode.String()))
		return errCode, err
	}
	oldIDs, newIDs, errCode, err := removeUserRole(c.ctx, c.appId, uid, c.roomId, roleID)
	if err != nil || errCode != servicepb.ErrorCode_SUCCESS {
		return errCode, err
	}

	if roomOpen {
		if toInfo == nil {
			toInfo = &servicepb.ChatroomMemberInfo{Uid: uid}
		}
		toInfo.RoleId = append(toInfo.RoleId, roleIDsToInt32(newIDs)...)
		_ = broadcastRoleChange(c.ctx, c.appId, c.roomId, c.ctx.MsgID(), uid, toInfo, map[string]string{
			"old_role_id": joinRoleIDs(oldIDs),
			"new_role_id": joinRoleIDs(newIDs),
		})
	}
	return servicepb.ErrorCode_SUCCESS, nil
}

func removeUserRole(ctx context.Context, appID string, uid, roomID, roleID int64) (oldIDs, newIDs []int64, errCode servicepb.ErrorCode, err error) {
	tx := db.Begin(readCommitted)
	if tx.Error != nil {
		return nil, nil, servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, tx.Error
	}
	defer tx.Rollback()

	has, err := exists(tx.Where("app_id = ? AND role_id = ?", appID, roleID), &model.Role{})
	if err != nil {
		return nil, nil, servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if !has {
		return nil, nil, servicepb.ErrorCode_CHATROOM_ROLE_NOT_EXISTS_ERROR, nil
	}
	var old []model.UserLogicRole
	if err := tx.Where("app_id = ? AND chatroom_id = ? AND uid = ?", appID, roomID, uid).Find(&old).Error; err != nil {
		return nil, nil, servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if err := tx.Where("app_id = ? AND chatroom_id = ? AND uid = ? AND role_id = ?", appID, roomID, uid, roleID).
		Delete(&model.UserLogicRole{}).Error; err != nil {
		return nil, nil, servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if err := tx.Commit().Error; err != nil {
		return nil, nil, servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	rdb.SRem(ctx, rediskey.ChatroomUserLogicRole(appID, roomID, uid), roleID)

	for _, r := range old {
		oldIDs = append(oldIDs, r.RoleId)
		if r.RoleId != roleID {
			newIDs = append(newIDs, r.RoleId)
		}
	}
	return oldIDs, newIDs, servicepb.ErrorCode_SUCCESS, nil
}

func permissionRows(appID string, roleID int64, perms []*servicepb.RolePermission) []model.Permission {
	now := model.Now()
	rows := make([]model.Permission, 0, len(perms))
	for _, p := range perms {
		rows = append(rows, model.Permission{
			AppId:           appID,
			RoleId:          roleID,
			ActionType:      p.ActionType,
			CanDoAction:     p.CanDoAction,
			InitiativeLevel: p.InitiativeLevel,
			PassivityLevel:  p.PassivityLevel,
			CreatedAt:       now,
			UpdatedAt:       now,
		})
	}
	return rows
}

// CreateRole creates an app role with its permissions.
func CreateRole(appID, name string, roleID int64, perms []*servicepb.RolePermission, keepOnClosed bool) (servicepb.ErrorCode, error) {
	tx := db.Begin()
	if tx.Error != nil {
		return servicepb.ErrorCode_SERVER_INTERNAL_ERROR, tx.Error
	}
	defer tx.Rollback()

	q := tx.Where("app_id = ?", appID)
	if roleID != 0 {
		q = q.Where("role_id = ?", roleID)
	}
	if name != "" {
		q = q.Where("name = ?", name)
	}
	has, err := exists(q, &model.Role{})
	if err != nil {
		return servicepb.ErrorCode_SERVER_INTERNAL_ERROR, err
	}
	if has {
		return servicepb.ErrorCode_CHATROOM_ROLE_EXIST, nil
	}

	now := model.Now()
	if err := tx.Create(&model.Role{
		AppId: appID, Name: name, RoleId: roleID, KeepOnClosed: keepOnClosed,
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		return servicepb.ErrorCode_SERVER_INTERNAL_ERROR, err
	}
	if rows := permissionRows(appID, roleID, perms); len(rows) > 0 {
		if err := tx.Create(&rows).Error; err != nil {
			return servicepb.ErrorCode_SERVER_INTERNAL_ERROR, err
		}
	}
	if err := tx.Commit().Error; err != nil {
		return servicepb.ErrorCode_SERVER_INTERNAL_ERROR, err
	}
	return servicepb.ErrorCode_SUCCESS, nil
}

// ModifyRole replaces the permissions and keep_on_closed flag of a role.
func ModifyRole(appID string, roleID int64, perms []*servicepb.RolePermission, keepOnClosed bool) (servicepb.ErrorCode, error) {
	tx := db.Begin()
	if tx.Error != nil {
		return servicepb.ErrorCode_SERVER_INTERNAL_ERROR, tx.Error
	}
	defer tx.Rollback()

	has, err := exists(tx.Where("app_id = ? AND role_id = ?", appID, roleID), &model.Role{})
	if err != nil {
		return servicepb.ErrorCode_SERVER_INTERNAL_ERROR, err
	}
	if !has {
		return servicepb.ErrorCode_CHATROOM_ROLE_NOT_EXISTS_ERROR, nil
	}
	if err := tx.Model(&model.Role{}).Where("app_id = ? AND role_id = ?", appID, roleID).
		Updates(map[string]any{"keep_on_closed": keepOnClosed, "updated_at": model.Now()}).Error; err != nil {
		return servicepb.ErrorCode_SERVER_INTERNAL_ERROR, err
	}
	if err := tx.Where("app_id = ? AND role_id = ?", appID, roleID).Delete(&model.Permission{}).Error; err != nil {
		return servicepb.ErrorCode_SERVER_INTERNAL_ERROR, err
	}
	if rows := permissionRows(appID, roleID, perms); len(rows) > 0 {
		if err := tx.Create(&rows).Error; err != nil {
			return servicepb.ErrorCode_SERVER_INTERNAL_ERROR, err
		}
	}
	if err := tx.Commit().Error; err != nil {
		return servicepb.ErrorCode_SERVER_INTERNAL_ERROR, err
	}
	return servicepb.ErrorCode_SUCCESS, nil
}

// QueryRole returns the given roles of an app, or all of them when roleIDs
// is empty.
func QueryRole(appID string, roleIDs []int64) ([]*servicepb.Role, servicepb.ErrorCode, error) {
	q := db.Where("app_id = ?", appID)
	if len(roleIDs) > 0 {
		q = q.Where("role_id IN ?", roleIDs)
	}
	var roles []model.Role
	if err := q.Find(&roles).Error; err != nil {
		return nil, servicepb.ErrorCode_SERVER_INTERNAL_ERROR, err
	}

	var out []*servicepb.Role
	for _, r := range roles {
		var perms []model.Permission
		if err := db.Where("app_id = ? AND role_id = ?", appID, r.RoleId).Find(&perms).Error; err != nil {
			return out, servicepb.ErrorCode_SERVER_INTERNAL_ERROR, err
		}
		var pbPerms []*servicepb.RolePermission
		for _, p := range perms {
			pbPerms = append(pbPerms, &servicepb.RolePermission{
				ActionType:      p.ActionType,
				CanDoAction:     p.CanDoAction,
				InitiativeLevel: p.InitiativeLevel,
				PassivityLevel:  p.PassivityLevel,
			})
		}
		out = append(out, &servicepb.Role{
			RoleName:     r.Name,
			RoleId:       r.RoleId,
			Permissions:  pbPerms,
			KeepOnClosed: r.KeepOnClosed,
		})
	}
	return out, servicepb.ErrorCode_SUCCESS, nil
}
