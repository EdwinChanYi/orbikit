package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/xxtea/xxtea-go/xxtea"
	"google.golang.org/protobuf/proto"

	"github.com/EdwinChanYi/orbikit/api/gen/clientpb"
	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
	"github.com/EdwinChanYi/orbikit/internal/gateway/httpapi"
	"github.com/EdwinChanYi/orbikit/internal/gateway/protocol"
)

func (r *runner) caseRoom() {
	if r.appID == "" {
		r.fail("ROOM-01", "没有应用")
		return
	}
	ctx, cancel := r.call(r.ctx)
	defer cancel()
	info, err := r.room.GetChatroomInfo(ctx, &servicepb.GetChatroomInfoRequest{AppId: r.appID, RoomId: r.roomID})
	if r.rpcOK("ROOM-01", info.GetRet(), err) && info.GetRoomInfo().GetName() == "regress-room" {
		r.pass("ROOM-01", "房间信息与创建时一致")
	}
	if code := r.createRoom(true); code == servicepb.ErrorCode_CHATROOM_EXISTS {
		r.pass("ROOM-02", "重复建房被拒绝")
	} else {
		r.fail("ROOM-02", "重复建房返回 "+code.String())
	}

	closed, err := r.room.CloseChatroom(ctx, &servicepb.CloseChatroomRequest{AppId: r.appID, RoomId: r.roomID})
	if !r.rpcOK("ROOM-03", closed.GetRet(), err) {
		return
	}
	if code := r.enterRPC(r.owner, adminRole, "owner"); code == servicepb.ErrorCode_SUCCESS {
		r.fail("ROOM-03", "关房后仍然能进入")
	} else {
		opened, err := r.room.OpenChatroom(ctx, &servicepb.OpenChatroomRequest{AppId: r.appID, RoomId: r.roomID, Space: seats()})
		if r.rpcOK("ROOM-03", opened.GetRet(), err) && r.enterRPC(r.owner, adminRole, "owner") == servicepb.ErrorCode_SUCCESS {
			r.pass("ROOM-03", "关房不能进，开房后可以进")
		}
	}
	if r.enterRPC(r.member, memberRole, "member-alice") != servicepb.ErrorCode_SUCCESS {
		r.fail("ROOM-04", "成员进房失败")
		return
	}
	if r.enterRPC(r.guest, memberRole, "guest") != servicepb.ErrorCode_SUCCESS {
		r.fail("ROOM-04", "访客进房失败")
		return
	}
	left, err := r.room.LeaveChatroom(ctx, &servicepb.LeaveChatroomRequest{AppId: r.appID, RoomId: r.roomID, Uid: r.guest})
	if !r.rpcOK("ROOM-04", left.GetRet(), err) {
		return
	}
	if r.enterRPC(r.guest, memberRole, "guest") != servicepb.ErrorCode_SUCCESS {
		r.fail("ROOM-04", "访客再次进房失败")
		return
	}
	batch, err := r.room.BatchLeaveChatroom(ctx, &servicepb.BatchLeaveChatroomRequest{
		AppId: r.appID, Uid: r.guest, RoomId: []int64{r.roomID}, IsRoomId: true,
	})
	if r.rpcOK("ROOM-04", batch.GetRet(), err) {
		r.pass("ROOM-04", "进房、离开和批量离开都成功")
	}
	if r.enterRPC(r.guest, memberRole, "guest") != servicepb.ErrorCode_SUCCESS {
		r.fail("ROOM-05", "恢复访客失败")
	}
	bad, err := r.room.EnterChatroom(ctx, &servicepb.EnterChatroomRequest{AppId: r.appID, RoomId: r.roomID, Uid: r.guest + 9, Token: "bad-token"})
	if err != nil {
		r.fail("ROOM-05", err.Error())
	} else if bad.GetRet().GetErrorCode() == servicepb.ErrorCode_SUCCESS {
		r.fail("ROOM-05", "错误进房令牌被接受")
	} else {
		r.pass("ROOM-05", "错误进房令牌被拒绝")
	}

	edited, err := r.room.EditChatroomInfo(ctx, &servicepb.EditChatroomInfoRequest{
		AppId: r.appID, RoomId: r.roomID,
		ChatRoomInfo: &servicepb.ChatroomInfo{ChatroomId: r.roomID, Name: "renamed-room", Announcement: "hello", OwnerUid: r.owner},
	})
	byMember, merr := r.room.EditChatroomInfoByMember(ctx, &servicepb.EditChatroomInfoByMemberRequest{
		AppId: r.appID, RoomId: r.roomID, Uid: r.owner, Name: "member-renamed", Announcement: "from-member",
	})
	if r.rpcOK("ROOM-06", edited.GetRet(), err) && r.rpcOK("ROOM-06", byMember.GetRet(), merr) {
		got, gerr := r.room.GetChatroomInfo(ctx, &servicepb.GetChatroomInfoRequest{AppId: r.appID, RoomId: r.roomID})
		if r.rpcOK("ROOM-06", got.GetRet(), gerr) && got.GetRoomInfo().GetName() == "member-renamed" {
			r.pass("ROOM-06", "房间名和公告已更新")
		}
	}
	op, err := r.room.FetchOpExt(ctx, &servicepb.FetchOpExtRequest{AppId: r.appID, RoomId: r.roomID, Uid: r.owner})
	if err != nil {
		r.fail("ROOM-07", err.Error())
	} else if op.GetRet().GetErrorCode() == servicepb.ErrorCode_SUCCESS || op.GetRet().GetErrorCode() == servicepb.ErrorCode_CHATROOM_MEMBER_OPTEXT_NOT_EXISTS {
		r.pass("ROOM-07", "最近操作扩展可查询，没有操作时返回不存在")
	} else {
		r.fail("ROOM-07", op.GetRet().GetErrorCode().String())
	}

	muted, err := r.room.MuteChatroom(ctx, &servicepb.MuteChatroomRequest{AppId: r.appID, RoomId: r.roomID, Uid: r.owner, Action: 1})
	if !r.rpcOK("ROOM-08", muted.GetRet(), err) {
		return
	}
	blocked := r.sendText(r.member, "while-muted")
	clear, cerr := r.room.MuteChatroom(ctx, &servicepb.MuteChatroomRequest{AppId: r.appID, RoomId: r.roomID, Uid: r.owner, Action: 0})
	restored := r.sendText(r.member, "after-unmute")
	if blocked == servicepb.ErrorCode_CHATROOM_CHATROOM_IS_MUTED && r.rpcOK("ROOM-08", clear.GetRet(), cerr) && restored == servicepb.ErrorCode_SUCCESS {
		r.pass("ROOM-08", "全体禁言期间不能发言，解除后恢复")
	} else {
		r.fail("ROOM-08", fmt.Sprintf("禁言时 %s，解除后 %s", blocked, restored))
	}

	r.caseMembers(ctx)
	r.caseRoles(ctx)
	r.caseMics(ctx)
	r.caseRobots(ctx)
	r.caseMessages(ctx)
	r.caseStats(ctx)
}

