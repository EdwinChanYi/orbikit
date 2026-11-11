// Command gateway-client is a minimal long-link client for the orbikit
// gateway: it connects over TCP or WebSocket, authenticates, enters a room,
// optionally sends a text message and prints every push it receives.
//
//	go run ./examples/gateway-client -transport tcp -addr localhost:8000 \
//	    -app demo -token <login token> -room 1001 -msg hello
package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/EdwinChanYi/orbikit/api/gen/clientpb"
	"github.com/EdwinChanYi/orbikit/internal/gateway/protocol"
)

type options struct {
	transport     string
	addr          string
	codec         string
	appID         string
	uid           int64
	token         string
	deviceID      string
	roomID        int64
	roomToken     string
	message       string
	clientVersion uint
	timeout       time.Duration
	duration      time.Duration
}

func main() {
	var o options
	flag.StringVar(&o.transport, "transport", "tcp", "tcp or ws")
	flag.StringVar(&o.addr, "addr", "", `gateway address; default "localhost:8000" for tcp, "ws://localhost:8001/ws" for ws`)
	flag.StringVar(&o.codec, "codec", "", "xxtea or plain; defaults to xxtea for tcp and plain for ws")
	flag.StringVar(&o.appID, "app", "", "app id (required)")
	flag.Int64Var(&o.uid, "uid", 0, "expected uid of the login token; 0 skips the check")
	flag.StringVar(&o.token, "token", "", "login token issued by the core service (required)")
	flag.StringVar(&o.deviceID, "device", "gateway-client", "device id")
	flag.Int64Var(&o.roomID, "room", 0, "room to enter; 0 skips entering")
	flag.StringVar(&o.roomToken, "room-token", "", "room entry token, if the room requires one")
	flag.StringVar(&o.message, "msg", "", "text message to send after entering the room")
	flag.UintVar(&o.clientVersion, "client-version", 100000000, "clientVersion written into request headers")
	flag.DurationVar(&o.timeout, "timeout", 10*time.Second, "per-request timeout")
	flag.DurationVar(&o.duration, "duration", 0, "how long to keep printing pushes; 0 runs until interrupted")
	flag.Parse()

	if err := run(o); err != nil {
		fmt.Fprintln(os.Stderr, "gateway-client:", err)
		os.Exit(1)
	}
}

func run(o options) error {
	if o.appID == "" || o.token == "" {
		return errors.New("-app and -token are required")
	}
	switch o.transport {
	case "tcp":
		if o.addr == "" {
			o.addr = "localhost:8000"
		}
		if o.codec == "" {
			o.codec = protocol.CodecXXTEA
		}
	case "ws":
		if o.addr == "" {
			o.addr = "ws://localhost:8001/ws"
		}
		if o.codec == "" {
			o.codec = protocol.CodecPlain
		}
	default:
		return fmt.Errorf("unknown transport %q", o.transport)
	}
	codec, err := protocol.NewCodec(o.codec)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if o.duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.duration)
		defer cancel()
	}

	conn, err := dial(ctx, o.transport, o.addr)
	if err != nil {
		return err
	}
	c := newClient(conn, codec, uint32(o.clientVersion))
	c.expectUID = o.uid
	defer c.close()
	go c.readLoop()
	fmt.Printf("connected to %s over %s, codec %s\n", o.addr, o.transport, codec.Name())

	if err := c.auth(ctx, o); err != nil {
		return err
	}
	go c.heartbeat(ctx, 30*time.Second)

	if o.roomID != 0 {
		resp := &clientpb.EnterChatroomResp{}
		if err := c.call(ctx, o.timeout, clientpb.CmdId_CHATROOM_ENTER,
			&clientpb.EnterChatroomReq{RoomId: o.roomID, Token: o.roomToken}, resp); err != nil {
			return fmt.Errorf("enter room: %w", err)
		}
		printMsg("enter room", resp)

		if o.message != "" {
			resp := &clientpb.SendMessageResp{}
			if err := c.call(ctx, o.timeout, clientpb.CmdId_CHATROOM_SEND_MESSAGE, &clientpb.SendMessageReq{
				Message: &clientpb.Message{
					MessageType:      clientpb.MessageType_MESSAGE_TYPE_TEXT,
					Session:          &clientpb.Session{SessionType: clientpb.SessionType_SESSION_TYPE_CHATROOM, SessionId: o.roomID},
					Text:             o.message,
					MessageId:        strconv.FormatInt(time.Now().UnixNano(), 10),
					SenderClientType: clientpb.ClientType_CLIENT_TYPE_PC,
				},
			}, resp); err != nil {
				return fmt.Errorf("send message: %w", err)
			}
			printMsg("send message", resp)
		}
	}

	fmt.Println("waiting for pushes, press Ctrl+C to quit")
	select {
	case <-ctx.Done():
		return nil
	case err := <-c.closed:
		return err
	}
}

