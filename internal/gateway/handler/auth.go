package handler

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net"
	"time"

	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	"github.com/yourname/roomkit/api/gen/clientpb"
	"github.com/yourname/roomkit/api/gen/servicepb"
	"github.com/yourname/roomkit/internal/gateway/protocol"
	"github.com/yourname/roomkit/internal/gateway/session"
	"github.com/yourname/roomkit/internal/gateway/store"
	"github.com/yourname/roomkit/pkg/log"
)

const (
	tokenLen            = 64
	userInfoRefresh     = 5 * time.Minute
	disconnectRPCBudget = 10 * time.Second
)

var errAuthFailed = errors.New("auth failed")

func (s *Service) auth(sess *session.Session, p *protocol.Packet) error {
	if sess.Info() != nil {
		return errors.New("duplicate auth")
	}
	reqBody, seal, err := sess.Codec().OpenAuth(p.Body)
	if seal == nil {
		return fmt.Errorf("open auth body: %w", err)
	}

	var (
		resp  *clientpb.AuthResp
		info  *session.Info
		rooms []int64
	)
	if err != nil {
		log.L().Info("auth body undecodable", zap.Uint64("conn_id", sess.ID()), zap.Error(err))
		resp = &clientpb.AuthResp{Ret: ret(clientpb.ErrorCode_MARS_MARSHAL_ERR)}
	} else {
		info, rooms, resp = s.login(context.Background(), sess, reqBody)
	}

	raw, err := proto.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal auth response: %w", err)
	}
	out := &protocol.Packet{ClientVersion: p.ClientVersion, CmdID: p.CmdID, SeqID: p.SeqID, Body: seal(raw)}
	sess.Reply(out.Encode())
	if info == nil {
		return fmt.Errorf("%w: %s", errAuthFailed, resp.GetRet().GetErrorCode())
	}

	// The AUTH response is queued before the session becomes visible to
	// pushes so that the client learns the mask key first.
	sess.SetInfo(info)
	if prev := s.Sessions.Bind(sess); prev != nil && prev != sess {
		s.Kick(prev)
	}
	if bcst, err := proto.Marshal(&clientpb.ChatroomInStatusBcst{Uid: info.UID, RoomId: rooms}); err == nil {
		sess.Push(clientpb.CmdId_CHATROOM_IN_STATUS_BCST, bcst)
	}
	go s.refreshUserInfo(sess, info.AuthToken)
	return nil
}

