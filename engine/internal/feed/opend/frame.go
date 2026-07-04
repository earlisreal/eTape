// Package opend is the moomoo OpenD adapter: raw-TCP framing, generated protobuf,
// and the connection client. It is the only package that knows moomoo exists.
package opend

import (
	"bufio"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"io"
)

// HeaderLen is the fixed OpenD frame header size (verified from the SDK:
// struct fmt "<1s1sI2B2I20s8s", pack(1), little-endian).
const HeaderLen = 44

// Protocol format types (Common.ProtoFmt). eTape only ever sends Protobuf.
const (
	FmtProtobuf uint8 = 0
	FmtJSON     uint8 = 1
)

const (
	magic0   = 'F'
	magic1   = 'T'
	protoVer = 0 // API_PROTO_VER
)

// Codec errors.
var (
	ErrShortHeader = errors.New("opend: frame shorter than 44-byte header")
	ErrBadMagic    = errors.New("opend: bad frame magic (want \"FT\")")
	ErrBodyLen     = errors.New("opend: frame length does not match header bodyLen")
	ErrBadSHA1     = errors.New("opend: body SHA1 does not match header")
)

// Frame is one decoded OpenD message: header identity + raw protobuf body.
type Frame struct {
	ProtoID  uint32
	FmtType  uint8
	ProtoVer uint8
	SerialNo uint32
	Body     []byte
}

// Encode builds a complete wire frame: 44-byte header (fmtType=protobuf,
// protoVer=0, SHA1 over body, 8 zero reserved bytes) followed by body.
func Encode(protoID, serialNo uint32, body []byte) []byte {
	sum := sha1.Sum(body)
	buf := make([]byte, HeaderLen+len(body))
	buf[0] = magic0
	buf[1] = magic1
	binary.LittleEndian.PutUint32(buf[2:6], protoID)
	buf[6] = FmtProtobuf
	buf[7] = protoVer
	binary.LittleEndian.PutUint32(buf[8:12], serialNo)
	binary.LittleEndian.PutUint32(buf[12:16], uint32(len(body)))
	copy(buf[16:36], sum[:])
	// buf[36:44] reserved — left zero.
	copy(buf[HeaderLen:], body)
	return buf
}

type header struct {
	protoID  uint32
	fmtType  uint8
	protoVer uint8
	serialNo uint32
	bodyLen  uint32
	sha20    [20]byte
}

// parseHeader decodes the fixed 44-byte header. It does not touch the body.
func parseHeader(b []byte) (header, error) {
	if len(b) < HeaderLen {
		return header{}, ErrShortHeader
	}
	if b[0] != magic0 || b[1] != magic1 {
		return header{}, ErrBadMagic
	}
	var h header
	h.protoID = binary.LittleEndian.Uint32(b[2:6])
	h.fmtType = b[6]
	h.protoVer = b[7]
	h.serialNo = binary.LittleEndian.Uint32(b[8:12])
	h.bodyLen = binary.LittleEndian.Uint32(b[12:16])
	copy(h.sha20[:], b[16:36])
	return h, nil
}

// Decode parses one complete frame (header immediately followed by its body)
// and verifies the body length and SHA1.
func Decode(frame []byte) (Frame, error) {
	h, err := parseHeader(frame)
	if err != nil {
		return Frame{}, err
	}
	if uint32(len(frame)-HeaderLen) != h.bodyLen {
		return Frame{}, ErrBodyLen
	}
	body := frame[HeaderLen : HeaderLen+int(h.bodyLen)]
	if sha1.Sum(body) != h.sha20 {
		return Frame{}, ErrBadSHA1
	}
	return Frame{
		ProtoID:  h.protoID,
		FmtType:  h.fmtType,
		ProtoVer: h.protoVer,
		SerialNo: h.serialNo,
		Body:     append([]byte(nil), body...),
	}, nil
}

// FrameReader reads whole frames from a byte stream (e.g. a TCP connection),
// blocking until each frame is complete. It is used by exactly one reader
// goroutine per connection.
type FrameReader struct {
	r *bufio.Reader
}

// NewFrameReader wraps r with a 128 KiB buffer (matching the SDK's recv size).
func NewFrameReader(r io.Reader) *FrameReader {
	return &FrameReader{r: bufio.NewReaderSize(r, 128*1024)}
}

// ReadFrame reads exactly one frame. It returns the underlying read error
// (io.EOF/io.ErrUnexpectedEOF on close) or a codec error on corruption.
func (fr *FrameReader) ReadFrame() (Frame, error) {
	var head [HeaderLen]byte
	if _, err := io.ReadFull(fr.r, head[:]); err != nil {
		return Frame{}, err
	}
	h, err := parseHeader(head[:])
	if err != nil {
		return Frame{}, err
	}
	body := make([]byte, h.bodyLen)
	if _, err := io.ReadFull(fr.r, body); err != nil {
		return Frame{}, err
	}
	if sha1.Sum(body) != h.sha20 {
		return Frame{}, ErrBadSHA1
	}
	return Frame{
		ProtoID:  h.protoID,
		FmtType:  h.fmtType,
		ProtoVer: h.protoVer,
		SerialNo: h.serialNo,
		Body:     body,
	}, nil
}
