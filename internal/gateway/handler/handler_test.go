package handler

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	"github.com/EdwinChanYi/orbikit/api/gen/clientpb"
	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
	"github.com/EdwinChanYi/orbikit/internal/gateway/moderation"
	"github.com/EdwinChanYi/orbikit/internal/gateway/store"
)

type fakeIDs struct{ n atomic.Uint64 }

func (f *fakeIDs) Next() uint64      { return f.n.Add(1) }
func (f *fakeIDs) MachineID() uint16 { return 1 }

type fakeChatroom struct {
	servicepb.ChatroomClient
	sent []*servicepb.ChatroomMsgRequest
	ret  servicepb.ErrorCode
}

func (f *fakeChatroom) SendChatroomMsg(_ context.Context, in *servicepb.ChatroomMsgRequest, _ ...grpc.CallOption) (*servicepb.ChatroomMsgReply, error) {
	f.sent = append(f.sent, in)
	return &servicepb.ChatroomMsgReply{Ret: &servicepb.UnifiedResponse{ErrorCode: f.ret}}, nil
}

type failingModerator struct{}

func (failingModerator) CheckText(context.Context, string, int64, int64, string) (moderation.Result, error) {
	return moderation.Result{}, errors.New("moderation backend down")
}

// offlineStore returns a store whose Redis calls fail immediately; message
// counters are best effort and must not affect the outcome.
func offlineStore() *store.Store {
	rdb := redis.NewClient(&redis.Options{
		Addr:       "offline:6379",
		MaxRetries: -1,
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("redis offline")
		},
	})
	return store.New(rdb)
}

type verdictModerator struct{ allowed bool }

func (v verdictModerator) CheckText(context.Context, string, int64, int64, string) (moderation.Result, error) {
	return moderation.Result{Allowed: v.allowed}, nil
}

func TestSetModeratorReplacesTheChecker(t *testing.T) {
	s := &Service{Deps: Deps{Moderator: verdictModerator{allowed: true}}}
	if res, err := s.currentModerator().CheckText(context.Background(), "a", 1, 1, "hi"); err != nil || !res.Allowed {
		t.Fatalf("initial = %+v, %v", res, err)
	}
	s.SetModerator(moderation.None{})
	if res, err := s.currentModerator().CheckText(context.Background(), "a", 1, 1, "hi"); err != nil || !res.Allowed {
		t.Fatalf("different type = %+v, %v", res, err)
	}
	s.SetModerator(verdictModerator{allowed: false})
	if res, err := s.currentModerator().CheckText(context.Background(), "a", 1, 1, "hi"); err != nil || res.Allowed {
		t.Fatalf("swapped = %+v, %v", res, err)
	}
	s.SetModerator(nil)
	if res, err := s.currentModerator().CheckText(context.Background(), "a", 1, 1, "hi"); err != nil || res.Allowed {
		t.Fatalf("nil swap = %+v, %v", res, err)
	}
}

func newTestService(chat servicepb.ChatroomClient, mod moderation.Moderator) *Service {
	return New(Deps{
		Chatroom:  chat,
		Store:     offlineStore(),
		IDs:       &fakeIDs{},
		Moderator: mod,
	}, Options{})
}

func TestRoutes(t *testing.T) {
	s := newTestService(&fakeChatroom{}, moderation.None{})
	urls := map[string]clientpb.CmdId{}
	for _, rt := range s.Routes() {
		if !strings.HasPrefix(rt.URL, "/base/service/") {
			t.Errorf("%s: url %q", rt.Cmd, rt.URL)
		}
		if prev, dup := urls[rt.URL]; dup {
			t.Errorf("url %q used by %s and %s", rt.URL, prev, rt.Cmd)
		}
		urls[rt.URL] = rt.Cmd
		if rt.AllowEmpty != (rt.Cmd == clientpb.CmdId_LOGOUT) {
			t.Errorf("%s: AllowEmpty = %v", rt.Cmd, rt.AllowEmpty)
		}
	}
	if len(urls) != 27 {
		t.Fatalf("%d routes registered, want 27", len(urls))
	}
	if _, ok := s.Route(clientpb.CmdId_AUTH); ok {
		t.Fatal("AUTH must only be served on the long link")
	}
}

func TestZeroLengthBody(t *testing.T) {
	s := newTestService(&fakeChatroom{}, moderation.None{})
	rt, _ := s.Route(clientpb.CmdId_CHATROOM_ENTER)
	resp, ok := rt.Serve(context.Background(), s.newRequest(rt.Cmd, "app", 1, "", "", "", 0), nil).(*clientpb.EnterChatroomResp)
	if !ok {
		t.Fatalf("response type %T", resp)
	}
	if resp.Ret.ErrorCode != clientpb.ErrorCode_MARS_AGENT_GET_ZERO_LEN_DATA {
		t.Fatalf("ret = %v", resp.Ret)
	}
}