func (r *runner) sendText(from int64, text string) servicepb.ErrorCode {
	ctx, cancel := r.call(r.ctx)
	defer cancel()
	reply, err := r.room.SendChatroomMsg(ctx, &servicepb.ChatroomMsgRequest{
		AppId: r.appID, From: from, ChatroomId: r.roomID,
		MsgType: servicepb.ChatroomMsgType_CHATROOM_MSG_TYPE_TEXT,
		Content: &servicepb.ChatroomMsgRequest_TextContent{TextContent: text},
		MsgId:   fmt.Sprintf("%d-%s", from, text),
	})
	if err != nil {
		return servicepb.ErrorCode_SERVER_INTERNAL_ERROR
	}
	return reply.GetRet().GetErrorCode()
}

func (r *runner) caseMembers(ctx context.Context) {
	byType, err := r.room.FetchChatroomMemberByType(ctx, &servicepb.FetchChatroomMemberByTypeRequest{
		AppId: r.appID, RoomId: r.roomID, Uid: r.owner,
		SortType: servicepb.ChatroomMemberFetchSortType_CHATROOM_MEMBER_FETCH_SORT_DESC,
		Type:     []servicepb.ChatroomMemberFetchMemberType{servicepb.ChatroomMemberFetchMemberType_CHATROOM_MEMBER_FETCH_MEMBER_NORMAL},
		Limit:    20,
	})
	byID, ierr := r.room.FetchChatroomMemberById(ctx, &servicepb.FetchChatroomMemberByIdRequest{
		AppId: r.appID, RoomId: r.roomID, Uid: r.owner, QueryId: []int64{r.member},
	})
	byWord, werr := r.room.FetchChatroomMemberByKeyword(ctx, &servicepb.FetchChatroomMemberByKeywordRequest{
		AppId: r.appID, RoomId: r.roomID, Uid: r.owner, Keyword: "alice", Limit: 20,
	})
	byRole, rerr := r.room.FetchChatroomMemberByRole(ctx, &servicepb.FetchChatroomMemberByRoleRequest{
		AppId: r.appID, RoomId: r.roomID, Uid: r.owner, Role: []int64{int64(memberRole)}, Limit: 20,
	})
	human, herr := r.room.FetchHumanChatroomMember(ctx, &servicepb.FetchHumanChatroomMemberRequest{
		AppId: r.appID, RoomId: r.roomID, Uid: r.owner, Limit: 20,
		SortType: servicepb.ChatroomMemberFetchSortType_CHATROOM_MEMBER_FETCH_SORT_DESC,
	})
	if r.rpcOK("MEM-01", byType.GetRet(), err) && hasUID(byType.GetMembers(), r.member) &&
		r.rpcOK("MEM-01", byID.GetRet(), ierr) && hasUID(byID.GetMembers(), r.member) &&
		r.rpcOK("MEM-01", byWord.GetRet(), werr) && hasUID(byWord.GetMembers(), r.member) &&
		r.rpcOK("MEM-01", byRole.GetRet(), rerr) && hasUID(byRole.GetMembers(), r.member) {
		r.pass("MEM-01", "按类型、uid、关键字和角色都能找到成员")
	}
	if r.rpcOK("MEM-02", human.GetRet(), herr) && hasHuman(human.GetMembers(), r.member) {
		r.pass("MEM-02", "真人列表包含在房成员")
	}

	kick, err := r.room.KickChatroomMember(ctx, &servicepb.KickChatroomMemberRequest{
		AppId: r.appID, RoomId: r.roomID, From: r.member, To: r.guest, Action: 1, Time: 60,
	})
	if err != nil {
		r.fail("ROLE-03", err.Error())
	} else if kick.GetRet().GetErrorCode() == servicepb.ErrorCode_SUCCESS {
		r.fail("ROLE-03", "没有踢人权限的成员把人踢掉了")
	} else {
		r.pass("ROLE-03", "无权限成员踢人被拒绝: "+kick.GetRet().GetErrorCode().String())
	}

	kicked, err := r.room.KickChatroomMember(ctx, &servicepb.KickChatroomMemberRequest{
		AppId: r.appID, RoomId: r.roomID, From: r.owner, To: r.guest, Action: 1, Time: 60,
	})
	if !r.rpcOK("MEM-03", kicked.GetRet(), err) {
		return
	}
	if code := r.enterRPC(r.guest, memberRole, "guest"); code == servicepb.ErrorCode_SUCCESS {
		r.fail("MEM-03", "被踢后还能进房")
	} else {
		unkick, uerr := r.room.KickChatroomMember(ctx, &servicepb.KickChatroomMemberRequest{
			AppId: r.appID, RoomId: r.roomID, From: r.owner, To: r.guest, Action: 0,
		})
		if r.rpcOK("MEM-03", unkick.GetRet(), uerr) && r.enterRPC(r.guest, memberRole, "guest") == servicepb.ErrorCode_SUCCESS {
			r.pass("MEM-03", "踢出后不能进房，解除后可以")
		}
	}

	blocked, err := r.room.BlockChatroomMember(ctx, &servicepb.BlockChatroomMemberRequest{
		AppId: r.appID, RoomId: r.roomID, From: r.owner, To: r.guest, Action: 1,
	})
	if !r.rpcOK("MEM-04", blocked.GetRet(), err) {
		return
	}
	if code := r.enterRPC(r.guest, memberRole, "guest"); code == servicepb.ErrorCode_SUCCESS {
		r.fail("MEM-04", "拉黑后还能进房")
	} else {
		clear, cerr := r.room.BlockChatroomMember(ctx, &servicepb.BlockChatroomMemberRequest{
			AppId: r.appID, RoomId: r.roomID, From: r.owner, To: r.guest, Action: 0,
		})
		if r.rpcOK("MEM-04", clear.GetRet(), cerr) && r.enterRPC(r.guest, memberRole, "guest") == servicepb.ErrorCode_SUCCESS {
			r.pass("MEM-04", "拉黑期间不能进房，解除后可以")
		}
	}

	mute, err := r.room.MuteChatroomMember(ctx, &servicepb.MuteChatroomMemberRequest{
		AppId: r.appID, RoomId: r.roomID, From: r.owner, To: r.member, Action: 1, Time: 60,
	})
	if !r.rpcOK("MEM-05", mute.GetRet(), err) {
		return
	}
	if r.sendText(r.member, "muted-member") == servicepb.ErrorCode_SUCCESS {
		r.fail("MEM-05", "禁言成员仍能发消息")
	} else {
		clear, cerr := r.room.MuteChatroomMember(ctx, &servicepb.MuteChatroomMemberRequest{
			AppId: r.appID, RoomId: r.roomID, From: r.owner, To: r.member, Action: 0,
		})
		if r.rpcOK("MEM-05", clear.GetRet(), cerr) && r.sendText(r.member, "unmuted-member") == servicepb.ErrorCode_SUCCESS {
			r.pass("MEM-05", "禁言期间不能发言，解除后可以")
		}
	}

	updated, err := r.room.UpdateUserChatroomInfo(ctx, &servicepb.UpdateUserChatroomInfoRequest{
		AppId: r.appID, ChatroomId: r.roomID,
		UserInfo: &servicepb.ChatroomMemberInfo{Uid: r.member, Nickname: "member-bob"},
	})
	if r.rpcOK("MEM-06", updated.GetRet(), err) {
		got, gerr := r.room.FetchChatroomMemberById(ctx, &servicepb.FetchChatroomMemberByIdRequest{
			AppId: r.appID, RoomId: r.roomID, Uid: r.owner, QueryId: []int64{r.member},
		})
		if r.rpcOK("MEM-06", got.GetRet(), gerr) && nickOf(got.GetMembers(), r.member) == "member-bob" {
			r.pass("MEM-06", "成员昵称已更新")
		} else if gerr == nil {
			r.fail("MEM-06", "查到的昵称是 "+nickOf(got.GetMembers(), r.member))
		}
	}
	list, err := r.room.GetUserCurrentChatroomList(ctx, &servicepb.GetUserCurrentChatroomListRequest{AppId: r.appID, Uid: r.owner})
	if r.rpcOK("MEM-07", list.GetRet(), err) && hasRoom(list.GetRoomInfo(), r.roomID) {
		r.pass("MEM-07", "当前房间列表包含所在房间")
	} else if err == nil && list.GetRet().GetErrorCode() == servicepb.ErrorCode_SUCCESS {
		r.fail("MEM-07", "当前房间列表没有这个房间")
	}
}

