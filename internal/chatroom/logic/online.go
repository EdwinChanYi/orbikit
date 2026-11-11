package logic

import (
	"context"
	"strconv"
	"time"

	"go.uber.org/zap"

	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
	"github.com/EdwinChanYi/orbikit/internal/chatroom/lock"
	"github.com/EdwinChanYi/orbikit/internal/chatroom/rediskey"
	"github.com/EdwinChanYi/orbikit/internal/chatroom/reqctx"
	"github.com/EdwinChanYi/orbikit/pkg/log"
)

const roomTaskTimeout = 30 * time.Second

// Offline marks connID of uid as gone. The user leaves all rooms once the
// offline flag expires or the after-deal queue picks it up, unless Online
// is called first.
func Offline(ctx *reqctx.Context, appID string, uid int64, connID uint64) error {
	connKey := rediskey.UserConnID(appID, uid)
	v, err := rdb.Get(ctx, connKey).Result()
	if isNil(err) {
		ctx.Error("Offline no key found", zap.String("appID", appID), zap.Int64("uid", uid))
		return nil
	}
	if err != nil {
		ctx.Error("Offline Get err", zap.Error(err))
		return err
	}
	saved, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		ctx.Error("Offline parse conn id err", zap.String("appID", appID), zap.Int64("uid", uid),
			zap.String("val", v), zap.Error(err))
		return err
	}
	if saved != connID {
		ctx.Error("Offline conn id mismatch", zap.String("appID", appID), zap.Int64("uid", uid),
			zap.Uint64("saved", saved), zap.Uint64("connID", connID))
		return nil
	}

	pipe := rdb.TxPipeline()
	pipe.Del(ctx, connKey)
	pipe.Set(ctx, rediskey.UserOfflineChatroom(appID, uid), 0, settings.OfflineLeaveAfter)
	if _, err := pipe.Exec(ctx); err != nil {
		ctx.Error("Offline Exec failed", zap.Error(err))
	}
	_ = AddUserToQueue(ctx, appID, uid)

	msgID := ctx.MsgID()
	go notifyRooms(msgID, appID, uid, evUserOffline)
	return nil
}

// Online records the new connID of uid and cancels a pending offline leave.
func Online(ctx *reqctx.Context, appID string, uid int64, connID uint64) error {
	pipe := rdb.TxPipeline()
	pipe.Set(ctx, rediskey.UserConnID(appID, uid), connID, 0)
	pipe.Del(ctx, rediskey.UserOfflineChatroom(appID, uid))
	_, err := pipe.Exec(ctx)
	_ = RemoveUserFromQueue(ctx, appID, uid)

	msgID := ctx.MsgID()
	go notifyRooms(msgID, appID, uid, evUserOnline)
	return err
}

// notifyRooms broadcasts an online/offline event to every room uid is in.
func notifyRooms(msgID uint64, appID string, uid int64, typ servicepb.ChatroomEventType) {
	lctx, cancel := bg()
	roomIDs, err := chatroomIDsOfUser(lctx, appID, uid)
	cancel()
	if err != nil {
		log.L().Error("notifyRooms list rooms err", zap.String("appID", appID), zap.Int64("uid", uid), zap.Error(err))
		return
	}
	log.L().Info("user online state changed", zap.String("appID", appID), zap.Int64("uid", uid),
		zap.String("event", typ.String()), zap.Int64s("rooms", roomIDs))

	for _, roomID := range roomIDs {
		func() {
			ctx, cancel := reqctx.WithTimeout(msgID, appID, roomTaskTimeout)
			defer cancel()
			if !lock.AcquireRoomProcess(ctx, appID, roomID) {
				ctx.Error("notifyRooms get chatroom lock fail", zap.Int64("roomID", roomID))
				return
			}
			defer lock.ReleaseRoomProcess(appID, roomID)

			room, err := GetChatroom(ctx, appID, roomID)
			if err != nil {
				ctx.Error("notifyRooms GetChatroom err", zap.Error(err))
				return
			}
			errCode, err := room.BaseCheck()
			if err != nil {
				ctx.Error("notifyRooms BaseCheck err", zap.Error(err))
				return
			}
			if typ == evUserOffline && errCode != servicepb.ErrorCode_SUCCESS {
				return
			}
			errCode, info, err := room.GetChatroomMemberInfo(uid)
			if err != nil || errCode != servicepb.ErrorCode_SUCCESS {
				ctx.Error("notifyRooms GetChatroomMemberInfo err", zap.Error(err), zap.Int("errCode", int(errCode)))
				return
			}
			_, _ = broadcastEvent(ctx, typ, appID, roomID, ctx.MsgID(), uid, 0, info, nil, nil)
		}()
	}
}

