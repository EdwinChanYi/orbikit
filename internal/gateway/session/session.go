// Package session tracks client connections independently of their transport
// and indexes authenticated ones by app and uid for push delivery.
package session

import (
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/EdwinChanYi/orbikit/api/gen/clientpb"
	"github.com/EdwinChanYi/orbikit/internal/gateway/protocol"
	"github.com/EdwinChanYi/orbikit/internal/gateway/transport"
	"github.com/EdwinChanYi/orbikit/pkg/log"
)

// Info describes the authenticated user behind a session.
type Info struct {
	AppID      string
	UID        int64
	IP         string
	DeviceID   string
	PhoneModel string
	ExtOS      string
	// LoginToken is the token the client presented in AUTH.
	LoginToken string
	// AuthToken is the gateway-issued token used by the HTTP short link.
	AuthToken string
	// MaskKey is the per-session body encryption key.
	MaskKey string
	// ConnUUID identifies this login to the chatroom service.
	ConnUUID uint64
}

// Options tunes session I/O.
type Options struct {
	// ClientVersion is written into server-initiated packets.
	ClientVersion uint32
	// HeartbeatInterval is how often a NOOPING packet is sent; 0 disables it.
	HeartbeatInterval time.Duration
	// AuthTimeout closes connections that have not authenticated in time; 0 disables it.
	AuthTimeout time.Duration
	// WriteTimeout bounds one batched write.
	WriteTimeout time.Duration
	// MaxPendingBytes closes a session whose unsent output exceeds this size.
	MaxPendingBytes int
}

// Handler processes packets of a session.
type Handler interface {
	// HandlePacket processes one inbound packet; an error closes the session.
	HandlePacket(s *Session, p *protocol.Packet) error
	// OnClose runs once after the connection is closed.
	OnClose(s *Session)
}

// Session is one client connection.
type Session struct {
	id    uint64
	conn  transport.Conn
	codec protocol.Codec
	opts  *Options
	hb    []byte

	info atomic.Pointer[Info]

	mu      sync.Mutex
	resp    [][]byte
	push    [][]byte
	pending int
	closed  bool

	wake       chan struct{}
	done       chan struct{}
	closeOnce  sync.Once
	writerDone chan struct{}
}

func newSession(id uint64, conn transport.Conn, codec protocol.Codec, opts *Options, hb []byte) *Session {
	return &Session{
		id:         id,
		conn:       conn,
		codec:      codec,
		opts:       opts,
		hb:         hb,
		wake:       make(chan struct{}, 1),
		done:       make(chan struct{}),
		writerDone: make(chan struct{}),
	}
}

// ID is unique within the process.
func (s *Session) ID() uint64 { return s.id }

// Codec is the body codec negotiated by the transport.
func (s *Session) Codec() protocol.Codec { return s.codec }

// RemoteAddr is the peer address as seen by the transport.
func (s *Session) RemoteAddr() string { return s.conn.RemoteAddr() }

// Transport names the underlying transport.
func (s *Session) Transport() string { return s.conn.Kind() }

// Info returns the authenticated user, or nil before AUTH succeeds.
func (s *Session) Info() *Info { return s.info.Load() }

// SetInfo marks the session as authenticated.
func (s *Session) SetInfo(info *Info) { s.info.Store(info) }

// Done is closed when the session starts shutting down.
func (s *Session) Done() <-chan struct{} { return s.done }

// ClientVersion is the version written into server-initiated packets.
func (s *Session) ClientVersion() uint32 { return s.opts.ClientVersion }

// Reply queues an encoded response packet. Responses are written before
// queued pushes.
func (s *Session) Reply(pkt []byte) bool { return s.enqueue(pkt, true) }

// Push seals plain with the session key and queues it as a server-initiated
// packet. It reports false when the session is not authenticated or closed.
func (s *Session) Push(cmd clientpb.CmdId, plain []byte) bool {
	info := s.info.Load()
	if info == nil {
		return false
	}
	pkt := &protocol.Packet{
		ClientVersion: s.opts.ClientVersion,
		CmdID:         uint32(cmd),
		Body:          s.codec.Seal(info.MaskKey, plain),
	}
	return s.enqueue(pkt.Encode(), false)
}

// Close flushes queued output and closes the connection asynchronously.
func (s *Session) Close() {
	s.closeOnce.Do(func() { close(s.done) })
}

func (s *Session) enqueue(pkt []byte, isResp bool) bool {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return false
	}
	if s.opts.MaxPendingBytes > 0 && s.pending+len(pkt) > s.opts.MaxPendingBytes {
		s.closed = true
		s.resp, s.push, s.pending = nil, nil, 0
		s.mu.Unlock()
		log.L().Warn("session output overflow, closing",
			zap.Uint64("conn_id", s.id), zap.Int("limit", s.opts.MaxPendingBytes))
		s.Close()
		return false
	}
	if isResp {
		s.resp = append(s.resp, pkt)
	} else {
		s.push = append(s.push, pkt)
	}
	s.pending += len(pkt)
	s.mu.Unlock()

	select {
	case s.wake <- struct{}{}:
	default:
	}
	return true
}

func (s *Session) take() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.resp) == 0 && len(s.push) == 0 {
		return nil
	}
	out := append(s.resp, s.push...)
	s.resp, s.push, s.pending = nil, nil, 0
	return out
}

func (s *Session) flush() error {
	pkts := s.take()
	if len(pkts) == 0 {
		return nil
	}
	return s.conn.WritePackets(pkts, time.Now().Add(s.opts.WriteTimeout))
}

func (s *Session) writeLoop() {
	defer close(s.writerDone)

	var hb <-chan time.Time
	if s.opts.HeartbeatInterval > 0 {
		t := time.NewTicker(s.opts.HeartbeatInterval)
		defer t.Stop()
		hb = t.C
	}
	var authDeadline <-chan time.Time
	if s.opts.AuthTimeout > 0 {
		t := time.NewTimer(s.opts.AuthTimeout)
		defer t.Stop()
		authDeadline = t.C
	}

	for {
		select {
		case <-s.wake:
			if err := s.flush(); err != nil {
				log.L().Debug("session write", zap.Uint64("conn_id", s.id), zap.Error(err))
				s.shutdown()
				return
			}
		case <-hb:
			s.enqueue(s.hb, false)
		case <-authDeadline:
			if s.Info() == nil {
				log.L().Info("session auth timeout", zap.Uint64("conn_id", s.id), zap.String("remote", s.RemoteAddr()))
				s.shutdown()
				return
			}
		case <-s.done:
			if err := s.flush(); err != nil {
				log.L().Debug("session final write", zap.Uint64("conn_id", s.id), zap.Error(err))
			}
			s.shutdown()
			return
		}
	}
}

// shutdown stops accepting output and closes the connection, which unblocks
// the reader.
func (s *Session) shutdown() {
	s.mu.Lock()
	s.closed = true
	s.resp, s.push, s.pending = nil, nil, 0
	s.mu.Unlock()
	s.Close()
	_ = s.conn.Close()
}