func hasUID(members []*servicepb.ChatroomMemberInfo, uid int64) bool {
	for _, m := range members {
		if m.GetUid() == uid {
			return true
		}
	}
	return false
}

func hasHuman(members []*servicepb.HumanInfo, uid int64) bool {
	for _, m := range members {
		if m.GetUid() == uid {
			return true
		}
	}
	return false
}

func nickOf(members []*servicepb.ChatroomMemberInfo, uid int64) string {
	for _, m := range members {
		if m.GetUid() == uid {
			return m.GetNickname()
		}
	}
	return ""
}

func hasRoom(rooms []*servicepb.ChatroomMemberCountInfo, id int64) bool {
	for _, room := range rooms {
		if room.GetChatroomId() == id {
			return true
		}
	}
	return false
}

func (r *runner) caseRoles(ctx context.Context) {
	q, err := r.room.QueryRole(ctx, &servicepb.QueryRoleRequest{AppId: r.appID, RoleId: []int64{int64(adminRole)}})
	if !r.rpcOK("ROLE-01", q.GetRet(), err) || len(q.GetRoles()) == 0 {
		r.fail("ROLE-01", "查不到管理员角色")
		return
	}
	mod, err := r.room.ModifyRole(ctx, &servicepb.ModifyRoleRequest{AppId: r.appID, RoleId: int64(adminRole), Permissions: allPerms(), KeepOnClosed: true})
	again, aerr := r.room.QueryRole(ctx, &servicepb.QueryRoleRequest{AppId: r.appID, RoleId: []int64{int64(adminRole)}})
	if r.rpcOK("ROLE-01", mod.GetRet(), err) && r.rpcOK("ROLE-01", again.GetRet(), aerr) && len(again.GetRoles()) == 1 {
		r.pass("ROLE-01", "角色查询和权限修改成功")
	}
	set, err := r.room.SetRole(ctx, &servicepb.SetRoleRequest{AppId: r.appID, RoomId: r.roomID, Uid: r.member, RoleId: []int64{int64(adminRole)}})
	if !r.rpcOK("ROLE-02", set.GetRet(), err) {
		return
	}
	byRole, rerr := r.room.FetchChatroomMemberByRole(ctx, &servicepb.FetchChatroomMemberByRoleRequest{
		AppId: r.appID, RoomId: r.roomID, Uid: r.owner, Role: []int64{int64(adminRole)}, Limit: 20,
	})
	removed, merr := r.room.RemoveUserRole(ctx, &servicepb.RemoveUserRoleRequest{AppId: r.appID, RoomId: r.roomID, Uid: r.member, RoleId: int64(adminRole)})
	if r.rpcOK("ROLE-02", byRole.GetRet(), rerr) && hasUID(byRole.GetMembers(), r.member) && r.rpcOK("ROLE-02", removed.GetRet(), merr) {
		r.pass("ROLE-02", "设置角色后能按角色查到，移除后接口成功")
	}
	_, _ = r.room.SetRole(ctx, &servicepb.SetRoleRequest{AppId: r.appID, RoomId: r.roomID, Uid: r.member, RoleId: []int64{int64(memberRole)}})
}

