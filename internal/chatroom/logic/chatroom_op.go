package logic

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
	"github.com/EdwinChanYi/orbikit/internal/chatroom/lock"
	"github.com/EdwinChanYi/orbikit/internal/chatroom/model"
	"github.com/EdwinChanYi/orbikit/internal/chatroom/rediskey"
)

const (
	enterTokenLen     = 64
	enterTokenLetters = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
)

func randString(n int, letters string) (string, error) {
	var sb strings.Builder
	sb.Grow(n)
	buf := make([]byte, 4*n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	l := uint32(len(letters))
	for i := 0; i < n; i++ {
		sb.WriteByte(letters[binary.BigEndian.Uint32(buf[4*i:])%l])
	}
	return sb.String(), nil
}

// enterUserDeal admits uid into the room once the enter token is verified.
func (c *ChatroomContainer) enterUserDeal(uid int64) (servicepb.ErrorCode, error) {
	kicked, err := isKickedMember(c.ctx, c.appId, c.roomId, uid)
	if err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if kicked != 0 {
		return servicepb.ErrorCode_CHATROOM_MEMBER_KIKY, nil
	}
	blocked, err := isBlockedMember(c.appId, c.roomId, uid)
	if err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if blocked {
		return servicepb.ErrorCode_CHATROOM_MEMBER_BLOCK, nil
	}

	if err := resetUserInfo(c.ctx, c.appId, c.roomId, uid); err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if err := setMemberOnline(c.appId, c.roomId, uid, true); err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if err := roomRecord(c.appId, c.roomId, uid, servicepb.AtRoomInfo_NONE); err != nil {
		c.ctx.Error("RoomRecord err", zap.Error(err))
	}

	if err := rdb.SAdd(c.ctx, rediskey.MemberInChatroomSet(c.appId, uid), c.roomId).Err(); err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	onlineKey := rediskey.ChatroomOnlineMember(c.appId, c.roomId)
	has, err := rdb.SIsMember(c.ctx, onlineKey, uid).Result()
	if err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if !has {
		if err := rdb.SAdd(c.ctx, onlineKey, uid).Err(); err != nil {
			return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
		}
		c.incrOnlineCount(1)
	}
	AddTaskOfDealChatroom(c.ctx, c.appId, c.roomId)
	return servicepb.ErrorCode_SUCCESS, nil
}

// Enter admits uid with the token issued by ApplyToken.
func (c *ChatroomContainer) Enter(uid int64, token string) (servicepb.ErrorCode, error) {
	if !lock.AcquireUser(c.ctx, c.appId, c.roomId, uid, true) {
		return servicepb.ErrorCode_CHATROOM_ROOM_USER_LOCKED, nil
	}
	defer lock.ReleaseUser(c.appId, c.roomId, uid)

	saved, err := rdb.Get(c.ctx, rediskey.ChatroomEnterToken(c.appId, c.roomId, uid)).Result()
	if err != nil {
		if isNil(err) {
			return servicepb.ErrorCode_CHATROOM_ENTER_TOKEN_ERROR, nil
		}
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if saved != token {
		return servicepb.ErrorCode_CHATROOM_ENTER_TOKEN_ERROR, nil
	}
	return c.enterUserDeal(uid)
}

// Leave removes uid from the room. It shares the user lock with Enter so the
// two cannot interleave.
func (c *ChatroomContainer) Leave(uid int64, reason servicepb.AtRoomInfo_LeaveReason) (servicepb.ErrorCode, error) {
	if !lock.AcquireUser(c.ctx, c.appId, c.roomId, uid, false) {
		return servicepb.ErrorCode_CHATROOM_ROOM_USER_LOCKED, nil
	}
	defer lock.ReleaseUser(c.appId, c.roomId, uid)

	if errCode, err := c.RemoveChatroomMemberInfo(uid); err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	} else if errCode != servicepb.ErrorCode_SUCCESS {
		return errCode, nil
	}
	if err := setMemberOnline(c.appId, c.roomId, uid, false); err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if err := roomRecord(c.appId, c.roomId, uid, reason); err != nil {
		c.ctx.Error("RoomRecord err", zap.Error(err))
	}

	if err := rdb.SRem(c.ctx, rediskey.MemberInChatroomSet(c.appId, uid), c.roomId).Err(); err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if err := rdb.SRem(c.ctx, rediskey.ChatroomOnlineRealMember(c.appId, c.roomId), uid).Err(); err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	onlineKey := rediskey.ChatroomOnlineMember(c.appId, c.roomId)
	has, err := rdb.SIsMember(c.ctx, onlineKey, uid).Result()
	if err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if !has {
		AddTaskOfDealChatroom(c.ctx, c.appId, c.roomId)
		return servicepb.ErrorCode_CHATROOM_NOT_IN_ROOM, nil
	}
	if err := rdb.SRem(c.ctx, onlineKey, uid).Err(); err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	c.incrOnlineCount(-1)
	AddTaskOfDealChatroom(c.ctx, c.appId, c.roomId)
	return servicepb.ErrorCode_SUCCESS, nil
}

// CreateChatroom creates the room, or refreshes an existing one and reports
// CHATROOM_EXISTS.
func (c *ChatroomContainer) CreateChatroom(ci *servicepb.ChatroomInfo) (servicepb.ErrorCode, error) {
	now := model.Now()
	info := &model.ChatroomInfo{
		AppId:        c.appId,
		ChatroomId:   c.roomId,
		Name:         ci.Name,
		Announcement: ci.Announcement,
		OwnerUid:     ci.OwnerUid,
		IsMuted:      ci.IsMuteAll,
		Filter:       ci.CustomInfoKeyFilter,
		Ext:          ci.Ext,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if info.Filter == nil {
		info.Filter = model.StringList{}
	}
	if info.Ext == nil {
		info.Ext = model.StringMap{}
	}

	tx := db.Begin()
	if tx.Error != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, tx.Error
	}
	defer tx.Rollback()

	has, err := exists(tx.Where("app_id = ? AND chatroom_id = ?", c.appId, c.roomId), &model.ChatroomInfo{})
	if err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	errCode := servicepb.ErrorCode_SUCCESS
	if has {
		updates := map[string]any{"filter": info.Filter, "ext": info.Ext, "updated_at": now}
		if info.Name != "" {
			updates["name"] = info.Name
		}
		if info.Announcement != "" {
			updates["announcement"] = info.Announcement
		}
		if info.OwnerUid != 0 {
			updates["owner_uid"] = info.OwnerUid
		}
		if err := tx.Model(&model.ChatroomInfo{}).Where("app_id = ? AND chatroom_id = ?", c.appId, c.roomId).
			Updates(updates).Error; err != nil {
			return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
		}
		errCode = servicepb.ErrorCode_CHATROOM_EXISTS
	} else if err := tx.Create(info).Error; err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}

	if err := syncChatroomInfoToRedis(c.ctx, c.appId, c.roomId, info); err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if err := tx.Commit().Error; err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if !has {
		c.isExists = true
	}
	return errCode, nil
}

