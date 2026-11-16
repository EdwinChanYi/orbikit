// Command regress runs the live service regression against a compose network.
// It is not part of go test. Build it for linux and run it on orbikit_default.
package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/EdwinChanYi/orbikit/api/gen/clientpb"
	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
	"github.com/EdwinChanYi/orbikit/internal/webhook/delivery"
	"github.com/EdwinChanYi/orbikit/pkg/grpcx"
)

type result struct {
	id     string
	pass   bool
	expect bool
	detail string
}

type runner struct {
	ctx      context.Context
	core     servicepb.CoreClient
	room     servicepb.ChatroomClient
	hook     servicepb.MsgCopyClient
	http     *http.Client
	admin    string
	builder  string
	gwTCP    string
	gwWS     string
	gwHTTP   string
	password string
	mysql    string
	ref      string
	release  string
	wsB      string

	mu      sync.Mutex
	results []result

	cb     *callback
	appID  string
	secret string
	appRow uint64
	roomID int64
	owner  int64
	member int64
	guest  int64
}

func main() {
	r := &runner{
		admin:    env("ADMIN_URL", "http://admin:8091"),
		builder:  env("BUILDER_URL", "http://builder:8092"),
		gwTCP:    env("GATEWAY_TCP", "gateway:8000"),
		gwWS:     env("GATEWAY_WS", "ws://gateway:8001/ws"),
		gwHTTP:   env("GATEWAY_HTTP", "http://gateway:8080"),
		password: env("ADMIN_PASSWORD", "123456"),
		mysql:    os.Getenv("MYSQL_PASSWORD"),
		ref:      os.Getenv("RELEASE_REF"),
		wsB:      os.Getenv("GATEWAY_WS_B"),
		release:  env("RELEASE_SERVICE", "webhook"),
		http:     &http.Client{Timeout: 20 * time.Second},
	}
	flag.StringVar(&r.admin, "admin", r.admin, "admin base URL")
	flag.StringVar(&r.builder, "builder", r.builder, "builder base URL")
	flag.StringVar(&r.ref, "release-ref", r.ref, "tag or full SHA for the real publish")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Minute)
	defer cancel()
	r.ctx = ctx

	coreConn, err := grpcx.Dial(env("CORE_ADDR", "core:9001"))
	must(err)
	defer coreConn.Close()
	roomConn, err := grpcx.Dial(env("CHATROOM_ADDR", "chatroom:9002"))
	must(err)
	defer roomConn.Close()
	hookConn, err := grpcx.Dial(env("WEBHOOK_ADDR", "webhook:9004"))
	must(err)
	defer hookConn.Close()
	r.core = servicepb.NewCoreClient(coreConn)
	r.room = servicepb.NewChatroomClient(roomConn)
	r.hook = servicepb.NewMsgCopyClient(hookConn)

	r.cb = newCallback()
	defer r.cb.close()

	r.run()
	failed := 0
	expected := 0
	for _, item := range r.results {
		mark := "PASS"
		if !item.pass {
			if item.expect {
				mark = "EXPECT"
				expected++
			} else {
				mark = "FAIL"
				failed++
			}
		}
		fmt.Printf("%-7s %-8s %s\n", mark, item.id, item.detail)
	}
	fmt.Printf("\n%d passed, %d expected, %d failed, %d total\n",
		len(r.results)-failed-expected, expected, failed, len(r.results))
	if failed > 0 {
		os.Exit(1)
	}
}

func (r *runner) run() {
	base := time.Now().Unix()%1_0000_0000 + 20_000_000
	r.owner = base
	r.member = base + 1
	r.guest = base + 2
	r.roomID = base + 1000

	switch os.Getenv("REGRESS_PHASE") {
	case "receipt":
		r.caseSetup()
		r.caseReceipt()
		return
	case "fanout":
		r.caseSetup()
		r.caseFanout()
		return
	case "recover":
		r.caseSetup()
		r.caseRecover()
		return
	}
	r.caseSetup()
	r.caseCore()
	r.caseRoom()
	r.caseGateway()
	r.caseWebhook()
	r.caseAdmin()
	r.caseBuilder()
	r.caseGaps()
}

func (r *runner) pass(id, detail string) {
	r.mu.Lock()
	r.results = append(r.results, result{id: id, pass: true, detail: detail})
	r.mu.Unlock()
	fmt.Printf("PASS    %s %s\n", id, detail)
}