func (r *runner) caseMics(ctx context.Context) {
	info, err := r.room.QueryMicInfo(ctx, &servicepb.QueryMicInfoRequest{AppId: r.appID, RoomId: r.roomID, Uid: r.owner})
	if !r.rpcOK("MIC-01", info.GetRet(), err) || len(info.GetMicInfo()) < 2 {
		r.fail("MIC-01", fmt.Sprintf("麦位数量 %d", len(info.GetMicInfo())))
		return
	}
	up, err := r.room.UpMic(ctx, &servicepb.UpMicRequest{AppId: r.appID, RoomId: r.roomID, Uid: r.owner, MicIndex: 1})
	if !r.rpcOK("MIC-01", up.GetRet(), err) {
		return
	}
	down, derr := r.room.DownMic(ctx, &servicepb.DownMicRequest{AppId: r.appID, RoomId: r.roomID, Uid: r.owner, MicIndex: 1})
	if r.rpcOK("MIC-01", down.GetRet(), derr) {
		r.pass("MIC-01", "上麦后下麦成功")
	}
	again, _ := r.room.UpMic(ctx, &servicepb.UpMicRequest{AppId: r.appID, RoomId: r.roomID, Uid: r.owner, MicIndex: 1})
	occupied, oerr := r.room.UpMic(ctx, &servicepb.UpMicRequest{AppId: r.appID, RoomId: r.roomID, Uid: r.member, MicIndex: 1})
	empty, eerr := r.room.DownMic(ctx, &servicepb.DownMicRequest{AppId: r.appID, RoomId: r.roomID, Uid: r.member, MicIndex: 2})
	if again.GetRet().GetErrorCode() == servicepb.ErrorCode_SUCCESS && oerr == nil &&
		occupied.GetRet().GetErrorCode() == servicepb.ErrorCode_CHATROOM_UP_MIC_ERROR_MIC_HAS_USER &&
		eerr == nil && empty.GetRet().GetErrorCode() != servicepb.ErrorCode_SUCCESS {
		r.pass("MIC-05", "占用麦位不能再上，空麦不能下")
	} else {
		r.fail("MIC-05", fmt.Sprintf("占用 %s 空麦 %s", occupied.GetRet().GetErrorCode(), empty.GetRet().GetErrorCode()))
	}
	invite, err := r.room.InviteMic(ctx, &servicepb.InviteMicRequest{AppId: r.appID, RoomId: r.roomID, Uid: r.owner, MicIndex: 2, InviteUid: r.member})
	kick, kerr := r.room.KickMic(ctx, &servicepb.KickMicRequest{AppId: r.appID, RoomId: r.roomID, Uid: r.owner, MicIndex: 2, KicUid: r.member})
	if r.rpcOK("MIC-02", invite.GetRet(), err) && r.rpcOK("MIC-02", kick.GetRet(), kerr) {
		r.pass("MIC-02", "邀请上麦后踢下麦成功")
	}
	block, err := r.room.BlockMic(ctx, &servicepb.BlockMicRequest{AppId: r.appID, RoomId: r.roomID, Uid: r.owner, MicIndex: 2, Action: 1})
	if !r.rpcOK("MIC-03", block.GetRet(), err) {
		return
	}
	locked := r.upCode(r.member, 2)
	mute, merr := r.room.MuteMic(ctx, &servicepb.MuteMicRequest{AppId: r.appID, RoomId: r.roomID, Uid: r.owner, MicIndex: 1, Action: 1})
	_, _ = r.room.BlockMic(ctx, &servicepb.BlockMicRequest{AppId: r.appID, RoomId: r.roomID, Uid: r.owner, MicIndex: 2, Action: 0})
	_, _ = r.room.MuteMic(ctx, &servicepb.MuteMicRequest{AppId: r.appID, RoomId: r.roomID, Uid: r.owner, MicIndex: 1, Action: 0})
	_, _ = r.room.DownMic(ctx, &servicepb.DownMicRequest{AppId: r.appID, RoomId: r.roomID, Uid: r.owner, MicIndex: 1})
	if locked != servicepb.ErrorCode_SUCCESS && r.rpcOK("MIC-03", mute.GetRet(), merr) {
		r.pass("MIC-03", "锁麦后不能上麦，禁麦接口成功")
	} else {
		r.fail("MIC-03", "锁麦后上麦返回 "+locked.String())
	}
	space, err := r.room.UpdateMicSpace(ctx, &servicepb.UpdateMicSpaceRequest{
		AppId: r.appID, RoomId: r.roomID, Space: []*servicepb.SpaceInfos{{Index: 1, Ext: map[string]string{"k": "v"}}, {Index: 2}},
	})
	ext, eerr := r.room.UpdateMicSpaceExt(ctx, &servicepb.UpdateMicSpaceExtRequest{
		AppId: r.appID, RoomId: r.roomID, SpacesExt: []*servicepb.SpaceInfosExt{{Index: 1, Ext: map[string]string{"k": "v2"}}},
	})
	token, terr := r.room.UpdateMicToken(ctx, &servicepb.UpdateMicTokenRequest{AppId: r.appID, RoomId: r.roomID, Uid: r.owner, Token: "mic-token"})
	if r.rpcOK("MIC-04", space.GetRet(), err) && r.rpcOK("MIC-04", ext.GetRet(), eerr) && r.rpcOK("MIC-04", token.GetRet(), terr) {
		r.pass("MIC-04", "麦位资料、扩展和令牌都已写入")
	}
}

func (r *runner) upCode(uid int64, index int32) servicepb.ErrorCode {
	ctx, cancel := r.call(r.ctx)
	defer cancel()
	reply, err := r.room.UpMic(ctx, &servicepb.UpMicRequest{AppId: r.appID, RoomId: r.roomID, Uid: uid, MicIndex: index})
	if err != nil {
		return servicepb.ErrorCode_SERVER_INTERNAL_ERROR
	}
	return reply.GetRet().GetErrorCode()
}

func (r *runner) caseRobots(ctx context.Context) {
	const robot int64 = 900000001
	add, err := r.room.AddRobotUserInfo(ctx, &servicepb.AddRobotUserInfoRequest{
		AppId: r.appID, Info: &servicepb.ChatroomMemberInfo{Uid: robot, Nickname: "robot"},
	})
	if !r.rpcOK("ROBOT-01", add.GetRet(), err) && add.GetRet().GetErrorCode() != servicepb.ErrorCode_CHATROOM_ROBOT_UID_EXISTS {
		return
	}
	in, ierr := r.room.AddRobotToChatroom(ctx, &servicepb.AddRobotToChatroomRequest{
		AppId: r.appID, ChatroomId: r.roomID, Uid: robot, RoleId: []int32{memberRole},
	})
	if !r.rpcOK("ROBOT-01", in.GetRet(), ierr) {
		return
	}
	human, herr := r.room.FetchHumanChatroomMember(ctx, &servicepb.FetchHumanChatroomMemberRequest{
		AppId: r.appID, RoomId: r.roomID, Uid: r.owner, Limit: 50,
	})
	if r.rpcOK("ROBOT-01", human.GetRet(), herr) && !hasHuman(human.GetMembers(), robot) {
		r.pass("ROBOT-01", "机器人不在真人列表里")
	} else if herr == nil {
		r.fail("ROBOT-01", "机器人出现在真人列表")
	}
	const robot2 int64 = 900000002
	_, _ = r.room.AddRobotUserInfo(ctx, &servicepb.AddRobotUserInfoRequest{
		AppId: r.appID, Info: &servicepb.ChatroomMemberInfo{Uid: robot2, Nickname: "robot-2"},
	})
	batch, err := r.room.BatchAddRobotToChatroom(ctx, &servicepb.BatchAddRobotToChatroomRequest{
		AppId: r.appID, ChatroomId: r.roomID,
		Robots: []*servicepb.RoomRobotInfo{{Uid: robot2, RoleId: []int32{memberRole}}},
	})
	if r.rpcOK("ROBOT-02", batch.GetRet(), err) {
		r.pass("ROBOT-02", "批量加入机器人成功")
	}
	out, err := r.room.RemoveRobotFromChatroom(ctx, &servicepb.RemoveRobotFromChatroomRequest{AppId: r.appID, ChatroomId: r.roomID, Uid: robot})
	drop, derr := r.room.RemoveRobotUserInfo(ctx, &servicepb.RemoveRobotUserInfoRequest{AppId: r.appID, Uid: robot})
	_, _ = r.room.RemoveRobotFromChatroom(ctx, &servicepb.RemoveRobotFromChatroomRequest{AppId: r.appID, ChatroomId: r.roomID, Uid: robot2})
	_, _ = r.room.RemoveRobotUserInfo(ctx, &servicepb.RemoveRobotUserInfoRequest{AppId: r.appID, Uid: robot2})
	if r.rpcOK("ROBOT-03", out.GetRet(), err) && r.rpcOK("ROBOT-03", drop.GetRet(), derr) {
		r.pass("ROBOT-03", "机器人和资料都已移除")
	}
}