// login validates the AuthReq and registers the connection. A nil info means
// failure and resp carries the reason.
func (s *Service) login(ctx context.Context, sess *session.Session, body []byte) (info *session.Info, rooms []int64, resp *clientpb.AuthResp) {
	fail := func(code clientpb.ErrorCode) (*session.Info, []int64, *clientpb.AuthResp) {
		return nil, nil, &clientpb.AuthResp{Ret: ret(code)}
	}

	req := &clientpb.AuthReq{}
	if err := proto.Unmarshal(body, req); err != nil {
		log.L().Info("auth: unmarshal", zap.Uint64("conn_id", sess.ID()), zap.Error(err))
		return fail(clientpb.ErrorCode_MARS_MARSHAL_ERR)
	}
	r := s.newRequest(clientpb.CmdId_AUTH, req.AppId, 0, hostOnly(sess.RemoteAddr()), req.DeviceId, req.AuthToken, sess.ID())
	r.Log.Info("auth begin",
		zap.String("transport", sess.Transport()),
		zap.String("device_id", req.DeviceId),
		zap.String("client_type", req.Type.String()),
		zap.String("ip", r.IP))

	cctx, cancel := s.rpcCtx(ctx)
	tokenInfo, err := s.Core.GetClientLoginTokenInfo(cctx, &servicepb.GetClientLoginTokenInfoRequest{
		LoginToken: req.AuthToken,
		MsgTraceId: r.TraceID,
	})
	cancel()
	if err != nil {
		r.Log.Error("auth: verify login token", zap.Error(err))
		return fail(clientpb.ErrorCode_MARS_AGENT_INTERNAL_ERROR)
	}
	if tokenInfo.GetRet().GetErrorCode() != servicepb.ErrorCode_SUCCESS || tokenInfo.AppId != req.AppId {
		r.Log.Info("auth: login token rejected", zap.String("ret", tokenInfo.GetRet().String()), zap.String("token_app_id", tokenInfo.GetAppId()))
		return fail(clientpb.ErrorCode_LOGIN_TOKEN_NOT_EXISTS)
	}
	r.UID = tokenInfo.Uid
	r.Log = r.Log.With(zap.Int64("uid", r.UID))

	lockCtx, lockCancel := context.WithTimeout(ctx, userLockWait)
	err = s.Store.LockUser(lockCtx, r.AppID, r.UID)
	lockCancel()
	if err != nil {
		r.Log.Error("auth: lock user", zap.Error(err))
		return fail(clientpb.ErrorCode_MARS_AGENT_INTERNAL_ERROR)
	}
	defer s.Store.UnlockUser(r.AppID, r.UID)

	oldToken, err := s.Store.LastLoginToken(ctx, r.AppID, r.UID)
	if err != nil {
		r.Log.Error("auth: read last login token", zap.Error(err))
		return fail(clientpb.ErrorCode_MARS_AGENT_INTERNAL_ERROR)
	}
	if err := s.Store.KickOtherConnections(ctx, s.IDs.MachineID(), sess.ID(), r.AppID, r.UID); err != nil {
		r.Log.Error("auth: kick other connections", zap.Error(err))
		return fail(clientpb.ErrorCode_MARS_AGENT_INTERNAL_ERROR)
	}
	if err := s.Store.SetLinkStatus(ctx, r.AppID, r.UID); err != nil {
		r.Log.Error("auth: set link status", zap.Error(err))
		return fail(clientpb.ErrorCode_MARS_AGENT_INTERNAL_ERROR)
	}
	online := false
	connUUID := s.IDs.Next()
	defer func() {
		if info != nil {
			return
		}
		if online {
			s.userOffline(r, connUUID)
		}
		if err := s.Store.ClearLinkStatus(context.Background(), r.AppID, r.UID); err != nil {
			r.Log.Warn("auth: clear link status", zap.Error(err))
		}
	}()

	maskKey, err1 := randomToken()
	authToken, err2 := randomToken()
	if err := errors.Join(err1, err2); err != nil {
		r.Log.Error("auth: generate keys", zap.Error(err))
		return fail(clientpb.ErrorCode_MARS_AGENT_INTERNAL_ERROR)
	}
	if err := s.Store.SaveUserInfo(ctx, authToken, &store.UserInfo{
		AppID:      r.AppID,
		UID:        r.UID,
		DeviceID:   req.DeviceId,
		MaskKey:    maskKey,
		LoginToken: req.AuthToken,
	}); err != nil {
		r.Log.Error("auth: save user info", zap.Error(err))
		return fail(clientpb.ErrorCode_MARS_AGENT_INTERNAL_ERROR)
	}

	cctx, cancel = s.rpcCtx(ctx)
	rs, err := s.Chatroom.UserOnline(cctx, &servicepb.UserOnlineRequest{
		AppId:      r.AppID,
		Uid:        r.UID,
		ConnUuid:   connUUID,
		MsgTraceId: r.TraceID,
	})
	cancel()
	if e := rpcRet(r, "UserOnline", err, rs.GetRet(), clientpb.ErrorCode_MARS_AGENT_INTERNAL_ERROR); e != nil {
		return fail(clientpb.ErrorCode_MARS_AGENT_INTERNAL_ERROR)
	}
	online = true

	rooms, e := s.currentRooms(ctx, r)
	if e != nil {
		return fail(clientpb.ErrorCode_MARS_AGENT_INTERNAL_ERROR)
	}
	// Signing in with a different login token means a new login rather than a
	// reconnect, so the previous login's rooms are left.
	if oldToken != "" && oldToken != req.AuthToken {
		if e := s.leaveRooms(ctx, r, rooms); e != nil {
			r.Log.Warn("auth: leave rooms of previous login", zap.Int64s("rooms", rooms))
		}
	}
	if err := s.Store.SetLastLoginToken(ctx, r.AppID, r.UID, req.AuthToken); err != nil {
		r.Log.Warn("auth: save login token", zap.Error(err))
	}
	s.Store.RecordLogin(ctx, r.AppID, r.UID, req.DeviceId)

	info = &session.Info{
		AppID:      r.AppID,
		UID:        r.UID,
		IP:         r.IP,
		DeviceID:   req.DeviceId,
		PhoneModel: req.Ext["phone_model"],
		ExtOS:      req.Ext["OS"],
		LoginToken: req.AuthToken,
		AuthToken:  authToken,
		MaskKey:    maskKey,
		ConnUUID:   connUUID,
	}
	r.Log.Info("auth success", zap.Uint64("conn_uuid", connUUID), zap.Int("rooms", len(rooms)))
	return info, rooms, &clientpb.AuthResp{Ret: ok(), MaskKey: maskKey, AuthToken: authToken}
}

