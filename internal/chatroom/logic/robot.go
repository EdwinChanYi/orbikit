package logic

import (
	"go.uber.org/zap"

	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
	"github.com/EdwinChanYi/orbikit/internal/chatroom/model"
	"github.com/EdwinChanYi/orbikit/internal/chatroom/rediskey"
	"github.com/EdwinChanYi/orbikit/pkg/log"
)

// AddRobotInfo stores (or replaces) the profile of a robot.
func AddRobotInfo(appID string, info *servicepb.ChatroomMemberInfo) (servicepb.ErrorCode, error) {
	tx := db.Begin()
	if tx.Error != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, tx.Error
	}
	defer tx.Rollback()

	if err := tx.Where("app_id = ? AND uid = ?", appID, info.Uid).Delete(&model.RobotInfo{}).Error; err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	now := model.Now()
	if err := tx.Create(&model.RobotInfo{
		AppId:             appID,
		Uid:               info.Uid,
		Nickname:          info.Nickname,
		Avatar:            info.Avatar,
		CustomInfo:        info.CustomInfo,
		ChatroomRemoteExt: info.ChatroomRemoteExt,
		SearchData:        info.SearchKeys,
		CreatedAt:         now,
		UpdatedAt:         now,
	}).Error; err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if err := tx.Commit().Error; err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	return servicepb.ErrorCode_SUCCESS, nil
}

// DelRobotInfo keeps robot profiles; robots may still be referenced by rooms.
func DelRobotInfo(appID string, uid int64) (servicepb.ErrorCode, error) {
	return servicepb.ErrorCode_SUCCESS, nil
}

func robotMemberInfo(r *model.RobotInfo) *servicepb.ChatroomMemberInfo {
	return &servicepb.ChatroomMemberInfo{
		Uid:               r.Uid,
		Nickname:          r.Nickname,
		Avatar:            r.Avatar,
		CustomInfo:        r.CustomInfo,
		ChatroomRemoteExt: r.ChatroomRemoteExt,
		SearchKeys:        r.SearchData,
	}
}

func robotInfo(appID string, uid int64) *servicepb.ChatroomMemberInfo {
	r := &model.RobotInfo{}
	has, err := first(db.Where("app_id = ? AND uid = ?", appID, uid), r)
	if err != nil {
		log.L().Error("GetRobotInfo Get err", zap.Error(err))
	}
	if !has {
		return nil
	}
	return robotMemberInfo(r)
}

func robotsInfo(appID string, uids []int64) []*servicepb.ChatroomMemberInfo {
	var rows []model.RobotInfo
	if err := db.Where("app_id = ? AND uid IN ?", appID, uids).Find(&rows).Error; err != nil {
		log.L().Error("GetRobotsInfo Find err", zap.Error(err))
		return nil
	}
	var out []*servicepb.ChatroomMemberInfo
	for i := range rows {
		out = append(out, robotMemberInfo(&rows[i]))
	}
	return out
}

// RoomFillRobotInfo stages the stored profile of a robot as its member info.
func (c *ChatroomContainer) RoomFillRobotInfo(uid int64) (servicepb.ErrorCode, error) {
	info := robotInfo(c.appId, uid)
	if info == nil {
		return servicepb.ErrorCode_CHATROOM_ROBOT_UID_INFO_NOT_EXISTS, nil
	}
	info.IsReboot = true
	return c.ResetMemberInfo(uid, info, false)
}

// RoomFillRobotsInfo stages and applies the member info of several robots.
func (c *ChatroomContainer) RoomFillRobotsInfo(robots map[int64][]int32) (servicepb.ErrorCode, error) {
	uids := make([]int64, 0, len(robots))
	for uid := range robots {
		uids = append(uids, uid)
	}
	infos := robotsInfo(c.appId, uids)
	if infos == nil {
		return servicepb.ErrorCode_CHATROOM_ROBOT_UID_INFO_NOT_EXISTS, nil
	}
	for _, info := range infos {
		info.RoleId = robots[info.Uid]
		info.IsReboot = true
		if _, err := c.ResetMemberInfo(info.Uid, info, true); err != nil {
			return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
		}
		if err := resetUserInfo(c.ctx, c.appId, c.roomId, info.Uid); err != nil {
			return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
		}
	}
	return servicepb.ErrorCode_SUCCESS, nil
}

