package transport

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/yourname/roomkit/internal/gateway/protocol"
	"github.com/yourname/roomkit/pkg/log"
)

// TCPOptions configures the TCP listener.
type TCPOptions struct {
	Codec protocol.Codec
	// IdleTimeout closes a connection that sends nothing for this long; 0 disables it.
	IdleTimeout time.Duration
	// KeepAlive is the TCP keep-alive period; 0 uses the Go default.
	KeepAlive  time.Duration
	MaxBodyLen int
}

// TCPServer accepts length-prefixed packet streams.
type TCPServer struct {
	ln   net.Listener
	opts TCPOptions
	wg   sync.WaitGroup
}

// ListenTCP binds addr.
func ListenTCP(ctx context.Context, addr string, opts TCPOptions) (*TCPServer, error) {
	lc := net.ListenConfig{KeepAlive: opts.KeepAlive}
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	return &TCPServer{ln: ln, opts: opts}, nil
}

// Addr returns the bound address.
func (s *TCPServer) Addr() net.Addr { return s.ln.Addr() }

// Serve accepts connections until Close is called, running h for each in its
// own goroutine.
func (s *TCPServer) Serve(h Handler) error {
	var backoff time.Duration
	for {
		nc, err := s.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				backoff = min(max(backoff*2, 5*time.Millisecond), time.Second)
				log.L().Warn("tcp accept", zap.Error(err), zap.Duration("retry_in", backoff))
				time.Sleep(backoff)
				continue
			}
			return err
		}
		backoff = 0
		if tc, ok := nc.(*net.TCPConn); ok {
			_ = tc.SetNoDelay(true)
		}
		c := &tcpConn{
			nc:     nc,
			reader: protocol.NewReader(nc, s.opts.MaxBodyLen),
			idle:   s.opts.IdleTimeout,
			remote: nc.RemoteAddr().String(),
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			h(c, s.opts.Codec)
		}()
	}
}

// Close stops accepting new connections.
func (s *TCPServer) Close() error { return s.ln.Close() }

// Wait blocks until every handler has returned or ctx is done.
func (s *TCPServer) Wait(ctx context.Context) error { return waitGroup(ctx, &s.wg) }

type tcpConn struct {
	nc     net.Conn
	reader *protocol.Reader
	idle   time.Duration
	remote string
}

func (c *tcpConn) ReadPacket() (*protocol.Packet, error) {
	if c.idle > 0 {
		if err := c.nc.SetReadDeadline(time.Now().Add(c.idle)); err != nil {
			return nil, err
		}
	}
	return c.reader.Next()
}

func (c *tcpConn) WritePackets(pkts [][]byte, deadline time.Time) error {
	if len(pkts) == 0 {
		return nil
	}
	if err := c.nc.SetWriteDeadline(deadline); err != nil {
		return err
	}
	bufs := net.Buffers(pkts)
	_, err := bufs.WriteTo(c.nc)
	return err
}

func (c *tcpConn) Close() error       { return c.nc.Close() }
func (c *tcpConn) RemoteAddr() string { return c.remote }
func (c *tcpConn) Kind() string       { return "tcp" }

func waitGroup(ctx context.Context, wg *sync.WaitGroup) error {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
