package server

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
	"github.com/EdwinChanYi/orbikit/internal/chatroom/logic"
	"github.com/EdwinChanYi/orbikit/internal/chatroom/reqctx"
)

var _ servicepb.ChatroomServer = (*Server)(nil)

func TestReplyCodes(t *testing.T) {
	if r := ret(servicepb.ErrorCode_SUCCESS); r.GetErrorCode() != servicepb.ErrorCode_SUCCESS || r.GetErrorMsg() != "SUCCESS" {
		t.Fatalf("ret(SUCCESS) = %v", r)
	}
	p := paramErr()
	if p.GetErrorCode() != servicepb.ErrorCode_PARAM_ERROR ||
		p.GetErrorMsg() != servicepb.ErrorCode_CHATROOM_SERVICE_ERROR.String() {
		t.Fatalf("paramErr = %v", p)
	}
}

func TestRecoverTo(t *testing.T) {
	ctx := reqctx.New(context.Background(), 1, "app")
	r := serviceErr()
	func() {
		defer recoverTo(ctx, "test", &r)
		panic("boom")
	}()
	if r.GetErrorCode() != servicepb.ErrorCode_PARAM_ERROR {
		t.Fatalf("after panic code = %v", r.GetErrorCode())
	}
}

func TestFailed(t *testing.T) {
	ctx := reqctx.New(context.Background(), 1, "app")
	r := serviceErr()
	if failed(ctx, "step", servicepb.ErrorCode_SUCCESS, nil, &r) {
		t.Fatal("success should not fail")
	}
	if !failed(ctx, "step", servicepb.ErrorCode_CHATROOM_NOT_IN_ROOM, nil, &r) ||
		r.GetErrorCode() != servicepb.ErrorCode_CHATROOM_NOT_IN_ROOM {
		t.Fatalf("non-success code should be stored, got %v", r)
	}
	r = serviceErr()
	if !failed(ctx, "step", servicepb.ErrorCode_SUCCESS, context.Canceled, &r) ||
		r.GetErrorCode() != servicepb.ErrorCode_CHATROOM_SERVICE_ERROR {
		t.Fatalf("errors keep the default code, got %v", r)
	}
}

func TestSeatsFromSpace(t *testing.T) {
	seats := seatsFromSpace([]*servicepb.SpaceInfos{
		{Index: 1, IsLocked: true},
		{Index: 2, IsUser: true, Uid: 9, Ext: map[string]string{"k": "v"}},
		nil,
	})
	if len(seats) != 3 || !seats[1].HasLock || seats[2].Uid != 9 || seats[2].Ext["k"] != "v" {
		t.Fatalf("seats = %+v", seats)
	}
}

func TestMemberReply(t *testing.T) {
	reply := &servicepb.FetchChatroomMemberReply{}
	var u logic.MemberDetail
	u.Uid = 5
	memberReply(reply, []logic.MemberDetail{u})
	if len(reply.GetMembers()) != 1 || reply.GetMembers()[0].GetUid() != 5 {
		t.Fatalf("members = %v", reply.GetMembers())
	}
	if _, ok := reply.GetMutedTime()[5]; !ok {
		t.Fatal("muted time missing")
	}
	if _, ok := reply.GetKickedTime()[5]; !ok {
		t.Fatal("kicked time missing")
	}
}

func TestUploadClientLogUnimplemented(t *testing.T) {
	s := New()
	if _, err := s.UploadClientLog(context.Background(), &servicepb.UploadClientLogRequest{}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("UploadClientLog err = %v", err)
	}
	if _, err := s.UploadClientLogStatus(context.Background(), &servicepb.UploadClientLogStatusRequest{}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("UploadClientLogStatus err = %v", err)
	}
}

func TestParamChecksBeforeDependencies(t *testing.T) {
	s := New()
	ctx := context.Background()

	cr, err := s.CreateChatroom(ctx, &servicepb.CreateChatroomRequest{})
	if err != nil || cr.GetRet().GetErrorCode() != servicepb.ErrorCode_PARAM_ERROR {
		t.Fatalf("CreateChatroom without info = %v, %v", cr, err)
	}
	sr, err := s.SetRole(ctx, &servicepb.SetRoleRequest{})
	if err != nil || sr.GetRet().GetErrorCode() != servicepb.ErrorCode_PARAM_ERROR {
		t.Fatalf("SetRole without roles = %v, %v", sr, err)
	}
	ar, err := s.ApplyEnterChatroomToken(ctx, &servicepb.ApplyEnterChatroomTokenRequest{})
	if err != nil || ar.GetRet().GetErrorCode() != servicepb.ErrorCode_CHATROOM_APPLAY_TOKEN_ROLEIDS_NOT_EXISTS {
		t.Fatalf("ApplyEnterChatroomToken without roles = %v, %v", ar, err)
	}
	ur, err := s.UpdateUserChatroomInfo(ctx, &servicepb.UpdateUserChatroomInfoRequest{})
	if err == nil || ur.GetRet().GetErrorCode() != servicepb.ErrorCode_PARAM_ERROR {
		t.Fatalf("UpdateUserChatroomInfo without user = %v, %v", ur, err)
	}
	rr, err := s.GetChatroomIdByChatroomMemberCount(ctx, &servicepb.GetChatroomIdByChatroomMemberCountRequest{AppId: "a"})
	if err != nil || rr.GetRet().GetErrorCode() != servicepb.ErrorCode_PARAM_ERROR {
		t.Fatalf("rank with page 0 = %v, %v", rr, err)
	}
	mr, err := s.UserMicTime(ctx, &servicepb.UserMicTimeRequest{SortType: servicepb.UserMicTimeRequest_SortType(99)})
	if err != nil || mr.GetRet().GetErrorCode() != servicepb.ErrorCode_PARAM_ERROR {
		t.Fatalf("UserMicTime bad sort = %v, %v", mr, err)
	}
}
