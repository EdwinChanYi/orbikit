package logic

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
	"github.com/EdwinChanYi/orbikit/internal/chatroom/rediskey"
	"github.com/EdwinChanYi/orbikit/internal/chatroom/reqctx"
)

// Mic operations carried in the "op" field of mic change broadcasts.
const (
	micOpUp     int32 = 1
	micOpDown   int32 = 2
	micOpSwitch int32 = 3
	micOpInvite int32 = 4
	micOpKick   int32 = 5
	micOpLock   int32 = 6
	micOpMute   int32 = 7
)

const (
	micFieldAppID  = "APP_ID"
	micFieldRoomID = "CHATROOM_ID"
)

// MicSpaceElementInfo is one mic seat. It is stored as JSON in the mic space
// hash, keyed by seat index, using the Go field names.
type MicSpaceElementInfo struct {
	Index   int32
	HasLock bool
	HasMute bool
	HasUser bool
	Uid     int64
	Ext     map[string]string
}

// MicSpaceInfo is the full mic layout of a room.
type MicSpaceInfo struct {
	AppId       string
	ChatroomId  int64
	ElementInfo map[int32]*MicSpaceElementInfo
}

// MicSpace manipulates the mic seats of a room.
type MicSpace struct {
	appId       string
	chatroomId  int64
	spaceInfo   *MicSpaceInfo
	userSeatMap map[int64]int32
	ctx         *reqctx.Context
}

// MakeMicSpace returns an uninitialized MicSpace; call Init to load it.
func MakeMicSpace(ctx *reqctx.Context, appID string, roomID int64) *MicSpace {
	return &MicSpace{
		appId:       appID,
		chatroomId:  roomID,
		userSeatMap: make(map[int64]int32),
		ctx:         ctx,
	}
}

// MicSpaceInfo returns the loaded layout, or nil before Init.
func (s *MicSpace) MicSpaceInfo() *MicSpaceInfo { return s.spaceInfo }

// decodeMicSpace parses the mic space hash, skipping the metadata fields.
func decodeMicSpace(data map[string]string) (map[int32]*MicSpaceElementInfo, error) {
	out := make(map[int32]*MicSpaceElementInfo, len(data))
	for k, v := range data {
		if k == micFieldAppID || k == micFieldRoomID {
			continue
		}
		index, err := strconv.ParseInt(k, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("mic space field %q: %w", k, err)
		}
		elem := &MicSpaceElementInfo{}
		if err := json.Unmarshal([]byte(v), elem); err != nil {
			return nil, fmt.Errorf("mic space field %q: %w", k, err)
		}
		out[int32(index)] = elem
	}
	return out, nil
}

func encodeMicSeats(fields map[string]any, seats map[int32]*MicSpaceElementInfo) error {
	for k, v := range seats {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		fields[strconv.FormatInt(int64(k), 10)] = string(b)
	}
	return nil
}

// Destroy removes every user from the mics and deletes the mic layout.
func (s *MicSpace) Destroy() error {
	countKey := rediskey.ChatroomUsedMicSpaceCount(s.appId, s.chatroomId)
	if err := rdb.Del(s.ctx, countKey).Err(); err != nil {
		s.ctx.Error("MicSpace del room mic space key failed", zap.String("key", countKey), zap.Error(err))
		return err
	}

	if s.spaceInfo != nil {
		for k, v := range s.spaceInfo.ElementInfo {
			if v == nil || !v.HasUser || v.Uid == 0 {
				continue
			}
			if err := micRecord(s.appId, s.chatroomId, v.Uid, k, servicepb.OnMicInfo_KICKED_ROOM); err != nil {
				s.ctx.Error("Destroy MicRecord", zap.Error(err), zap.Int64("roomId", s.chatroomId),
					zap.Int64("uid", v.Uid), zap.Int32("micIndex", k))
			}
			s.dropMicToken(v.Uid)
			if _, err := broadcastEvent(s.ctx, evMicChange, s.appId, s.chatroomId, s.ctx.MsgID(), v.Uid, 0, nil, nil,
				s.GetMicInfo(micOpDown, k, 0)); err != nil {
				s.ctx.Error("Destroy CreateMicroChangeBcstMsg error", zap.Error(err))
			}
		}
	}

	if err := rdb.Del(s.ctx, rediskey.MicSpaceInfo(s.appId, s.chatroomId)).Err(); err != nil {
		s.ctx.Error("MicSpace destroy error", zap.Error(err))
		return err
	}
	return nil
}

