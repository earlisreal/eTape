package opend

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"io"
	"testing"
)

func TestEncodeLayoutMatchesSpec(t *testing.T) {
	body := []byte("hello-opend")
	frame := Encode(1001, 42, body)

	if len(frame) != HeaderLen+len(body) {
		t.Fatalf("frame len = %d, want %d", len(frame), HeaderLen+len(body))
	}
	if frame[0] != 'F' || frame[1] != 'T' {
		t.Fatalf("magic = %q, want FT", frame[0:2])
	}
	if got := binary.LittleEndian.Uint32(frame[2:6]); got != 1001 {
		t.Fatalf("protoID = %d, want 1001", got)
	}
	if frame[6] != FmtProtobuf {
		t.Fatalf("fmtType = %d, want %d", frame[6], FmtProtobuf)
	}
	if frame[7] != 0 {
		t.Fatalf("protoVer = %d, want 0", frame[7])
	}
	if got := binary.LittleEndian.Uint32(frame[8:12]); got != 42 {
		t.Fatalf("serialNo = %d, want 42", got)
	}
	if got := binary.LittleEndian.Uint32(frame[12:16]); got != uint32(len(body)) {
		t.Fatalf("bodyLen = %d, want %d", got, len(body))
	}
	sum := sha1.Sum(body)
	if !bytes.Equal(frame[16:36], sum[:]) {
		t.Fatal("sha1 mismatch in header")
	}
	for i := 36; i < 44; i++ {
		if frame[i] != 0 {
			t.Fatalf("reserved byte %d = %d, want 0", i, frame[i])
		}
	}
	if !bytes.Equal(frame[HeaderLen:], body) {
		t.Fatal("body not appended verbatim")
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	body := []byte{0x0a, 0x03, 0x66, 0x6f, 0x6f}
	f, err := Decode(Encode(3001, 7, body))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if f.ProtoID != 3001 || f.SerialNo != 7 || f.FmtType != FmtProtobuf {
		t.Fatalf("decoded header = %+v", f)
	}
	if !bytes.Equal(f.Body, body) {
		t.Fatalf("decoded body = %x, want %x", f.Body, body)
	}
}

func TestDecodeErrors(t *testing.T) {
	good := Encode(1001, 1, []byte("abc"))

	if _, err := Decode(good[:10]); err != ErrShortHeader {
		t.Fatalf("short header err = %v, want ErrShortHeader", err)
	}
	bad := append([]byte(nil), good...)
	bad[0] = 'X'
	if _, err := Decode(bad); err != ErrBadMagic {
		t.Fatalf("bad magic err = %v, want ErrBadMagic", err)
	}
	short := append([]byte(nil), good...)
	short = short[:len(short)-1] // body one byte short of header's bodyLen
	if _, err := Decode(short); err != ErrBodyLen {
		t.Fatalf("body len err = %v, want ErrBodyLen", err)
	}
	corrupt := append([]byte(nil), good...)
	corrupt[HeaderLen] ^= 0xFF // flip a body byte → SHA1 no longer matches
	if _, err := Decode(corrupt); err != ErrBadSHA1 {
		t.Fatalf("sha1 err = %v, want ErrBadSHA1", err)
	}
}

func TestFrameReaderPipelinedAndPartial(t *testing.T) {
	// Two frames concatenated, fed through a reader that yields tiny chunks,
	// exercising both pipelining (>1 frame buffered) and partial reads.
	a := Encode(1001, 1, []byte("first"))
	b := Encode(1004, 2, []byte("second-frame-body"))
	stream := append(append([]byte(nil), a...), b...)

	fr := NewFrameReader(&chunkyReader{data: stream, chunk: 3})

	f1, err := fr.ReadFrame()
	if err != nil {
		t.Fatalf("frame 1: %v", err)
	}
	if f1.ProtoID != 1001 || string(f1.Body) != "first" {
		t.Fatalf("frame 1 = %+v", f1)
	}
	f2, err := fr.ReadFrame()
	if err != nil {
		t.Fatalf("frame 2: %v", err)
	}
	if f2.ProtoID != 1004 || string(f2.Body) != "second-frame-body" {
		t.Fatalf("frame 2 = %+v", f2)
	}
	if _, err := fr.ReadFrame(); err != io.EOF {
		t.Fatalf("after last frame err = %v, want io.EOF", err)
	}
}

func TestFrameReaderRejectsCorruptBody(t *testing.T) {
	f := Encode(3001, 9, []byte("payload"))
	f[HeaderLen+1] ^= 0xFF // corrupt a body byte
	if _, err := NewFrameReader(bytes.NewReader(f)).ReadFrame(); err != ErrBadSHA1 {
		t.Fatalf("err = %v, want ErrBadSHA1", err)
	}
}

// chunkyReader yields at most `chunk` bytes per Read to simulate a dribbling socket.
type chunkyReader struct {
	data  []byte
	chunk int
	pos   int
}

func (c *chunkyReader) Read(p []byte) (int, error) {
	if c.pos >= len(c.data) {
		return 0, io.EOF
	}
	n := c.chunk
	if n > len(p) {
		n = len(p)
	}
	if n > len(c.data)-c.pos {
		n = len(c.data) - c.pos
	}
	copy(p, c.data[c.pos:c.pos+n])
	c.pos += n
	return n, nil
}
