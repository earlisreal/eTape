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

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/keepalive"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetkl"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotrequesthistorykl"
)

type goldenFrame struct {
	ProtoID  uint32 `json:"proto_id"`
	Dir      string `json:"direction"`
	SerialNo uint32 `json:"serial_no"`
	BodyLen  int    `json:"body_len"`
	FrameHex string `json:"frame_hex"`
	BodyHex  string `json:"body_hex"`
}

// readGoldenFile parses one testdata/golden/<name>.jsonl file. It returns
// (nil, err) if the file doesn't exist — the caller decides whether that's
// fatal (loadGolden) or just means "not captured yet" (goldenFrames), since
// the qot_*.jsonl fixtures are live-dependent and may not all exist.
func readGoldenFile(t *testing.T, name string) ([]goldenFrame, error) {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "golden", name))
	if err != nil {
		return nil, err
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
			t.Fatalf("%s: golden line: %v", name, err)
		}
		out = append(out, g)
	}
	return out, nil
}

func loadGolden(t *testing.T) []goldenFrame {
	t.Helper()
	out, err := readGoldenFile(t, "frames.jsonl")
	if err != nil {
		t.Skipf("no golden corpus (run scripts/capture_golden_frames.py against live OpenD): %v", err)
	}
	return out
}

// goldenFrames loads and concatenates frames from one or more
// testdata/golden/<name>.jsonl files, in order. Missing files are skipped
// (not fatal): the qot capture (scripts/capture_golden_frames.py --qot) is
// live-dependent and may not have produced every file yet — callers should
// t.Skip when the combined result is empty.
func goldenFrames(t *testing.T, names ...string) []goldenFrame {
	t.Helper()
	var out []goldenFrame
	for _, name := range names {
		frames, err := readGoldenFile(t, name)
		if err != nil {
			continue
		}
		out = append(out, frames...)
	}
	return out
}

// qotGoldenFiles is the fixed set of per-protocol fixtures that
// scripts/capture_golden_frames.py --qot SYMBOL --secs N may produce. Push
// files (qot_update_*) only populate when the market was live during
// capture; goldenFrames tolerates whichever subset exists.
var qotGoldenFiles = []string{
	"qot_sub.jsonl",
	"qot_getbasicqot.jsonl",
	"qot_getkl.jsonl",
	"qot_getticker.jsonl",
	"qot_getorderbook.jsonl",
	"qot_requesthistorykl.jsonl",
	"qot_update_basicqot.jsonl",
	"qot_update_ticker.jsonl",
	"qot_update_orderbook.jsonl",
	"qot_update_kl.jsonl",
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

// TestGoldenQotFramesReencode is TestGoldenDecodeReencode's check applied to
// the Plan 2 qot corpus (separate files from frames.jsonl, since the qot
// capture is live-dependent and lands independently).
func TestGoldenQotFramesReencode(t *testing.T) {
	frames := goldenFrames(t, qotGoldenFiles...)
	if len(frames) == 0 {
		t.Skip("no qot golden fixtures (run scripts/capture_golden_frames.py --qot SYMBOL --secs N against live OpenD)")
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
			if got := Encode(g.ProtoID, g.SerialNo, wantBody); !bytes.Equal(got, raw) {
				t.Fatal("re-encoded frame != real frame bytes")
			}
		})
	}
}

// TestGoldenQotPushesDecode requires every captured push frame (protoID in
// IsPushProtoID) to decode cleanly via DecodePush. Zero events is acceptable
// (e.g. a K-line push for a non-1m KLType is intentionally filtered) — the
// requirement is "no error", not "at least one event".
func TestGoldenQotPushesDecode(t *testing.T) {
	frames := goldenFrames(t, qotGoldenFiles...)
	if len(frames) == 0 {
		t.Skip("no qot golden fixtures (run scripts/capture_golden_frames.py --qot SYMBOL --secs N against live OpenD)")
	}
	found := false
	for _, g := range frames {
		if g.Dir != "s2c" || !IsPushProtoID(g.ProtoID) {
			continue
		}
		found = true
		raw, err := hex.DecodeString(g.FrameHex)
		if err != nil {
			t.Fatalf("proto %d: %v", g.ProtoID, err)
		}
		f, err := Decode(raw)
		if err != nil {
			t.Fatalf("proto %d: Decode: %v", g.ProtoID, err)
		}
		if _, err := DecodePush(f); err != nil {
			t.Fatalf("proto %d: DecodePush: %v", g.ProtoID, err)
		}
	}
	if !found {
		t.Skip("no push frames captured yet (market idle) — re-run capture during a live session")
	}
}

// TestGoldenQotKLinesNormalizeToMinuteBuckets pins the end-label → start-label
// K-line normalization (decodeKLine, see decode.go) against real OpenD bytes:
// every K-line in a real Qot_GetKL/Qot_RequestHistoryKL response must decode
// to a BucketMs that lands on a 60,000ms boundary.
func TestGoldenQotKLinesNormalizeToMinuteBuckets(t *testing.T) {
	frames := goldenFrames(t, "qot_getkl.jsonl", "qot_requesthistorykl.jsonl")
	if len(frames) == 0 {
		t.Skip("no qot K-line golden fixtures (run scripts/capture_golden_frames.py --qot SYMBOL --secs N against live OpenD)")
	}
	checked := 0
	for _, f := range frames {
		if f.Dir != "s2c" {
			continue
		}
		body, err := hex.DecodeString(f.BodyHex)
		if err != nil {
			t.Fatalf("%s/%d: %v", f.Dir, f.ProtoID, err)
		}

		var klList []*qotcommon.KLine
		switch f.ProtoID {
		case ProtoQotGetKL:
			var resp qotgetkl.Response
			if err := proto.Unmarshal(body, &resp); err != nil {
				t.Fatalf("qot_getkl.jsonl: %v", err)
			}
			if resp.GetRetType() != 0 {
				continue // error response carries no K-lines to check
			}
			klList = resp.GetS2C().GetKlList()
		case ProtoQotRequestHistoryKL:
			var resp qotrequesthistorykl.Response
			if err := proto.Unmarshal(body, &resp); err != nil {
				t.Fatalf("qot_requesthistorykl.jsonl: %v", err)
			}
			if resp.GetRetType() != 0 {
				continue
			}
			klList = resp.GetS2C().GetKlList()
		default:
			continue
		}

		for _, k := range klList {
			bar, err := decodeKLine("US.TEST", k, feed.Res1m)
			if err != nil {
				t.Fatalf("proto %d: decodeKLine: %v", f.ProtoID, err)
			}
			if bar.BucketMs%60_000 != 0 {
				t.Fatalf("proto %d: bucket %d not minute-aligned", f.ProtoID, bar.BucketMs)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Skip("no K-lines in captured qot_getkl.jsonl/qot_requesthistorykl.jsonl responses")
	}
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
