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
