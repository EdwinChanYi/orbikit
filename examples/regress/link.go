package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"sync/atomic"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"

	"github.com/EdwinChanYi/orbikit/api/gen/clientpb"
	"github.com/EdwinChanYi/orbikit/internal/gateway/protocol"
)

// link is one gateway connection. Pushes are decrypted bodies.
type link struct {
	conn     net.Conn
	ws       *websocket.Conn
	reader   *protocol.Reader
	codec    protocol.Codec
	version  uint32
	seq      atomic.Uint32
	maskKey  atomic.Value
	writeMu  sync.Mutex
	mu       sync.Mutex
	pending  map[uint32]chan *protocol.Packet
	pushes   chan pushMsg
	closed   chan struct{}
	closeErr error
}

type pushMsg struct {
	cmd  clientpb.CmdId
	body []byte
}

func dialWS(ctx context.Context, url string) (*link, error) {
	ws, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	if err != nil {
		return nil, err
	}
	codec, err := protocol.NewCodec(protocol.CodecPlain)
	if err != nil {
		_ = ws.Close()
		return nil, err
	}
	l := newLink(codec)
	l.ws = ws
	go l.readLoop()
	return l, nil
}

func dialTCP(ctx context.Context, addr string) (*link, error) {
	var d net.Dialer
	nc, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	codec, err := protocol.NewCodec(protocol.CodecXXTEA)
	if err != nil {
		_ = nc.Close()
		return nil, err
	}
	l := newLink(codec)
	l.conn = nc
	l.reader = protocol.NewReader(nc, 1<<20)
	go l.readLoop()
	return l, nil
}

func newLink(codec protocol.Codec) *link {
	l := &link{
		codec:   codec,
		version: 100000000,
		pending: make(map[uint32]chan *protocol.Packet),
		pushes:  make(chan pushMsg, 64),
		closed:  make(chan struct{}),
	}
	l.maskKey.Store("")
	return l
}

func (l *link) Close() {
	if l.ws != nil {
		_ = l.ws.Close()
	}
	if l.conn != nil {
		_ = l.conn.Close()
	}
}

func (l *link) write(pkt []byte) error {
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	if l.ws != nil {
		return l.ws.WriteMessage(websocket.BinaryMessage, pkt)
	}
	_, err := l.conn.Write(pkt)
	return err
}

func (l *link) readPacket() (*protocol.Packet, error) {
	if l.ws != nil {
		for {
			kind, data, err := l.ws.ReadMessage()
			if err != nil {
				return nil, err
			}
			if kind == websocket.BinaryMessage {
				return protocol.Decode(data, 1<<20)
			}
		}
	}
	return l.reader.Next()
}

func (l *link) readLoop() {
	for {
		p, err := l.readPacket()
		if err != nil {
			l.mu.Lock()
			l.closeErr = err
			l.mu.Unlock()
			close(l.closed)
			return
		}
		if p.SeqID != 0 {
			l.mu.Lock()
			ch := l.pending[p.SeqID]
			l.mu.Unlock()
			if ch != nil {
				ch <- p
				continue
			}
		}
		body, err := l.codec.Open(l.maskKey.Load().(string), p.Body)
		if err != nil {
			body = nil
		}
		select {
		case l.pushes <- pushMsg{cmd: clientpb.CmdId(int32(p.CmdID)), body: body}:
		default:
		}
	}
}

func (l *link) roundTrip(ctx context.Context, cmd clientpb.CmdId, body []byte) (*protocol.Packet, error) {
	seq := l.seq.Add(1)
	ch := make(chan *protocol.Packet, 1)
	l.mu.Lock()
	l.pending[seq] = ch
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		delete(l.pending, seq)
		l.mu.Unlock()
	}()
	pkt := &protocol.Packet{ClientVersion: l.version, CmdID: uint32(cmd), SeqID: seq, Body: body}
	if err := l.write(pkt.Encode()); err != nil {
		return nil, err
	}
	select {
	case p := <-ch:
		return p, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-l.closed:
		return nil, fmt.Errorf("connection closed: %v", l.closeErr)
	}
}

func (l *link) auth(ctx context.Context, appID, token, device string) (*clientpb.AuthResp, error) {
	raw, err := proto.Marshal(&clientpb.AuthReq{
		AppId: appID, AuthToken: token, DeviceId: device, Type: clientpb.ClientType_CLIENT_TYPE_PC,
	})
	if err != nil {
		return nil, err
	}
	body := raw
	var maskNum uint64
	if x, ok := l.codec.(protocol.XXTEA); ok {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, err
		}
		maskNum = binary.BigEndian.Uint64(b[:])
		body = x.SealAuthRequest(maskNum, raw)
	}
	p, err := l.roundTrip(ctx, clientpb.CmdId_AUTH, body)
	if err != nil {
		return nil, err
	}
	plain := p.Body
	if x, ok := l.codec.(protocol.XXTEA); ok {
		plain, err = x.OpenAuthResponse(maskNum, p.Body)
		if err != nil {
			return nil, err
		}
	}
	resp := &clientpb.AuthResp{}
	if err := proto.Unmarshal(plain, resp); err != nil {
		return nil, err
	}
	if resp.GetRet().GetErrorCode() == clientpb.ErrorCode_SUCCESS {
		l.maskKey.Store(resp.GetMaskKey())
	}
	return resp, nil
}

func (l *link) call(ctx context.Context, cmd clientpb.CmdId, req, resp proto.Message) error {
	raw, err := proto.Marshal(req)
	if err != nil {
		return err
	}
	p, err := l.roundTrip(ctx, cmd, l.codec.Seal(l.maskKey.Load().(string), raw))
	if err != nil {
		return err
	}
	body, err := l.codec.Open(l.maskKey.Load().(string), p.Body)
	if err != nil {
		return err
	}
	return proto.Unmarshal(body, resp)
}

func (l *link) waitPush(ctx context.Context, cmd clientpb.CmdId, match func([]byte) bool) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-l.closed:
			return fmt.Errorf("connection closed: %v", l.closeErr)
		case p := <-l.pushes:
			if p.cmd == cmd && (match == nil || match(p.body)) {
				return nil
			}
		}
	}
}

func (l *link) gone(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-l.closed:
		return nil
	}
}