// RobotFill puts a robot whose member info is ready online in the room.
func (c *ChatroomContainer) RobotFill(uid int64) (servicepb.ErrorCode, error) {
	blocked, err := isBlockedMember(c.appId, c.roomId, uid)
	if err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if blocked {
		return servicepb.ErrorCode_CHATROOM_MEMBER_BLOCK, nil
	}
	kicked, err := isKickedMember(c.ctx, c.appId, c.roomId, uid)
	if err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if kicked != 0 {
		return servicepb.ErrorCode_CHATROOM_MEMBER_KIKY, nil
	}

	if err := setMemberOnline(c.appId, c.roomId, uid, true); err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if err := rdb.SAdd(c.ctx, rediskey.MemberInChatroomSet(c.appId, uid), c.roomId).Err(); err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	onlineKey := rediskey.ChatroomOnlineMember(c.appId, c.roomId)
	has, err := rdb.SIsMember(c.ctx, onlineKey, uid).Result()
	if err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if has {
		return servicepb.ErrorCode_SUCCESS, nil
	}
	if err := rdb.SAdd(c.ctx, onlineKey, uid).Err(); err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	c.incrOnlineCount(1)
	c.incrRobotCount(1)
	if err := updateMicmemZ(c.ctx, c.appId, c.roomId); err != nil {
		c.ctx.Error("update mic mem zrank failed", zap.Error(err))
	}
	return servicepb.ErrorCode_SUCCESS, nil
}

// RobotEnter makes a robot enter the room with the given roles.
func (c *ChatroomContainer) RobotEnter(uid int64, roleIDs []int32) (servicepb.ErrorCode, error) {
	if errCode, err := c.RoomFillRobotInfo(uid); err != nil || errCode != servicepb.ErrorCode_SUCCESS {
		return errCode, err
	}
	ids := make([]int64, 0, len(roleIDs))
	for _, v := range roleIDs {
		ids = append(ids, int64(v))
	}
	_ = resetUserInfo(c.ctx, c.appId, c.roomId, uid)
	_, _ = c.SetChatRoomUserRole(uid, ids, false)
	return c.RobotFill(uid)
}

// RobotsEnter makes several robots enter the room.
func (c *ChatroomContainer) RobotsEnter(robots map[int64][]int32) (servicepb.ErrorCode, error) {
	if errCode, err := c.RoomFillRobotsInfo(robots); err != nil || errCode != servicepb.ErrorCode_SUCCESS {
		return errCode, err
	}
	for uid := range robots {
		if _, err := c.RobotFill(uid); err != nil {
			return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
		}
	}
	return servicepb.ErrorCode_SUCCESS, nil
}

// RobotLeave takes a robot out of the room.
func (c *ChatroomContainer) RobotLeave(uid int64) (servicepb.ErrorCode, error) {
	if errCode, err := c.RemoveChatroomMemberInfo(uid); err != nil || errCode != servicepb.ErrorCode_SUCCESS {
		return errCode, err
	}
	if err := setMemberOnline(c.appId, c.roomId, uid, false); err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if err := rdb.SRem(c.ctx, rediskey.MemberInChatroomSet(c.appId, uid), c.roomId).Err(); err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	onlineKey := rediskey.ChatroomOnlineMember(c.appId, c.roomId)
	has, err := rdb.SIsMember(c.ctx, onlineKey, uid).Result()
	if err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if !has {
		return servicepb.ErrorCode_CHATROOM_NOT_IN_ROOM, nil
	}
	if err := rdb.SRem(c.ctx, onlineKey, uid).Err(); err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	c.incrRobotCount(-1)
	c.incrOnlineCount(-1)
	if err := updateMicmemZ(c.ctx, c.appId, c.roomId); err != nil {
		c.ctx.Error("update mic mem zrank failed", zap.Error(err))
	}
	return servicepb.ErrorCode_SUCCESS, nil
}
