// Package protocol implements the gateway wire format: a fixed 20-byte header
// followed by a body whose encoding is chosen by a Codec.
//
// Header layout, every field a big-endian uint32:
//
//	0       4                8        12       16       20
//	+-------+----------------+--------+--------+--------+
//	| hlen  | client_version | cmd_id | seq_id | body   |
//	| (=20) |                |        |        | _len   |
//	+-------+----------------+--------+--------+--------+
package protocol

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	// HeaderLen is the only accepted value of the header length field.
	HeaderLen = 20
	// DefaultMaxBodyLen bounds the body of a single packet.
	DefaultMaxBodyLen = 256 * 1024
)

var (
	// ErrIncomplete means more bytes are needed before a packet can be decoded.
	ErrIncomplete = errors.New("protocol: incomplete packet")
	// ErrBadHeaderLen means the header length field is not HeaderLen.
	ErrBadHeaderLen = errors.New("protocol: bad header length")
	// ErrBodyTooLarge means the body length exceeds the configured limit.
	ErrBodyTooLarge = errors.New("protocol: body too large")
	// ErrTrailingData means a frame holds more bytes than one packet.
	ErrTrailingData = errors.New("protocol: trailing data after packet")
)

// Packet is one decoded frame.
type Packet struct {
	ClientVersion uint32
	CmdID         uint32
	SeqID         uint32
	Body          []byte
}

// Encode serializes p into a new buffer.
func (p *Packet) Encode() []byte {
	buf := make([]byte, HeaderLen+len(p.Body))
	binary.BigEndian.PutUint32(buf[0:4], HeaderLen)
	binary.BigEndian.PutUint32(buf[4:8], p.ClientVersion)
	binary.BigEndian.PutUint32(buf[8:12], p.CmdID)
	binary.BigEndian.PutUint32(buf[12:16], p.SeqID)
	binary.BigEndian.PutUint32(buf[16:20], uint32(len(p.Body)))
	copy(buf[HeaderLen:], p.Body)
	return buf
}

// FrameLen inspects the start of data and returns the total length of the
// first packet. It returns ErrIncomplete when data is too short to tell or to
// hold the whole packet.
func FrameLen(data []byte, maxBodyLen int) (int, error) {
	if len(data) < HeaderLen {
		return 0, ErrIncomplete
	}
	if binary.BigEndian.Uint32(data[0:4]) != HeaderLen {
		return 0, ErrBadHeaderLen
	}
	bodyLen := binary.BigEndian.Uint32(data[16:20])
	if uint64(bodyLen) > uint64(maxBodyLen) {
		return 0, fmt.Errorf("%w: %d > %d", ErrBodyTooLarge, bodyLen, maxBodyLen)
	}
	total := HeaderLen + int(bodyLen)
	if len(data) < total {
		return 0, ErrIncomplete
	}
	return total, nil
}

// Decode parses data that must hold exactly one packet. The body is copied.
func Decode(data []byte, maxBodyLen int) (*Packet, error) {
	n, err := FrameLen(data, maxBodyLen)
	if err != nil {
		return nil, err
	}
	if n != len(data) {
		return nil, ErrTrailingData
	}
	return decodeFrame(data), nil
}

func decodeFrame(frame []byte) *Packet {
	body := make([]byte, len(frame)-HeaderLen)
	copy(body, frame[HeaderLen:])
	return &Packet{
		ClientVersion: binary.BigEndian.Uint32(frame[4:8]),
		CmdID:         binary.BigEndian.Uint32(frame[8:12]),
		SeqID:         binary.BigEndian.Uint32(frame[12:16]),
		Body:          body,
	}
}

// SplitFunc returns a bufio.SplitFunc that yields one whole packet per token,
// handling partial reads and multiple packets per read.
func SplitFunc(maxBodyLen int) bufio.SplitFunc {
	return func(data []byte, atEOF bool) (int, []byte, error) {
		if atEOF && len(data) == 0 {
			return 0, nil, nil
		}
		n, err := FrameLen(data, maxBodyLen)
		switch {
		case err == nil:
			return n, data[:n], nil
		case errors.Is(err, ErrIncomplete):
			if atEOF {
				return 0, nil, io.ErrUnexpectedEOF
			}
			return 0, nil, nil
		default:
			return 0, nil, err
		}
	}
}

// Reader reads packets from a byte stream such as a TCP connection.
type Reader struct {
	sc         *bufio.Scanner
	maxBodyLen int
}

// NewReader wraps r. maxBodyLen <= 0 selects DefaultMaxBodyLen.
func NewReader(r io.Reader, maxBodyLen int) *Reader {
	if maxBodyLen <= 0 {
		maxBodyLen = DefaultMaxBodyLen
	}
	sc := bufio.NewScanner(r)
	initial := 64 * 1024
	if limit := HeaderLen + maxBodyLen; initial > limit {
		initial = limit
	}
	sc.Buffer(make([]byte, initial), HeaderLen+maxBodyLen)
	sc.Split(SplitFunc(maxBodyLen))
	return &Reader{sc: sc, maxBodyLen: maxBodyLen}
}

// Next returns the next packet, or io.EOF on a clean end of stream.
func (r *Reader) Next() (*Packet, error) {
	if !r.sc.Scan() {
		if err := r.sc.Err(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	return decodeFrame(r.sc.Bytes()), nil
}
