package opend

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
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
