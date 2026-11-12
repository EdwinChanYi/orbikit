// Command web serves a browser chat client. The page speaks the gateway's
// WebSocket protocol directly; this process only plays the business server:
// it creates a demo app and room, then hands the browser a login token.
//
//	go run ./examples/web
//	# open http://localhost:8090
package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
	"github.com/EdwinChanYi/orbikit/pkg/grpcx"
)

//go:embed static
var content embed.FS

func main() {
	addr := flag.String("http", ":8090", "address for the demo page")
	coreAddr := flag.String("core", "localhost:9001", "core gRPC address")
	chatroomAddr := flag.String("chatroom", "localhost:9002", "chatroom gRPC address")
	wsURL := flag.String("ws", "ws://localhost:8001/ws", "gateway WebSocket URL the browser connects to")
	flag.Parse()

	if err := serve(*addr, *coreAddr, *chatroomAddr, *wsURL); err != nil {
		fmt.Fprintln(os.Stderr, "web:", err)
		os.Exit(1)
	}
}

func serve(addr, coreAddr, chatroomAddr, wsURL string) error {
	coreConn, err := grpcx.Dial(coreAddr)
	if err != nil {
		return err
	}
	defer coreConn.Close()
	roomConn, err := grpcx.Dial(chatroomAddr)
	if err != nil {
		return err
	}
	defer roomConn.Close()

	static, err := fs.Sub(content, "static")
	if err != nil {
		return err
	}
	demo := &demo{
		core:  servicepb.NewCoreClient(coreConn),
		room:  servicepb.NewChatroomClient(roomConn),
		wsURL: wsURL,
		rooms: map[int64]struct{}{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/join", demo.join)
	files := http.FileServer(http.FS(static))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	}))

	shown := addr
	if strings.HasPrefix(addr, ":") {
		shown = "127.0.0.1" + addr
	}
	fmt.Printf("demo page: http://%s\n", shown)
	return http.ListenAndServe(addr, mux)
}

type demo struct {
	core  servicepb.CoreClient
	room  servicepb.ChatroomClient
	wsURL string

	mu        sync.Mutex
	appID     string
	roleReady bool
	rooms     map[int64]struct{}
	seq       atomic.Int64
}

type joinRequest struct {
	Nickname string `json:"nickname"`
	RoomID   int64  `json:"room_id"`
}

type joinResponse struct {
	AppID      string `json:"app_id"`
	UID        int64  `json:"uid"`
	Nickname   string `json:"nickname"`
	LoginToken string `json:"login_token"`
	RoomID     int64  `json:"room_id"`
	RoomToken  string `json:"room_token"`
	WSURL      string `json:"ws_url"`
}

func (d *demo) join(w http.ResponseWriter, r *http.Request) {
	var req joinRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式不对")
		return
	}
	nick := strings.TrimSpace(req.Nickname)
	if nick == "" || len([]rune(nick)) > 24 {
		writeError(w, http.StatusBadRequest, "昵称需要 1 到 24 个字符")
		return
	}
	if req.RoomID == 0 {
		req.RoomID = 1001
	}
	if req.RoomID < 0 {
		writeError(w, http.StatusBadRequest, "房间号不合法")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	uid := 10000 + d.seq.Add(1)
	resp, err := d.prepare(ctx, nick, uid, req.RoomID)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (d *demo) prepare(ctx context.Context, nick string, uid, roomID int64) (*joinResponse, error) {
	appID, err := d.ensureApp(ctx)
	if err != nil {
		return nil, err
	}
	login, err := d.core.ApplyClientLoginToken(ctx, &servicepb.ApplyClientLoginTokenRequest{AppId: appID, Uid: uid})
	if err := rpcErr("签发登录令牌", login.GetRet(), err); err != nil {
		return nil, err
	}
	if err := d.ensureRoom(ctx, appID, roomID, uid); err != nil {
		return nil, err
	}
	if err := d.ensureMemberRole(ctx, appID); err != nil {
		return nil, err
	}
	enter, err := d.room.ApplyEnterChatroomToken(ctx, &servicepb.ApplyEnterChatroomTokenRequest{
		AppId:      appID,
		RoomId:     roomID,
		Uid:        uid,
		MemberInfo: &servicepb.ChatroomMemberInfo{Uid: uid, Nickname: nick, RoleId: []int32{memberRoleID}},
	})
	if err := rpcErr("申请进房令牌", enter.GetRet(), err); err != nil {
		return nil, err
	}
	return &joinResponse{
		AppID:      appID,
		UID:        uid,
		Nickname:   nick,
		LoginToken: login.GetLoginToken(),
		RoomID:     roomID,
		RoomToken:  enter.GetToken(),
		WSURL:      d.wsURL,
	}, nil
}

func (d *demo) ensureApp(ctx context.Context) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.appID != "" {
		return d.appID, nil
	}
	created, err := d.core.CreateApp(ctx, &servicepb.CreateAppRequest{Name: "web-demo", Desc: "created by examples/web"})
	if err := rpcErr("创建应用", created.GetRet(), err); err != nil {
		return "", err
	}
	d.appID = created.GetInfo().GetAppId()
	return d.appID, nil
}

const memberRoleID int32 = 1

func (d *demo) ensureMemberRole(ctx context.Context, appID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.roleReady {
		return nil
	}
	reply, err := d.room.CreateRole(ctx, &servicepb.CreateRoleRequest{
		AppId:  appID,
		Name:   "member",
		RoleId: int64(memberRoleID),
	})
	if err != nil {
		return fmt.Errorf("创建角色失败：%w", err)
	}
	code := reply.GetRet().GetErrorCode()
	if code != servicepb.ErrorCode_SUCCESS && code != servicepb.ErrorCode_CHATROOM_ROLE_EXIST {
		return fmt.Errorf("创建角色失败：%s %s", code, reply.GetRet().GetErrorMsg())
	}
	d.roleReady = true
	return nil
}

func (d *demo) ensureRoom(ctx context.Context, appID string, roomID, owner int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.rooms[roomID]; ok {
		return nil
	}
	created, err := d.room.CreateChatroom(ctx, &servicepb.CreateChatroomRequest{
		AppId:    appID,
		NeedOpen: true,
		ChatroomInfo: &servicepb.ChatroomInfo{
			ChatroomId: roomID,
			Name:       fmt.Sprintf("房间 %d", roomID),
			OwnerUid:   owner,
		},
	})
	if err := rpcErr("创建房间", created.GetRet(), err); err != nil {
		return err
	}
	d.rooms[roomID] = struct{}{}
	return nil
}

func rpcErr(op string, ret *servicepb.UnifiedResponse, err error) error {
	if err != nil {
		return fmt.Errorf("%s失败：%w", op, err)
	}
	if ret.GetErrorCode() != servicepb.ErrorCode_SUCCESS {
		return fmt.Errorf("%s失败：%s %s", op, ret.GetErrorCode(), ret.GetErrorMsg())
	}
	return nil
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