// conn sends and receives whole packets.
type conn interface {
	write(pkt []byte) error
	read() (*protocol.Packet, error)
	close() error
}

func dial(ctx context.Context, transport, addr string) (conn, error) {
	if transport == "ws" {
		ws, _, err := websocket.DefaultDialer.DialContext(ctx, addr, nil)
		if err != nil {
			return nil, fmt.Errorf("dial %s: %w", addr, err)
		}
		return &wsConn{ws: ws}, nil
	}
	var d net.Dialer
	nc, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	return &tcpConn{nc: nc, r: protocol.NewReader(nc, 1<<20)}, nil
}

type tcpConn struct {
	nc net.Conn
	r  *protocol.Reader
}

func (c *tcpConn) write(pkt []byte) error          { _, err := c.nc.Write(pkt); return err }
func (c *tcpConn) read() (*protocol.Packet, error) { return c.r.Next() }
func (c *tcpConn) close() error                    { return c.nc.Close() }

type wsConn struct{ ws *websocket.Conn }

func (c *wsConn) write(pkt []byte) error { return c.ws.WriteMessage(websocket.BinaryMessage, pkt) }
func (c *wsConn) read() (*protocol.Packet, error) {
	for {
		kind, data, err := c.ws.ReadMessage()
		if err != nil {
			return nil, err
		}
		if kind == websocket.BinaryMessage {
			return protocol.Decode(data, 1<<20)
		}
	}
}
func (c *wsConn) close() error { return c.ws.Close() }

type client struct {
	conn      conn
	codec     protocol.Codec
	version   uint32
	expectUID int64
	seq       atomic.Uint32
	maskKey   atomic.Value

	writeMu  sync.Mutex
	mu       sync.Mutex
	pending  map[uint32]chan *protocol.Packet
	closed   chan error
	authDone chan struct{}
}

func newClient(c conn, codec protocol.Codec, version uint32) *client {
	cl := &client{
		conn:     c,
		codec:    codec,
		version:  version,
		pending:  make(map[uint32]chan *protocol.Packet),
		closed:   make(chan error, 1),
		authDone: make(chan struct{}),
	}
	cl.maskKey.Store("")
	return cl
}

func (c *client) close() { _ = c.conn.close() }

func (c *client) send(cmd clientpb.CmdId, body []byte) (uint32, chan *protocol.Packet, error) {
	seq := c.seq.Add(1)
	ch := make(chan *protocol.Packet, 1)
	c.mu.Lock()
	c.pending[seq] = ch
	c.mu.Unlock()
	pkt := &protocol.Packet{ClientVersion: c.version, CmdID: uint32(cmd), SeqID: seq, Body: body}
	c.writeMu.Lock()
	err := c.conn.write(pkt.Encode())
	c.writeMu.Unlock()
	if err != nil {
		c.forget(seq)
		return 0, nil, err
	}
	return seq, ch, nil
}

func (c *client) forget(seq uint32) {
	c.mu.Lock()
	delete(c.pending, seq)
	c.mu.Unlock()
}

func (c *client) roundTrip(ctx context.Context, timeout time.Duration, cmd clientpb.CmdId, body []byte) (*protocol.Packet, error) {
	seq, ch, err := c.send(cmd, body)
	if err != nil {
		return nil, err
	}
	defer c.forget(seq)
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case p := <-ch:
		return p, nil
	case <-t.C:
		return nil, fmt.Errorf("%s timed out", cmd)
	case <-ctx.Done():
		return nil, ctx.Err()
	case err := <-c.closed:
		c.closed <- err
		return nil, err
	}
}

// call sends an encrypted request and decodes the response into resp.
func (c *client) call(ctx context.Context, timeout time.Duration, cmd clientpb.CmdId, req, resp proto.Message) error {
	raw, err := proto.Marshal(req)
	if err != nil {
		return err
	}
	key := c.maskKey.Load().(string)
	p, err := c.roundTrip(ctx, timeout, cmd, c.codec.Seal(key, raw))
	if err != nil {
		return err
	}
	body, err := c.codec.Open(key, p.Body)
	if err != nil {
		return err
	}
	return proto.Unmarshal(body, resp)
}

