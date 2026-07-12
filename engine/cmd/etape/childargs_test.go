package main

import (
	"reflect"
	"testing"
)

func TestChildArgsReplay(t *testing.T) {
	got := childArgs(baseFlags{ConfigPath: "/c.toml", DistDir: "ui/dist"}, replayMode{Live: false, Day: "2026-07-06", Speed: 4})
	want := []string{"-config", "/c.toml", "-dist", "ui/dist", "-no-open", "-replay", "2026-07-06", "-speed", "4", "-replay-hold"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("childArgs replay:\n got=%v\nwant=%v", got, want)
	}
}

func TestChildArgsLiveOmitsReplayFlags(t *testing.T) {
	got := childArgs(baseFlags{ConfigPath: "/c.toml"}, replayMode{Live: true})
	want := []string{"-config", "/c.toml", "-no-open"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("childArgs live:\n got=%v\nwant=%v", got, want)
	}
}

func TestChildArgsPreservesLogPath(t *testing.T) {
	got := childArgs(baseFlags{ConfigPath: "/c.toml", LogPath: "/var/log/etape.log"}, replayMode{Live: true})
	want := []string{"-config", "/c.toml", "-log", "/var/log/etape.log", "-no-open"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("childArgs log:\n got=%v\nwant=%v", got, want)
	}
}

// TestChildArgsDemo covers the StartDemo relaunch (Task 1): a UI-triggered
// demo entry takes no knobs (-demo-day/-demo-speed were removed from the
// synth chunk), so it must produce exactly -demo and nothing else — in
// particular it must NOT fall into the replay branch just because Live is
// left at its zero value (false).
func TestChildArgsDemo(t *testing.T) {
	got := childArgs(baseFlags{ConfigPath: "/c.toml"}, replayMode{Demo: true})
	want := []string{"-config", "/c.toml", "-no-open", "-demo"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("childArgs demo:\n got=%v\nwant=%v", got, want)
	}
	for _, a := range got {
		if a == "-replay" || a == "-speed" {
			t.Fatalf("childArgs demo must not include replay flags, got=%v", got)
		}
	}
}