func chatroomIDsOfUser(ctx context.Context, appID string, uid int64) ([]int64, error) {
	members, err := rdb.SMembers(ctx, rediskey.MemberInChatroomSet(appID, uid)).Result()
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(members))
	for _, v := range members {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			log.L().Error("chatroomIDsOfUser parse room id err", zap.String("v", v))
			continue
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// GetChatroomsByAppIdUid lists the rooms uid is in with their online counts.
func GetChatroomsByAppIdUid(ctx context.Context, appID string, uid int64) ([]*servicepb.ChatroomMemberCountInfo, error) {
	members, err := rdb.SMembers(ctx, rediskey.MemberInChatroomSet(appID, uid)).Result()
	if err != nil {
		return nil, err
	}
	var rooms []*servicepb.ChatroomMemberCountInfo
	for _, v := range members {
		roomID, _ := strconv.ParseInt(v, 10, 64)
		memCnt, err := onlineUserCount(ctx, appID, roomID)
		if err != nil {
			if !isNil(err) {
				log.L().Error("get room member count failed", zap.Error(err), zap.String("appId", appID), zap.Int64("roomId", roomID))
				return rooms, err
			}
			memCnt = 0
		}
		rooms = append(rooms, &servicepb.ChatroomMemberCountInfo{
			ChatroomId:  roomID,
			OnlineCount: int32(memCnt),
			RobotCount:  int32(robotCountNoError(ctx, appID, roomID)),
		})
	}
	return rooms, nil
}

// BatchOutChatroom makes uid leave every room it is in, taking it off all
// mic seats.
func BatchOutChatroom(appID string, uid int64) {
	lctx, cancel := bg()
	rooms, err := GetChatroomsByAppIdUid(lctx, appID, uid)
	cancel()
	if err != nil {
		log.L().Error("BatchOutChatroom GetChatroomsByAppIdUid err", zap.String("appID", appID), zap.Int64("uid", uid))
		return
	}
	log.L().Info("BatchOutChatroom begin LeaveAll", zap.String("appID", appID), zap.Int64("uid", uid), zap.Int("rooms", len(rooms)))

	for _, r := range rooms {
		leaveForOffline(appID, r.ChatroomId, uid)
	}
}

func leaveForOffline(appID string, roomID, uid int64) {
	ctx, cancel := reqctx.WithTimeout(0, appID, roomTaskTimeout)
	defer cancel()
	fields := []zap.Field{zap.Int64("roomID", roomID), zap.Int64("uid", uid)}

	if !lock.AcquireRoomProcess(ctx, appID, roomID) {
		ctx.Error("BatchOutChatroom get chatroom lock fail", fields...)
		return
	}
	defer lock.ReleaseRoomProcess(appID, roomID)

	room, err := GetChatroom(ctx, appID, roomID)
	if err != nil {
		ctx.Error("BatchOutChatroom GetChatroom err", append(fields, zap.Error(err))...)
		return
	}
	if errCode, err := room.BaseCheck(); err != nil {
		ctx.Error("BatchOutChatroom BaseCheck err", append(fields, zap.Error(err))...)
		return
	} else if errCode != servicepb.ErrorCode_SUCCESS {
		return
	}
	// The user may already have left through another instance.
	if errCode, err := room.IsUserInChatroom(uid); err != nil || errCode != servicepb.ErrorCode_SUCCESS {
		ctx.Debug("BatchOutChatroom user not in room", append(fields, zap.Error(err), zap.String("errCode", errCode.String()))...)
		return
	}
	errCode, info, err := room.GetChatroomMemberInfo(uid)
	if err != nil || errCode != servicepb.ErrorCode_SUCCESS {
		ctx.Error("BatchOutChatroom GetChatroomMemberInfo err", append(fields, zap.Error(err), zap.String("errCode", errCode.String()))...)
		return
	}
	if errCode, err := room.Leave(uid, servicepb.AtRoomInfo_OFF_LINE); err != nil {
		ctx.Error("BatchOutChatroom Leave err", append(fields, zap.Error(err))...)
		return
	} else if errCode != servicepb.ErrorCode_SUCCESS {
		ctx.Error("BatchOutChatroom Leave errCode", append(fields, zap.String("errCode", errCode.String()))...)
		return
	}

	space := MakeMicSpace(ctx, appID, roomID)
	if errCode, err := space.Init(); err != nil {
		ctx.Error("BatchOutChatroom space.Init err", append(fields, zap.Error(err))...)
		return
	} else if errCode != servicepb.ErrorCode_SUCCESS {
		ctx.Error("BatchOutChatroom space.Init errCode", append(fields, zap.String("errCode", errCode.String()))...)
		return
	}
	if _, err := space.DownAll(uid, servicepb.OnMicInfo_OFFLINE_EXPIRE); err != nil {
		ctx.Error("BatchOutChatroom DownAll err", append(fields, zap.Error(err))...)
		return
	}
	_, _ = broadcastEvent(ctx, evExit, appID, roomID, ctx.MsgID(), uid, 0, info, nil, nil)
	ctx.Info("BatchOutChatroom LeaveAll OK", fields...)
}

// QueryUserAtMicDuration pages the closed at-mic records of uid.
func QueryUserAtMicDuration(appID string, uid, lastEnter int64, sortType servicepb.UserMicTimeRequest_SortType, pageSize int32) (
	[]*servicepb.OnMicInfo, servicepb.ErrorCode, error) {
	records, err := queryUserAtMicRecord(appID, uid, lastEnter, int32(sortType), int(pageSize))
	if err != nil {
		return nil, servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	var out []*servicepb.OnMicInfo
	for _, r := range records {
		out = append(out, &servicepb.OnMicInfo{
			RoomId:    r.ChatroomId,
			UpMicId:   r.UpIndex,
			DownMicId: r.DownIndex,
			Reason:    servicepb.OnMicInfo_OffReason(r.DownReason),
			UpAt:      r.UpTime,
			DownAt:    r.DownTime,
		})
	}
	return out, servicepb.ErrorCode_SUCCESS, nil
}

// QueryUserAtRoomDuration pages the closed at-room records of uid.
func QueryUserAtRoomDuration(appID string, uid, lastEnter int64, sortType servicepb.UserAtRoomTimeRequest_SortType, pageSize int32) (
	[]*servicepb.AtRoomInfo, servicepb.ErrorCode, error) {
	records, err := queryUserAtRoomRecord(appID, uid, lastEnter, int32(sortType), int(pageSize))
	if err != nil {
		return nil, servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	var out []*servicepb.AtRoomInfo
	for _, r := range records {
		out = append(out, &servicepb.AtRoomInfo{
			RoomId:  r.ChatroomId,
			EnterAt: r.EnterTime,
			LeaveAt: r.LeaveTime,
			Reason:  servicepb.AtRoomInfo_LeaveReason(r.LeaveReason),
		})
	}
	return out, servicepb.ErrorCode_SUCCESS, nil
}
