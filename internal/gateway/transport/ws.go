package transport

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"github.com/EdwinChanYi/orbikit/internal/gateway/protocol"
	"github.com/EdwinChanYi/orbikit/pkg/log"
)

// WSOptions configures the WebSocket listener.
type WSOptions struct {
	Codec protocol.Codec
	// Path is the upgrade endpoint, e.g. "/ws".
	Path string
	// IdleTimeout closes a connection that sends no message and answers no
	// ping for this long. The server pings at a third of this interval.
	IdleTimeout time.Duration
	MaxBodyLen  int
	// AllowedOrigins restricts the Origin header; empty allows any origin.
	AllowedOrigins []string
	// TLSCertFile and TLSKeyFile enable wss when both are set.
	TLSCertFile string
	TLSKeyFile  string
}

// WSServer accepts WebSocket connections where every binary message carries
// exactly one packet.
type WSServer struct {
	ln       net.Listener
	srv      *http.Server
	opts     WSOptions
	upgrader websocket.Upgrader
	handler  Handler
	wg       sync.WaitGroup
}

// ListenWS binds addr.
func ListenWS(ctx context.Context, addr string, opts WSOptions) (*WSServer, error) {
	if opts.Path == "" {
		opts.Path = "/ws"
	}
	if opts.MaxBodyLen <= 0 {
		opts.MaxBodyLen = protocol.DefaultMaxBodyLen
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	s := &WSServer{ln: ln, opts: opts}
	s.upgrader = websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin:     originChecker(opts.AllowedOrigins),
	}
	mux := http.NewServeMux()
	mux.HandleFunc(opts.Path, s.serveHTTP)
	s.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	return s, nil
}

// Addr returns the bound address.
func (s *WSServer) Addr() net.Addr { return s.ln.Addr() }

// Serve handles upgrades until Close is called.
func (s *WSServer) Serve(h Handler) error {
	s.handler = h
	var err error
	if s.opts.TLSCertFile != "" && s.opts.TLSKeyFile != "" {
		err = s.srv.ServeTLS(s.ln, s.opts.TLSCertFile, s.opts.TLSKeyFile)
	} else {
		err = s.srv.Serve(s.ln)
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Close stops accepting new connections. Hijacked WebSocket connections are
// not affected; close them through their sessions.
func (s *WSServer) Close() error { return s.srv.Close() }

// Wait blocks until every handler has returned or ctx is done.
func (s *WSServer) Wait(ctx context.Context) error { return waitGroup(ctx, &s.wg) }

func (s *WSServer) serveHTTP(w http.ResponseWriter, r *http.Request) {
	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.L().Debug("ws upgrade", zap.String("remote", r.RemoteAddr), zap.Error(err))
		return
	}
	ws.SetReadLimit(int64(protocol.HeaderLen + s.opts.MaxBodyLen))
	c := &wsConn{
		ws:         ws,
		idle:       s.opts.IdleTimeout,
		maxBodyLen: s.opts.MaxBodyLen,
		remote:     r.RemoteAddr,
		done:       make(chan struct{}),
	}
	if c.idle > 0 {
		ws.SetPongHandler(func(string) error {
			return ws.SetReadDeadline(time.Now().Add(c.idle))
		})
		go c.pingLoop()
	}
	s.wg.Add(1)
	defer s.wg.Done()
	s.handler(c, s.opts.Codec)
}

func originChecker(allowed []string) func(*http.Request) bool {
	if len(allowed) == 0 {
		return func(*http.Request) bool { return true }
	}
	set := make(map[string]struct{}, len(allowed))
	for _, o := range allowed {
		set[strings.ToLower(strings.TrimRight(o, "/"))] = struct{}{}
	}
	return func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		_, ok := set[strings.ToLower(u.Scheme+"://"+u.Host)]
		return ok
	}
}

type wsConn struct {
	ws         *websocket.Conn
	idle       time.Duration
	maxBodyLen int
	remote     string
	closeOnce  sync.Once
	done       chan struct{}
}

func (c *wsConn) pingLoop() {
	t := time.NewTicker(c.idle / 3)
	defer t.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-t.C:
			if err := c.ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(c.idle/3)); err != nil {
				return
			}
		}
	}
}

func (c *wsConn) ReadPacket() (*protocol.Packet, error) {
	if c.idle > 0 {
		if err := c.ws.SetReadDeadline(time.Now().Add(c.idle)); err != nil {
			return nil, err
		}
	}
	mt, data, err := c.ws.ReadMessage()
	if err != nil {
		return nil, err
	}
	if mt != websocket.BinaryMessage {
		return nil, fmt.Errorf("ws: unexpected message type %d", mt)
	}
	return protocol.Decode(data, c.maxBodyLen)
}

func (c *wsConn) WritePackets(pkts [][]byte, deadline time.Time) error {
	if err := c.ws.SetWriteDeadline(deadline); err != nil {
		return err
	}
	for _, p := range pkts {
		if err := c.ws.WriteMessage(websocket.BinaryMessage, p); err != nil {
			return err
		}
	}
	return nil
}

func (c *wsConn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		close(c.done)
		_ = c.ws.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		err = c.ws.Close()
	})
	return err
}

func (c *wsConn) RemoteAddr() string { return c.remote }
func (c *wsConn) Kind() string       { return "ws" }
