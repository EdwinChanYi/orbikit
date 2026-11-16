package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"google.golang.org/protobuf/proto"

	"github.com/EdwinChanYi/orbikit/api/gen/clientpb"
	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
)

func (r *runner) caseGaps() {
	if r.appID == "" {
		r.fail("GAP-01", "没有应用")
		return
	}
	r.caseFilterReadback()
	r.caseDirected()
	r.caseClientCommands()
	r.caseSignature()
	r.caseQPS()
	r.caseHotConfig()
	r.caseBeta()
	r.caseConcurrency()
	r.caseSession()
	r.caseReleaseSurface()
	r.caseLoad()
	r.caseOfflineLeave()
}

func (r *runner) caseFilterReadback() {
	ctx, cancel := r.call(r.ctx)
	defer cancel()
	if r.enterRPC(r.member, memberRole, "member-bob") != servicepb.ErrorCode_SUCCESS &&
		r.enterRPC(r.member, memberRole, "member-bob") != servicepb.ErrorCode_CHATROOM_ALREADY_IN_ROOM {
		r.fail("GAP-01", "成员不在房间")
		return
	}
	updated, err := r.room.UpdateUserChatroomInfo(ctx, &servicepb.UpdateUserChatroomInfoRequest{
		AppId: r.appID, ChatroomId: r.roomID,
		UserInfo: &servicepb.ChatroomMemberInfo{
			Uid: r.member, Nickname: "member-bob",
			CustomInfo: map[string]string{"nick": "n1", "city": "hz"},
		},
	})
	if !r.rpcOK("GAP-01", updated.GetRet(), err) {
		return
	}
	filter, err := r.room.UpdateAppCustomInfoKeyFilter(ctx, &servicepb.UpdateAppCustomInfoKeyFilterRequest{
		AppId: r.appID, CustomInfoKeyFilter: []string{"nick"},
	})
	if !r.rpcOK("GAP-01", filter.GetRet(), err) {
		return
	}
	got, gerr := r.room.FetchChatroomMemberById(ctx, &servicepb.FetchChatroomMemberByIdRequest{
		AppId: r.appID, RoomId: r.roomID, Uid: r.owner, QueryId: []int64{r.member},
	})
	info := memberOf(got.GetMembers(), r.member)
	if !r.rpcOK("GAP-01", got.GetRet(), gerr) || info == nil {
		r.fail("GAP-01", "过滤后查不到成员")
		return
	}
	if info.GetCustomInfo()["nick"] == "n1" && info.GetCustomInfo()["city"] == "" {
		r.pass("GAP-01", "过滤键只留下 nick，city 已去掉")
	} else {
		r.fail("GAP-01", fmt.Sprintf("资料 %v", info.GetCustomInfo()))
	}
}

func memberOf(members []*servicepb.ChatroomMemberInfo, uid int64) *servicepb.ChatroomMemberInfo {
	for _, m := range members {
		if m.GetUid() == uid {
			return m
		}
	}
	return nil
}

func (r *runner) caseDirected() {
	ctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
	defer cancel()
	owner, member, guest, ok := r.threeClients(ctx, "GAP-02")
	if !ok {
		return
	}
	defer owner.Close()
	defer member.Close()
	defer guest.Close()
	sent, err := r.room.SendChatroomMsg(ctx, &servicepb.ChatroomMsgRequest{
		AppId: r.appID, From: r.owner, To: r.member, ChatroomId: r.roomID,
		MsgType: servicepb.ChatroomMsgType_CHATROOM_MSG_TYPE_TEXT,
		Content: &servicepb.ChatroomMsgRequest_TextContent{TextContent: "only-member"},
		MsgId:   "only-member",
	})
	if err != nil || sent.GetRet().GetErrorCode() != servicepb.ErrorCode_SUCCESS {
		r.fail("GAP-02", fmt.Sprintf("定向发送 %v %s", err, sent.GetRet().GetErrorCode()))
		return
	}
	wait, cancelWait := context.WithTimeout(r.ctx, 8*time.Second)
	err = member.waitPush(wait, clientpb.CmdId_CHATROOM_RECEIVE_MESSAGE, func(body []byte) bool {
		msg := &clientpb.Message{}
		return proto.Unmarshal(body, msg) == nil && msg.GetText() == "only-member"
	})
	cancelWait()
	if err != nil {
		r.fail("GAP-02", "指定成员没有收到: "+err.Error())
		return
	}
	wait, cancelWait = context.WithTimeout(r.ctx, 1500*time.Millisecond)
	err = guest.waitPush(wait, clientpb.CmdId_CHATROOM_RECEIVE_MESSAGE, func(body []byte) bool {
		msg := &clientpb.Message{}
		return proto.Unmarshal(body, msg) == nil && msg.GetText() == "only-member"
	})
	cancelWait()
	if err == nil {
		r.fail("GAP-02", "未指定的成员也收到了定向消息")
		return
	}
	r.pass("GAP-02", "定向消息只到达指定成员")
}