func (c *client) auth(ctx context.Context, o options) error {
	defer close(c.authDone)
	raw, err := proto.Marshal(&clientpb.AuthReq{
		AppId:     o.appID,
		AuthToken: o.token,
		DeviceId:  o.deviceID,
		Type:      clientpb.ClientType_CLIENT_TYPE_PC,
		Ext:       map[string]string{"OS": "gateway-client"},
	})
	if err != nil {
		return err
	}

	var maskNum uint64
	body := raw
	if x, ok := c.codec.(protocol.XXTEA); ok {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return err
		}
		maskNum = binary.BigEndian.Uint64(b[:])
		body = x.SealAuthRequest(maskNum, raw)
	}

	p, err := c.roundTrip(ctx, o.timeout, clientpb.CmdId_AUTH, body)
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	plain := p.Body
	if x, ok := c.codec.(protocol.XXTEA); ok {
		if plain, err = x.OpenAuthResponse(maskNum, p.Body); err != nil {
			return fmt.Errorf("auth: %w", err)
		}
	}
	resp := &clientpb.AuthResp{}
	if err := proto.Unmarshal(plain, resp); err != nil {
		return fmt.Errorf("auth: decode response: %w", err)
	}
	if code := resp.GetRet().GetErrorCode(); code != clientpb.ErrorCode_SUCCESS {
		return fmt.Errorf("auth rejected: %s %s", code, resp.GetRet().GetErrorMsg())
	}
	c.maskKey.Store(resp.MaskKey)
	fmt.Println("auth ok")
	return nil
}

func (c *client) heartbeat(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pkt := &protocol.Packet{ClientVersion: c.version, CmdID: uint32(clientpb.CmdId_NOOPING), SeqID: c.seq.Add(1)}
			c.writeMu.Lock()
			err := c.conn.write(pkt.Encode())
			c.writeMu.Unlock()
			if err != nil {
				return
			}
		}
	}
}

func (c *client) readLoop() {
	for {
		p, err := c.conn.read()
		if err != nil {
			c.closed <- fmt.Errorf("connection closed: %w", err)
			return
		}
		if p.SeqID != 0 {
			c.mu.Lock()
			ch := c.pending[p.SeqID]
			c.mu.Unlock()
			if ch != nil {
				ch <- p
				if p.CmdID == uint32(clientpb.CmdId_AUTH) {
					// Pushes after AUTH are sealed with the new mask key.
					<-c.authDone
				}
				continue
			}
		}
		c.onPush(p)
	}
}

func (c *client) onPush(p *protocol.Packet) {
	cmd := clientpb.CmdId(int32(p.CmdID))
	var m proto.Message
	switch cmd {
	case clientpb.CmdId_NOOPING:
		return
	case clientpb.CmdId_CHATROOM_RECEIVE_MESSAGE:
		m = &clientpb.Message{}
	case clientpb.CmdId_CHATROOM_IN_STATUS_BCST:
		m = &clientpb.ChatroomInStatusBcst{}
	case clientpb.CmdId_CHATROOM_HAS_BEEN_KICKED:
		m = &clientpb.ChatRoomMemberBeKickedBcst{}
	case clientpb.CmdId_KICK:
		m = &clientpb.LoginKickedBcst{}
	case clientpb.CmdId_UPLOAD_LOG_BROADCAST:
		m = &clientpb.UploadLogBroadcast{}
	default:
		fmt.Printf("push %s: %d bytes\n", cmd, len(p.Body))
		return
	}
	body, err := c.codec.Open(c.maskKey.Load().(string), p.Body)
	if err == nil {
		err = proto.Unmarshal(body, m)
	}
	if err != nil {
		fmt.Printf("push %s: undecodable: %v\n", cmd, err)
		return
	}
	printMsg("push "+cmd.String(), m)
	if st, ok := m.(*clientpb.ChatroomInStatusBcst); ok && c.expectUID != 0 && st.Uid != c.expectUID {
		fmt.Printf("warning: token belongs to uid %d, expected %d\n", st.Uid, c.expectUID)
	}
}

func printMsg(label string, m proto.Message) {
	b, err := protojson.MarshalOptions{EmitUnpopulated: false}.Marshal(m)
	if err != nil {
		fmt.Printf("%s: %v\n", label, m)
		return
	}
	fmt.Printf("%s: %s\n", label, b)
}
