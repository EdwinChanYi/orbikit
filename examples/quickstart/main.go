// Command quickstart plays the role of a business server: it creates an app,
// issues a login token, creates and opens a room, applies for a room entry
// token, and prints the gateway-client command that joins the room.
//
//	go run ./examples/quickstart -uid 10001 -room 1001
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
	"github.com/EdwinChanYi/orbikit/pkg/grpcx"
)

func main() {
	coreAddr := flag.String("core", "localhost:9001", "core gRPC address")
	chatroomAddr := flag.String("chatroom", "localhost:9002", "chatroom gRPC address")
	appID := flag.String("app", "", "existing app id; empty creates a new app")
	appName := flag.String("app-name", "quickstart", "name of the app to create")
	uid := flag.Int64("uid", 10001, "user id to issue tokens for")
	roomID := flag.Int64("room", 1001, "room id to create")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := run(ctx, *coreAddr, *chatroomAddr, *appID, *appName, *uid, *roomID); err != nil {
		fmt.Fprintln(os.Stderr, "quickstart:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, coreAddr, chatroomAddr, appID, appName string, uid, roomID int64) error {
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

	core := servicepb.NewCoreClient(coreConn)
	room := servicepb.NewChatroomClient(roomConn)

	if appID == "" {
		app, err := core.CreateApp(ctx, &servicepb.CreateAppRequest{Name: appName, Desc: "created by examples/quickstart"})
		if err := check("CreateApp", app.GetRet(), err); err != nil {
			return err
		}
		appID = app.GetInfo().GetAppId()
		fmt.Printf("app created:   app_id=%s app_secret=%s\n", appID, app.GetInfo().GetAppSecret())
	}

	login, err := core.ApplyClientLoginToken(ctx, &servicepb.ApplyClientLoginTokenRequest{AppId: appID, Uid: uid})
	if err := check("ApplyClientLoginToken", login.GetRet(), err); err != nil {
		return err
	}
	fmt.Printf("login token:   uid=%d token=%s\n", uid, login.GetLoginToken())

	if err := ensureMemberRole(ctx, room, appID); err != nil {
		return err
	}

	created, err := room.CreateChatroom(ctx, &servicepb.CreateChatroomRequest{
		AppId:    appID,
		NeedOpen: true,
		ChatroomInfo: &servicepb.ChatroomInfo{
			ChatroomId: roomID,
			Name:       fmt.Sprintf("room-%d", roomID),
			OwnerUid:   uid,
		},
	})
	if err := check("CreateChatroom", created.GetRet(), err); err != nil {
		fmt.Fprintln(os.Stderr, "warning:", err, "(continuing in case the room already exists)")
	} else {
		fmt.Printf("room created:  room_id=%d\n", roomID)
	}

	enter, err := room.ApplyEnterChatroomToken(ctx, &servicepb.ApplyEnterChatroomTokenRequest{
		AppId:      appID,
		RoomId:     roomID,
		Uid:        uid,
		MemberInfo: &servicepb.ChatroomMemberInfo{Uid: uid, Nickname: fmt.Sprintf("user-%d", uid), RoleId: []int32{memberRoleID}},
	})
	if err := check("ApplyEnterChatroomToken", enter.GetRet(), err); err != nil {
		return err
	}
	fmt.Printf("room token:    %s\n\n", enter.GetToken())

	fmt.Println("join the room over WebSocket:")
	fmt.Printf("  go run ./examples/gateway-client -transport ws -app %s -uid %d -token %s -room %d -room-token %s -msg hello\n",
		appID, uid, login.GetLoginToken(), roomID, enter.GetToken())
	return nil
}

const memberRoleID int32 = 1

// ensureMemberRole creates the audience role used by the sample clients.
// Entering a room requires a role id, and that role must already exist for the app.
func ensureMemberRole(ctx context.Context, room servicepb.ChatroomClient, appID string) error {
	reply, err := room.CreateRole(ctx, &servicepb.CreateRoleRequest{
		AppId:  appID,
		Name:   "member",
		RoleId: int64(memberRoleID),
	})
	if err != nil {
		return fmt.Errorf("CreateRole: %w", err)
	}
	code := reply.GetRet().GetErrorCode()
	if code != servicepb.ErrorCode_SUCCESS && code != servicepb.ErrorCode_CHATROOM_ROLE_EXIST {
		return errors.New("CreateRole: " + code.String() + " " + reply.GetRet().GetErrorMsg())
	}
	return nil
}

func check(op string, ret *servicepb.UnifiedResponse, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	if ret.GetErrorCode() != servicepb.ErrorCode_SUCCESS {
		return errors.New(op + ": " + ret.GetErrorCode().String() + " " + ret.GetErrorMsg())
	}
	return nil
}
