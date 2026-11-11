package handler

import (
	"context"

	"github.com/yourname/roomkit/api/gen/clientpb"
	"github.com/yourname/roomkit/api/gen/servicepb"
	"github.com/yourname/roomkit/internal/gateway/convert"
)

func (s *Service) enterChatroom(ctx context.Context, r *Request, body []byte) *clientpb.EnterChatroomResp {
	req := &clientpb.EnterChatroomReq{}
	if e := unmarshal(r, body, req); e != nil {
		return &clientpb.EnterChatroomResp{Ret: e}
	}
	cctx, cancel := s.rpcCtx(ctx)
	defer cancel()
	rs, err := s.Chatroom.EnterChatroom(cctx, &servicepb.EnterChatroomRequest{
		RoomId:     req.RoomId,
		Token:      req.Token,
		Uid:        r.UID,
		AppId:      r.AppID,
		MsgTraceId: r.TraceID,
	})
	if e := rpcRet(r, "EnterChatroom", err, rs.GetRet(), clientpb.ErrorCode_MARS_AGENT_INTERNAL_ERROR); e != nil {
		return &clientpb.EnterChatroomResp{Ret: e}
	}
	return &clientpb.EnterChatroomResp{
		Ret:      ok(),
		Chatroom: convert.Chatroom(rs.RoomInfo),
		Me:       convert.MemberBaseInfo(rs.YourselfInfo, false),
	}
}

func (s *Service) exitChatroom(ctx context.Context, r *Request, body []byte) *clientpb.ExitChatroomResp {
	req := &clientpb.ExitChatroomReq{}
	if e := unmarshal(r, body, req); e != nil {
		return &clientpb.ExitChatroomResp{Ret: e}
	}
	cctx, cancel := s.rpcCtx(ctx)
	defer cancel()
	rs, err := s.Chatroom.LeaveChatroom(cctx, &servicepb.LeaveChatroomRequest{
		RoomId:     req.RoomId,
		Uid:        r.UID,
		AppId:      r.AppID,
		MsgTraceId: r.TraceID,
	})
	if e := rpcRet(r, "LeaveChatroom", err, rs.GetRet(), clientpb.ErrorCode_CHATROOM_SERVICE_ERROR); e != nil {
		return &clientpb.ExitChatroomResp{Ret: e}
	}
	return &clientpb.ExitChatroomResp{Ret: ok()}
}

func (s *Service) batchExitChatroom(ctx context.Context, r *Request, body []byte) *clientpb.BatchExitChatroomResp {
	req := &clientpb.BatchExitChatroomReq{}
	if e := unmarshal(r, body, req); e != nil {
		return &clientpb.BatchExitChatroomResp{Ret: e}
	}
	cctx, cancel := s.rpcCtx(ctx)
	defer cancel()
	rs, err := s.Chatroom.BatchLeaveChatroom(cctx, &servicepb.BatchLeaveChatroomRequest{
		RoomId:     req.RoomId,
		Uid:        r.UID,
		AppId:      r.AppID,
		MsgTraceId: r.TraceID,
	})
	if e := rpcRet(r, "BatchLeaveChatroom", err, rs.GetRet(), clientpb.ErrorCode_CHATROOM_SERVICE_ERROR); e != nil {
		return &clientpb.BatchExitChatroomResp{Ret: e}
	}
	return &clientpb.BatchExitChatroomResp{Ret: ok()}
}

func (s *Service) fetchChatroomInfo(ctx context.Context, r *Request, body []byte) *clientpb.FetchChatroomInfoResp {
	req := &clientpb.FetchChatroomInfoReq{}
	if e := unmarshal(r, body, req); e != nil {
		return &clientpb.FetchChatroomInfoResp{Ret: e}
	}
	cctx, cancel := s.rpcCtx(ctx)
	defer cancel()
	rs, err := s.Chatroom.GetChatroomInfo(cctx, &servicepb.GetChatroomInfoRequest{
		RoomId:     req.RoomId,
		AppId:      r.AppID,
		MsgTraceId: r.TraceID,
	})
	if e := rpcRet(r, "GetChatroomInfo", err, rs.GetRet(), clientpb.ErrorCode_CHATROOM_SERVICE_ERROR); e != nil {
		return &clientpb.FetchChatroomInfoResp{Ret: e}
	}
	return &clientpb.FetchChatroomInfoResp{Ret: ok(), Chatroom: convert.Chatroom(rs.RoomInfo)}
}

func (s *Service) updateChatroom(ctx context.Context, r *Request, body []byte) *clientpb.ChatroomUpdateResp {
	req := &clientpb.ChatroomUpdateReq{}
	if e := unmarshal(r, body, req); e != nil {
		return &clientpb.ChatroomUpdateResp{Ret: e}
	}
	in := &servicepb.EditChatroomInfoByMemberRequest{
		RoomId:     req.RoomId,
		AppId:      r.AppID,
		Uid:        r.UID,
		MsgTraceId: r.TraceID,
	}
	for _, u := range req.UpdatedInfo {
		switch u.Type {
		case clientpb.ChatroomUpdateTag_UPDATE_CHATROOM_NAME:
			in.Name = u.Content
		case clientpb.ChatroomUpdateTag_UPDATE_ANNOUNCEMENT:
			in.Announcement = u.Content
		}
	}
	cctx, cancel := s.rpcCtx(ctx)
	defer cancel()
	rs, err := s.Chatroom.EditChatroomInfoByMember(cctx, in)
	if e := rpcRet(r, "EditChatroomInfoByMember", err, rs.GetRet(), clientpb.ErrorCode_CHATROOM_SERVICE_ERROR); e != nil {
		return &clientpb.ChatroomUpdateResp{Ret: e}
	}
	return &clientpb.ChatroomUpdateResp{Ret: ok()}
}

func (s *Service) lastOperationExt(ctx context.Context, r *Request, body []byte) *clientpb.GetLastOperationExtResp {
	req := &clientpb.GetLastOperationExtReq{}
	if e := unmarshal(r, body, req); e != nil {
		return &clientpb.GetLastOperationExtResp{Ret: e}
	}
	cctx, cancel := s.rpcCtx(ctx)
	defer cancel()
	rs, err := s.Chatroom.FetchOpExt(cctx, &servicepb.FetchOpExtRequest{
		RoomId:     req.RoomId,
		Uid:        r.UID,
		AppId:      r.AppID,
		MsgTraceId: r.TraceID,
	})
	if e := rpcRet(r, "FetchOpExt", err, rs.GetRet(), clientpb.ErrorCode_CHATROOM_SERVICE_ERROR); e != nil {
		return &clientpb.GetLastOperationExtResp{Ret: e}
	}
	return &clientpb.GetLastOperationExtResp{Ret: ok(), RemoteExt: rs.Ext}
}