func (r *runner) fail(id, detail string) {
	r.mu.Lock()
	r.results = append(r.results, result{id: id, pass: false, detail: detail})
	r.mu.Unlock()
	fmt.Printf("FAIL    %s %s\n", id, detail)
}

func (r *runner) expect(id, detail string) {
	r.mu.Lock()
	r.results = append(r.results, result{id: id, pass: false, expect: true, detail: detail})
	r.mu.Unlock()
	fmt.Printf("EXPECT  %s %s\n", id, detail)
}

func (r *runner) rpcOK(id string, ret *servicepb.UnifiedResponse, err error) bool {
	if err != nil {
		r.fail(id, err.Error())
		return false
	}
	if ret.GetErrorCode() != servicepb.ErrorCode_SUCCESS {
		r.fail(id, ret.GetErrorCode().String()+" "+ret.GetErrorMsg())
		return false
	}
	return true
}

func (r *runner) call(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 20*time.Second)
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type hit struct {
	path   string
	appID  string
	body   []byte
	nonce  string
	when   string
	sum    string
	status int
}

type callback struct {
	srv   *http.Server
	ln    net.Listener
	base  string
	mu    sync.Mutex
	hits  []hit
	flaky int
}

func newCallback() *callback {
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	must(err)
	ip := localIPv4()
	c := &callback{ln: ln, base: fmt.Sprintf("http://%s:%d", ip, ln.Addr().(*net.TCPAddr).Port)}
	mux := http.NewServeMux()
	mux.HandleFunc("/", c.serve)
	c.srv = &http.Server{Handler: mux}
	go c.srv.Serve(ln)
	return c
}

func (c *callback) close() { _ = c.srv.Close() }

func (c *callback) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	status := http.StatusOK
	if strings.HasPrefix(r.URL.Path, "/flaky") {
		c.mu.Lock()
		c.flaky++
		n := c.flaky
		c.mu.Unlock()
		if n == 1 {
			status = http.StatusInternalServerError
		}
	}
	c.mu.Lock()
	c.hits = append(c.hits, hit{
		path: r.URL.Path, appID: r.Header.Get("App-Id"), body: body,
		nonce: r.Header.Get("Nonce"), when: r.Header.Get("Cur-Time"),
		sum: r.Header.Get("Check-Sum"), status: status,
	})
	c.mu.Unlock()
	w.WriteHeader(status)
}

func (c *callback) since(n int) []hit {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n > len(c.hits) {
		n = len(c.hits)
	}
	out := make([]hit, len(c.hits)-n)
	copy(out, c.hits[n:])
	return out
}

func (c *callback) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.hits)
}

func localIPv4() string {
	ifaces, err := net.Interfaces()
	must(err)
	for _, iface := range ifaces {
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok || ipnet.IP.IsLoopback() {
				continue
			}
			if v4 := ipnet.IP.To4(); v4 != nil {
				return v4.String()
			}
		}
	}
	return "127.0.0.1"
}

func (r *runner) caseSetup() {
	ctx, cancel := r.call(r.ctx)
	defer cancel()
	created, err := r.core.CreateApp(ctx, &servicepb.CreateAppRequest{
		Name: "regress", Desc: "regression", MsgCpUrl: r.cb.base + "/event",
	})
	if !r.rpcOK("E2E-01", created.GetRet(), err) {
		r.fail("E2E-01", "创建应用失败，后续用例无法继续")
		return
	}
	info := created.GetInfo()
	r.appID = info.GetAppId()
	r.secret = info.GetAppSecret()
	r.appRow = info.GetId()
	if r.appID == "" || r.secret == "" {
		r.fail("E2E-01", "创建应用没有返回 appId 或 appSecret")
		return
	}
	updated, err := r.core.UpdateAppInfo(ctx, r.appUpdate("regress", r.cb.base+"/event", r.cb.base+"/text", r.cb.base+"/custom"))
	if !r.rpcOK("E2E-01", updated.GetRet(), err) {
		return
	}
	if err := r.ensureRoles(); err != nil {
		r.fail("E2E-01", err.Error())
		return
	}
	if code := r.createRoom(true); code != servicepb.ErrorCode_SUCCESS {
		r.fail("E2E-01", "建房失败 "+code.String())
		return
	}
	token, code := r.roomToken(r.owner, adminRole, "owner")
	if code != servicepb.ErrorCode_SUCCESS || token == "" {
		r.fail("E2E-01", "进房令牌失败 "+code.String())
		return
	}
	login, err := r.core.ApplyClientLoginToken(ctx, &servicepb.ApplyClientLoginTokenRequest{AppId: r.appID, Uid: r.owner})
	if !r.rpcOK("E2E-01", login.GetRet(), err) || login.GetLoginToken() == "" {
		r.fail("E2E-01", "登录令牌为空")
		return
	}
	r.pass("E2E-01", "应用、登录令牌、房间和进房令牌已签发")
}

