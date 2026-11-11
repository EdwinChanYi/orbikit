package handler

import (
	"context"

	"google.golang.org/protobuf/proto"

	"github.com/yourname/roomkit/api/gen/clientpb"
	"github.com/yourname/roomkit/internal/gateway/convert"
)

var convertCode = convert.ErrorCode

type uniResp = clientpb.UnifiedResponse

func route[Resp proto.Message](
	cmd clientpb.CmdId, url string,
	empty func(*uniResp) Resp,
	h func(context.Context, *Request, []byte) Resp,
) *Route {
	return &Route{
		Cmd:    cmd,
		URL:    url,
		empty:  func(r *uniResp) proto.Message { return empty(r) },
		handle: func(ctx context.Context, r *Request, b []byte) proto.Message { return h(ctx, r, b) },
	}
}

func (s *Service) buildRoutes() map[clientpb.CmdId]*Route {
	list := []*Route{
		route(clientpb.CmdId_ECHO, "/base/service/echo",
			func(r *uniResp) *clientpb.EchoResp { return &clientpb.EchoResp{Ret: r} }, s.echo),
		route(clientpb.CmdId_LOGOUT, "/base/service/logout",
			func(r *uniResp) *clientpb.LoginOutResp { return &clientpb.LoginOutResp{Ret: r} }, s.logout),
		route(clientpb.CmdId_UPLOAD_LOG_REPORT, "/base/service/upload.log",
			func(r *uniResp) *clientpb.UploadLogReportResp { return &clientpb.UploadLogReportResp{Ret: r} }, s.uploadLogReport),

		route(clientpb.CmdId_CHATROOM_ENTER, "/base/service/chatroom/enter",
			func(r *uniResp) *clientpb.EnterChatroomResp { return &clientpb.EnterChatroomResp{Ret: r} }, s.enterChatroom),
		route(clientpb.CmdId_CHATROOM_OUT, "/base/service/chatroom/out",
			func(r *uniResp) *clientpb.ExitChatroomResp { return &clientpb.ExitChatroomResp{Ret: r} }, s.exitChatroom),
		route(clientpb.CmdId_CHATROOM_BATCH_OUT, "/base/service/chatroom/batch.out",
			func(r *uniResp) *clientpb.BatchExitChatroomResp { return &clientpb.BatchExitChatroomResp{Ret: r} }, s.batchExitChatroom),
		route(clientpb.CmdId_CHATROOM_GET_INFO, "/base/service/chatroom/info.get",
			func(r *uniResp) *clientpb.FetchChatroomInfoResp { return &clientpb.FetchChatroomInfoResp{Ret: r} }, s.fetchChatroomInfo),
		route(clientpb.CmdId_CHATROOM_EDIT_INFO, "/base/service/chatroom/info.edit",
			func(r *uniResp) *clientpb.ChatroomUpdateResp { return &clientpb.ChatroomUpdateResp{Ret: r} }, s.updateChatroom),
		route(clientpb.CmdId_CHATROOM_GET_LAST_OPERATION_EXT, "/base/service/opext/get",
			func(r *uniResp) *clientpb.GetLastOperationExtResp { return &clientpb.GetLastOperationExtResp{Ret: r} }, s.lastOperationExt),

		route(clientpb.CmdId_CHATROOM_SEND_MESSAGE, "/base/service/chatroom/msg/send",
			func(r *uniResp) *clientpb.SendMessageResp { return &clientpb.SendMessageResp{Ret: r} }, s.sendMessage),
		route(clientpb.CmdId_CHATROOM_MESSAGE_RECEIPT_FOR_RECEIVE, "/base/service/message/confirm",
			func(r *uniResp) *clientpb.MessageReceiptForReceiveResp {
				return &clientpb.MessageReceiptForReceiveResp{Ret: r}
			}, s.messageReceipt),
		route(clientpb.CmdId_MESSAGE_HISTORY_QUERY, "/base/service/message/query",
			func(r *uniResp) *clientpb.MessageQueryV2Resp { return &clientpb.MessageQueryV2Resp{Ret: r} }, s.messageHistoryUnsupported),
		route(clientpb.CmdId_MESSAGE_HISTORY_QUERY_V2, "/base/service/message/queryV2",
			func(r *uniResp) *clientpb.MessageQueryV2Resp { return &clientpb.MessageQueryV2Resp{Ret: r} }, s.messageHistoryUnsupported),

		route(clientpb.CmdId_CHATROOM_MEMBER_KICK, "/base/service/chatroom/edit.member/kick",
			func(r *uniResp) *clientpb.ChatRoomMemberKickResp { return &clientpb.ChatRoomMemberKickResp{Ret: r} }, s.kickMember),
		route(clientpb.CmdId_CHATROOM_MEMBER_BLOCK, "/base/service/chatroom/edit.member/block",
			func(r *uniResp) *clientpb.SettingChatroomInBlackResp {
				return &clientpb.SettingChatroomInBlackResp{Ret: r}
			}, s.blockMember),
		route(clientpb.CmdId_CHATROOM_MEMBER_MUTE, "/base/service/chatroom/edit.member/mute",
			func(r *uniResp) *clientpb.SettingMuteMessageResp { return &clientpb.SettingMuteMessageResp{Ret: r} }, s.muteMember),
		route(clientpb.CmdId_CHATROOM_FETCH_MEMBER_BY_TYPE, "/base/service/chatroom/fetch.member.info/by.type",
			func(r *uniResp) *clientpb.GetChatroomMemberByTypeResp {
				return &clientpb.GetChatroomMemberByTypeResp{Ret: r}
			}, s.membersByType),
		route(clientpb.CmdId_CHATROOM_FETCH_MEMBER_BY_IDS, "/base/service/chatroom/fetch.member.info/by.ids",
			func(r *uniResp) *clientpb.GetChatroomMemberByIdsResp {
				return &clientpb.GetChatroomMemberByIdsResp{Ret: r}
			}, s.membersByIDs),
		route(clientpb.CmdId_CHATROOM_FETCH_MEMBER_BY_KEYWORDS, "/base/service/chatroom/fetch.member.info/by.keywords",
			func(r *uniResp) *clientpb.GetChatroomMemberByKeywordResp {
				return &clientpb.GetChatroomMemberByKeywordResp{Ret: r}
			}, s.membersByKeyword),
		route(clientpb.CmdId_CHATROOM_FETCH_MEMBER_BY_ROLE, "/base/service/chatroom/fetch.member.info/by.role",
			func(r *uniResp) *clientpb.GetChatroomMemberByRoleResp {
				return &clientpb.GetChatroomMemberByRoleResp{Ret: r}
			}, s.membersByRole),

		route(clientpb.CmdId_CHATROOM_POSITION_UP, "/base/service/chatroom/position.up",
			func(r *uniResp) *clientpb.UpChatroomMicroResp { return &clientpb.UpChatroomMicroResp{Ret: r} }, s.micUp),
		route(clientpb.CmdId_CHATROOM_POSITION_DOWN, "/base/service/chatroom/position.down",
			func(r *uniResp) *clientpb.DownChatroomMicroResp { return &clientpb.DownChatroomMicroResp{Ret: r} }, s.micDown),
		route(clientpb.CmdId_CHATROOM_POSITION_KICK, "/base/service/chatroom/position.kick",
			func(r *uniResp) *clientpb.KickChatroomMicroResp { return &clientpb.KickChatroomMicroResp{Ret: r} }, s.micKick),
		route(clientpb.CmdId_CHATROOM_POSITION_INVITE, "/base/service/chatroom/position.invite",
			func(r *uniResp) *clientpb.InviteChatroomMicroResp { return &clientpb.InviteChatroomMicroResp{Ret: r} }, s.micInvite),
		route(clientpb.CmdId_CHATROOM_POSITION_EDIT, "/base/service/chatroom/position.edit",
			func(r *uniResp) *clientpb.EditMicroPositionInfoRes { return &clientpb.EditMicroPositionInfoRes{Ret: r} }, s.micEdit),
		route(clientpb.CmdId_CHATROOM_POSITION_INFO, "/base/service/chatroom/position.info",
			func(r *uniResp) *clientpb.QueryChatroomMicroResp { return &clientpb.QueryChatroomMicroResp{Ret: r} }, s.micInfo),
		route(clientpb.CmdId_CHATROOM_POSITION_TOKEN_UPDATE, "/base/service/chatroom/position.token.update",
			func(r *uniResp) *clientpb.UpdateChatroomMicroTokenResp {
				return &clientpb.UpdateChatroomMicroTokenResp{Ret: r}
			}, s.micTokenUpdate),
	}

	m := make(map[clientpb.CmdId]*Route, len(list))
	for _, rt := range list {
		if rt.Cmd == clientpb.CmdId_LOGOUT {
			rt.AllowEmpty = true
		}
		m[rt.Cmd] = rt
	}
	return m
}
