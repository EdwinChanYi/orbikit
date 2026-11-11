package session

import (
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"

	"go.uber.org/zap"

	"github.com/EdwinChanYi/orbikit/api/gen/clientpb"
	"github.com/EdwinChanYi/orbikit/internal/gateway/protocol"
	"github.com/EdwinChanYi/orbikit/internal/gateway/transport"
	"github.com/EdwinChanYi/orbikit/pkg/log"
)

// Manager owns all sessions of the process.
type Manager struct {
	opts   Options
	hb     []byte
	nextID atomic.Uint64

	mu    sync.RWMutex
	all   map[uint64]*Session
	users map[string]map[int64]*Session
}

// NewManager returns an empty manager.
func NewManager(opts Options) *Manager {
	hb := (&protocol.Packet{ClientVersion: opts.ClientVersion, CmdID: uint32(clientpb.CmdId_NOOPING)}).Encode()
	return &Manager{
		opts:  opts,
		hb:    hb,
		all:   make(map[uint64]*Session),
		users: make(map[string]map[int64]*Session),
	}
}

// Serve runs a connection until it closes. It is a transport.Handler once h is bound.
func (m *Manager) Serve(conn transport.Conn, codec protocol.Codec, h Handler) {
	s := newSession(m.nextID.Add(1), conn, codec, &m.opts, m.hb)
	m.mu.Lock()
	m.all[s.id] = s
	m.mu.Unlock()

	go s.writeLoop()

	defer func() {
		s.Close()
		<-s.writerDone
		_ = conn.Close()
		h.OnClose(s)
		m.mu.Lock()
		delete(m.all, s.id)
		m.mu.Unlock()
	}()

	for {
		p, err := conn.ReadPacket()
		if err != nil {
			if !isClosedErr(err) {
				log.L().Debug("session read", zap.Uint64("conn_id", s.id), zap.String("remote", conn.RemoteAddr()), zap.Error(err))
			}
			return
		}
		if err := h.HandlePacket(s, p); err != nil {
			log.L().Info("session closed by handler", zap.Uint64("conn_id", s.id),
				zap.Uint32("cmd_id", p.CmdID), zap.String("remote", conn.RemoteAddr()), zap.Error(err))
			return
		}
	}
}

// Handler adapts Serve to transport.Handler.
func (m *Manager) Handler(h Handler) transport.Handler {
	return func(c transport.Conn, codec protocol.Codec) { m.Serve(c, codec, h) }
}

func isClosedErr(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed)
}

// Bind indexes an authenticated session by its app and uid and returns the
// session previously bound to the same user, if any.
func (m *Manager) Bind(s *Session) *Session {
	info := s.Info()
	if info == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	byUID := m.users[info.AppID]
	if byUID == nil {
		byUID = make(map[int64]*Session)
		m.users[info.AppID] = byUID
	}
	prev := byUID[info.UID]
	byUID[info.UID] = s
	if prev == s {
		return nil
	}
	return prev
}

// Unbind removes s from the user index if it is still the bound session.
func (m *Manager) Unbind(s *Session) bool {
	info := s.Info()
	if info == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	byUID := m.users[info.AppID]
	if byUID[info.UID] != s {
		return false
	}
	delete(byUID, info.UID)
	if len(byUID) == 0 {
		delete(m.users, info.AppID)
	}
	return true
}

// Lookup returns the session bound to appID/uid.
func (m *Manager) Lookup(appID string, uid int64) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.users[appID][uid]
}

// RangeApp calls fn for every bound session of appID until fn returns false.
func (m *Manager) RangeApp(appID string, fn func(*Session) bool) {
	m.mu.RLock()
	list := make([]*Session, 0, len(m.users[appID]))
	for _, s := range m.users[appID] {
		list = append(list, s)
	}
	m.mu.RUnlock()
	for _, s := range list {
		if !fn(s) {
			return
		}
	}
}

// Len returns the number of open connections.
func (m *Manager) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.all)
}

// CloseAll closes every session.
func (m *Manager) CloseAll() {
	m.mu.RLock()
	list := make([]*Session, 0, len(m.all))
	for _, s := range m.all {
		list = append(list, s)
	}
	m.mu.RUnlock()
	for _, s := range list {
		s.Close()
	}
}