// dropMicToken invalidates the media token of a user leaving the mics.
func (s *MicSpace) dropMicToken(uid int64) {
	if err := rdb.Del(s.ctx, rediskey.UserMicToken(s.appId, s.chatroomId, uid)).Err(); err != nil {
		s.ctx.Error("delete mic token error",
			zap.Int64("roomID", s.chatroomId), zap.Int64("uid", uid), zap.Error(err))
	}
}

// CreateOrUpdate replaces the mic layout, keeping users already seated on
// indices that still exist. isLess reports whether the seat count shrank.
func (s *MicSpace) CreateOrUpdate(si *MicSpaceInfo) (isLess bool, errCode servicepb.ErrorCode, err error) {
	key := rediskey.MicSpaceInfo(s.appId, s.chatroomId)
	logErr := func(msg string, err error) {
		s.ctx.Error(msg, zap.String("appid", s.appId), zap.Int64("roomid", s.chatroomId), zap.Error(err))
	}

	n, err := rdb.Exists(s.ctx, key).Result()
	if err != nil {
		logErr("Exists err", err)
		return false, servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}

	fields := map[string]any{micFieldAppID: si.AppId, micFieldRoomID: si.ChatroomId}
	if n == 1 {
		oldData, err := rdb.HGetAll(s.ctx, key).Result()
		if err != nil {
			logErr("HGetAll err", err)
			return false, servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
		}
		oldSeats, err := decodeMicSpace(oldData)
		if err != nil {
			logErr("decode mic space err", err)
			return false, servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
		}

		s.userSeatMap = make(map[int64]int32)
		for k, v := range si.ElementInfo {
			if old, ok := oldSeats[k]; ok {
				v.Uid = old.Uid
				v.HasUser = old.HasUser
				if v.Uid != 0 {
					s.userSeatMap[v.Uid] = k
				}
			}
		}
		newSeats := make(map[int32]*MicSpaceElementInfo, len(si.ElementInfo))
		for _, v := range si.ElementInfo {
			newSeats[v.Index] = v
		}
		if err := encodeMicSeats(fields, newSeats); err != nil {
			logErr("Marshal err", err)
			return false, servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
		}

		pipe := rdb.TxPipeline()
		pipe.Del(s.ctx, key)
		pipe.HSet(s.ctx, key, fields)
		if _, err := pipe.Exec(s.ctx); err != nil {
			logErr("Exec err", err)
			return false, servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
		}
		isLess = len(si.ElementInfo) < len(oldSeats)
	} else {
		newSeats := make(map[int32]*MicSpaceElementInfo, len(si.ElementInfo))
		for _, v := range si.ElementInfo {
			newSeats[v.Index] = v
		}
		if err := encodeMicSeats(fields, newSeats); err != nil {
			logErr("Marshal err", err)
			return false, servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
		}
		if err := rdb.HSet(s.ctx, key, fields).Err(); err != nil {
			logErr("HMSet err", err)
			return false, servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
		}
	}

	if err := rdb.Set(s.ctx, rediskey.ChatroomUsedMicSpaceCount(s.appId, s.chatroomId), len(s.userSeatMap), 0).Err(); err != nil {
		logErr("Set err", err)
		return isLess, servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	_ = updateMemmicZ(s.ctx, s.appId, s.chatroomId)
	_ = updateMicmemZ(s.ctx, s.appId, s.chatroomId)
	return isLess, servicepb.ErrorCode_SUCCESS, nil
}

// applyMicExt merges ext updates into existing seats. An empty ext clears the
// seat ext; an empty value deletes that key.
func applyMicExt(seats map[int32]*MicSpaceElementInfo, updates []*servicepb.SpaceInfosExt) bool {
	for _, u := range updates {
		old, ok := seats[u.Index]
		if !ok {
			return false
		}
		if len(u.Ext) == 0 {
			old.Ext = nil
			continue
		}
		if old.Ext == nil {
			old.Ext = make(map[string]string)
		}
		for k, v := range u.Ext {
			if v == "" {
				delete(old.Ext, k)
			} else {
				old.Ext[k] = v
			}
		}
	}
	return true
}

// CreateOrUpdateExt updates the ext of existing seats.
func (s *MicSpace) CreateOrUpdateExt(si *servicepb.UpdateMicSpaceExtRequest) (servicepb.ErrorCode, error) {
	key := rediskey.MicSpaceInfo(s.appId, s.chatroomId)
	logErr := func(msg string, err error) {
		s.ctx.Error(msg, zap.String("appid", s.appId), zap.Int64("roomid", s.chatroomId), zap.Error(err))
	}

	n, err := rdb.Exists(s.ctx, key).Result()
	if err != nil {
		logErr("Exists err", err)
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if n != 1 {
		return servicepb.ErrorCode_CHATROOM_MIC_INDEX_NOT_EXISTS, nil
	}

	oldData, err := rdb.HGetAll(s.ctx, key).Result()
	if err != nil {
		logErr("HGetAll err", err)
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	seats, err := decodeMicSpace(oldData)
	if err != nil {
		logErr("decode mic space err", err)
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}

	s.userSeatMap = seatsByUser(seats)
	if !applyMicExt(seats, si.SpacesExt) {
		return servicepb.ErrorCode_CHATROOM_MIC_INDEX_NOT_EXISTS, nil
	}

	fields := map[string]any{micFieldAppID: si.AppId, micFieldRoomID: si.RoomId}
	if err := encodeMicSeats(fields, seats); err != nil {
		logErr("Marshal err", err)
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	pipe := rdb.TxPipeline()
	pipe.Del(s.ctx, key)
	pipe.HSet(s.ctx, key, fields)
	if _, err := pipe.Exec(s.ctx); err != nil {
		logErr("Exec err", err)
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}

	if err := rdb.Set(s.ctx, rediskey.ChatroomUsedMicSpaceCount(s.appId, s.chatroomId), len(s.userSeatMap), 0).Err(); err != nil {
		logErr("Set err", err)
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	return servicepb.ErrorCode_SUCCESS, nil
}

// Init loads the mic layout from Redis.
func (s *MicSpace) Init() (servicepb.ErrorCode, error) {
	key := rediskey.MicSpaceInfo(s.appId, s.chatroomId)
	data, err := rdb.HGetAll(s.ctx, key).Result()
	if err != nil {
		s.ctx.Error("MicSpace Init Error", zap.Error(err))
		return servicepb.ErrorCode_CHATROOM_MIC_SPACE_NOT_EXISTS, err
	}
	if len(data) == 0 {
		s.ctx.Warn("MicSpace Init Error empty")
		return servicepb.ErrorCode_CHATROOM_MIC_SPACE_NOT_EXISTS, nil
	}
	appID, ok := data[micFieldAppID]
	if !ok {
		s.ctx.Error("MicSpace Init Get MicSpace Info", zap.Any("data", data))
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, fmt.Errorf("space init get wrong info key : %s", key)
	}
	info := &MicSpaceInfo{AppId: appID}
	if v, ok := data[micFieldRoomID]; ok {
		if info.ChatroomId, err = strconv.ParseInt(v, 10, 64); err != nil {
			return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
		}
	}
	if info.ElementInfo, err = decodeMicSpace(data); err != nil {
		s.ctx.Error("MicSpace Init decode Error", zap.Error(err))
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	s.userSeatMap = seatsByUser(info.ElementInfo)
	s.spaceInfo = info
	return servicepb.ErrorCode_SUCCESS, nil
}

func seatsByUser(seats map[int32]*MicSpaceElementInfo) map[int64]int32 {
	m := make(map[int64]int32)
	for index, elem := range seats {
		if elem.Uid != 0 {
			m[elem.Uid] = index
		}
	}
	return m
}

// GetUserSeatIndex returns the seat of uid, if seated.
func (s *MicSpace) GetUserSeatIndex(uid int64) (bool, int32) {
	index, ok := s.userSeatMap[uid]
	return ok, index
}

// CheckUserPermission checks a mic privilege; from == 0 is a system call.
func (s *MicSpace) CheckUserPermission(from, to int64, p servicepb.Privilege, noPermission servicepb.ErrorCode) (servicepb.ErrorCode, error) {
	if from == 0 {
		return servicepb.ErrorCode_SUCCESS, nil
	}
	return checkUserPermission(s.ctx, s.appId, s.chatroomId, from, to, p, noPermission)
}

// SyncMicSpace writes the given seats (all seats when none are given) and
// refreshes the used mic count and rank entries.
func (s *MicSpace) SyncMicSpace(index ...int32) (servicepb.ErrorCode, error) {
	seats := s.spaceInfo.ElementInfo
	if len(index) > 0 {
		seats = make(map[int32]*MicSpaceElementInfo, len(index))
		for _, i := range index {
			if v, ok := s.spaceInfo.ElementInfo[i]; ok {
				seats[i] = v
			}
		}
	}
	fields := make(map[string]any, len(seats))
	if err := encodeMicSeats(fields, seats); err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}

	if err := rdb.Set(s.ctx, rediskey.ChatroomUsedMicSpaceCount(s.appId, s.chatroomId), len(s.userSeatMap), 0).Err(); err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	_ = updateMemmicZ(s.ctx, s.appId, s.chatroomId)
	_ = updateMicmemZ(s.ctx, s.appId, s.chatroomId)

	if len(fields) > 0 {
		if err := rdb.HSet(s.ctx, rediskey.MicSpaceInfo(s.appId, s.chatroomId), fields).Err(); err != nil {
			return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
		}
	}
	return servicepb.ErrorCode_SUCCESS, nil
}

func int32Bytes(v int32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, uint32(v))
	return b
}

// GetMicInfo builds the event content of a mic broadcast: the full sorted
// mic list plus the operation and the old/new positions as big-endian int32.
func (s *MicSpace) GetMicInfo(op, oldPos, newPos int32) map[string][]byte {
	chatroom, err := GetChatroom(s.ctx, s.appId, s.chatroomId)
	if err != nil {
		s.ctx.Error("GetMicInfo get chatroom err",
			zap.String("appID", s.appId), zap.Int64("roomID", s.chatroomId), zap.Error(err))
		return nil
	}

	seatUsers := make(map[int32]int64, len(s.spaceInfo.ElementInfo))
	mics := make([]*servicepb.MicInfo, 0, len(s.spaceInfo.ElementInfo))
	for _, v := range s.spaceInfo.ElementInfo {
		elem := &servicepb.MicInfo{
			Position: v.Index,
			IsLocked: v.HasLock,
			IsMuted:  v.HasMute,
			Ext:      v.Ext,
		}
		if v.HasUser {
			errCode, member, err := chatroom.GetChatRoomMemberInfoMore(v.Uid)
			if err != nil || errCode != servicepb.ErrorCode_SUCCESS {
				s.ctx.Error("GetMicInfo GetChatRoomMemberInfoMore err",
					zap.String("appID", s.appId), zap.Int64("roomID", s.chatroomId),
					zap.Int("errCode", int(errCode)), zap.Error(err))
			}
			elem.MemberInfo = member
			seatUsers[v.Index] = v.Uid
		} else {
			seatUsers[v.Index] = 0
		}
		mics = append(mics, elem)
	}
	sort.SliceStable(mics, func(i, j int) bool { return mics[i].Position < mics[j].Position })

	content, err := proto.Marshal(&servicepb.QueryMicInfoReply{MicInfo: mics})
	if err != nil {
		return nil
	}

	s.ctx.Info("GetMicInfo",
		zap.String("appID", s.appId), zap.Int64("roomID", s.chatroomId),
		zap.Int32("op", op), zap.Int32("old", oldPos), zap.Int32("new", newPos),
		zap.Any("micInfo", seatUsers))

	return map[string][]byte{
		"data":   content,
		"op":     int32Bytes(op),
		"oldPos": int32Bytes(oldPos),
		"newPos": int32Bytes(newPos),
	}
}

// Up seats uid on index, moving them from their current seat if any.
func (s *MicSpace) Up(uid int64, index int32) (servicepb.ErrorCode, error) {
	if s.spaceInfo == nil {
		return servicepb.ErrorCode_CHATROOM_MIC_SPACE_NOT_EXISTS, nil
	}
	elem, ok := s.spaceInfo.ElementInfo[index]
	if !ok {
		return servicepb.ErrorCode_CHATROOM_MIC_INDEX_NOT_EXISTS, nil
	}
	if elem.HasUser {
		return servicepb.ErrorCode_CHATROOM_UP_MIC_ERROR_MIC_HAS_USER, nil
	}
	if elem.HasLock {
		errCode, err := s.CheckUserPermission(uid, 0, servicepb.Privilege_EDIT_MICRO,
			servicepb.ErrorCode_CHATROOM_UP_MIC_ERROR_MIC_HAS_BEEN_LOCKED)
		if err != nil {
			return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
		}
		if errCode != servicepb.ErrorCode_SUCCESS {
			return errCode, nil
		}
	}

	elem.Uid = uid
	elem.HasUser = true
	indices := []int32{index}
	op, oldPos := micOpUp, int32(0)
	if oldIndex, ok := s.userSeatMap[uid]; ok {
		if oldElem, ok := s.spaceInfo.ElementInfo[oldIndex]; ok {
			oldElem.Uid = 0
			oldElem.HasUser = false
			op = micOpSwitch
			oldPos = oldIndex
			indices = append(indices, oldIndex)
		}
	}

	if err := micRecord(s.appId, s.chatroomId, uid, index, servicepb.OnMicInfo_MIC_CHANGED); err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	s.userSeatMap[uid] = index

	if errCode, err := s.SyncMicSpace(indices...); err != nil || errCode != servicepb.ErrorCode_SUCCESS {
		return errCode, err
	}
	if _, err := broadcastEvent(s.ctx, evMicChange, s.appId, s.chatroomId, s.ctx.MsgID(), uid, 0, nil, nil,
		s.GetMicInfo(op, oldPos, index)); err != nil {
		s.ctx.Error("UpMic CreateMicroChangeBcstMsg error", zap.Error(err))
	}
	return servicepb.ErrorCode_SUCCESS, nil
}

// DownAll removes uid from every seat they occupy.
func (s *MicSpace) DownAll(uid int64, reason servicepb.OnMicInfo_OffReason) (servicepb.ErrorCode, error) {
	if s.spaceInfo == nil {
		return servicepb.ErrorCode_CHATROOM_MIC_SPACE_NOT_EXISTS, nil
	}

	var index int32
	var indices []int32
	for i, v := range s.spaceInfo.ElementInfo {
		if v.Uid != uid {
			continue
		}
		v.Uid = 0
		v.HasUser = false
		delete(s.userSeatMap, uid)
		index = i
		indices = append(indices, i)

		if err := micRecord(s.appId, s.chatroomId, uid, i, reason); err != nil {
			s.ctx.Error("DownAll MicRecord err",
				zap.String("appID", s.appId), zap.Int64("chatroomID", s.chatroomId),
				zap.Int64("uid", uid), zap.Int32("index", i), zap.Any("reason", reason), zap.Error(err))
			return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
		}
	}
	if len(indices) == 0 {
		return servicepb.ErrorCode_SUCCESS, nil
	}
	if len(indices) > 1 {
		s.ctx.Error("DownAll user down many times",
			zap.String("appID", s.appId), zap.Int64("chatroomID", s.chatroomId),
			zap.Int64("uid", uid), zap.Int32s("indices", indices))
	}

	if errCode, err := s.SyncMicSpace(indices...); err != nil || errCode != servicepb.ErrorCode_SUCCESS {
		return errCode, err
	}
	s.dropMicToken(uid)
	if _, err := broadcastEvent(s.ctx, evMicChange, s.appId, s.chatroomId, s.ctx.MsgID(), uid, 0, nil, nil,
		s.GetMicInfo(micOpDown, index, 0)); err != nil {
		s.ctx.Error("DownAll CreateMicroChangeBcstMsg err",
			zap.String("appID", s.appId), zap.Int64("chatroomID", s.chatroomId),
			zap.Int64("uid", uid), zap.Error(err))
	}
	return servicepb.ErrorCode_SUCCESS, nil
}

// Down removes uid from index.
func (s *MicSpace) Down(uid int64, index int32, reason servicepb.OnMicInfo_OffReason) (servicepb.ErrorCode, error) {
	if s.spaceInfo == nil {
		return servicepb.ErrorCode_CHATROOM_MIC_SPACE_NOT_EXISTS, nil
	}
	elem, ok := s.spaceInfo.ElementInfo[index]
	if !ok {
		return servicepb.ErrorCode_CHATROOM_MIC_INDEX_NOT_EXISTS, nil
	}
	if !elem.HasUser {
		return servicepb.ErrorCode_CHATROOM_DOWN_MIC_ERROR_MIC_HAS_NOT_USER, nil
	}
	if elem.Uid != uid {
		return servicepb.ErrorCode_CHATROOM_DOWN_MIC_ERROR_USER_NOT_IN_TARGET_INDEX, nil
	}

	elem.Uid = 0
	elem.HasUser = false
	delete(s.userSeatMap, uid)

	if err := micRecord(s.appId, s.chatroomId, uid, index, reason); err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if errCode, err := s.SyncMicSpace(index); err != nil || errCode != servicepb.ErrorCode_SUCCESS {
		return errCode, err
	}
	s.dropMicToken(uid)
	if _, err := broadcastEvent(s.ctx, evMicChange, s.appId, s.chatroomId, s.ctx.MsgID(), uid, 0, nil, nil,
		s.GetMicInfo(micOpDown, index, 0)); err != nil {
		s.ctx.Error("DownMic CreateMicroChangeBcstMsg error", zap.Error(err))
	}
	return servicepb.ErrorCode_SUCCESS, nil
}

// Kick removes to from index on behalf of from.
func (s *MicSpace) Kick(from, to int64, index int32) (servicepb.ErrorCode, error) {
	if s.spaceInfo == nil {
		return servicepb.ErrorCode_CHATROOM_MIC_SPACE_NOT_EXISTS, nil
	}
	elem, ok := s.spaceInfo.ElementInfo[index]
	if !ok {
		return servicepb.ErrorCode_CHATROOM_MIC_INDEX_NOT_EXISTS, nil
	}
	if !elem.HasUser || elem.Uid != to {
		return servicepb.ErrorCode_CHATROOM_DOWN_MIC_ERROR_USER_NOT_IN_TARGET_INDEX, nil
	}
	errCode, err := s.CheckUserPermission(from, to, servicepb.Privilege_EDIT_MICRO, servicepb.ErrorCode_CHATROOM_USER_PERMISSION_ERROR)
	if err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if errCode != servicepb.ErrorCode_SUCCESS {
		return errCode, nil
	}

	elem.Uid = 0
	elem.HasUser = false
	delete(s.userSeatMap, to)

	if err := micRecord(s.appId, s.chatroomId, to, index, servicepb.OnMicInfo_KICKED_MIC); err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if errCode, err := s.SyncMicSpace(index); err != nil || errCode != servicepb.ErrorCode_SUCCESS {
		return errCode, err
	}
	s.dropMicToken(to)
	if _, err := broadcastEvent(s.ctx, evMicChange, s.appId, s.chatroomId, s.ctx.MsgID(), from, to, nil, nil,
		s.GetMicInfo(micOpKick, index, 0)); err != nil {
		s.ctx.Error("KickMic CreateMicroChangeBcstMsg error", zap.Error(err))
	}
	return servicepb.ErrorCode_SUCCESS, nil
}

// Invite seats to on index on behalf of from.
func (s *MicSpace) Invite(from, to int64, index int32) (servicepb.ErrorCode, error) {
	if s.spaceInfo == nil {
		return servicepb.ErrorCode_CHATROOM_MIC_SPACE_NOT_EXISTS, nil
	}
	elem, ok := s.spaceInfo.ElementInfo[index]
	if !ok {
		return servicepb.ErrorCode_CHATROOM_MIC_INDEX_NOT_EXISTS, nil
	}
	if elem.HasUser {
		return servicepb.ErrorCode_CHATROOM_UP_MIC_ERROR_MIC_HAS_USER, nil
	}
	errCode, err := s.CheckUserPermission(from, to, servicepb.Privilege_EDIT_MICRO, servicepb.ErrorCode_CHATROOM_USER_PERMISSION_ERROR)
	if err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if errCode != servicepb.ErrorCode_SUCCESS {
		return errCode, nil
	}

	indices := []int32{index}
	var oldPos int32
	if oldIndex, ok := s.userSeatMap[to]; ok {
		if oldElem, ok := s.spaceInfo.ElementInfo[oldIndex]; ok {
			oldElem.Uid = 0
			oldElem.HasUser = false
			oldPos = oldIndex
			indices = append(indices, oldIndex)
			if err := micRecord(s.appId, s.chatroomId, to, oldIndex, servicepb.OnMicInfo_MIC_CHANGED); err != nil {
				return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
			}
		}
	}

	elem.Uid = to
	elem.HasUser = true
	s.userSeatMap[to] = index

	if err := micRecord(s.appId, s.chatroomId, to, index, servicepb.OnMicInfo_MIC_CHANGED); err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if errCode, err := s.SyncMicSpace(indices...); err != nil || errCode != servicepb.ErrorCode_SUCCESS {
		return errCode, err
	}
	if _, err := broadcastEvent(s.ctx, evMicChange, s.appId, s.chatroomId, s.ctx.MsgID(), from, to, nil, nil,
		s.GetMicInfo(micOpInvite, oldPos, index)); err != nil {
		s.ctx.Error("InviteMic CreateMicroChangeBcstMsg error", zap.Error(err))
	}
	return servicepb.ErrorCode_SUCCESS, nil
}

// Block locks (action > 0) or unlocks a seat.
func (s *MicSpace) Block(uid int64, index int32, action int32) (servicepb.ErrorCode, error) {
	if s.spaceInfo == nil {
		return servicepb.ErrorCode_CHATROOM_MIC_SPACE_NOT_EXISTS, nil
	}
	elem, ok := s.spaceInfo.ElementInfo[index]
	if !ok {
		return servicepb.ErrorCode_CHATROOM_MIC_INDEX_NOT_EXISTS, nil
	}
	errCode, err := s.CheckUserPermission(uid, 0, servicepb.Privilege_EDIT_MICRO_STATUS, servicepb.ErrorCode_CHATROOM_USER_PERMISSION_ERROR)
	if err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if errCode != servicepb.ErrorCode_SUCCESS {
		return errCode, nil
	}

	elem.HasLock = action > 0
	if errCode, err := s.SyncMicSpace(index); err != nil || errCode != servicepb.ErrorCode_SUCCESS {
		return errCode, err
	}
	if _, err := broadcastEvent(s.ctx, evMicStatus, s.appId, s.chatroomId, s.ctx.MsgID(), uid, 0, nil, nil,
		s.GetMicInfo(micOpLock, index, 0)); err != nil {
		s.ctx.Error("BlockMic CreateMicroStatusChangeBcstMsg error", zap.Error(err))
	}
	return servicepb.ErrorCode_SUCCESS, nil
}

// Mute mutes (action > 0) or unmutes a seat.
func (s *MicSpace) Mute(uid int64, index int32, action int32) (servicepb.ErrorCode, error) {
	if s.spaceInfo == nil {
		return servicepb.ErrorCode_CHATROOM_MIC_SPACE_NOT_EXISTS, nil
	}
	elem, ok := s.spaceInfo.ElementInfo[index]
	if !ok {
		return servicepb.ErrorCode_CHATROOM_MIC_INDEX_NOT_EXISTS, nil
	}
	var target int64
	if elem.HasUser {
		target = elem.Uid
	}
	errCode, err := s.CheckUserPermission(uid, target, servicepb.Privilege_EDIT_MICRO_STATUS, servicepb.ErrorCode_CHATROOM_USER_PERMISSION_ERROR)
	if err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if errCode != servicepb.ErrorCode_SUCCESS {
		return errCode, nil
	}

	elem.HasMute = action > 0
	if errCode, err := s.SyncMicSpace(index); err != nil || errCode != servicepb.ErrorCode_SUCCESS {
		return errCode, err
	}
	if _, err := broadcastEvent(s.ctx, evMicStatus, s.appId, s.chatroomId, s.ctx.MsgID(), uid, 0, nil, nil,
		s.GetMicInfo(micOpMute, index, 0)); err != nil {
		s.ctx.Error("MuteMic CreateMicroStatusChangeBcstMsg error", zap.Error(err))
	}
	return servicepb.ErrorCode_SUCCESS, nil
}

// UpdateMicToken stores the media token of a seated user.
func UpdateMicToken(ctx *reqctx.Context, appID, token string, roomID, uid int64) (servicepb.ErrorCode, error) {
	if err := rdb.Set(ctx, rediskey.UserMicToken(appID, roomID, uid), token, 0).Err(); err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	return servicepb.ErrorCode_SUCCESS, nil
}
