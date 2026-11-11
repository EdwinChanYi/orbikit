package handler

import (
	"context"
	"time"

	"github.com/EdwinChanYi/orbikit/api/gen/clientpb"
	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
	"github.com/EdwinChanYi/orbikit/internal/gateway/convert"
)

func boolAction(set bool) int32 {
	if set {
		return 1
	}
	return 0
}

func (s *Service) kickMember(ctx context.Context, r *Request, body []byte) *clientpb.ChatRoomMemberKickResp {
	req := &clientpb.ChatroomMemberKickReq{}
	if e := unmarshal(r, body, req); e != nil {
		return &clientpb.ChatRoomMemberKickResp{Ret: e}
	}
	cctx, cancel := s.rpcCtx(ctx)
	defer cancel()
	rs, err := s.Chatroom.KickChatroomMember(cctx, &servicepb.KickChatroomMemberRequest{
		RoomId:     req.RoomId,
		AppId:      r.AppID,
		From:       r.UID,
		To:         req.UserId,
		Time:       req.Duration,
		Ext:        req.RemoteExt,
		Action:     1,
		MsgTraceId: r.TraceID,
		NotifyExt:  req.NotifyExt,
	})
	if e := rpcRet(r, "KickChatroomMember", err, rs.GetRet(), clientpb.ErrorCode_MARS_AGENT_INTERNAL_ERROR); e != nil {
		return &clientpb.ChatRoomMemberKickResp{Ret: e}
	}
	return &clientpb.ChatRoomMemberKickResp{Ret: ok()}
}

func (s *Service) blockMember(ctx context.Context, r *Request, body []byte) *clientpb.SettingChatroomInBlackResp {
	req := &clientpb.SettingChatroomInBlackReq{}
	if e := unmarshal(r, body, req); e != nil {
		return &clientpb.SettingChatroomInBlackResp{Ret: e}
	}
	if req.Data == nil {
		return &clientpb.SettingChatroomInBlackResp{Ret: ret(clientpb.ErrorCode_MARS_AGENT_GET_BAD_PARAM)}
	}
	cctx, cancel := s.rpcCtx(ctx)
	defer cancel()
	rs, err := s.Chatroom.BlockChatroomMember(cctx, &servicepb.BlockChatroomMemberRequest{
		RoomId:     req.Data.RoomId,
		AppId:      r.AppID,
		From:       r.UID,
		To:         req.Data.UserId,
		Ext:        req.Data.RemoteExt,
		Action:     boolAction(req.Data.Enable),
		MsgTraceId: r.TraceID,
	})
	if e := rpcRet(r, "BlockChatroomMember", err, rs.GetRet(), clientpb.ErrorCode_CHATROOM_SERVICE_ERROR); e != nil {
		return &clientpb.SettingChatroomInBlackResp{Ret: e}
	}
	return &clientpb.SettingChatroomInBlackResp{Ret: ok()}
}

func (s *Service) muteMember(ctx context.Context, r *Request, body []byte) *clientpb.SettingMuteMessageResp {
	req := &clientpb.SettingMuteMessageReq{}
	if e := unmarshal(r, body, req); e != nil {
		return &clientpb.SettingMuteMessageResp{Ret: e}
	}
	if req.Data == nil {
		return &clientpb.SettingMuteMessageResp{Ret: ret(clientpb.ErrorCode_MARS_AGENT_GET_BAD_PARAM)}
	}
	cctx, cancel := s.rpcCtx(ctx)
	defer cancel()
	rs, err := s.Chatroom.MuteChatroomMember(cctx, &servicepb.MuteChatroomMemberRequest{
		RoomId:     req.Data.RoomId,
		AppId:      r.AppID,
		From:       r.UID,
		To:         req.Data.UserId,
		Ext:        req.Data.RemoteExt,
		Action:     boolAction(req.Data.Enable),
		Time:       req.Duration,
		MsgTraceId: r.TraceID,
	})
	if e := rpcRet(r, "MuteChatroomMember", err, rs.GetRet(), clientpb.ErrorCode_CHATROOM_SERVICE_ERROR); e != nil {
		return &clientpb.SettingMuteMessageResp{Ret: e}
	}
	return &clientpb.SettingMuteMessageResp{Ret: ok()}
}

func sortType(t clientpb.ChatroomMemberFetchSortType) servicepb.ChatroomMemberFetchSortType {
	switch t {
	case clientpb.ChatroomMemberFetchSortType_CHATROOM_MEMBER_FETCH_SORT_DESC:
		return servicepb.ChatroomMemberFetchSortType_CHATROOM_MEMBER_FETCH_SORT_DESC
	case clientpb.ChatroomMemberFetchSortType_CHATROOM_MEMBER_FETCH_SORT_ASC:
		return servicepb.ChatroomMemberFetchSortType_CHATROOM_MEMBER_FETCH_SORT_ASC
	default:
		return servicepb.ChatroomMemberFetchSortType_CHATROOM_MEMBER_FETCH_SORT_UNDEFINED
	}
}

// memberList converts a member query reply, overlaying kick/mute end times
// (nanoseconds in the reply, milliseconds for clients).
func memberList(rs *servicepb.FetchChatroomMemberReply) []*clientpb.ChatroomMemberInfo {
	out := make([]*clientpb.ChatroomMemberInfo, 0, len(rs.Members))
	for _, m := range rs.Members {
		info := &clientpb.ChatroomMemberInfo{Member: convert.MemberBaseInfo(m, true)}
		if info.Member != nil {
			if v, ok := rs.KickedTime[m.Uid]; ok && v != nil {
				info.Member.KickStatus.EndTime = v.EndTime / int64(time.Millisecond)
			}
			if v, ok := rs.MutedTime[m.Uid]; ok && v != nil {
				info.Member.MuteStatus.EndTime = v.EndTime / int64(time.Millisecond)
			}
		}
		out = append(out, info)
	}
	return out
}

