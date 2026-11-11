package handler

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/yourname/roomkit/api/gen/clientpb"
	"github.com/yourname/roomkit/api/gen/servicepb"
)

const userLockWait = 10 * time.Second

func (s *Service) echo(_ context.Context, r *Request, body []byte) *clientpb.EchoResp {
	req := &clientpb.EchoReq{}
	if e := unmarshal(r, body, req); e != nil {
		return &clientpb.EchoResp{Ret: e}
	}
	return &clientpb.EchoResp{Ret: ok(), Data: req.Data}
}

func (s *Service) messageReceipt(_ context.Context, r *Request, body []byte) *clientpb.MessageReceiptForReceiveResp {
	req := &clientpb.MessageReceiptForReceiveReq{}
	if e := unmarshal(r, body, req); e != nil {
		return &clientpb.MessageReceiptForReceiveResp{Ret: e}
	}
	r.Log.Debug("message receipt", zap.String("message_id", req.MessageId))
	return &clientpb.MessageReceiptForReceiveResp{Ret: ok()}
}

func (s *Service) uploadLogReport(_ context.Context, r *Request, body []byte) *clientpb.UploadLogReportResp {
	req := &clientpb.UploadLogReportReq{}
	if e := unmarshal(r, body, req); e != nil {
		return &clientpb.UploadLogReportResp{Ret: e}
	}
	r.Log.Info("upload log report",
		zap.String("result", req.Result.String()),
		zap.String("desc", req.Desc),
		zap.String("device_id", req.DeviceId),
		zap.Int64("id", req.Id))
	return &clientpb.UploadLogReportResp{Ret: ok()}
}

func (s *Service) logout(ctx context.Context, r *Request, body []byte) *clientpb.LoginOutResp {
	req := &clientpb.LoginOutReq{}
	if e := unmarshal(r, body, req); e != nil {
		return &clientpb.LoginOutResp{Ret: e}
	}

	lockCtx, cancel := context.WithTimeout(ctx, userLockWait)
	defer cancel()
	if err := s.Store.LockUser(lockCtx, r.AppID, r.UID); err != nil {
		r.Log.Error("logout: lock user", zap.Error(err))
		return &clientpb.LoginOutResp{Ret: ret(clientpb.ErrorCode_MARS_AGENT_INTERNAL_ERROR)}
	}
	defer s.Store.UnlockUser(r.AppID, r.UID)

	cctx, ccancel := s.rpcCtx(ctx)
	rs, err := s.Core.DeleteClientLoginToken(cctx, &servicepb.DeleteClientLoginTokenReq{
		AppId:      r.AppID,
		Uid:        r.UID,
		LoginToken: r.LoginToken,
		MsgTraceId: r.TraceID,
	})
	ccancel()
	// A stale or already deleted token must not block leaving the rooms.
	_ = rpcRet(r, "DeleteClientLoginToken", err, rs.GetRet(), clientpb.ErrorCode_CORESERVER_INTER_ERROR)

	if e := s.leaveAllRooms(ctx, r); e != nil {
		return &clientpb.LoginOutResp{Ret: e}
	}
	return &clientpb.LoginOutResp{Ret: ok()}
}

func (s *Service) currentRooms(ctx context.Context, r *Request) ([]int64, *clientpb.UnifiedResponse) {
	cctx, cancel := s.rpcCtx(ctx)
	defer cancel()
	rs, err := s.Chatroom.GetUserCurrentChatroomList(cctx, &servicepb.GetUserCurrentChatroomListRequest{
		AppId:      r.AppID,
		Uid:        r.UID,
		MsgTraceId: r.TraceID,
	})
	if e := rpcRet(r, "GetUserCurrentChatroomList", err, rs.GetRet(), clientpb.ErrorCode_CHATROOM_SERVICE_ERROR); e != nil {
		return nil, e
	}
	rooms := make([]int64, 0, len(rs.RoomInfo))
	for _, info := range rs.RoomInfo {
		rooms = append(rooms, info.ChatroomId)
	}
	return rooms, nil
}

func (s *Service) leaveRooms(ctx context.Context, r *Request, rooms []int64) *clientpb.UnifiedResponse {
	if len(rooms) == 0 {
		return nil
	}
	cctx, cancel := s.rpcCtx(ctx)
	defer cancel()
	rs, err := s.Chatroom.BatchLeaveChatroom(cctx, &servicepb.BatchLeaveChatroomRequest{
		AppId:      r.AppID,
		Uid:        r.UID,
		RoomId:     rooms,
		MsgTraceId: r.TraceID,
	})
	return rpcRet(r, "BatchLeaveChatroom", err, rs.GetRet(), clientpb.ErrorCode_CHATROOM_SERVICE_ERROR)
}

func (s *Service) leaveAllRooms(ctx context.Context, r *Request) *clientpb.UnifiedResponse {
	rooms, e := s.currentRooms(ctx, r)
	if e != nil {
		return e
	}
	return s.leaveRooms(ctx, r, rooms)
}