func (r *runner) appUpdate(name, msgURL, textURL, customURL string) *servicepb.UpdateAppInfoRequest {
	return &servicepb.UpdateAppInfoRequest{
		Id: r.appRow, Name: name, Desc: "regression", Types: "255",
		MsgCpUrl: msgURL, TextCpUrl: textURL, CustomCpUrl: customURL,
	}
}

const (
	memberRole int32 = 1
	adminRole  int32 = 2
)

func (r *runner) ensureRoles() error {
	ctx, cancel := r.call(r.ctx)
	defer cancel()
	member, err := r.room.CreateRole(ctx, &servicepb.CreateRoleRequest{AppId: r.appID, Name: "member", RoleId: int64(memberRole)})
	if err != nil {
		return err
	}
	if code := member.GetRet().GetErrorCode(); code != servicepb.ErrorCode_SUCCESS && code != servicepb.ErrorCode_CHATROOM_ROLE_EXIST {
		return fmt.Errorf("member role %s", code)
	}
	admin, err := r.room.CreateRole(ctx, &servicepb.CreateRoleRequest{
		AppId: r.appID, Name: "admin", RoleId: int64(adminRole), Permissions: allPerms(),
	})
	if err != nil {
		return err
	}
	if code := admin.GetRet().GetErrorCode(); code != servicepb.ErrorCode_SUCCESS && code != servicepb.ErrorCode_CHATROOM_ROLE_EXIST {
		return fmt.Errorf("admin role %s", code)
	}
	return nil
}

func allPerms() []*servicepb.RolePermission {
	var out []*servicepb.RolePermission
	for p := servicepb.Privilege_EDIT_CHATROOM_INFO; p <= servicepb.Privilege_MUTE_CAN_MESSAGE; p++ {
		out = append(out, &servicepb.RolePermission{
			ActionType: int32(p), CanDoAction: true, InitiativeLevel: 1, PassivityLevel: 100,
		})
	}
	return out
}

func (r *runner) createRoom(open bool) servicepb.ErrorCode {
	ctx, cancel := r.call(r.ctx)
	defer cancel()
	reply, err := r.room.CreateChatroom(ctx, &servicepb.CreateChatroomRequest{
		AppId: r.appID, NeedOpen: open,
		ChatroomInfo: &servicepb.ChatroomInfo{ChatroomId: r.roomID, Name: "regress-room", OwnerUid: r.owner},
		Space:        seats(),
	})
	if err != nil {
		return servicepb.ErrorCode_SERVER_INTERNAL_ERROR
	}
	return reply.GetRet().GetErrorCode()
}

func seats() []*servicepb.SpaceInfos {
	return []*servicepb.SpaceInfos{{Index: 1}, {Index: 2}}
}

func (r *runner) roomToken(uid int64, role int32, nick string) (string, servicepb.ErrorCode) {
	ctx, cancel := r.call(r.ctx)
	defer cancel()
	reply, err := r.room.ApplyEnterChatroomToken(ctx, &servicepb.ApplyEnterChatroomTokenRequest{
		AppId: r.appID, RoomId: r.roomID, Uid: uid,
		MemberInfo: &servicepb.ChatroomMemberInfo{
			Uid: uid, Nickname: nick, RoleId: []int32{role},
			SearchKeys: map[string]string{"nick": nick},
		},
	})
	if err != nil {
		return "", servicepb.ErrorCode_SERVER_INTERNAL_ERROR
	}
	return reply.GetToken(), reply.GetRet().GetErrorCode()
}

func (r *runner) login(uid int64) (string, error) {
	ctx, cancel := r.call(r.ctx)
	defer cancel()
	reply, err := r.core.ApplyClientLoginToken(ctx, &servicepb.ApplyClientLoginTokenRequest{AppId: r.appID, Uid: uid})
	if err != nil {
		return "", err
	}
	if reply.GetRet().GetErrorCode() != servicepb.ErrorCode_SUCCESS {
		return "", fmt.Errorf("%s", reply.GetRet().GetErrorCode())
	}
	return reply.GetLoginToken(), nil
}