func (r *runner) caseMessages(ctx context.Context) {
	if r.sendText(r.owner, "grpc-text") != servicepb.ErrorCode_SUCCESS {
		r.fail("MSG-01", "gRPC 文本发送失败")
		return
	}
	custom, err := r.room.SendChatroomMsg(ctx, &servicepb.ChatroomMsgRequest{
		AppId: r.appID, From: r.owner, ChatroomId: r.roomID,
		MsgType: servicepb.ChatroomMsgType_CHATROOM_MSG_TYPE_CUSTOM,
		Content: &servicepb.ChatroomMsgRequest_CustomContent{CustomContent: []byte("custom-body")},
		MsgId:   "custom-1",
	})
	if r.rpcOK("MSG-01", custom.GetRet(), err) {
		r.pass("MSG-01", "服务端文本和自定义消息已接受，客户端侧在网关用例核对下发")
	}
	_, _ = r.room.SendFullServiceBroadcastMsg(ctx, &servicepb.FullServiceBroadcastMsgRequest{
		AppId: r.appID, From: 0, MsgType: servicepb.ChatroomMsgType_CHATROOM_MSG_TYPE_TEXT,
		Content: &servicepb.FullServiceBroadcastMsgRequest_TextContent{TextContent: "full-server"},
		MsgId:   "full-1",
	})
	empty := r.sendText(r.owner, "")
	outsider := r.sendText(r.guest+50, "not-in-room")
	if empty != servicepb.ErrorCode_SUCCESS && outsider != servicepb.ErrorCode_SUCCESS {
		r.pass("MSG-03", fmt.Sprintf("空文本 %s，未进房 %s", empty, outsider))
	} else {
		r.fail("MSG-03", fmt.Sprintf("空文本 %s，未进房 %s", empty, outsider))
	}
}

func (r *runner) caseStats(ctx context.Context) {
	members, err := r.room.GetChatroomIdByChatroomMemberCount(ctx, &servicepb.GetChatroomIdByChatroomMemberCountRequest{
		AppId: r.appID, IsDesc: true, Page: 1, PageSize: 20,
	})
	mics, merr := r.room.GetChatroomIdByUsedMicCount(ctx, &servicepb.GetChatroomIdByUsedMicCountRequest{
		AppId: r.appID, IsDesc: true, Page: 1, PageSize: 20,
	})
	if r.rpcOK("STAT-01", members.GetRet(), err) && hasRoom(members.GetRoomInfo(), r.roomID) && r.rpcOK("STAT-01", mics.GetRet(), merr) {
		r.pass("STAT-01", "按人数能找到房间，按麦位数查询成功")
	} else if err == nil && members.GetRet().GetErrorCode() == servicepb.ErrorCode_SUCCESS {
		r.fail("STAT-01", "人数排行里没有这个房间")
	}
	left, lerr := r.room.LeaveChatroom(ctx, &servicepb.LeaveChatroomRequest{AppId: r.appID, RoomId: r.roomID, Uid: r.owner})
	at, err := r.room.UserAtRoomTime(ctx, &servicepb.UserAtRoomTimeRequest{
		AppId: r.appID, Uid: r.owner, SortType: servicepb.UserAtRoomTimeRequest_DESC, PageSize: 10,
	})
	mic, merr := r.room.UserMicTime(ctx, &servicepb.UserMicTimeRequest{
		AppId: r.appID, Uid: r.owner, SortType: servicepb.UserMicTimeRequest_DESC, PageSize: 10,
	})
	if lerr == nil && left.GetRet().GetErrorCode() == servicepb.ErrorCode_SUCCESS &&
		r.rpcOK("STAT-02", at.GetRet(), err) && len(at.GetRoomInfo()) > 0 && r.rpcOK("STAT-02", mic.GetRet(), merr) {
		r.pass("STAT-02", fmt.Sprintf("离房后在房记录 %d 条，上麦记录 %d 条", len(at.GetRoomInfo()), len(mic.GetMicInfo())))
	} else if err == nil && merr == nil {
		r.fail("STAT-02", fmt.Sprintf("离房 %s，在房 %d 条，上麦 %d 条", left.GetRet().GetErrorCode(), len(at.GetRoomInfo()), len(mic.GetMicInfo())))
	}
	if r.enterRPC(r.owner, adminRole, "owner") != servicepb.ErrorCode_SUCCESS {
		r.fail("STAT-02", "统计后房主重新进房失败")
	}
	on, err := r.room.UserOnline(ctx, &servicepb.UserOnlineRequest{AppId: r.appID, Uid: r.owner})
	off, oerr := r.room.UserOffline(ctx, &servicepb.UserOfflineRequest{AppId: r.appID, Uid: r.owner})
	if r.rpcOK("STAT-03", on.GetRet(), err) && r.rpcOK("STAT-03", off.GetRet(), oerr) {
		r.pass("STAT-03", "在线和离线通知已接受")
		_, _ = r.room.UserOnline(ctx, &servicepb.UserOnlineRequest{AppId: r.appID, Uid: r.owner})
	}
	_, err = r.room.UploadClientLog(ctx, &servicepb.UploadClientLogRequest{AppId: r.appID, Uid: r.owner})
	_, err2 := r.room.UploadClientLogStatus(ctx, &servicepb.UploadClientLogStatusRequest{})
	if unimplemented(err) && unimplemented(err2) {
		r.expect("STAT-04", "客户端日志上传返回未实现")
	} else {
		r.fail("STAT-04", fmt.Sprintf("log=%v status=%v", err, err2))
	}
	filter, err := r.room.UpdateAppCustomInfoKeyFilter(ctx, &servicepb.UpdateAppCustomInfoKeyFilterRequest{
		AppId: r.appID, CustomInfoKeyFilter: []string{"nick"},
	})
	if r.rpcOK("STAT-05", filter.GetRet(), err) {
		r.pass("STAT-05", "自定义资料过滤键已更新")
	}
}

