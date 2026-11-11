package session_test

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/yourname/roomkit/api/gen/clientpb"
	"github.com/yourname/roomkit/internal/gateway/protocol"
	"github.com/yourname/roomkit/internal/gateway/session"
	"github.com/yourname/roomkit/internal/gateway/transport"
)

// echoHandler authenticates on AUTH, echoes ECHO bodies and fails on
// anything else.
type echoHandler struct {
	m      *session.Manager
	closed atomic.Int32
}

func (h *echoHandler) HandlePacket(s *session.Session, p *protocol.Packet) error {
	switch clientpb.CmdId(int32(p.CmdID)) {
	case clientpb.CmdId_AUTH:
		s.SetInfo(&session.Info{AppID: "app", UID: int64(len(p.Body))})
		h.m.Bind(s)
		s.Reply((&protocol.Packet{ClientVersion: p.ClientVersion, CmdID: p.CmdID, SeqID: p.SeqID}).Encode())
		return nil
	case clientpb.CmdId_ECHO:
		s.Reply(p.Encode())
		return nil
	}
	return errors.New("unexpected cmd")
}

func (h *echoHandler) OnClose(s *session.Session) {
	h.m.Unbind(s)
	h.closed.Add(1)
}

type client interface {
	write(p *protocol.Packet) error
	read() (*protocol.Packet, error)
	close()
}

type tcpClient struct {
	nc net.Conn
	r  *protocol.Reader
}

func (c *tcpClient) write(p *protocol.Packet) error {
	_, err := c.nc.Write(p.Encode())
	return err
}
func (c *tcpClient) read() (*protocol.Packet, error) {
	_ = c.nc.SetReadDeadline(time.Now().Add(2 * time.Second))
	return c.r.Next()
}
func (c *tcpClient) close() { _ = c.nc.Close() }

type wsClient struct{ ws *websocket.Conn }

func (c *wsClient) write(p *protocol.Packet) error {
	return c.ws.WriteMessage(websocket.BinaryMessage, p.Encode())
}
func (c *wsClient) read() (*protocol.Packet, error) {
	_ = c.ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := c.ws.ReadMessage()
	if err != nil {
		return nil, err
	}
	return protocol.Decode(data, protocol.DefaultMaxBodyLen)
}
func (c *wsClient) close() { _ = c.ws.Close() }

type server struct {
	h    *echoHandler
	dial func(t *testing.T) client
}

func startTCP(t *testing.T, opts session.Options) *server {
	t.Helper()
	m := session.NewManager(opts)
	h := &echoHandler{m: m}
	srv, err := transport.ListenTCP(context.Background(), "127.0.0.1:0", transport.TCPOptions{
		Codec: protocol.Plain{}, MaxBodyLen: protocol.DefaultMaxBodyLen,
	})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(m.Handler(h)) }()
	t.Cleanup(func() { _ = srv.Close(); m.CloseAll() })
	return &server{h: h, dial: func(t *testing.T) client {
		nc, err := net.Dial("tcp", srv.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		return &tcpClient{nc: nc, r: protocol.NewReader(nc, protocol.DefaultMaxBodyLen)}
	}}
}