// OnClose implements session.Handler.
func (s *Service) OnClose(sess *session.Session) {
	info := sess.Info()
	if info == nil {
		return
	}
	r := s.newRequest(clientpb.CmdId_LOGOUT, info.AppID, info.UID, info.IP, info.DeviceID, info.LoginToken, sess.ID())
	// A session replaced by a newer local login must not clear the state the
	// newer one owns.
	if s.Sessions.Unbind(sess) {
		ctx, cancel := context.WithTimeout(context.Background(), disconnectRPCBudget)
		if err := s.Store.ClearLinkStatus(ctx, info.AppID, info.UID); err != nil {
			r.Log.Warn("disconnect: clear link status", zap.Error(err))
		}
		cancel()
		if s.userOffline(r, info.ConnUUID) {
			ctx, cancel := context.WithTimeout(context.Background(), disconnectRPCBudget)
			s.Store.RecordLogout(ctx, info.AppID, info.UID, info.DeviceID)
			cancel()
		}
	} else {
		s.userOffline(r, info.ConnUUID)
	}
	r.Log.Info("disconnected", zap.String("transport", sess.Transport()))
}

func (s *Service) userOffline(r *Request, connUUID uint64) bool {
	ctx, cancel := context.WithTimeout(context.Background(), disconnectRPCBudget)
	defer cancel()
	rs, err := s.Chatroom.UserOffline(ctx, &servicepb.UserOfflineRequest{
		AppId:      r.AppID,
		Uid:        r.UID,
		ConnUuid:   connUUID,
		MsgTraceId: r.TraceID,
	})
	return rpcRet(r, "UserOffline", err, rs.GetRet(), clientpb.ErrorCode_CHATROOM_SERVICE_ERROR) == nil
}

// Kick tells the client it was signed in elsewhere and closes the session.
func (s *Service) Kick(sess *session.Session) {
	if raw, err := proto.Marshal(&clientpb.LoginKickedBcst{Reason: clientpb.LoginKickReason_LOGIN_KICK_REASON_BY_CLIENT}); err == nil {
		sess.Push(clientpb.CmdId_KICK, raw)
	}
	sess.Close()
}

func (s *Service) refreshUserInfo(sess *session.Session, authToken string) {
	t := time.NewTicker(userInfoRefresh)
	defer t.Stop()
	for {
		select {
		case <-sess.Done():
			return
		case <-t.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := s.Store.TouchUserInfo(ctx, authToken); err != nil {
				log.L().Warn("refresh user info", zap.Uint64("conn_id", sess.ID()), zap.Error(err))
			}
			cancel()
		}
	}
}

const tokenAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func randomToken() (string, error) {
	b := make([]byte, tokenLen)
	max := big.NewInt(int64(len(tokenAlphabet)))
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = tokenAlphabet[n.Int64()]
	}
	return string(b), nil
}

func hostOnly(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}
