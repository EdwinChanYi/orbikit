// Package server implements the Chatroom gRPC service on top of the logic
// package.
package server

import (
	"context"
	"strconv"

	"go.uber.org/zap"

	"github.com/yourname/roomkit/api/gen/servicepb"
	"github.com/yourname/roomkit/internal/chatroom/logic"
	"github.com/yourname/roomkit/internal/chatroom/reqctx"
)

// Server implements servicepb.ChatroomServer.
type Server struct {
	servicepb.UnimplementedChatroomServer
}

// New returns the Chatroom service.
func New() *Server { return &Server{} }

// newCtx builds the request context. Handlers run multi-step updates that
// must not stop halfway when the caller goes away, so cancellation of the
// incoming context is not propagated.
func newCtx(goCtx context.Context, traceID, appID string) *reqctx.Context {
	msgID, _ := strconv.ParseUint(traceID, 10, 64)
	return reqctx.New(context.WithoutCancel(goCtx), msgID, appID)
}

func ret(code servicepb.ErrorCode) *servicepb.UnifiedResponse {
	return &servicepb.UnifiedResponse{ErrorCode: code, ErrorMsg: servicepb.ErrorCode_name[int32(code)]}
}

func success() *servicepb.UnifiedResponse { return ret(servicepb.ErrorCode_SUCCESS) }

func serviceErr() *servicepb.UnifiedResponse { return ret(servicepb.ErrorCode_CHATROOM_SERVICE_ERROR) }

// paramErr is also the reply after a recovered panic; its message is the
// service error name, as clients have always received.
func paramErr() *servicepb.UnifiedResponse {
	return &servicepb.UnifiedResponse{
		ErrorCode: servicepb.ErrorCode_PARAM_ERROR,
		ErrorMsg:  servicepb.ErrorCode_name[int32(servicepb.ErrorCode_CHATROOM_SERVICE_ERROR)],
	}
}

// recoverTo turns a panic into a PARAM_ERROR reply. It must be deferred
// directly so recover can see the panic.
func recoverTo(ctx *reqctx.Context, name string, r **servicepb.UnifiedResponse) {
	if e := recover(); e != nil {
		ctx.Error(name+" unknown error", zap.Any("error", e), zap.Stack("stack"))
		*r = paramErr()
	}
}

// checkApp reports whether appID is a valid app. An invalid app sets the
// reply code; a lookup failure is returned as the gRPC error.
func checkApp(ctx *reqctx.Context, name, appID string, r **servicepb.UnifiedResponse) (bool, error) {
	ok, err := logic.IsAppValid(ctx, appID)
	if err != nil {
		ctx.Error(name+" IsAppValid err", zap.Error(err))
		return false, err
	}
	if !ok {
		*r = ret(servicepb.ErrorCode_CORE_APP_ID_NOT_EXISTS)
	}
	return ok, nil
}

// openRoom loads a room that passes the base check (exists and is open).
// It returns a nil room when the handler must stop.
func openRoom(ctx *reqctx.Context, name, appID string, roomID int64, r **servicepb.UnifiedResponse) (*logic.ChatroomContainer, error) {
	room, err := logic.GetChatroom(ctx, appID, roomID)
	if err != nil {
		ctx.Error(name+" GetChatroom err", zap.Error(err))
		return nil, err
	}
	code, err := room.BaseCheck()
	if err != nil {
		ctx.Error(name+" BaseCheck err", zap.Error(err))
		return nil, err
	}
	if code != servicepb.ErrorCode_SUCCESS {
		*r = ret(code)
		return nil, nil
	}
	return room, nil
}

// failed reports whether a logic step failed. Errors are logged and left
// for the caller to return; other non-success codes become the reply code.
func failed(ctx *reqctx.Context, step string, code servicepb.ErrorCode, err error, r **servicepb.UnifiedResponse) bool {
	if err != nil {
		ctx.Error(step+" err", zap.Error(err))
		return true
	}
	if code != servicepb.ErrorCode_SUCCESS {
		*r = ret(code)
		return true
	}
	return false
}

func seatsFromSpace(spaces []*servicepb.SpaceInfos) map[int32]*logic.MicSpaceElementInfo {
	seats := make(map[int32]*logic.MicSpaceElementInfo, len(spaces))
	for _, s := range spaces {
		seats[s.GetIndex()] = &logic.MicSpaceElementInfo{
			Index:   s.GetIndex(),
			HasLock: s.GetIsLocked(),
			HasMute: s.GetIsMute(),
			HasUser: s.GetIsUser(),
			Uid:     s.GetUid(),
			Ext:     s.GetExt(),
		}
	}
	return seats
}

// micInfos converts the loaded mic layout to the reply form, attaching the
// member on each occupied seat.
func micInfos(room *logic.ChatroomContainer, space *logic.MicSpace) ([]*servicepb.MicInfo, servicepb.ErrorCode, error) {
	seats := space.MicSpaceInfo().ElementInfo
	if len(seats) == 0 {
		return nil, servicepb.ErrorCode_SUCCESS, nil
	}
	out := make([]*servicepb.MicInfo, 0, len(seats))
	for _, v := range seats {
		elem := &servicepb.MicInfo{
			Position: v.Index,
			IsLocked: v.HasLock,
			IsMuted:  v.HasMute,
			Ext:      v.Ext,
		}
		if v.HasUser {
			code, info, err := room.GetChatRoomMemberInfoMore(v.Uid)
			if err != nil || code != servicepb.ErrorCode_SUCCESS {
				return nil, code, err
			}
			elem.MemberInfo = info
		}
		out = append(out, elem)
	}
	return out, servicepb.ErrorCode_SUCCESS, nil
}

// memberReply fills a member list reply with mute and kick periods.
func memberReply(reply *servicepb.FetchChatroomMemberReply, users []logic.MemberDetail) {
	reply.MutedTime = make(map[int64]*servicepb.MemberOpTime, len(users))
	reply.KickedTime = make(map[int64]*servicepb.MemberOpTime, len(users))
	for i := range users {
		u := &users[i]
		reply.Members = append(reply.Members, u.Proto())
		reply.MutedTime[u.Uid] = &servicepb.MemberOpTime{
			BeginTime: u.MutedBegin.UnixNano(),
			EndTime:   u.MutedEnd.UnixNano(),
		}
		reply.KickedTime[u.Uid] = &servicepb.MemberOpTime{
			BeginTime: u.KickedBegin.UnixNano(),
			EndTime:   u.KickedEnd.UnixNano(),
		}
	}
}