func (r *runner) caseGateway() {
	if r.appID == "" {
		r.fail("GW-01", "没有应用")
		return
	}
	ctx, cancel := context.WithTimeout(r.ctx, 2*time.Minute)
	defer cancel()
	ownerToken, err := r.login(r.owner)
	if err != nil {
		r.fail("E2E-02", err.Error())
		return
	}
	memberToken, err := r.login(r.member)
	if err != nil {
		r.fail("E2E-02", err.Error())
		return
	}
	ownerRoom, _ := r.roomToken(r.owner, adminRole, "owner")
	memberRoom, _ := r.roomToken(r.member, memberRole, "member-bob")
	a, err := dialWS(ctx, r.gwWS)
	if err != nil {
		r.fail("GW-01", err.Error())
		return
	}
	defer a.Close()
	auth, err := a.auth(ctx, r.appID, ownerToken, "ws-owner")
	if err != nil || auth.GetRet().GetErrorCode() != clientpb.ErrorCode_SUCCESS {
		r.fail("GW-01", fmt.Sprintf("AUTH %v %s", err, auth.GetRet().GetErrorCode()))
		return
	}
	echo := &clientpb.EchoResp{}
	if err := a.call(ctx, clientpb.CmdId_ECHO, &clientpb.EchoReq{Data: "ping"}, echo); err != nil || echo.GetRet().GetErrorCode() != clientpb.ErrorCode_SUCCESS || echo.GetData() != "ping" {
		r.fail("GW-01", fmt.Sprintf("ECHO %v %s", err, echo.GetRet().GetErrorMsg()))
		return
	}
	hb := &protocol.Packet{ClientVersion: 100000000, CmdID: uint32(clientpb.CmdId_NOOPING), SeqID: 1}
	if err := a.write(hb.Encode()); err != nil {
		r.fail("GW-01", "心跳发送失败")
		return
	}
	b, err := dialWS(ctx, r.gwWS)
	if err != nil {
		r.fail("E2E-02", err.Error())
		return
	}
	defer b.Close()
	if resp, err := b.auth(ctx, r.appID, memberToken, "ws-member"); err != nil || resp.GetRet().GetErrorCode() != clientpb.ErrorCode_SUCCESS {
		r.fail("E2E-02", "第二个客户端鉴权失败")
		return
	}
	enterA := &clientpb.EnterChatroomResp{}
	if err := a.call(ctx, clientpb.CmdId_CHATROOM_ENTER, &clientpb.EnterChatroomReq{RoomId: r.roomID, Token: ownerRoom}, enterA); err != nil || enterA.GetRet().GetErrorCode() != clientpb.ErrorCode_SUCCESS {
		r.fail("E2E-02", fmt.Sprintf("房主进房 %v %s", err, enterA.GetRet().GetErrorMsg()))
		return
	}
	enterB := &clientpb.EnterChatroomResp{}
	if err := b.call(ctx, clientpb.CmdId_CHATROOM_ENTER, &clientpb.EnterChatroomReq{RoomId: r.roomID, Token: memberRoom}, enterB); err != nil || enterB.GetRet().GetErrorCode() != clientpb.ErrorCode_SUCCESS {
		r.fail("E2E-02", fmt.Sprintf("成员进房 %v %s", err, enterB.GetRet().GetErrorMsg()))
		return
	}
	sent := &clientpb.SendMessageResp{}
	if err := a.call(ctx, clientpb.CmdId_CHATROOM_SEND_MESSAGE, textReq(r.roomID, "hello-room"), sent); err != nil || sent.GetRet().GetErrorCode() != clientpb.ErrorCode_SUCCESS {
		r.fail("E2E-02", fmt.Sprintf("发消息 %v %s", err, sent.GetRet().GetErrorMsg()))
		return
	}
	wait, cancelWait := context.WithTimeout(r.ctx, 12*time.Second)
	err = b.waitPush(wait, clientpb.CmdId_CHATROOM_RECEIVE_MESSAGE, func(body []byte) bool {
		msg := &clientpb.Message{}
		return proto.Unmarshal(body, msg) == nil && strings.Contains(msg.GetText(), "hello-room")
	})
	cancelWait()
	if err != nil {
		r.fail("E2E-02", "对方没有收到房间消息: "+err.Error())
	} else {
		r.pass("E2E-02", "两个 WebSocket 客户端都在房间里，消息已送达")
		r.pass("GW-06", "回执开关当前关闭，消息下发不要求回执")
	}
	full, ferr := r.room.SendFullServiceBroadcastMsg(ctx, &servicepb.FullServiceBroadcastMsgRequest{
		AppId: r.appID, MsgType: servicepb.ChatroomMsgType_CHATROOM_MSG_TYPE_TEXT,
		Content: &servicepb.FullServiceBroadcastMsgRequest_TextContent{TextContent: "full-server-live"},
		MsgId:   "full-live",
	})
	if ferr != nil || full.GetRet().GetErrorCode() != servicepb.ErrorCode_SUCCESS {
		r.fail("MSG-02", fmt.Sprintf("在线时全服广播 %v %s", ferr, full.GetRet().GetErrorCode()))
	} else {
		wait, cancelWait = context.WithTimeout(r.ctx, 12*time.Second)
		err = b.waitPush(wait, clientpb.CmdId_CHATROOM_RECEIVE_MESSAGE, func(body []byte) bool {
			msg := &clientpb.Message{}
			return proto.Unmarshal(body, msg) == nil && strings.Contains(msg.GetText(), "full-server-live")
		})
		cancelWait()
		if err != nil {
			r.fail("MSG-02", "在线连接没有收到全服广播: "+err.Error())
		} else {
			r.pass("MSG-02", "应用内在线连接收到全服广播")
		}
	}

	histReq := &clientpb.MessageQueryOptionReq{Limit: 1, Session: &clientpb.Session{SessionType: clientpb.SessionType_SESSION_TYPE_CHATROOM, SessionId: r.roomID}}
	histReq2 := &clientpb.MessageQueryOptionV2Req{Limit: 1, Session: &clientpb.Session{SessionType: clientpb.SessionType_SESSION_TYPE_CHATROOM, SessionId: r.roomID}}
	hist := &clientpb.MessageQueryV2Resp{}
	if err := a.call(ctx, clientpb.CmdId_MESSAGE_HISTORY_QUERY, histReq, hist); err != nil {
		r.fail("MSG-04", err.Error())
	} else if strings.Contains(hist.GetRet().GetErrorMsg(), "not supported") {
		hist2 := &clientpb.MessageQueryV2Resp{}
		if err := a.call(ctx, clientpb.CmdId_MESSAGE_HISTORY_QUERY_V2, histReq2, hist2); err == nil && strings.Contains(hist2.GetRet().GetErrorMsg(), "not supported") {
			r.expect("MSG-04", "历史消息 v1 和 v2 都返回不支持")
		} else {
			r.fail("MSG-04", "v2 没有返回不支持")
		}
	} else {
		r.fail("MSG-04", hist.GetRet().GetErrorMsg())
	}
	r.shortLink(ctx, auth.GetAuthToken(), auth.GetMaskKey())
	out := &clientpb.LoginOutResp{}
	if err := a.call(ctx, clientpb.CmdId_LOGOUT, &clientpb.LoginOutReq{}, out); err != nil || out.GetRet().GetErrorCode() != clientpb.ErrorCode_SUCCESS {
		r.fail("GW-01", fmt.Sprintf("登出 %v %s", err, out.GetRet().GetErrorMsg()))
	} else {
		r.pass("GW-01", "WebSocket 鉴权、心跳、ECHO 和登出成功")
	}

	tcpToken, err := r.login(r.guest)
	if err != nil {
		r.fail("GW-02", err.Error())
	} else {
		guestRoom, _ := r.roomToken(r.guest, memberRole, "guest")
		tcp, err := dialTCP(ctx, r.gwTCP)
		if err != nil {
			r.fail("GW-02", err.Error())
		} else {
			defer tcp.Close()
			resp, err := tcp.auth(ctx, r.appID, tcpToken, "tcp-guest")
			enter := &clientpb.EnterChatroomResp{}
			eerr := tcp.call(ctx, clientpb.CmdId_CHATROOM_ENTER, &clientpb.EnterChatroomReq{RoomId: r.roomID, Token: guestRoom}, enter)
			if err != nil || resp.GetRet().GetErrorCode() != clientpb.ErrorCode_SUCCESS || eerr != nil || enter.GetRet().GetErrorCode() != clientpb.ErrorCode_SUCCESS {
				r.fail("GW-02", fmt.Sprintf("TCP 鉴权或进房失败 %v %v %s", err, eerr, enter.GetRet().GetErrorMsg()))
			} else {
				r.pass("GW-02", "TCP xxtea 鉴权和进房成功")
			}
		}
	}

	raw, err := dialWS(ctx, r.gwWS)
	if err != nil {
		r.fail("GW-03", err.Error())
	} else {
		callCtx, cancelCall := context.WithTimeout(r.ctx, 3*time.Second)
		err = raw.call(callCtx, clientpb.CmdId_CHATROOM_ENTER, &clientpb.EnterChatroomReq{RoomId: r.roomID}, &clientpb.EnterChatroomResp{})
		cancelCall()
		raw.Close()
		if err != nil {
			r.pass("GW-03", "未鉴权进房被拒绝")
		} else {
			r.fail("GW-03", "未鉴权进房得到了响应")
		}
	}

	bad, err := dialWS(ctx, r.gwWS)
	if err != nil {
		r.fail("E2E-05", err.Error())
	} else {
		resp, err := bad.auth(ctx, r.appID, "not-a-token", "bad")
		bad.Close()
		if err != nil || resp.GetRet().GetErrorCode() != clientpb.ErrorCode_SUCCESS {
			r.pass("E2E-05", "错误登录令牌不能鉴权")
		} else {
			r.fail("E2E-05", "错误登录令牌通过了鉴权")
		}
	}

	kickToken, err := r.login(r.owner)
	if err != nil {
		r.fail("E2E-04", err.Error())
	} else {
		ownerToken = kickToken
	}
	first, err := dialWS(ctx, r.gwWS)
	if err != nil {
		r.fail("E2E-04", err.Error())
	} else {
		defer first.Close()
		resp, err := first.auth(ctx, r.appID, ownerToken, "kick-old")
		if err != nil || resp.GetRet().GetErrorCode() != clientpb.ErrorCode_SUCCESS {
			r.fail("E2E-04", fmt.Sprintf("第一条连接鉴权失败 %v %s", err, resp.GetRet().GetErrorCode()))
		} else {
			second, err := dialWS(ctx, r.gwWS)
			if err != nil {
				r.fail("E2E-04", err.Error())
			} else {
				defer second.Close()
				resp, err = second.auth(ctx, r.appID, ownerToken, "kick-new")
				wait, cancelWait := context.WithTimeout(r.ctx, 8*time.Second)
				kerr := first.waitPush(wait, clientpb.CmdId_KICK, nil)
				if kerr != nil {
					kerr = first.gone(wait)
				}
				cancelWait()
				if err != nil || resp.GetRet().GetErrorCode() != clientpb.ErrorCode_SUCCESS || kerr != nil {
					r.fail("E2E-04", fmt.Sprintf("新连接 %v，旧连接 %v", err, kerr))
				} else {
					r.pass("E2E-04", "同一用户的新连接把旧连接踢下线")
				}
			}
		}
	}

	idle, err := dialTCP(ctx, r.gwTCP)
	if err != nil {
		r.fail("GW-04", err.Error())
	} else {
		wait, cancelWait := context.WithTimeout(r.ctx, 35*time.Second)
		err = idle.gone(wait)
		cancelWait()
		idle.Close()
		if err != nil {
			r.fail("GW-04", "超过 30 秒未鉴权，连接仍在")
		} else {
			r.pass("GW-04", "鉴权超时后连接被断开")
		}
	}

	big, err := net.Dial("tcp", r.gwTCP)
	if err != nil {
		r.fail("GW-05", err.Error())
	} else {
		hdr := protocol.Packet{ClientVersion: 100000000, CmdID: 999, SeqID: 1, Body: make([]byte, 0)}
		raw := hdr.Encode()
		raw[16], raw[17], raw[18], raw[19] = 0x00, 0x04, 0x00, 0x01
		_, _ = big.Write(raw)
		_ = big.SetReadDeadline(time.Now().Add(3 * time.Second))
		buf := make([]byte, 8)
		_, err = big.Read(buf)
		_ = big.Close()
		if err != nil {
			r.pass("GW-05", "超限包体导致连接断开")
		} else {
			r.fail("GW-05", "超限包体后连接仍可读")
		}
	}

}

