// Package handler implements the gateway commands shared by the long link
// (TCP/WebSocket) and the HTTP short link.
package handler

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	"github.com/EdwinChanYi/orbikit/api/gen/clientpb"
	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
	"github.com/EdwinChanYi/orbikit/internal/gateway/moderation"
	"github.com/EdwinChanYi/orbikit/internal/gateway/protocol"
	"github.com/EdwinChanYi/orbikit/internal/gateway/session"
	"github.com/EdwinChanYi/orbikit/internal/gateway/store"
	"github.com/EdwinChanYi/orbikit/pkg/log"
)

// IDGenerator issues cluster-unique ids.
type IDGenerator interface {
	Next() uint64
	MachineID() uint16
}

// Options tunes handler behaviour.
type Options struct {
	// RPCTimeout bounds each downstream gRPC call.
	RPCTimeout time.Duration
	// MessageReceipt asks clients to acknowledge received room messages.
	MessageReceipt bool
}

// Deps are the collaborators of Service.
type Deps struct {
	Core      servicepb.CoreClient
	Chatroom  servicepb.ChatroomClient
	Store     *store.Store
	IDs       IDGenerator
	Moderator moderation.Moderator
	Sessions  *session.Manager
}

// Service dispatches client commands.
type Service struct {
	Deps
	opts   Options
	routes map[clientpb.CmdId]*Route
}

// New wires a Service and registers every command.
func New(deps Deps, opts Options) *Service {
	if opts.RPCTimeout <= 0 {
		opts.RPCTimeout = 20 * time.Second
	}
	s := &Service{Deps: deps, opts: opts}
	s.routes = s.buildRoutes()
	return s
}

// Request carries the caller identity of one command.
type Request struct {
	Cmd        clientpb.CmdId
	AppID      string
	UID        int64
	IP         string
	DeviceID   string
	LoginToken string
	ConnID     uint64
	TraceID    string
	Log        *zap.Logger
}

func (s *Service) newRequest(cmd clientpb.CmdId, appID string, uid int64, ip, deviceID, loginToken string, connID uint64) *Request {
	trace := strconv.FormatUint(s.IDs.Next(), 10)
	return &Request{
		Cmd:        cmd,
		AppID:      appID,
		UID:        uid,
		IP:         ip,
		DeviceID:   deviceID,
		LoginToken: loginToken,
		ConnID:     connID,
		TraceID:    trace,
		Log: log.L().With(
			zap.String("cmd", cmd.String()),
			zap.String("trace_id", trace),
			zap.String("app_id", appID),
			zap.Int64("uid", uid),
			zap.Uint64("conn_id", connID),
		),
	}
}

// Route binds a command to its handler and short-link URL.
type Route struct {
	Cmd clientpb.CmdId
	URL string
	// AllowEmpty accepts requests without a body.
	AllowEmpty bool
	// empty builds the response type carrying only ret.
	empty  func(ret *clientpb.UnifiedResponse) proto.Message
	handle func(ctx context.Context, r *Request, body []byte) proto.Message
}

// Serve runs the handler, rejecting empty bodies unless allowed.
func (rt *Route) Serve(ctx context.Context, r *Request, body []byte) proto.Message {
	if len(body) == 0 && !rt.AllowEmpty {
		return rt.empty(&clientpb.UnifiedResponse{
			ErrorCode: clientpb.ErrorCode_MARS_AGENT_GET_ZERO_LEN_DATA,
			ErrorMsg:  "get zero len data",
		})
	}
	return rt.handle(ctx, r, body)
}

// Fail builds this route's response with only an error code.
func (rt *Route) Fail(code clientpb.ErrorCode, msg string) proto.Message {
	return rt.empty(&clientpb.UnifiedResponse{ErrorCode: code, ErrorMsg: msg})
}

// Routes returns every registered command.
func (s *Service) Routes() []*Route {
	out := make([]*Route, 0, len(s.routes))
	for _, rt := range s.routes {
		out = append(out, rt)
	}
	return out
}

// Route looks up a command.
func (s *Service) Route(cmd clientpb.CmdId) (*Route, bool) {
	rt, ok := s.routes[cmd]
	return rt, ok
}

// NewShortLinkRequest builds a request for the HTTP short link.
func (s *Service) NewShortLinkRequest(cmd clientpb.CmdId, u *store.UserInfo, ip string) *Request {
	return s.newRequest(cmd, u.AppID, u.UID, ip, u.DeviceID, u.LoginToken, 0)
}

// HandlePacket implements session.Handler.
func (s *Service) HandlePacket(sess *session.Session, p *protocol.Packet) error {
	cmd := clientpb.CmdId(int32(p.CmdID))
	if cmd == clientpb.CmdId_AUTH {
		return s.auth(sess, p)
	}

	info := sess.Info()
	if info == nil {
		return fmt.Errorf("cmd %d before auth", p.CmdID)
	}
	if cmd == clientpb.CmdId_NOOPING {
		sess.Reply(p.Encode())
		return nil
	}

	rt, ok := s.routes[cmd]
	if !ok {
		return fmt.Errorf("unknown cmd %d", p.CmdID)
	}
	r := s.newRequest(cmd, info.AppID, info.UID, info.IP, info.DeviceID, info.LoginToken, sess.ID())

	var resp proto.Message
	body, err := sess.Codec().Open(info.MaskKey, p.Body)
	if err != nil {
		r.Log.Warn("decode body", zap.Error(err), zap.Int("body_len", len(p.Body)))
		resp = rt.Fail(clientpb.ErrorCode_MARS_MARSHAL_ERR, "decode body failed")
	} else {
		resp = rt.Serve(context.Background(), r, body)
	}
	raw, err := proto.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}
	out := &protocol.Packet{
		ClientVersion: p.ClientVersion,
		CmdID:         p.CmdID,
		SeqID:         p.SeqID,
		Body:          sess.Codec().Seal(info.MaskKey, raw),
	}
	sess.Reply(out.Encode())
	return nil
}

func ret(code clientpb.ErrorCode) *clientpb.UnifiedResponse {
	return &clientpb.UnifiedResponse{ErrorCode: code, ErrorMsg: code.String()}
}

func ok() *clientpb.UnifiedResponse { return ret(clientpb.ErrorCode_SUCCESS) }

// rpcRet converts the outcome of a downstream call. failCode is used when the
// call itself failed or returned no status.
func rpcRet(r *Request, method string, err error, sret *servicepb.UnifiedResponse, failCode clientpb.ErrorCode) *clientpb.UnifiedResponse {
	if err != nil || sret == nil {
		if err == nil {
			err = errors.New("empty reply")
		}
		r.Log.Error("rpc failed", zap.String("method", method), zap.Error(err))
		return ret(failCode)
	}
	if sret.ErrorCode != servicepb.ErrorCode_SUCCESS {
		r.Log.Debug("rpc returned error", zap.String("method", method), zap.String("ret", sret.String()))
		return ret(convertCode(sret.ErrorCode))
	}
	return nil
}

func (s *Service) rpcCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, s.opts.RPCTimeout)
}

func unmarshal(r *Request, body []byte, m proto.Message) *clientpb.UnifiedResponse {
	if err := proto.Unmarshal(body, m); err != nil {
		r.Log.Warn("unmarshal request", zap.Error(err))
		return ret(clientpb.ErrorCode_MARS_MARSHAL_ERR)
	}
	return nil
}
