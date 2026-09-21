package uihub

import (
	"testing"

	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

func TestWindowStateDecodeAndNormalize(t *testing.T) {
	doc, err := decodeWindowState(`{"version":1,"entries":[` +
		`{"workspaceId":"window-2","x":-1920,"y":0,"width":1200,"height":900},` +
		`{"workspaceId":"window-2","x":10,"y":20,"width":1200,"height":900},` +
		`{"workspaceId":"bad id","x":0,"y":0,"width":1200,"height":900},` +
		`{"workspaceId":"deleted","x":0,"y":0,"width":1200,"height":900}` +
		`]}`)
	if err != nil {
		t.Fatal(err)
	}
	got, dropped := normalizeWindowState(doc, map[string]bool{"window-2": true})
	if len(dropped) != 2 || dropped[0] != "bad id" || dropped[1] != "deleted" {
		t.Fatalf("dropped = %v, want invalid and catalog-missing ids", dropped)
	}
	if len(got.Entries) != 1 || got.Entries[0].X != 10 {
		t.Fatalf("normalized entries = %+v, want last valid duplicate", got.Entries)
	}
}

func TestWindowStateRegistryReleasesOnlyLastOwner(t *testing.T) {
	cfg := &spyCfg{values: map[string]string{}}
	r := newWindowStateRegistry(cfg)
	args := wsmsg.SetWindowStateArgs{WorkspaceID: "monitoring", X: 1920, Width: 1200, Height: 900}
	if ack := r.set(1, args); ack.Status != wsmsg.AckAccepted {
		t.Fatalf("first set ack = %+v", ack)
	}
	sets := cfg.sets
	if ack := r.set(1, args); ack.Status != wsmsg.AckAccepted || cfg.sets != sets {
		t.Fatalf("unchanged set ack=%+v writes=%d want no additional write", ack, cfg.sets-sets)
	}
	if ack := r.set(2, args); ack.Status != wsmsg.AckAccepted {
		t.Fatalf("second set ack = %+v", ack)
	}
	r.release(1)
	if cfg.got[WindowStateConfigKey] == "" {
		t.Fatal("release of first owner should persist a state document")
	}
	doc, err := decodeWindowState(cfg.got[WindowStateConfigKey])
	if err != nil || len(doc.Entries) != 1 {
		t.Fatalf("after first release entries = %+v, err=%v; want shared entry", doc.Entries, err)
	}
	r.release(2)
	doc, err = decodeWindowState(cfg.got[WindowStateConfigKey])
	if err != nil || len(doc.Entries) != 0 {
		t.Fatalf("after last release entries = %+v, err=%v; want empty", doc.Entries, err)
	}
}

func TestWindowStateRegistryRejectsBadBounds(t *testing.T) {
	r := newWindowStateRegistry(&spyCfg{values: map[string]string{}})
	ack := r.set(1, wsmsg.SetWindowStateArgs{WorkspaceID: "main", Width: 0, Height: 800})
	if ack.Status != wsmsg.AckBlocked {
		t.Fatalf("ack = %+v, want blocked", ack)
	}
}

type windowStateTestClient struct{ idValue uint64 }

func (c *windowStateTestClient) id() uint64                  { return c.idValue }
func (c *windowStateTestClient) enqueue([]byte, string) bool { return true }
func (c *windowStateTestClient) close()                      {}

func TestHubShutdownLeavesWindowStateOwnedByDisconnectedClient(t *testing.T) {
	cfg := &spyCfg{values: map[string]string{}}
	r := newWindowStateRegistry(cfg)
	if ack := r.set(7, wsmsg.SetWindowStateArgs{WorkspaceID: "monitoring", X: 10, Y: 20, Width: 1200, Height: 900}); ack.Status != wsmsg.AckAccepted {
		t.Fatalf("set ack = %+v", ack)
	}
	h := &Hub{closed: make(chan struct{}), cmd: &commands{windowState: r}}
	close(h.closed)
	h.Unregister(&windowStateTestClient{idValue: 7})
	doc, err := decodeWindowState(cfg.got[WindowStateConfigKey])
	if err != nil || len(doc.Entries) != 1 || doc.Entries[0].WorkspaceID != "monitoring" {
		t.Fatalf("shutdown state = %+v, err=%v; want preserved entry", doc, err)
	}
}