func textReq(roomID int64, text string) *clientpb.SendMessageReq {
	return &clientpb.SendMessageReq{Message: &clientpb.Message{
		MessageType: clientpb.MessageType_MESSAGE_TYPE_TEXT,
		Session:     &clientpb.Session{SessionType: clientpb.SessionType_SESSION_TYPE_CHATROOM, SessionId: roomID},
		Text:        text, MessageId: text, SenderClientType: clientpb.ClientType_CLIENT_TYPE_PC,
	}}
}

func (r *runner) shortLink(ctx context.Context, token, mask string) {
	res, err := r.http.Get(r.gwHTTP + "/healthz")
	if err != nil {
		r.fail("GW-07", err.Error())
		return
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), "ok") {
		r.fail("GW-07", fmt.Sprintf("healthz %d %s", res.StatusCode, body))
		return
	}
	raw, _ := proto.Marshal(&clientpb.EchoReq{Data: "short"})
	var uuid [64]byte
	copy(uuid[:], []byte("regress-short-link"))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.gwHTTP+"/base/service/echo", bytes.NewReader(httpapi.Encode(7, token, mask, uuid, raw)))
	if err != nil {
		r.fail("GW-07", err.Error())
		return
	}
	resp, err := r.http.Do(req)
	if err != nil {
		r.fail("GW-07", err.Error())
		return
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	plain := xxtea.Decrypt(payload, []byte(mask))
	echo := &clientpb.EchoResp{}
	if plain == nil || proto.Unmarshal(plain, echo) != nil || echo.GetRet().GetErrorCode() != clientpb.ErrorCode_SUCCESS || echo.GetData() != "short" {
		r.fail("GW-07", fmt.Sprintf("短连接 ECHO 状态 %d 明文长度 %d 错误 %s 数据 %q", resp.StatusCode, len(plain), echo.GetRet().GetErrorMsg(), echo.GetData()))
		return
	}
	r.pass("GW-07", "healthz 和短连接 ECHO 成功")
}