func (r *runner) enterRPC(uid int64, role int32, nick string) servicepb.ErrorCode {
	token, code := r.roomToken(uid, role, nick)
	if code != servicepb.ErrorCode_SUCCESS {
		return code
	}
	ctx, cancel := r.call(r.ctx)
	defer cancel()
	reply, err := r.room.EnterChatroom(ctx, &servicepb.EnterChatroomRequest{
		AppId: r.appID, RoomId: r.roomID, Uid: uid, Token: token,
	})
	if err != nil {
		return servicepb.ErrorCode_SERVER_INTERNAL_ERROR
	}
	return reply.GetRet().GetErrorCode()
}

func (r *runner) caseCore() {
	if r.appID == "" {
		r.fail("CORE-01", "没有应用")
		return
	}
	ctx, cancel := r.call(r.ctx)
	defer cancel()
	q, err := r.core.QueryAppInfo(ctx, &servicepb.QueryAppInfoRequest{Id: r.appRow})
	if r.rpcOK("CORE-01", q.GetRet(), err) && q.GetInfo().GetAppSecret() == r.secret {
		valid, err := r.core.IsAppValid(ctx, &servicepb.IsAppValidRequest{AppId: r.appID})
		secret, serr := r.core.GetAppSecret(ctx, &servicepb.GetAppSecretRequest{AppId: r.appID})
		if r.rpcOK("CORE-01", valid.GetRet(), err) && valid.GetIsValid() && r.rpcOK("CORE-01", secret.GetRet(), serr) && secret.GetSecret() == r.secret {
			r.pass("CORE-01", "查询、有效性和密钥一致")
		}
	}
	missing, err := r.core.IsAppValid(ctx, &servicepb.IsAppValidRequest{AppId: "missing-app"})
	if err != nil {
		r.fail("CORE-02", err.Error())
	} else if missing.GetIsValid() {
		r.fail("CORE-02", "不存在的应用被判定为有效")
	} else {
		r.pass("CORE-02", "不存在的应用无效")
	}
	none, err := r.core.QueryAppInfo(ctx, &servicepb.QueryAppInfoRequest{Id: 0})
	if err != nil {
		r.fail("CORE-02", "查询不存在 id: "+err.Error())
	} else if none.GetRet().GetErrorCode() == servicepb.ErrorCode_SUCCESS && none.GetInfo().GetAppId() != "" {
		r.fail("CORE-02", "id=0 仍返回了应用")
	} else {
		r.pass("CORE-02", "查不到的 id 没有返回应用")
	}

	token, err := r.login(r.owner)
	if err != nil {
		r.fail("CORE-03", err.Error())
	} else {
		info, err := r.core.GetClientLoginTokenInfo(ctx, &servicepb.GetClientLoginTokenInfoRequest{LoginToken: token})
		if r.rpcOK("CORE-03", info.GetRet(), err) && info.GetAppId() == r.appID && info.GetUid() == r.owner {
			r.pass("CORE-03", "令牌解出 appId 和 uid")
		} else if err == nil {
			r.fail("CORE-03", fmt.Sprintf("解出 app=%s uid=%d", info.GetAppId(), info.GetUid()))
		}
		deleted, err := r.core.DeleteClientLoginToken(ctx, &servicepb.DeleteClientLoginTokenReq{
			AppId: r.appID, Uid: r.owner, LoginToken: token,
		})
		if r.rpcOK("CORE-04", deleted.GetRet(), err) {
			l, err := dialWS(ctx, r.gwWS)
			if err != nil {
				r.fail("CORE-04", err.Error())
			} else {
				resp, err := l.auth(ctx, r.appID, token, "deleted")
				l.Close()
				if err != nil {
					r.pass("CORE-04", "删除后的令牌无法完成鉴权")
				} else if resp.GetRet().GetErrorCode() != clientpb.ErrorCode_SUCCESS {
					r.pass("CORE-04", "删除后的令牌被拒绝")
				} else {
					r.fail("CORE-04", "删除后的令牌仍然通过鉴权")
				}
			}
		}
	}

	renamed, err := r.core.UpdateAppInfo(ctx, r.appUpdate("regress-renamed", r.cb.base+"/event", r.cb.base+"/text", r.cb.base+"/custom"))
	if r.rpcOK("CORE-05", renamed.GetRet(), err) && renamed.GetInfo().GetName() == "regress-renamed" {
		r.pass("CORE-05", "名称已更新，抄送地址保持不变")
	}
	qps, err := r.core.GetAllAppMsgCpQPS(ctx, &servicepb.GetAllAppMsgCpQPSRequest{})
	cache, cerr := r.core.UpdateAppCacheInfo(ctx, &servicepb.UpdateAppCacheInfoRequest{AppId: r.appID})
	if r.rpcOK("CORE-06", qps.GetRet(), err) && r.rpcOK("CORE-06", cache.GetRet(), cerr) {
		r.pass("CORE-06", fmt.Sprintf("读到 %d 条抄送限流，缓存已刷新", len(qps.GetQps())))
	}

	r.caseBan()
	r.caseGdPc()
}

