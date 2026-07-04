package opend

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/keepalive"
)

type goldenFrame struct {
	ProtoID  uint32 `json:"proto_id"`
	Dir      string `json:"direction"`
	SerialNo uint32 `json:"serial_no"`
	BodyLen  int    `json:"body_len"`
	FrameHex string `json:"frame_hex"`
	BodyHex  string `json:"body_hex"`
}

func loadGolden(t *testing.T) []goldenFrame {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "golden", "frames.jsonl"))
	if err != nil {
		t.Skipf("no golden corpus (run scripts/capture_golden_frames.py against live OpenD): %v", err)
	}
	defer f.Close()
	var out []goldenFrame
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var g goldenFrame
		if err := json.Unmarshal(sc.Bytes(), &g); err != nil {
			t.Fatalf("golden line: %v", err)
		}
		out = append(out, g)
	}
	return out
}

// The codec must decode every real frame and re-encode it byte-for-byte.
func TestGoldenDecodeReencode(t *testing.T) {
	frames := loadGolden(t)
	if len(frames) == 0 {
		t.Skip("empty golden corpus")
	}
	for _, g := range frames {
		t.Run(g.Dir+"/"+itoa(g.ProtoID), func(t *testing.T) {
			raw, err := hex.DecodeString(g.FrameHex)
			if err != nil {
				t.Fatal(err)
			}
			f, err := Decode(raw)
			if err != nil {
				t.Fatalf("Decode real frame: %v", err)
			}
			if f.ProtoID != g.ProtoID || f.SerialNo != g.SerialNo || len(f.Body) != g.BodyLen {
				t.Fatalf("header mismatch: got protoID=%d serial=%d bodyLen=%d", f.ProtoID, f.SerialNo, len(f.Body))
			}
			wantBody, _ := hex.DecodeString(g.BodyHex)
			if !bytes.Equal(f.Body, wantBody) {
				t.Fatal("decoded body != stored body")
			}
			// Re-encode from the STORED body bytes (not a re-marshaled message):
			// must reproduce the exact wire frame.
			if got := Encode(g.ProtoID, g.SerialNo, wantBody); !bytes.Equal(got, raw) {
				t.Fatal("re-encoded frame != real frame bytes")
			}
		})
	}
}

// Spot-check that a real KeepAlive body decodes with the generated pb type.
func TestGoldenKeepAliveDecodesSemantically(t *testing.T) {
	for _, g := range loadGolden(t) {
		if g.ProtoID != 1004 || g.Dir != "s2c" {
			continue
		}
		body, _ := hex.DecodeString(g.BodyHex)
		var resp keepalive.Response
		if err := proto.Unmarshal(body, &resp); err != nil {
			t.Fatalf("keepalive decode: %v", err)
		}
		if resp.GetRetType() != 0 {
			t.Fatalf("keepalive retType = %d, want 0", resp.GetRetType())
		}
		return // one is enough
	}
	t.Skip("no KeepAlive s2c frame in corpus")
}

func itoa(u uint32) string {
	if u == 0 {
		return "0"
	}
	var b [10]byte
	i := len(b)
	for u > 0 {
		i--
		b[i] = byte('0' + u%10)
		u /= 10
	}
	return string(b[i:])
}
