package protocol

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"testing/iotest"
)

func TestEncodeHeaderLayout(t *testing.T) {
	p := &Packet{ClientVersion: 0x01020304, CmdID: 800, SeqID: 7, Body: []byte{0xAA, 0xBB}}
	got := p.Encode()
	want := []byte{
		0, 0, 0, 20,
		1, 2, 3, 4,
		0, 0, 0x03, 0x20,
		0, 0, 0, 7,
		0, 0, 0, 2,
		0xAA, 0xBB,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("Encode() = %x, want %x", got, want)
	}
}

func TestDecodeRoundTrip(t *testing.T) {
	in := &Packet{ClientVersion: 3, CmdID: 1050, SeqID: 99, Body: []byte("hello")}
	out, err := Decode(in.Encode(), DefaultMaxBodyLen)
	if err != nil {
		t.Fatal(err)
	}
	if out.ClientVersion != 3 || out.CmdID != 1050 || out.SeqID != 99 || string(out.Body) != "hello" {
		t.Fatalf("Decode() = %+v", out)
	}
}

func TestDecodeEmptyBody(t *testing.T) {
	in := &Packet{CmdID: 6}
	out, err := Decode(in.Encode(), DefaultMaxBodyLen)
	if err != nil {
		t.Fatal(err)
	}
	if out.CmdID != 6 || len(out.Body) != 0 {
		t.Fatalf("Decode() = %+v", out)
	}
}

func TestDecodeErrors(t *testing.T) {
	valid := (&Packet{CmdID: 1, Body: []byte("abc")}).Encode()

	badHeader := append([]byte(nil), valid...)
	badHeader[3] = 16

	tooLarge := (&Packet{CmdID: 1, Body: make([]byte, 11)}).Encode()

	cases := []struct {
		name string
		data []byte
		max  int
		want error
	}{
		{"short header", valid[:10], DefaultMaxBodyLen, ErrIncomplete},
		{"half body", valid[:HeaderLen+1], DefaultMaxBodyLen, ErrIncomplete},
		{"bad header len", badHeader, DefaultMaxBodyLen, ErrBadHeaderLen},
		{"body too large", tooLarge, 10, ErrBodyTooLarge},
		{"trailing", append(append([]byte(nil), valid...), 0), DefaultMaxBodyLen, ErrTrailingData},
		{"two packets", append(append([]byte(nil), valid...), valid...), DefaultMaxBodyLen, ErrTrailingData},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Decode(c.data, c.max); !errors.Is(err, c.want) {
				t.Fatalf("Decode() err = %v, want %v", err, c.want)
			}
		})
	}
}

func TestDecodeCopiesBody(t *testing.T) {
	frame := (&Packet{CmdID: 1, Body: []byte("abc")}).Encode()
	p, err := Decode(frame, DefaultMaxBodyLen)
	if err != nil {
		t.Fatal(err)
	}
	frame[HeaderLen] = 'X'
	if string(p.Body) != "abc" {
		t.Fatalf("body aliases input: %q", p.Body)
	}
}

func readAll(t *testing.T, r *Reader) ([]*Packet, error) {
	t.Helper()
	var out []*Packet
	for {
		p, err := r.Next()
		if err != nil {
			if err == io.EOF {
				return out, nil
			}
			return out, err
		}
		out = append(out, p)
	}
}

func TestReaderStickyPackets(t *testing.T) {
	var stream bytes.Buffer
	for i := uint32(1); i <= 3; i++ {
		stream.Write((&Packet{CmdID: i, SeqID: i, Body: bytes.Repeat([]byte{byte(i)}, int(i)*10)}).Encode())
	}
	stream.Write((&Packet{CmdID: 6}).Encode())

	pkts, err := readAll(t, NewReader(&stream, DefaultMaxBodyLen))
	if err != nil {
		t.Fatal(err)
	}
	if len(pkts) != 4 {
		t.Fatalf("got %d packets, want 4", len(pkts))
	}
	for i, p := range pkts[:3] {
		n := i + 1
		if p.CmdID != uint32(n) || len(p.Body) != n*10 || p.Body[0] != byte(n) {
			t.Fatalf("packet %d = %+v", i, p)
		}
	}
	if pkts[3].CmdID != 6 || len(pkts[3].Body) != 0 {
		t.Fatalf("heartbeat packet = %+v", pkts[3])
	}
}

func TestReaderHalfPackets(t *testing.T) {
	var stream bytes.Buffer
	stream.Write((&Packet{CmdID: 1001, SeqID: 1, Body: []byte("first")}).Encode())
	stream.Write((&Packet{CmdID: 1002, SeqID: 2, Body: bytes.Repeat([]byte("x"), 70000)}).Encode())

	pkts, err := readAll(t, NewReader(iotest.OneByteReader(&stream), DefaultMaxBodyLen))
	if err != nil {
		t.Fatal(err)
	}
	if len(pkts) != 2 || string(pkts[0].Body) != "first" || len(pkts[1].Body) != 70000 {
		t.Fatalf("unexpected packets: %d", len(pkts))
	}
}

func TestReaderTruncatedStream(t *testing.T) {
	frame := (&Packet{CmdID: 1, Body: []byte("abcdef")}).Encode()
	_, err := readAll(t, NewReader(bytes.NewReader(frame[:len(frame)-2]), DefaultMaxBodyLen))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestReaderOversizedPacket(t *testing.T) {
	var stream bytes.Buffer
	stream.Write((&Packet{CmdID: 1, Body: []byte("ok")}).Encode())
	stream.Write((&Packet{CmdID: 2, Body: make([]byte, 1025)}).Encode())

	pkts, err := readAll(t, NewReader(&stream, 1024))
	if !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("err = %v, want ErrBodyTooLarge", err)
	}
	if len(pkts) != 1 || pkts[0].CmdID != 1 {
		t.Fatalf("packets before error = %+v", pkts)
	}
}

func TestReaderOversizedHeaderOnly(t *testing.T) {
	// A header announcing a huge body must fail before the body arrives.
	hdr := (&Packet{CmdID: 2}).Encode()
	hdr[16], hdr[17], hdr[18], hdr[19] = 0x7F, 0xFF, 0xFF, 0xFF
	_, err := NewReader(bytes.NewReader(hdr), DefaultMaxBodyLen).Next()
	if !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("err = %v, want ErrBodyTooLarge", err)
	}
}

func TestReaderBadHeaderLen(t *testing.T) {
	frame := (&Packet{CmdID: 1}).Encode()
	frame[3] = 24
	_, err := NewReader(bytes.NewReader(frame), DefaultMaxBodyLen).Next()
	if !errors.Is(err, ErrBadHeaderLen) {
		t.Fatalf("err = %v, want ErrBadHeaderLen", err)
	}
}