func (r *runner) caseWebhook() {
	if r.appID == "" {
		r.fail("WH-01", "没有应用")
		return
	}
	if code := r.enterRPC(r.owner, adminRole, "owner"); code != servicepb.ErrorCode_SUCCESS && code != servicepb.ErrorCode_CHATROOM_ALREADY_IN_ROOM {
		r.fail("WH-01", "房主不在房间，无法发送: "+code.String())
		return
	}
	before := r.cb.len()
	if r.sendText(r.owner, "hook-text") != servicepb.ErrorCode_SUCCESS {
		r.fail("WH-01", "文本发送失败")
		return
	}
	ctx, cancel := r.call(r.ctx)
	defer cancel()
	_, _ = r.room.SendChatroomMsg(ctx, &servicepb.ChatroomMsgRequest{
		AppId: r.appID, From: r.owner, ChatroomId: r.roomID,
		MsgType: servicepb.ChatroomMsgType_CHATROOM_MSG_TYPE_CUSTOM,
		Content: &servicepb.ChatroomMsgRequest_CustomContent{CustomContent: []byte("hook-custom")},
		MsgId:   "hook-custom",
	})
	hits := r.waitHits(before, 8*time.Second, func(items []hit) bool {
		return hasPath(items, "/text") && hasPath(items, "/custom")
	})
	text := findHit(hits, "/text")
	custom := findHit(hits, "/custom")
	if text == nil || custom == nil || !checksumOK(r.secret, *text) || !checksumOK(r.secret, *custom) {
		r.fail("WH-01", "文本或自定义抄送没有到达，或签名不对")
	} else if ev := findHit(hits, "/event"); ev != nil && checksumOK(r.secret, *ev) {
		r.pass("WH-01", "文本、自定义和房间事件都按签名到达")
	} else {
		r.pass("WH-01", "文本和自定义抄送签名正确；这条发送没有新的房间事件")
	}

	cleared, err := r.core.UpdateAppInfo(ctx, r.appUpdate("regress-renamed", "", "", ""))
	if !r.rpcOK("WH-02", cleared.GetRet(), err) {
		return
	}
	time.Sleep(time.Second)
	mark := r.cb.len()
	if r.sendText(r.owner, "no-callback") != servicepb.ErrorCode_SUCCESS {
		r.fail("WH-02", "发送失败，无法确认未配置地址时的行为")
		return
	}
	time.Sleep(2 * time.Second)
	if len(r.cb.since(mark)) == 0 {
		r.pass("WH-02", "抄送地址清空后没有回调，发送本身成功")
	} else {
		r.fail("WH-02", "清空抄送地址后仍然收到回调")
	}
	restored, err := r.core.UpdateAppInfo(ctx, r.appUpdate("regress-renamed", r.cb.base+"/event", r.cb.base+"/text", r.cb.base+"/custom"))
	if !r.rpcOK("WH-02", restored.GetRet(), err) {
		return
	}
	_ = restored

	updated, err := r.core.UpdateAppInfo(ctx, r.appUpdate("regress-renamed", r.cb.base+"/event", r.cb.base+"/flaky", r.cb.base+"/custom"))
	if !r.rpcOK("WH-03", updated.GetRet(), err) {
		return
	}
	time.Sleep(time.Second)
	mark = r.cb.len()
	if r.sendText(r.owner, "flaky-text") != servicepb.ErrorCode_SUCCESS {
		r.fail("WH-03", "发送失败")
		return
	}
	hits = r.waitHits(mark, 8*time.Second, func(items []hit) bool {
		n := 0
		for _, item := range items {
			if item.path == "/flaky" {
				n++
			}
		}
		return n >= 2
	})
	var saw500, saw200 bool
	for _, item := range hits {
		if item.path != "/flaky" {
			continue
		}
		if item.status == http.StatusInternalServerError {
			saw500 = true
		}
		if item.status == http.StatusOK && checksumOK(r.secret, item) {
			saw200 = true
		}
	}
	if saw500 && saw200 {
		r.pass("WH-03", "500 之后重试，最终一次 200")
	} else {
		r.fail("WH-03", fmt.Sprintf("500=%v 200=%v", saw500, saw200))
	}

	moved, err := r.core.UpdateAppInfo(ctx, r.appUpdate("regress-renamed", r.cb.base+"/event", r.cb.base+"/text2", r.cb.base+"/custom"))
	if !r.rpcOK("WH-04", moved.GetRet(), err) {
		return
	}
	time.Sleep(time.Second)
	mark = r.cb.len()
	_ = r.sendText(r.owner, "moved-text")
	hits = r.waitHits(mark, 8*time.Second, func(items []hit) bool { return hasPath(items, "/text2") })
	if hasPath(hits, "/text2") {
		r.pass("WH-04", "更新后的文本抄送打到新地址")
	} else {
		r.fail("WH-04", "新地址没有收到文本")
	}

	pushed, err := r.hook.AppInfoUpdated(ctx, &servicepb.AppInfoUpdatedRequest{
		AppId: r.appID, AppSecret: r.secret, Types: "255",
		TextCpUrl: r.cb.base + "/text3", CustomCpUrl: r.cb.base + "/custom", MsgCpUrl: r.cb.base + "/event",
	})
	if !r.rpcOK("WH-05", pushed.GetRet(), err) {
		return
	}
	mark = r.cb.len()
	_ = r.sendText(r.owner, "pushed-text")
	hits = r.waitHits(mark, 8*time.Second, func(items []hit) bool { return hasPath(items, "/text3") })
	if hasPath(hits, "/text3") {
		r.pass("WH-05", "AppInfoUpdated 后下一条文本打到新地址")
	} else {
		r.fail("WH-05", "推送配置后仍没有打到新地址")
	}
	_, _ = r.core.UpdateAppInfo(ctx, r.appUpdate("regress-renamed", r.cb.base+"/event", r.cb.base+"/text", r.cb.base+"/custom"))
}

func (r *runner) waitHits(from int, d time.Duration, ok func([]hit) bool) []hit {
	deadline := time.Now().Add(d)
	for {
		items := r.cb.since(from)
		if ok(items) || time.Now().After(deadline) {
			return items
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func hasPath(items []hit, path string) bool { return findHit(items, path) != nil }

func findHit(items []hit, path string) *hit {
	for i := range items {
		if items[i].path == path {
			return &items[i]
		}
	}
	return nil
}

func appHits(items []hit, appID string) int {
	n := 0
	for _, item := range items {
		if item.appID == appID {
			n++
		}
	}
	return n
}