func startWS(t *testing.T, opts session.Options) *server {
	t.Helper()
	m := session.NewManager(opts)
	h := &echoHandler{m: m}
	srv, err := transport.ListenWS(context.Background(), "127.0.0.1:0", transport.WSOptions{
		Codec: protocol.Plain{}, Path: "/ws", IdleTimeout: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(m.Handler(h)) }()
	t.Cleanup(func() { _ = srv.Close(); m.CloseAll() })
	return &server{h: h, dial: func(t *testing.T) client {
		ws, _, err := websocket.DefaultDialer.Dial("ws://"+srv.Addr().String()+"/ws", nil)
		if err != nil {
			t.Fatal(err)
		}
		return &wsClient{ws: ws}
	}}
}

var transports = map[string]func(*testing.T, session.Options) *server{
	"tcp": startTCP,
	"ws":  startWS,
}

func readCmd(t *testing.T, c client, cmd clientpb.CmdId) *protocol.Packet {
	t.Helper()
	for {
		p, err := c.read()
		if err != nil {
			t.Fatalf("waiting for %s: %v", cmd, err)
		}
		if p.CmdID == uint32(cmd) {
			return p
		}
	}
}

func TestRequestResponseAndPush(t *testing.T) {
	for name, start := range transports {
		t.Run(name, func(t *testing.T) {
			srv := start(t, session.Options{ClientVersion: 9, WriteTimeout: time.Second})
			c := srv.dial(t)
			defer c.close()

			if err := c.write(&protocol.Packet{ClientVersion: 1, CmdID: uint32(clientpb.CmdId_AUTH), SeqID: 1, Body: []byte("abc")}); err != nil {
				t.Fatal(err)
			}
			if p := readCmd(t, c, clientpb.CmdId_AUTH); p.SeqID != 1 || p.ClientVersion != 1 {
				t.Fatalf("auth reply = %+v", p)
			}

			req := &protocol.Packet{ClientVersion: 1, CmdID: uint32(clientpb.CmdId_ECHO), SeqID: 2, Body: []byte("ping")}
			if err := c.write(req); err != nil {
				t.Fatal(err)
			}
			if p := readCmd(t, c, clientpb.CmdId_ECHO); p.SeqID != 2 || string(p.Body) != "ping" {
				t.Fatalf("echo reply = %+v", p)
			}

			s := srv.h.m.Lookup("app", 3)
			if s == nil {
				t.Fatal("session not bound")
			}
			if !s.Push(clientpb.CmdId_CHATROOM_RECEIVE_MESSAGE, []byte("push")) {
				t.Fatal("push refused")
			}
			p := readCmd(t, c, clientpb.CmdId_CHATROOM_RECEIVE_MESSAGE)
			if p.SeqID != 0 || p.ClientVersion != 9 || string(p.Body) != "push" {
				t.Fatalf("push = %+v", p)
			}
		})
	}
}

func TestHeartbeat(t *testing.T) {
	for name, start := range transports {
		t.Run(name, func(t *testing.T) {
			srv := start(t, session.Options{ClientVersion: 9, HeartbeatInterval: 20 * time.Millisecond, WriteTimeout: time.Second})
			c := srv.dial(t)
			defer c.close()
			p := readCmd(t, c, clientpb.CmdId_NOOPING)
			if len(p.Body) != 0 || p.ClientVersion != 9 {
				t.Fatalf("heartbeat = %+v", p)
			}
		})
	}
}

func TestAuthTimeoutAndHandlerErrorClose(t *testing.T) {
	for name, start := range transports {
		t.Run(name, func(t *testing.T) {
			srv := start(t, session.Options{AuthTimeout: 50 * time.Millisecond, WriteTimeout: time.Second})

			c := srv.dial(t)
			defer c.close()
			if _, err := c.read(); err == nil {
				t.Fatal("unauthenticated connection stayed open")
			}

			c2 := srv.dial(t)
			defer c2.close()
			if err := c2.write(&protocol.Packet{CmdID: 12345}); err != nil {
				t.Fatal(err)
			}
			if _, err := c2.read(); err == nil {
				t.Fatal("connection stayed open after handler error")
			}

			deadline := time.Now().Add(2 * time.Second)
			for srv.h.closed.Load() < 2 && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if n := srv.h.closed.Load(); n != 2 {
				t.Fatalf("OnClose ran %d times, want 2", n)
			}
		})
	}
}

func TestUnauthenticatedSessionNotBound(t *testing.T) {
	srv := startTCP(t, session.Options{WriteTimeout: time.Second})
	c := srv.dial(t)
	defer c.close()
	if err := c.write(&protocol.Packet{CmdID: uint32(clientpb.CmdId_ECHO), SeqID: 1, Body: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	readCmd(t, c, clientpb.CmdId_ECHO)
	if srv.h.m.Len() != 1 {
		t.Fatalf("Len = %d", srv.h.m.Len())
	}
	if s := srv.h.m.Lookup("app", 0); s != nil {
		t.Fatal("unauthenticated session is bound")
	}
}