func (r *runner) threeClients(ctx context.Context, id string) (owner, member, guest *link, ok bool) {
	if r.enterRPC(r.owner, adminRole, "owner") != servicepb.ErrorCode_SUCCESS &&
		r.enterRPC(r.owner, adminRole, "owner") != servicepb.ErrorCode_CHATROOM_ALREADY_IN_ROOM {
		r.fail(id, "房主进房失败")
		return
	}
	ownerToken, err := r.login(r.owner)
	memberToken, merr := r.login(r.member)
	guestToken, gerr := r.login(r.guest)
	if err != nil || merr != nil || gerr != nil {
		r.fail(id, "登录令牌失败")
		return
	}
	ownerRoom, code := r.roomToken(r.owner, adminRole, "owner")
	memberRoom, mcode := r.roomToken(r.member, memberRole, "member-bob")
	guestRoom, gcode := r.roomToken(r.guest, memberRole, "guest")
	if code != servicepb.ErrorCode_SUCCESS || mcode != servicepb.ErrorCode_SUCCESS || gcode != servicepb.ErrorCode_SUCCESS {
		r.fail(id, "进房令牌失败")
		return
	}
	owner, err = dialWS(ctx, r.gwWS)
	member, merr = dialWS(ctx, r.gwWS)
	guest, gerr = dialWS(ctx, r.gwWS)
	if err != nil || merr != nil || gerr != nil {
		r.fail(id, "连接网关失败")
		return
	}
	if !r.authEnter(ctx, id, owner, ownerToken, ownerRoom, "gap-owner") ||
		!r.authEnter(ctx, id, member, memberToken, memberRoom, "gap-member") ||
		!r.authEnter(ctx, id, guest, guestToken, guestRoom, "gap-guest") {
		owner.Close()
		member.Close()
		guest.Close()
		return nil, nil, nil, false
	}
	return owner, member, guest, true
}

func (r *runner) authEnter(ctx context.Context, id string, l *link, token, roomToken, device string) bool {
	auth, err := l.auth(ctx, r.appID, token, device)
	if err != nil || auth.GetRet().GetErrorCode() != clientpb.ErrorCode_SUCCESS {
		r.fail(id, device+" 鉴权失败")
		return false
	}
	enter := &clientpb.EnterChatroomResp{}
	if err := l.call(ctx, clientpb.CmdId_CHATROOM_ENTER, &clientpb.EnterChatroomReq{RoomId: r.roomID, Token: roomToken}, enter); err != nil || enter.GetRet().GetErrorCode() != clientpb.ErrorCode_SUCCESS {
		r.fail(id, device+" 进房失败 "+enter.GetRet().GetErrorMsg())
		return false
	}
	return true
}

func (r *runner) caseClientCommands() {
	ctx, cancel := context.WithTimeout(r.ctx, 40*time.Second)
	defer cancel()
	_, _ = r.room.KickChatroomMember(ctx, &servicepb.KickChatroomMemberRequest{
		AppId: r.appID, RoomId: r.roomID, From: r.owner, To: r.guest, Action: 0,
	})
	_, _ = r.room.BlockChatroomMember(ctx, &servicepb.BlockChatroomMemberRequest{
		AppId: r.appID, RoomId: r.roomID, From: r.owner, To: r.guest, Action: 0,
	})
	_, _ = r.room.MuteChatroomMember(ctx, &servicepb.MuteChatroomMemberRequest{
		AppId: r.appID, RoomId: r.roomID, From: r.owner, To: r.member, Action: 0,
	})
	owner, member, guest, ok := r.threeClients(ctx, "GAP-03")
	if !ok {
		return
	}
	defer owner.Close()
	defer member.Close()
	defer guest.Close()

	kick := &clientpb.ChatRoomMemberKickResp{}
	if err := owner.call(ctx, clientpb.CmdId_CHATROOM_MEMBER_KICK, &clientpb.ChatroomMemberKickReq{
		RoomId: r.roomID, UserId: r.guest, Duration: 60,
	}, kick); err != nil || kick.GetRet().GetErrorCode() != clientpb.ErrorCode_SUCCESS {
		r.fail("GAP-03", fmt.Sprintf("客户端踢人 %v %s", err, kick.GetRet().GetErrorMsg()))
		return
	}
	if code := r.enterRPC(r.guest, memberRole, "guest"); code == servicepb.ErrorCode_SUCCESS {
		r.fail("GAP-03", "客户端踢人后仍能进房")
		return
	}
	unkick, uerr := r.room.KickChatroomMember(ctx, &servicepb.KickChatroomMemberRequest{
		AppId: r.appID, RoomId: r.roomID, From: r.owner, To: r.guest, Action: 0,
	})
	if uerr != nil || unkick.GetRet().GetErrorCode() != servicepb.ErrorCode_SUCCESS {
		r.fail("GAP-03", "解除踢出失败")
		return
	}

	mute := &clientpb.SettingMuteMessageResp{}
	if err := owner.call(ctx, clientpb.CmdId_CHATROOM_MEMBER_MUTE, &clientpb.SettingMuteMessageReq{
		Data: &clientpb.ChatroomMemberUpdatedField{RoomId: r.roomID, UserId: r.member, Enable: true}, Duration: 60,
	}, mute); err != nil || mute.GetRet().GetErrorCode() != clientpb.ErrorCode_SUCCESS {
		r.fail("GAP-03", fmt.Sprintf("客户端禁言 %v %s", err, mute.GetRet().GetErrorMsg()))
		return
	}
	if r.sendText(r.member, "client-muted") == servicepb.ErrorCode_SUCCESS {
		r.fail("GAP-03", "客户端禁言后仍能发言")
		return
	}
	unmute := &clientpb.SettingMuteMessageResp{}
	if err := owner.call(ctx, clientpb.CmdId_CHATROOM_MEMBER_MUTE, &clientpb.SettingMuteMessageReq{
		Data: &clientpb.ChatroomMemberUpdatedField{RoomId: r.roomID, UserId: r.member, Enable: false},
	}, unmute); err != nil || unmute.GetRet().GetErrorCode() != clientpb.ErrorCode_SUCCESS {
		r.fail("GAP-03", "客户端解除禁言失败")
		return
	}

	block := &clientpb.SettingChatroomInBlackResp{}
	if err := owner.call(ctx, clientpb.CmdId_CHATROOM_MEMBER_BLOCK, &clientpb.SettingChatroomInBlackReq{
		Data: &clientpb.ChatroomMemberUpdatedField{RoomId: r.roomID, UserId: r.guest, Enable: true},
	}, block); err != nil || block.GetRet().GetErrorCode() != clientpb.ErrorCode_SUCCESS {
		r.fail("GAP-03", fmt.Sprintf("客户端拉黑 %v %s", err, block.GetRet().GetErrorMsg()))
		return
	}
	if r.enterRPC(r.guest, memberRole, "guest") == servicepb.ErrorCode_SUCCESS {
		r.fail("GAP-03", "客户端拉黑后仍能进房")
		return
	}
	unblock := &clientpb.SettingChatroomInBlackResp{}
	if err := owner.call(ctx, clientpb.CmdId_CHATROOM_MEMBER_BLOCK, &clientpb.SettingChatroomInBlackReq{
		Data: &clientpb.ChatroomMemberUpdatedField{RoomId: r.roomID, UserId: r.guest, Enable: false},
	}, unblock); err != nil || unblock.GetRet().GetErrorCode() != clientpb.ErrorCode_SUCCESS {
		r.fail("GAP-03", "客户端解除拉黑失败")
		return
	}

	up := &clientpb.UpChatroomMicroResp{}
	if err := member.call(ctx, clientpb.CmdId_CHATROOM_POSITION_UP, &clientpb.UpChatroomMicroReq{RoomId: r.roomID, Position: 1}, up); err != nil || up.GetRet().GetErrorCode() != clientpb.ErrorCode_SUCCESS {
		r.fail("GAP-03", fmt.Sprintf("客户端上麦 %v %s", err, up.GetRet().GetErrorMsg()))
		return
	}
	down := &clientpb.DownChatroomMicroResp{}
	if err := member.call(ctx, clientpb.CmdId_CHATROOM_POSITION_DOWN, &clientpb.DownChatroomMicroReq{RoomId: r.roomID, Position: 1}, down); err != nil || down.GetRet().GetErrorCode() != clientpb.ErrorCode_SUCCESS {
		r.fail("GAP-03", fmt.Sprintf("客户端下麦 %v %s", err, down.GetRet().GetErrorMsg()))
		return
	}
	r.pass("GAP-03", "客户端踢人、禁言、拉黑、上下麦都经过网关协议")
}