// reloadChatroomInfo reads the room row inside tx and refreshes the cache.
func (c *ChatroomContainer) reloadChatroomInfo(tx *gorm.DB) (bool, error) {
	info := &model.ChatroomInfo{}
	has, err := first(tx.Where("app_id = ? AND chatroom_id = ?", c.appId, c.roomId), info)
	if err != nil || !has {
		return has, err
	}
	return true, syncChatroomInfoToRedis(c.ctx, c.appId, c.roomId, info)
}

// SetChatroomInfoIsMuted mutes or unmutes the whole room.
func (c *ChatroomContainer) SetChatroomInfoIsMuted(isMuted bool) (servicepb.ErrorCode, error) {
	tx := db.Begin()
	if tx.Error != nil {
		c.ctx.Error("SetChatroomInfoIsMuted tx.Begin err", zap.Error(tx.Error))
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, tx.Error
	}
	defer tx.Rollback()

	if err := tx.Model(&model.ChatroomInfo{}).Where("app_id = ? AND chatroom_id = ?", c.appId, c.roomId).
		Updates(map[string]any{"is_muted": isMuted, "updated_at": model.Now()}).Error; err != nil {
		c.ctx.Error("SetChatroomInfoIsMuted Update err", zap.Error(err))
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	has, err := c.reloadChatroomInfo(tx)
	if err != nil {
		c.ctx.Error("SetChatroomInfoIsMuted reload err", zap.Error(err))
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if !has {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, nil
	}
	if err := tx.Commit().Error; err != nil {
		c.ctx.Error("SetChatroomInfoIsMuted Commit err", zap.Error(err))
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}

	typ := evRoomUnmuted
	if isMuted {
		typ = evRoomMuted
	}
	if _, err := broadcastEvent(c.ctx, typ, c.appId, c.roomId, c.ctx.MsgID(), 0, 0, nil, nil, nil); err != nil {
		c.ctx.Error("SetChatroomInfoIsMuted broadcast err", zap.Error(err))
	}
	return servicepb.ErrorCode_SUCCESS, nil
}

// IsOpened reports whether the room is open, caching the state in Redis.
func (c *ChatroomContainer) IsOpened() (bool, error) {
	key := rediskey.ChatroomIsOpened(c.appId, c.roomId)
	if v, err := rdb.Get(c.ctx, key).Result(); err == nil {
		switch v {
		case "0":
			return false, nil
		case "1":
			return true, nil
		}
	}

	info := &model.ChatroomInfo{}
	has, err := first(db.Select("is_open").Where("app_id = ? AND chatroom_id = ?", c.appId, c.roomId), info)
	if err != nil {
		c.ctx.Error("IsOpened db get err", zap.Error(err))
		return false, err
	}
	if !has {
		return false, nil
	}
	v := "0"
	if info.IsOpen {
		v = "1"
	}
	rdb.Set(c.ctx, key, v, 0)
	return info.IsOpen, nil
}

func (c *ChatroomContainer) setOpen(open bool) error {
	return db.Model(&model.ChatroomInfo{}).Where("app_id = ? AND chatroom_id = ?", c.appId, c.roomId).
		Updates(map[string]any{"is_open": open, "updated_at": model.Now()}).Error
}

// Open opens an existing room.
func (c *ChatroomContainer) Open() (servicepb.ErrorCode, error) {
	if !c.isExists {
		return servicepb.ErrorCode_CHATROOM_NOT_EXISTS, nil
	}
	opened, err := c.IsOpened()
	if err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if opened {
		return servicepb.ErrorCode_CHATROOM_OPENED, nil
	}
	if err := c.setOpen(true); err != nil {
		c.ctx.Error("Open update err", zap.Error(err))
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if err := rdb.Set(c.ctx, rediskey.ChatroomIsOpened(c.appId, c.roomId), "1", 0).Err(); err != nil {
		c.ctx.Error("Open Set err", zap.Error(err))
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	return servicepb.ErrorCode_SUCCESS, nil
}

// Close closes the room: clears the mic layout and member data and kicks
// everyone online. The room info itself is kept.
func (c *ChatroomContainer) Close() (servicepb.ErrorCode, error) {
	logFields := []zap.Field{zap.String("appID", c.appId), zap.Int64("roomID", c.roomId)}
	opened, err := c.IsOpened()
	if err != nil {
		c.ctx.Error("Close IsOpened err", append(logFields, zap.Error(err))...)
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if !opened {
		return servicepb.ErrorCode_CHATROOM_CLOSED, nil
	}

	if err := c.setOpen(false); err != nil {
		c.ctx.Error("Close update err", zap.Error(err))
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if err := rdb.Set(c.ctx, rediskey.ChatroomIsOpened(c.appId, c.roomId), 0, 0).Err(); err != nil {
		c.ctx.Error("Close Set is opened err", zap.Error(err))
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}

	space := MakeMicSpace(c.ctx, c.appId, c.roomId)
	if errCode, err := space.Init(); err != nil {
		c.ctx.Error("Close space.Init err", append(logFields, zap.Error(err))...)
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	} else if errCode != servicepb.ErrorCode_SUCCESS {
		return errCode, nil
	}
	if err := space.Destroy(); err != nil {
		c.ctx.Error("Close Destroy err", append(logFields, zap.Error(err))...)
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}

	if errCode, err := c.FlushAllMemberInfo(); err != nil {
		c.ctx.Error("Close FlushAllMemberInfo err", append(logFields, zap.Error(err))...)
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	} else if errCode != servicepb.ErrorCode_SUCCESS {
		return errCode, nil
	}

	kicked := &servicepb.ChatRoomMemberBeKickedBcst{
		AppId:        c.appId,
		RoomId:       c.roomId,
		KickedReason: servicepb.ChatroomKickReason_KICK_BY_CHATROOM_INVALID,
	}
	uids, err := rdb.SMembers(c.ctx, rediskey.ChatroomOnlineMember(c.appId, c.roomId)).Result()
	if err != nil {
		c.ctx.Error("Close SMembers online member err", append(logFields, zap.Error(err))...)
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	for _, v := range uids {
		uid, _ := strconv.ParseInt(v, 10, 64)
		_ = roomRecord(c.appId, c.roomId, uid, servicepb.AtRoomInfo_CLOSE_ROOM)
		if err := rdb.SRem(c.ctx, rediskey.MemberInChatroomSet(c.appId, uid), c.roomId).Err(); err != nil {
			c.ctx.Error("Close SRem member room set err", append(logFields, zap.Error(err))...)
			continue
		}
		if err := rdb.Del(c.ctx, rediskey.ChatroomMemberInfo(c.appId, c.roomId, uid)).Err(); err != nil {
			c.ctx.Error("Close Del member info err", append(logFields, zap.Error(err))...)
			continue
		}
		if err := pushKickedReason(c.ctx, c.appId, c.roomId, uid, 0, servicepb.ChatroomKickReason_KICK_BY_CHATROOM_INVALID); err != nil {
			return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
		}
		kicked.ToUid = append(kicked.ToUid, uid)
	}
	events.Kicked(kicked)

	c.ctx.Info("user status trace", zap.String("target", "close room"), zap.Int64("chatroom id", c.roomId))
	if err := rdb.Del(c.ctx, rediskey.ChatroomOnlineMember(c.appId, c.roomId)).Err(); err != nil {
		c.ctx.Error("Close Del online member err", zap.Error(err))
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	c.delRobotCount()
	c.delOnlineCount()
	_ = delMemmicZ(c.ctx, c.appId, c.roomId)
	_ = delMicmemZ(c.ctx, c.appId, c.roomId)
	return servicepb.ErrorCode_SUCCESS, nil
}

// ApplyToken issues (or renews) the enter token of uid and stages its
// member info.
func (c *ChatroomContainer) ApplyToken(uid int64, memberInfo *servicepb.ChatroomMemberInfo) (servicepb.ErrorCode, string, error) {
	key := rediskey.ChatroomEnterToken(c.appId, c.roomId, uid)
	token, err := rdb.Get(c.ctx, key).Result()
	if err != nil {
		if !isNil(err) {
			return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, "", err
		}
		if token, err = randString(enterTokenLen, enterTokenLetters); err != nil {
			return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, "", err
		}
	}
	if err := rdb.Set(c.ctx, key, token, settings.EnterTokenTTL).Err(); err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, "", err
	}
	if errCode, err := c.ResetMemberInfo(uid, memberInfo, false); err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, "", err
	} else if errCode != servicepb.ErrorCode_SUCCESS {
		return errCode, "", nil
	}
	return servicepb.ErrorCode_SUCCESS, token, nil
}

// ModifyChatroomInfoByUser lets a member with EDIT_CHATROOM_INFO change the
// room name and announcement.
func (c *ChatroomContainer) ModifyChatroomInfoByUser(uid int64, name, announcement string) (servicepb.ErrorCode, error) {
	if errCode, err := checkUserPermission(c.ctx, c.appId, c.roomId, uid, 0, servicepb.Privilege_EDIT_CHATROOM_INFO,
		servicepb.ErrorCode_CHATROOM_MEMBER_NOT_MODIFY_PERMISSION); err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	} else if errCode != servicepb.ErrorCode_SUCCESS {
		return errCode, nil
	}
	errCode, memberInfo, err := c.GetChatroomMemberInfo(uid)
	if err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if errCode != servicepb.ErrorCode_SUCCESS {
		return errCode, nil
	}

	tx := db.Begin()
	if tx.Error != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, tx.Error
	}
	defer tx.Rollback()

	has, err := exists(tx.Where("app_id = ? AND chatroom_id = ?", c.appId, c.roomId), &model.ChatroomInfo{})
	if err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if !has {
		return servicepb.ErrorCode_CHATROOM_NOT_EXISTS, nil
	}
	if err := tx.Model(&model.ChatroomInfo{}).Where("app_id = ? AND chatroom_id = ?", c.appId, c.roomId).
		Updates(map[string]any{"name": name, "announcement": announcement, "updated_at": model.Now()}).Error; err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if has, err := c.reloadChatroomInfo(tx); err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	} else if !has {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, nil
	}
	if err := tx.Commit().Error; err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}

	if _, err := broadcastEvent(c.ctx, evInfoUpdated, c.appId, c.roomId, c.ctx.MsgID(), uid, 0, memberInfo, nil,
		c.GetChatroomInfoToMap()); err != nil {
		c.ctx.Error("ModifyChatroomInfoByUser broadcast err", zap.Error(err))
	}
	return servicepb.ErrorCode_SUCCESS, nil
}

// ModifyChatroomInfo overwrites the room info, creating the room as open
// when it does not exist.
func (c *ChatroomContainer) ModifyChatroomInfo(ci *servicepb.ChatroomInfo) (servicepb.ErrorCode, error) {
	var info *model.ChatroomInfo
	data, err := rdb.Get(c.ctx, rediskey.ChatroomInfo(c.appId, c.roomId)).Result()
	switch {
	case err != nil && !isNil(err):
		c.ctx.Error("ModifyChatroomInfo get cache err", zap.Error(err))
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	case err != nil:
		info = &model.ChatroomInfo{}
		has, err := first(db.Where("app_id = ? AND chatroom_id = ?", c.appId, c.roomId), info)
		if err != nil {
			c.ctx.Error("ModifyChatroomInfo db find err", zap.Error(err))
			return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
		}
		if !has {
			return servicepb.ErrorCode_CHATROOM_NOT_EXISTS, nil
		}
		info.Announcement = ci.Announcement
		info.Name = ci.Name
		info.IsMuted = ci.IsMuteAll
		info.Filter = ci.CustomInfoKeyFilter
		info.Ext = ci.Ext
	default:
		var cached model.ChatroomInfo
		if err := json.Unmarshal([]byte(data), &cached); err != nil {
			c.ctx.Error("ModifyChatroomInfo unmarshal cache err", zap.Error(err))
			return servicepb.ErrorCode_SERVER_INTERNAL_ERROR, err
		}
		info = &model.ChatroomInfo{
			AppId:        c.appId,
			ChatroomId:   c.roomId,
			Name:         ci.Name,
			Announcement: ci.Announcement,
			OwnerUid:     cached.OwnerUid,
			IsMuted:      ci.IsMuteAll,
			Filter:       ci.CustomInfoKeyFilter,
			Ext:          ci.Ext,
		}
	}
	if info.Filter == nil {
		info.Filter = model.StringList{}
	}
	if info.Ext == nil {
		info.Ext = model.StringMap{}
	}

	filter, err := json.Marshal(ci.CustomInfoKeyFilter)
	if err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	ext, err := json.Marshal(ci.Ext)
	if err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}

	tx := db.Begin()
	if tx.Error != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, tx.Error
	}
	defer tx.Rollback()

	sNow := model.FormatTime(time.Now())
	if err := tx.Exec(`INSERT INTO chatroom_info(app_id, chatroom_id,
		name, announcement, owner_uid, is_muted, filter, ext, is_open, created_at, updated_at) VALUES(
		?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON DUPLICATE KEY UPDATE name=?, announcement=?, is_muted=?, filter=?, ext=?, updated_at=?`,
		c.appId, c.roomId, ci.Name, ci.Announcement, ci.OwnerUid, ci.IsMuteAll, string(filter), string(ext), true, sNow, sNow,
		ci.Name, ci.Announcement, ci.IsMuteAll, string(filter), string(ext), sNow).Error; err != nil {
		c.ctx.Error("ModifyChatroomInfo upsert err", zap.Error(err))
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if err := syncChatroomInfoToRedis(c.ctx, c.appId, c.roomId, info); err != nil {
		c.ctx.Error("ModifyChatroomInfo sync cache err", zap.Error(err))
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if err := tx.Commit().Error; err != nil {
		c.ctx.Error("ModifyChatroomInfo commit err", zap.Error(err))
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	return servicepb.ErrorCode_SUCCESS, nil
}