func TestMessageHistoryNotSupported(t *testing.T) {
	s := newTestService(&fakeChatroom{}, moderation.None{})
	for _, cmd := range []clientpb.CmdId{clientpb.CmdId_MESSAGE_HISTORY_QUERY, clientpb.CmdId_MESSAGE_HISTORY_QUERY_V2} {
		rt, ok := s.Route(cmd)
		if !ok {
			t.Fatalf("%s not registered", cmd)
		}
		resp := rt.Serve(context.Background(), s.newRequest(cmd, "app", 1, "", "", "", 0), []byte{0x08, 0x01}).(*clientpb.MessageQueryV2Resp)
		if resp.Ret.ErrorCode == clientpb.ErrorCode_SUCCESS || !strings.Contains(resp.Ret.ErrorMsg, "not supported") {
			t.Fatalf("%s: ret = %v", cmd, resp.Ret)
		}
	}
}

func sendText(t *testing.T, s *Service, msg *clientpb.Message) *clientpb.SendMessageResp {
	t.Helper()
	body, err := proto.Marshal(&clientpb.SendMessageReq{Message: msg})
	if err != nil {
		t.Fatal(err)
	}
	rt, _ := s.Route(clientpb.CmdId_CHATROOM_SEND_MESSAGE)
	return rt.Serve(context.Background(), s.newRequest(rt.Cmd, "app", 42, "", "", "", 0), body).(*clientpb.SendMessageResp)
}

func roomText(text string) *clientpb.Message {
	return &clientpb.Message{
		MessageType: clientpb.MessageType_MESSAGE_TYPE_TEXT,
		Session:     &clientpb.Session{SessionType: clientpb.SessionType_SESSION_TYPE_CHATROOM, SessionId: 1001},
		Text:        text,
	}
}

func TestSendMessage(t *testing.T) {
	reject, err := moderation.NewKeyword(moderation.KeywordConfig{}, []string{"bad"})
	if err != nil {
		t.Fatal(err)
	}
	replace, err := moderation.NewKeyword(moderation.KeywordConfig{Mode: moderation.ModeReplace}, []string{"bad"})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		mod      moderation.Moderator
		msg      *clientpb.Message
		wantCode clientpb.ErrorCode
		wantSent string
	}{
		{"no moderation", moderation.None{}, roomText("a bad word"), clientpb.ErrorCode_SUCCESS, "a bad word"},
		{"clean text", reject, roomText("hello"), clientpb.ErrorCode_SUCCESS, "hello"},
		{"rejected", reject, roomText("a bad word"), clientpb.ErrorCode_MARS_AGENT_GD_ILLEGAL, ""},
		{"masked", replace, roomText("a bad word"), clientpb.ErrorCode_SUCCESS, "a *** word"},
		{"moderator failure", failingModerator{}, roomText("hello"), clientpb.ErrorCode_MARS_AGENT_GD_HTTP_ERR, ""},
		{"missing session", moderation.None{}, &clientpb.Message{Text: "x"}, clientpb.ErrorCode_MARS_AGENT_GET_BAD_PARAM, ""},
		{"non-chatroom session", moderation.None{}, &clientpb.Message{
			Session: &clientpb.Session{SessionType: clientpb.SessionType_SESSION_TYPE_SYSTEM}, Text: "x",
		}, clientpb.ErrorCode_MARS_AGENT_GET_BAD_PARAM, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chat := &fakeChatroom{}
			resp := sendText(t, newTestService(chat, tt.mod), tt.msg)
			if resp.Ret.ErrorCode != tt.wantCode {
				t.Fatalf("ret = %v, want %s", resp.Ret, tt.wantCode)
			}
			if tt.wantSent == "" {
				if len(chat.sent) != 0 {
					t.Fatalf("message forwarded: %v", chat.sent)
				}
				return
			}
			if len(chat.sent) != 1 {
				t.Fatalf("forwarded %d messages", len(chat.sent))
			}
			got := chat.sent[0]
			if got.GetTextContent() != tt.wantSent || got.From != 42 || got.AppId != "app" || got.ChatroomId != 1001 || got.MsgId == "" {
				t.Fatalf("forwarded %v", got)
			}
		})
	}
}

func TestSendMessageServiceError(t *testing.T) {
	chat := &fakeChatroom{ret: servicepb.ErrorCode_CHATROOM_NOT_IN_ROOM}
	resp := sendText(t, newTestService(chat, moderation.None{}), roomText("hi"))
	if resp.Ret.ErrorCode != clientpb.ErrorCode_CHATROOM_NOT_IN_ROOM {
		t.Fatalf("ret = %v", resp.Ret)
	}
}
