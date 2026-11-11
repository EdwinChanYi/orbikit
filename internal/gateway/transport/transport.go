// Package transport accepts client connections over TCP or WebSocket and
// exposes both as a packet-oriented Conn, so session and business code never
// see the underlying transport.
package transport

import (
	"time"

	"github.com/yourname/roomkit/internal/gateway/protocol"
)

// Conn is a packet-oriented client connection.
//
// ReadPacket must be called from a single goroutine, WritePackets from a
// single (possibly different) goroutine. Close may be called concurrently.
type Conn interface {
	// ReadPacket blocks until a whole packet arrives or the idle timeout expires.
	ReadPacket() (*protocol.Packet, error)
	// WritePackets writes encoded packets in order before deadline.
	WritePackets(pkts [][]byte, deadline time.Time) error
	Close() error
	RemoteAddr() string
	// Kind names the transport, e.g. "tcp" or "ws".
	Kind() string
}

// Handler serves one accepted connection and returns when it is finished.
type Handler func(c Conn, codec protocol.Codec)