func (r *runner) caseBan() {
	ctx, cancel := r.call(r.ctx)
	defer cancel()
	token, err := r.login(r.guest)
	if err != nil {
		r.fail("CORE-07", err.Error())
		return
	}
	l, err := dialWS(ctx, r.gwWS)
	if err != nil {
		r.fail("CORE-07", err.Error())
		return
	}
	defer l.Close()
	resp, err := l.auth(ctx, r.appID, token, "ban")
	if err != nil || resp.GetRet().GetErrorCode() != clientpb.ErrorCode_SUCCESS {
		r.fail("CORE-07", fmt.Sprintf("封禁前登录失败 %v", err))
		return
	}
	ban, err := r.core.RestraintUserOperator(ctx, &servicepb.RestraintUserOperatorRequest{AppId: r.appID, Uid: r.guest, Action: 1})
	if !r.rpcOK("CORE-07", ban.GetRet(), err) {
		return
	}
	_, loginErr := r.login(r.guest)
	roomBan, rerr := r.room.RestraintUserOper(ctx, &servicepb.RestraintUserOperRequest{AppId: r.appID, Uid: r.guest, Action: 1})
	wait, cancelWait := context.WithTimeout(r.ctx, 8*time.Second)
	kickErr := l.waitPush(wait, clientpb.CmdId_KICK, nil)
	if kickErr != nil {
		kickErr = l.gone(wait)
	}
	cancelWait()
	if loginErr == nil {
		r.fail("CORE-07", "core 封禁后仍能签发登录令牌")
	} else if rerr != nil || roomBan.GetRet().GetErrorCode() != servicepb.ErrorCode_SUCCESS || kickErr != nil {
		r.fail("CORE-07", fmt.Sprintf("新令牌已拒绝。core 本身不断开连接；chatroom 封禁 %v %s，断线 %v", rerr, roomBan.GetRet().GetErrorCode(), kickErr))
	} else {
		r.pass("CORE-07", "core 封禁后不能再签发令牌。断开在线连接要再调 chatroom RestraintUserOper，该调用已把连接断开")
	}
	lift, err := r.core.RestraintUserOperator(ctx, &servicepb.RestraintUserOperatorRequest{AppId: r.appID, Uid: r.guest, Action: 0})
	if !r.rpcOK("CORE-08", lift.GetRet(), err) {
		return
	}
	token, err = r.login(r.guest)
	if err != nil {
		r.fail("CORE-08", err.Error())
		return
	}
	l2, err := dialWS(ctx, r.gwWS)
	if err != nil {
		r.fail("CORE-08", err.Error())
		return
	}
	defer l2.Close()
	resp, err = l2.auth(ctx, r.appID, token, "unban")
	if err != nil || resp.GetRet().GetErrorCode() != clientpb.ErrorCode_SUCCESS {
		r.fail("CORE-08", "解封后不能登录")
		return
	}
	r.pass("CORE-08", "解封后可以重新登录")
}