func (s *Service) membersByType(ctx context.Context, r *Request, body []byte) *clientpb.GetChatroomMemberByTypeResp {
	req := &clientpb.GetChatroomMemberByTypeReq{}
	if e := unmarshal(r, body, req); e != nil {
		return &clientpb.GetChatroomMemberByTypeResp{Ret: e}
	}
	in := &servicepb.FetchChatroomMemberByTypeRequest{
		RoomId:     req.RoomId,
		AppId:      r.AppID,
		Uid:        r.UID,
		SortType:   sortType(req.SortType),
		EnterTime:  req.EnterTime,
		Limit:      req.Limit,
		FoundRobot: req.FoundRobot,
		MsgTraceId: r.TraceID,
	}
	for _, t := range req.Type {
		in.Type = append(in.Type, servicepb.ChatroomMemberFetchMemberType(t))
	}
	cctx, cancel := s.rpcCtx(ctx)
	defer cancel()
	rs, err := s.Chatroom.FetchChatroomMemberByType(cctx, in)
	if e := rpcRet(r, "FetchChatroomMemberByType", err, rs.GetRet(), clientpb.ErrorCode_CHATROOM_SERVICE_ERROR); e != nil {
		return &clientpb.GetChatroomMemberByTypeResp{Ret: e}
	}
	return &clientpb.GetChatroomMemberByTypeResp{Ret: ok(), Members: memberList(rs)}
}

func (s *Service) membersByIDs(ctx context.Context, r *Request, body []byte) *clientpb.GetChatroomMemberByIdsResp {
	req := &clientpb.GetChatroomMemberByIdsReq{}
	if e := unmarshal(r, body, req); e != nil {
		return &clientpb.GetChatroomMemberByIdsResp{Ret: e}
	}
	cctx, cancel := s.rpcCtx(ctx)
	defer cancel()
	rs, err := s.Chatroom.FetchChatroomMemberById(cctx, &servicepb.FetchChatroomMemberByIdRequest{
		RoomId:     req.RoomId,
		AppId:      r.AppID,
		Uid:        r.UID,
		SortType:   sortType(req.SortType),
		QueryId:    req.UserIds,
		MsgTraceId: r.TraceID,
	})
	if e := rpcRet(r, "FetchChatroomMemberById", err, rs.GetRet(), clientpb.ErrorCode_CHATROOM_SERVICE_ERROR); e != nil {
		return &clientpb.GetChatroomMemberByIdsResp{Ret: e}
	}
	return &clientpb.GetChatroomMemberByIdsResp{Ret: ok(), Members: memberList(rs)}
}

func (s *Service) membersByKeyword(ctx context.Context, r *Request, body []byte) *clientpb.GetChatroomMemberByKeywordResp {
	req := &clientpb.GetChatroomMemberByKeywordReq{}
	if e := unmarshal(r, body, req); e != nil {
		return &clientpb.GetChatroomMemberByKeywordResp{Ret: e}
	}
	cctx, cancel := s.rpcCtx(ctx)
	defer cancel()
	rs, err := s.Chatroom.FetchChatroomMemberByKeyword(cctx, &servicepb.FetchChatroomMemberByKeywordRequest{
		RoomId:     req.RoomId,
		AppId:      r.AppID,
		Uid:        r.UID,
		SortType:   sortType(req.SortType),
		Keyword:    req.Keyword,
		Limit:      req.Limit,
		MsgTraceId: r.TraceID,
	})
	if e := rpcRet(r, "FetchChatroomMemberByKeyword", err, rs.GetRet(), clientpb.ErrorCode_CHATROOM_SERVICE_ERROR); e != nil {
		return &clientpb.GetChatroomMemberByKeywordResp{Ret: e}
	}
	return &clientpb.GetChatroomMemberByKeywordResp{Ret: ok(), Members: memberList(rs)}
}

func (s *Service) membersByRole(ctx context.Context, r *Request, body []byte) *clientpb.GetChatroomMemberByRoleResp {
	req := &clientpb.GetChatroomMemberByRoleReq{}
	if e := unmarshal(r, body, req); e != nil {
		return &clientpb.GetChatroomMemberByRoleResp{Ret: e}
	}
	in := &servicepb.FetchChatroomMemberByRoleRequest{
		RoomId:     req.RoomId,
		AppId:      r.AppID,
		Uid:        r.UID,
		SortType:   sortType(req.SortType),
		IsOnline:   !req.IsIncludeOffline,
		EnterTime:  req.EnterTime,
		Limit:      req.Limit,
		FoundRobot: req.FoundRobot,
		MsgTraceId: r.TraceID,
	}
	for _, role := range req.Role {
		in.Role = append(in.Role, int64(role))
	}
	cctx, cancel := s.rpcCtx(ctx)
	defer cancel()
	rs, err := s.Chatroom.FetchChatroomMemberByRole(cctx, in)
	if e := rpcRet(r, "FetchChatroomMemberByRole", err, rs.GetRet(), clientpb.ErrorCode_CHATROOM_SERVICE_ERROR); e != nil {
		return &clientpb.GetChatroomMemberByRoleResp{Ret: e}
	}
	return &clientpb.GetChatroomMemberByRoleResp{Ret: ok(), Members: memberList(rs)}
}