func (r *runner) caseSignature() {
	before := r.cb.len()
	if r.sendText(r.owner, "signed-body") != servicepb.ErrorCode_SUCCESS {
		r.fail("GAP-04", "发送失败，无法核对签名")
		return
	}
	hits := r.waitHits(before, 8*time.Second, func(items []hit) bool { return findHit(items, "/text") != nil })
	item := findHit(hits, "/text")
	if item == nil || !checksumOK(r.secret, *item) {
		r.fail("GAP-04", "没有拿到一份签名正确的抄送")
		return
	}
	tampered := *item
	tampered.body = append([]byte(nil), item.body...)
	if len(tampered.body) == 0 {
		tampered.body = []byte("x")
	} else {
		tampered.body[0] ^= 0xff
	}
	blank := *item
	blank.sum = ""
	replay := checksumOK(r.secret, *item)
	if checksumOK(r.secret, tampered) || checksumOK(r.secret, blank) || !replay {
		r.fail("GAP-04", "篡改或空签名没有被拒绝")
		return
	}
	r.pass("GAP-04", "改一个字节或拿掉签名都会失败。同一份签名可以再次校验，协议没有一次性随机数")
}

func (r *runner) caseQPS() {
	ctx, cancel := r.call(r.ctx)
	defer cancel()
	restore := r.appUpdate("regress-renamed", r.cb.base+"/event", r.cb.base+"/text", r.cb.base+"/custom")
	restore.Qps, restore.TextQps, restore.CustomQps = 1000, 1000, 1000
	defer func() {
		c, cancel := r.call(r.ctx)
		defer cancel()
		_, _ = r.core.UpdateAppInfo(c, restore)
		_, _ = r.core.UpdateAppCacheInfo(c, &servicepb.UpdateAppCacheInfoRequest{AppId: r.appID})
	}()
	limited := r.appUpdate("regress-renamed", r.cb.base+"/event", r.cb.base+"/text", r.cb.base+"/custom")
	limited.Qps, limited.TextQps, limited.CustomQps = 1, 1, 1
	updated, err := r.core.UpdateAppInfo(ctx, limited)
	if !r.rpcOK("GAP-05", updated.GetRet(), err) {
		return
	}
	_, _ = r.core.UpdateAppCacheInfo(ctx, &servicepb.UpdateAppCacheInfoRequest{AppId: r.appID})
	time.Sleep(500 * time.Millisecond)
	before := r.cb.len()
	for i := 0; i < 4; i++ {
		if r.sendText(r.owner, fmt.Sprintf("qps-%d", i)) != servicepb.ErrorCode_SUCCESS {
			r.fail("GAP-05", "限流期间发送失败")
			return
		}
	}
	var first, fourth time.Time
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		n := 0
		for _, item := range r.cb.since(before) {
			if item.path == "/text" && strings.Contains(string(item.body), "qps-") {
				n++
			}
		}
		if n >= 1 && first.IsZero() {
			first = time.Now()
		}
		if n >= 4 {
			fourth = time.Now()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if fourth.IsZero() {
		r.fail("GAP-05", "20 秒内没有收齐 4 条文本抄送")
		return
	}
	span := fourth.Sub(first)
	if span < 2*time.Second {
		r.fail("GAP-05", fmt.Sprintf("4 条抄送间隔 %s，限流没有生效", span.Round(time.Millisecond)))
		return
	}
	r.pass("GAP-05", fmt.Sprintf("文本 QPS 设为 1 后，4 条抄送拉开了 %s", span.Round(time.Millisecond)))
}

func (r *runner) caseHotConfig() {
	_, _, session := r.do(http.MethodPost, r.admin+"/api/login", map[string]string{"password": r.password}, nil)
	if session == nil {
		r.fail("GAP-06", "登录管理后台失败")
		return
	}
	original, ok := r.nacosGet(session, "gateway")
	if !ok {
		r.fail("GAP-06", "读不到网关配置")
		return
	}
	defer r.nacosPut(session, "gateway", original)

	debug := strings.Replace(original, "level: info", "level: debug", 1)
	if debug == original || r.nacosPut(session, "gateway", debug) != http.StatusNoContent {
		r.fail("GAP-07", "没能把网关日志级别改成 debug")
	} else {
		time.Sleep(8 * time.Second)
		if r.sendText(r.owner, "gap-log-probe") != servicepb.ErrorCode_SUCCESS {
			r.fail("GAP-07", "探针消息发送失败")
		} else {
			r.pass("GAP-07", "已把网关日志改为 debug 并发送探针消息 gap-log-probe")
		}
		if r.nacosPut(session, "gateway", original) != http.StatusNoContent {
			r.fail("GAP-07", "日志级别没有改回去")
		}
	}

	keyword := strings.Replace(original, "type: none", "type: keyword", 1)
	if keyword == original || r.nacosPut(session, "gateway", keyword) != http.StatusNoContent {
		r.fail("GAP-06", "没能打开关键词审核")
		return
	}
	ctx, cancel := context.WithTimeout(r.ctx, 40*time.Second)
	defer cancel()
	token, err := r.login(r.owner)
	roomToken, code := r.roomToken(r.owner, adminRole, "owner")
	if err != nil || code != servicepb.ErrorCode_SUCCESS {
		r.fail("GAP-06", "审核用例登录或进房令牌失败")
		return
	}
	l, err := dialWS(ctx, r.gwWS)
	if err != nil || !r.authEnter(ctx, "GAP-06", l, token, roomToken, "gap-mod") {
		r.fail("GAP-06", "审核用例连接失败")
		return
	}
	defer l.Close()
	var rejected, allowed bool
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && !rejected {
		sent := &clientpb.SendMessageResp{}
		err = l.call(ctx, clientpb.CmdId_CHATROOM_SEND_MESSAGE, textReq(r.roomID, "forbiddenword"), sent)
		if err == nil && sent.GetRet().GetErrorCode() == clientpb.ErrorCode_MARS_AGENT_GD_ILLEGAL {
			rejected = true
			break
		}
		time.Sleep(time.Second)
	}
	clean := &clientpb.SendMessageResp{}
	err = l.call(ctx, clientpb.CmdId_CHATROOM_SEND_MESSAGE, textReq(r.roomID, "ordinary-text"), clean)
	allowed = err == nil && clean.GetRet().GetErrorCode() == clientpb.ErrorCode_SUCCESS
	if !rejected || !allowed {
		r.fail("GAP-06", fmt.Sprintf("关键词拒绝 %v，普通文本 %s", rejected, clean.GetRet().GetErrorMsg()))
		return
	}
	r.pass("GAP-06", "关键词 forbiddenword 被拒绝，普通文本仍可发送")

	receipt := strings.Replace(original, "message_receipt: false", "message_receipt: true", 1)
	if receipt == original || r.nacosPut(session, "gateway", receipt) != http.StatusNoContent {
		r.fail("GAP-08", "没能写入回执开关")
		return
	}
	time.Sleep(3 * time.Second)
	memberToken, merr := r.login(r.member)
	memberRoom, mcode := r.roomToken(r.member, memberRole, "member-bob")
	other, oerr := dialWS(ctx, r.gwWS)
	if merr != nil || mcode != servicepb.ErrorCode_SUCCESS || oerr != nil || !r.authEnter(ctx, "GAP-08", other, memberToken, memberRoom, "gap-receipt") {
		if other != nil {
			other.Close()
		}
		return
	}
	defer other.Close()
	probe := &clientpb.SendMessageResp{}
	if err := l.call(ctx, clientpb.CmdId_CHATROOM_SEND_MESSAGE, textReq(r.roomID, "receipt-probe"), probe); err != nil || probe.GetRet().GetErrorCode() != clientpb.ErrorCode_SUCCESS {
		r.fail("GAP-08", "回执探针发送失败")
		return
	}
	wait, cancelWait := context.WithTimeout(r.ctx, 8*time.Second)
	var msg clientpb.Message
	err = other.waitPush(wait, clientpb.CmdId_CHATROOM_RECEIVE_MESSAGE, func(body []byte) bool {
		if proto.Unmarshal(body, &msg) != nil || msg.GetText() != "receipt-probe" {
			return false
		}
		return true
	})
	cancelWait()
	if err != nil {
		r.fail("GAP-08", "没有收到回执探针: "+err.Error())
		return
	}
	if msg.GetSetting().GetReceiptForReceive() {
		r.fail("GAP-08", "未重启网关，回执开关却已经生效")
		return
	}
	r.pass("GAP-08", "未重启网关时，下发的消息仍不要求回执")
}

func (r *runner) caseBeta() {
	_, _, session := r.do(http.MethodPost, r.admin+"/api/login", map[string]string{"password": r.password}, nil)
	if session == nil {
		r.fail("GAP-09", "登录失败")
		return
	}
	content, ok := r.nacosGet(session, "builder")
	if !ok {
		r.fail("GAP-09", "读不到 builder 配置")
		return
	}
	defer func() {
		r.do(http.MethodDelete, r.admin+"/api/config/builder/beta", nil, session)
	}()
	code, body, _ := r.do(http.MethodPut, r.admin+"/api/config/builder/beta", map[string]any{
		"content": content, "ips": []string{"10.255.0.8"},
	}, session)
	if code != http.StatusNoContent {
		r.fail("GAP-09", fmt.Sprintf("发布灰度 %d %s", code, trim(body)))
		return
	}
	code, body, _ = r.do(http.MethodGet, r.admin+"/api/config/builder/beta", nil, session)
	var beta struct {
		Content string   `json:"content"`
		IPs     []string `json:"ips"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &beta) != nil || beta.Content != content || len(beta.IPs) != 1 || beta.IPs[0] != "10.255.0.8" {
		r.fail("GAP-09", fmt.Sprintf("读回灰度 %d %s", code, trim(body)))
		return
	}
	code, _, _ = r.do(http.MethodDelete, r.admin+"/api/config/builder/beta", nil, session)
	if code != http.StatusNoContent {
		r.fail("GAP-09", fmt.Sprintf("删除灰度 %d", code))
		return
	}
	r.pass("GAP-09", "合法 IP 的灰度可以发布、读回并删除")
}

func (r *runner) caseConcurrency() {
	ctx, cancel := context.WithTimeout(r.ctx, 20*time.Second)
	defer cancel()
	_ = r.enterRPC(r.owner, adminRole, "owner")
	_ = r.enterRPC(r.member, memberRole, "member-bob")
	codes := make([]servicepb.ErrorCode, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	for i, uid := range []int64{r.owner, r.member} {
		go func(i int, uid int64) {
			defer wg.Done()
			c, cancel := context.WithTimeout(r.ctx, 15*time.Second)
			defer cancel()
			reply, err := r.room.UpMic(c, &servicepb.UpMicRequest{AppId: r.appID, RoomId: r.roomID, Uid: uid, MicIndex: 1})
			if err != nil {
				codes[i] = servicepb.ErrorCode_SERVER_INTERNAL_ERROR
				return
			}
			codes[i] = reply.GetRet().GetErrorCode()
		}(i, uid)
	}
	wg.Wait()
	wins := 0
	for _, code := range codes {
		if code == servicepb.ErrorCode_SUCCESS {
			wins++
		}
	}
	_, _ = r.room.DownMic(ctx, &servicepb.DownMicRequest{AppId: r.appID, RoomId: r.roomID, Uid: r.owner, MicIndex: 1})
	_, _ = r.room.DownMic(ctx, &servicepb.DownMicRequest{AppId: r.appID, RoomId: r.roomID, Uid: r.member, MicIndex: 1})
	if wins != 1 {
		r.fail("GAP-10", fmt.Sprintf("同时上同一麦位成功 %d 次 %s %s", wins, codes[0], codes[1]))
		return
	}

	names := []string{"race-a", "race-b"}
	editCodes := make([]servicepb.ErrorCode, 2)
	wg.Add(2)
	for i, name := range names {
		go func(i int, name string) {
			defer wg.Done()
			c, cancel := context.WithTimeout(r.ctx, 15*time.Second)
			defer cancel()
			reply, err := r.room.EditChatroomInfo(c, &servicepb.EditChatroomInfoRequest{
				AppId: r.appID, RoomId: r.roomID,
				ChatRoomInfo: &servicepb.ChatroomInfo{ChatroomId: r.roomID, Name: name, Announcement: name, OwnerUid: r.owner},
			})
			if err != nil {
				editCodes[i] = servicepb.ErrorCode_SERVER_INTERNAL_ERROR
				return
			}
			editCodes[i] = reply.GetRet().GetErrorCode()
		}(i, name)
	}
	wg.Wait()
	got, err := r.room.GetChatroomInfo(ctx, &servicepb.GetChatroomInfoRequest{AppId: r.appID, RoomId: r.roomID})
	finalName := got.GetRoomInfo().GetName()
	if err != nil || (finalName != "race-a" && finalName != "race-b") || editCodes[0] != servicepb.ErrorCode_SUCCESS || editCodes[1] != servicepb.ErrorCode_SUCCESS {
		r.fail("GAP-10", fmt.Sprintf("并发改名 %s %s 最终 %q", editCodes[0], editCodes[1], finalName))
		return
	}
	r.pass("GAP-10", "同一麦位只有一人成功；并发改名后房间名是其中一次写入")
}

func (r *runner) caseSession() {
	var last int
	for i := 0; i < 6; i++ {
		last, _, _ = r.do(http.MethodPost, r.admin+"/api/login", map[string]string{"password": "wrong-password"}, nil)
		if last != http.StatusUnauthorized {
			r.fail("GAP-12", fmt.Sprintf("第 %d 次错误口令状态 %d", i+1, last))
			return
		}
	}
	code, _, session := r.do(http.MethodPost, r.admin+"/api/login", map[string]string{"password": r.password}, nil)
	if code != http.StatusNoContent || session == nil {
		r.fail("GAP-12", "连续错误口令之后正确口令无法登录")
		return
	}
	r.pass("GAP-12", "连续 6 次错误口令都是 401，没有锁定，正确口令仍可登录")

	if r.mysql == "" {
		r.fail("GAP-11", "没有 MYSQL_PASSWORD，无法把会话改成过期")
		return
	}
	raw, err := hex.DecodeString(session.Value)
	if err != nil || len(raw) != 32 {
		r.fail("GAP-11", "会话 cookie 无法解码")
		return
	}
	sum := sha256.Sum256(raw)
	db, err := sql.Open("mysql", fmt.Sprintf("root:%s@tcp(mysql:3306)/orbikit_admin?parseTime=true", r.mysql))
	if err != nil {
		r.fail("GAP-11", err.Error())
		return
	}
	defer db.Close()
	res, err := db.ExecContext(r.ctx, "UPDATE sessions SET expires_at = ? WHERE token_hash = ?", time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), sum[:])
	if err != nil {
		r.fail("GAP-11", err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		r.fail("GAP-11", fmt.Sprintf("过期更新影响 %d 行", n))
		return
	}
	code, _, _ = r.do(http.MethodGet, r.admin+"/api/me", nil, session)
	if code != http.StatusUnauthorized {
		r.fail("GAP-11", fmt.Sprintf("过期会话状态 %d", code))
		return
	}
	code, _, _ = r.do(http.MethodPost, r.admin+"/api/login", map[string]string{"password": r.password}, nil)
	if code != http.StatusNoContent {
		r.fail("GAP-11", "过期后重新登录失败")
		return
	}
	r.pass("GAP-11", "把数据库里的过期时间改到过去后，会话被拒绝，重新登录成功")
}

func (r *runner) caseReleaseSurface() {
	code, body, _ := r.do(http.MethodGet, r.builder+"/v1/releases/images?service=webhook", nil, nil)
	if code != http.StatusOK {
		r.fail("BLD-08", fmt.Sprintf("镜像列表 %d %s", code, trim(body)))
		return
	}
	code, body, _ = r.do(http.MethodGet, r.builder+"/v1/releases/refs", nil, nil)
	if code == http.StatusBadRequest && contains(body, "凭证") {
		r.pass("BLD-08", "镜像列表可读。没有 Git 凭证时，远端引用被拒绝")
		return
	}
	if code != http.StatusOK || !contains(body, "refs") {
		r.fail("BLD-08", fmt.Sprintf("远端引用 %d %s", code, trim(body)))
		return
	}
	r.pass("BLD-08", "镜像列表可读，Git 凭证已配置，远端引用能列出来。回滚会替换正在运行的容器，放在单独的发布阶段")
}

func (r *runner) caseLoad() {
	ctx, cancel := context.WithTimeout(r.ctx, 40*time.Second)
	defer cancel()
	token, err := r.login(r.owner)
	roomToken, code := r.roomToken(r.owner, adminRole, "owner")
	if err != nil || code != servicepb.ErrorCode_SUCCESS {
		r.fail("GAP-13", "压测登录失败")
		return
	}
	l, err := dialWS(ctx, r.gwWS)
	if err != nil || !r.authEnter(ctx, "GAP-13", l, token, roomToken, "gap-load") {
		if l != nil {
			l.Close()
		}
		return
	}
	defer l.Close()
	start := time.Now()
	var slowest time.Duration
	for i := 0; i < 30; i++ {
		one := time.Now()
		sent := &clientpb.SendMessageResp{}
		err = l.call(ctx, clientpb.CmdId_CHATROOM_SEND_MESSAGE, textReq(r.roomID, fmt.Sprintf("load-%d", i)), sent)
		elapsed := time.Since(one)
		if elapsed > slowest {
			slowest = elapsed
		}
		if err != nil || sent.GetRet().GetErrorCode() != clientpb.ErrorCode_SUCCESS {
			r.fail("GAP-13", fmt.Sprintf("第 %d 条 %v %s", i, err, sent.GetRet().GetErrorMsg()))
			return
		}
	}
	total := time.Since(start)
	if total > 20*time.Second {
		r.fail("GAP-13", fmt.Sprintf("30 条用了 %s", total.Round(time.Millisecond)))
		return
	}
	r.pass("GAP-13", fmt.Sprintf("30 条房间消息 %s，最慢一条 %s", total.Round(time.Millisecond), slowest.Round(time.Millisecond)))

	for i := 0; i < 15; i++ {
		c, err := dialWS(ctx, r.gwWS)
		if err != nil {
			r.fail("GAP-14", err.Error())
			return
		}
		auth, aerr := c.auth(ctx, r.appID, token, fmt.Sprintf("churn-%d", i))
		c.Close()
		if aerr != nil || auth.GetRet().GetErrorCode() != clientpb.ErrorCode_SUCCESS {
			r.fail("GAP-14", fmt.Sprintf("第 %d 次连接鉴权失败", i))
			return
		}
	}
	again, err := dialWS(ctx, r.gwWS)
	if err != nil {
		r.fail("GAP-14", err.Error())
		return
	}
	defer again.Close()
	auth, aerr := again.auth(ctx, r.appID, token, "churn-last")
	if aerr != nil || auth.GetRet().GetErrorCode() != clientpb.ErrorCode_SUCCESS {
		r.fail("GAP-14", "反复连接后新的鉴权失败")
		return
	}
	r.pass("GAP-14", "连续 15 次连接并关闭后，新连接仍能鉴权")
}

func (r *runner) caseOfflineLeave() {
	ctx, cancel := context.WithTimeout(r.ctx, 90*time.Second)
	defer cancel()
	uid := r.owner + 20
	token, err := r.login(uid)
	roomToken, code := r.roomToken(uid, memberRole, "leaver")
	if err != nil || code != servicepb.ErrorCode_SUCCESS {
		r.fail("GAP-15", "离线用例登录或进房令牌失败")
		return
	}
	l, err := dialWS(ctx, r.gwWS)
	if err != nil {
		r.fail("GAP-15", err.Error())
		return
	}
	if !r.authEnter(ctx, "GAP-15", l, token, roomToken, "gap-leaver") {
		l.Close()
		return
	}
	l.Close()
	if !r.inRoom(uid) {
		r.fail("GAP-15", "连接刚断开就已经不在房间")
		return
	}
	deadline := time.Now().Add(80 * time.Second)
	for time.Now().Before(deadline) {
		if !r.inRoom(uid) {
			r.pass("GAP-15", "断开连接后仍在房间，宽限时间过后自动离开")
			return
		}
		time.Sleep(2 * time.Second)
	}
	r.fail("GAP-15", "80 秒后用户仍在房间")
}

func (r *runner) inRoom(uid int64) bool {
	ctx, cancel := r.call(r.ctx)
	defer cancel()
	list, err := r.room.GetUserCurrentChatroomList(ctx, &servicepb.GetUserCurrentChatroomListRequest{AppId: r.appID, Uid: uid})
	return err == nil && list.GetRet().GetErrorCode() == servicepb.ErrorCode_SUCCESS && hasRoom(list.GetRoomInfo(), r.roomID)
}

func (r *runner) nacosGet(session *http.Cookie, service string) (string, bool) {
	code, body, _ := r.do(http.MethodGet, r.admin+"/api/config/"+service, nil, session)
	var current struct {
		Content string `json:"content"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &current) != nil || current.Content == "" {
		return "", false
	}
	return current.Content, true
}

func (r *runner) nacosPut(session *http.Cookie, service, content string) int {
	code, _, _ := r.do(http.MethodPut, r.admin+"/api/config/"+service, map[string]string{"content": content}, session)
	return code
}

func (r *runner) caseReceipt() {
	ctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
	defer cancel()
	token, err := r.login(r.owner)
	memberToken, merr := r.login(r.member)
	if err != nil || merr != nil {
		r.fail("RCP-01", "登录失败")
		return
	}
	ownerRoom, code := r.roomToken(r.owner, adminRole, "owner")
	memberRoom, mcode := r.roomToken(r.member, memberRole, "member")
	if code != servicepb.ErrorCode_SUCCESS || mcode != servicepb.ErrorCode_SUCCESS {
		r.fail("RCP-01", "进房令牌失败")
		return
	}
	a, err := dialWS(ctx, r.gwWS)
	b, berr := dialWS(ctx, r.gwWS)
	if err != nil || berr != nil {
		r.fail("RCP-01", "连接失败")
		return
	}
	defer a.Close()
	defer b.Close()
	if !r.authEnter(ctx, "RCP-01", a, token, ownerRoom, "receipt-owner") || !r.authEnter(ctx, "RCP-01", b, memberToken, memberRoom, "receipt-member") {
		return
	}
	sent := &clientpb.SendMessageResp{}
	if err := a.call(ctx, clientpb.CmdId_CHATROOM_SEND_MESSAGE, textReq(r.roomID, "receipt-on"), sent); err != nil || sent.GetRet().GetErrorCode() != clientpb.ErrorCode_SUCCESS {
		r.fail("RCP-01", fmt.Sprintf("发送 %v %s", err, sent.GetRet().GetErrorMsg()))
		return
	}
	wait, cancelWait := context.WithTimeout(r.ctx, 8*time.Second)
	var msg clientpb.Message
	err = b.waitPush(wait, clientpb.CmdId_CHATROOM_RECEIVE_MESSAGE, func(body []byte) bool {
		if proto.Unmarshal(body, &msg) != nil || msg.GetText() != "receipt-on" {
			return false
		}
		return true
	})
	cancelWait()
	if err != nil || !msg.GetSetting().GetReceiptForReceive() {
		r.fail("RCP-01", fmt.Sprintf("回执标记 %v on=%v", err, msg.GetSetting().GetReceiptForReceive()))
		return
	}
	ack := &clientpb.MessageReceiptForReceiveResp{}
	if err := b.call(ctx, clientpb.CmdId_CHATROOM_MESSAGE_RECEIPT_FOR_RECEIVE, &clientpb.MessageReceiptForReceiveReq{MessageId: msg.GetMessageId()}, ack); err != nil || ack.GetRet().GetErrorCode() != clientpb.ErrorCode_SUCCESS {
		r.fail("RCP-01", fmt.Sprintf("回执 %v %s", err, ack.GetRet().GetErrorMsg()))
		return
	}
	r.pass("RCP-01", "重启后的网关要求回执，客户端回执被接受")
}

func (r *runner) caseFanout() {
	if r.wsB == "" {
		r.fail("FAN-01", "没有第二个网关地址")
		return
	}
	ctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
	defer cancel()
	ownerToken, err := r.login(r.owner)
	memberToken, merr := r.login(r.member)
	if err != nil || merr != nil {
		r.fail("FAN-01", "登录失败")
		return
	}
	ownerRoom, code := r.roomToken(r.owner, adminRole, "owner")
	memberRoom, mcode := r.roomToken(r.member, memberRole, "member")
	if code != servicepb.ErrorCode_SUCCESS || mcode != servicepb.ErrorCode_SUCCESS {
		r.fail("FAN-01", "进房令牌失败")
		return
	}
	a, err := dialWS(ctx, r.gwWS)
	b, berr := dialWS(ctx, r.wsB)
	if err != nil || berr != nil {
		r.fail("FAN-01", fmt.Sprintf("连接 %v %v", err, berr))
		return
	}
	defer a.Close()
	defer b.Close()
	if !r.authEnter(ctx, "FAN-01", a, ownerToken, ownerRoom, "fan-a") || !r.authEnter(ctx, "FAN-01", b, memberToken, memberRoom, "fan-b") {
		return
	}
	sent := &clientpb.SendMessageResp{}
	if err := a.call(ctx, clientpb.CmdId_CHATROOM_SEND_MESSAGE, textReq(r.roomID, "fanout-room"), sent); err != nil || sent.GetRet().GetErrorCode() != clientpb.ErrorCode_SUCCESS {
		r.fail("FAN-01", fmt.Sprintf("房间消息 %v %s", err, sent.GetRet().GetErrorMsg()))
		return
	}
	wait, cancelWait := context.WithTimeout(r.ctx, 8*time.Second)
	err = b.waitPush(wait, clientpb.CmdId_CHATROOM_RECEIVE_MESSAGE, func(body []byte) bool {
		msg := &clientpb.Message{}
		return proto.Unmarshal(body, msg) == nil && msg.GetText() == "fanout-room"
	})
	cancelWait()
	if err != nil {
		r.fail("FAN-01", "第二个网关上的成员没有收到房间消息: "+err.Error())
		return
	}
	full, ferr := r.room.SendFullServiceBroadcastMsg(ctx, &servicepb.FullServiceBroadcastMsgRequest{
		AppId: r.appID, MsgType: servicepb.ChatroomMsgType_CHATROOM_MSG_TYPE_TEXT,
		Content: &servicepb.FullServiceBroadcastMsgRequest_TextContent{TextContent: "fanout-all"},
		MsgId:   "fanout-all",
	})
	if ferr != nil || full.GetRet().GetErrorCode() != servicepb.ErrorCode_SUCCESS {
		r.fail("FAN-01", "全服广播发送失败")
		return
	}
	wait, cancelWait = context.WithTimeout(r.ctx, 8*time.Second)
	err = b.waitPush(wait, clientpb.CmdId_CHATROOM_RECEIVE_MESSAGE, func(body []byte) bool {
		msg := &clientpb.Message{}
		return proto.Unmarshal(body, msg) == nil && msg.GetText() == "fanout-all"
	})
	cancelWait()
	if err != nil {
		r.fail("FAN-01", "第二个网关没有收到全服广播: "+err.Error())
		return
	}
	r.pass("FAN-01", "两个网关都在线时，房间消息和全服广播都能到达另一个网关上的连接")
}

func (r *runner) caseRecover() {
	ctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
	defer cancel()
	token, err := r.login(r.owner)
	roomToken, code := r.roomToken(r.owner, adminRole, "owner")
	if err != nil || code != servicepb.ErrorCode_SUCCESS {
		r.fail("REC-01", fmt.Sprintf("登录或进房令牌 %v %s", err, code))
		return
	}
	l, err := dialWS(ctx, r.gwWS)
	if err != nil || !r.authEnter(ctx, "REC-01", l, token, roomToken, "recover") {
		if l != nil {
			l.Close()
		}
		return
	}
	defer l.Close()
	memberToken, merr := r.login(r.member)
	memberRoom, mcode := r.roomToken(r.member, memberRole, "member")
	b, berr := dialWS(ctx, r.gwWS)
	if merr != nil || mcode != servicepb.ErrorCode_SUCCESS || berr != nil || !r.authEnter(ctx, "REC-01", b, memberToken, memberRoom, "recover-b") {
		if b != nil {
			b.Close()
		}
		return
	}
	defer b.Close()
	sent := &clientpb.SendMessageResp{}
	if err := l.call(ctx, clientpb.CmdId_CHATROOM_SEND_MESSAGE, textReq(r.roomID, "after-restart"), sent); err != nil || sent.GetRet().GetErrorCode() != clientpb.ErrorCode_SUCCESS {
		r.fail("REC-01", fmt.Sprintf("发送 %v %s", err, sent.GetRet().GetErrorMsg()))
		return
	}
	wait, cancelWait := context.WithTimeout(r.ctx, 12*time.Second)
	err = b.waitPush(wait, clientpb.CmdId_CHATROOM_RECEIVE_MESSAGE, func(body []byte) bool {
		msg := &clientpb.Message{}
		return proto.Unmarshal(body, msg) == nil && msg.GetText() == "after-restart"
	})
	cancelWait()
	if err != nil {
		r.fail("REC-01", "重启后消息没有送达: "+err.Error())
		return
	}
	code2, _, session := r.do(http.MethodPost, r.admin+"/api/login", map[string]string{"password": r.password}, nil)
	cfgCode, _, _ := r.do(http.MethodGet, r.admin+"/api/config/admin", nil, session)
	clusterCode, _, _ := r.do(http.MethodGet, r.admin+"/api/cluster", nil, session)
	if code2 != http.StatusNoContent || cfgCode != http.StatusOK || clusterCode != http.StatusOK {
		r.fail("REC-01", fmt.Sprintf("管理后台 登录 %d 配置 %d 集群 %d", code2, cfgCode, clusterCode))
		return
	}
	r.pass("REC-01", "应用、进房、消息、管理后台配置和集群查询都恢复了")
}