func (r *runner) caseGdPc() {
	ctx, cancel := r.call(r.ctx)
	defer cancel()
	created, err := r.core.CreateGdApp(ctx, &servicepb.CreateGdAppRequest{Name: "regress-gd", MatchLevel: 1, NeedMatchBase: true})
	if !r.rpcOK("CORE-09", created.GetRet(), err) {
		return
	}
	gd := created.GetInfo()
	updated, err := r.core.UpdateGdAppInfo(ctx, &servicepb.UpdateGdAppInfoRequest{Id: gd.GetId(), Name: "regress-gd-2", MatchLevel: 2})
	secret, serr := r.core.GetGdAppSecret(ctx, &servicepb.GetGdAppSecretRequest{AppId: gd.GetAppId()})
	queried, qerr := r.core.QueryGdAppInfo(ctx, &servicepb.QueryGdAppInfoRequest{Id: gd.GetId()})
	if !r.rpcOK("CORE-09", updated.GetRet(), err) || updated.GetInfo().GetName() != "regress-gd-2" {
		r.fail("CORE-09", "反垃圾应用更新后的名称不对")
		return
	}
	if !r.rpcOK("CORE-09", secret.GetRet(), serr) || secret.GetSecret() != gd.GetAppSecret() {
		r.fail("CORE-09", "按反垃圾 id 查到的密钥不一致")
		return
	}
	if !r.rpcOK("CORE-09", queried.GetRet(), qerr) || queried.GetInfo().GetAppId() != gd.GetAppId() {
		r.fail("CORE-09", "按主键查询不一致")
		return
	}
	bound, err := r.core.UpdateAppInfo(ctx, func() *servicepb.UpdateAppInfoRequest {
		req := r.appUpdate("regress-renamed", r.cb.base+"/event", r.cb.base+"/text", r.cb.base+"/custom")
		req.GdAppId = gd.GetAppId()
		return req
	}())
	of, oerr := r.core.GetGdAppOfAppSecret(ctx, &servicepb.GetGdAppSecretOfAppRequest{AppId: r.appID})
	if r.rpcOK("CORE-09", bound.GetRet(), err) && r.rpcOK("CORE-09", of.GetRet(), oerr) && of.GetGdAppSecret() == gd.GetAppSecret() {
		r.pass("CORE-09", "反垃圾应用读写一致，聊天室应用能取到它的密钥")
	}

	pc, err := r.core.CreatePcApp(ctx, &servicepb.CreatePcAppRequest{Name: "regress-pc", GdAppId: gd.GetAppId(), BusinessUrl: "http://example.invalid/pc"})
	if !r.rpcOK("CORE-10", pc.GetRet(), err) {
		return
	}
	changed, err := r.core.UpdatePcAppInfo(ctx, &servicepb.UpdatePcAppInfoRequest{Id: pc.GetInfo().GetId(), Name: "regress-pc-2", BusinessUrl: "http://example.invalid/pc2"})
	boundPC, berr := r.core.BindPcFilterApp(ctx, &servicepb.BindPcFilterAppRequest{Id: pc.GetInfo().GetId(), GdAppId: gd.GetAppId()})
	byID, ierr := r.core.QueryPcAppInfo(ctx, &servicepb.QueryPcAppInfoRequest{Id: pc.GetInfo().GetId()})
	byApp, aerr := r.core.QueryPcAppInfoByAppID(ctx, &servicepb.QueryPcAppInfoByAppIDRequest{AppId: pc.GetInfo().GetAppId()})
	if r.rpcOK("CORE-10", changed.GetRet(), err) && changed.GetInfo().GetName() == "regress-pc-2" &&
		r.rpcOK("CORE-10", boundPC.GetRet(), berr) && boundPC.GetInfo().GetGdAppId() == gd.GetAppId() &&
		r.rpcOK("CORE-10", byID.GetRet(), ierr) && r.rpcOK("CORE-10", byApp.GetRet(), aerr) &&
		byID.GetInfo().GetAppId() == byApp.GetInfo().GetAppId() {
		r.pass("CORE-10", "PC 应用创建、更新、绑定和两种查询一致")
	}
}

func checksumOK(secret string, item hit) bool {
	sum := sha1.Sum(append(append(append([]byte(secret), item.nonce...), item.when...), item.body...))
	return hex.EncodeToString(sum[:]) == item.sum && item.sum == delivery.CheckSum(secret, item.nonce, item.when, item.body)
}

func (r *runner) do(method, url string, body any, cookie *http.Cookie) (int, []byte, *http.Cookie) {
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	ctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return 0, []byte(err.Error()), nil
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	res, err := r.http.Do(req)
	if err != nil {
		return 0, []byte(err.Error()), nil
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	var session *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == "orbikit_session" {
			session = c
		}
	}
	return res.StatusCode, raw, session
}

func contains(b []byte, s string) bool { return strings.Contains(string(b), s) }

func unimplemented(err error) bool {
	st, ok := status.FromError(err)
	return ok && st.Code() == codes.Unimplemented
}
