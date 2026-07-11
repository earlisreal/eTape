package uihub

import (
	"testing"

	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

func TestSnapshotFramesSession(t *testing.T) {
	m := newMirror(nil, wsmsg.GlobalLimitsView{}, 10, 10, 10, 10, 10)
	m.session = wsmsg.SessionSnapshot{Mode: "replay", Day: "2026-07-06", Speed: 4}
	frames := m.snapshotFrames(wsmsg.TopicSysSession)
	if len(frames) != 1 {
		t.Fatalf("want 1 frame, got %d", len(frames))
	}
	got, ok := frames[0].Payload.(wsmsg.SessionSnapshot)
	if !ok || got.Mode != "replay" || got.Day != "2026-07-06" || got.Speed != 4 {
		t.Fatalf("bad session frame: %+v", frames[0].Payload)
	}
}
