package main

import (
	"reflect"
	"testing"
)

func TestChildArgsBase(t *testing.T) {
	got := childArgs(baseFlags{ConfigPath: "/c.toml"}, false)
	want := []string{"-config", "/c.toml", "-no-open"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("childArgs base:\n got=%v\nwant=%v", got, want)
	}
}

func TestChildArgsPreservesLogPath(t *testing.T) {
	got := childArgs(baseFlags{ConfigPath: "/c.toml", LogPath: "/var/log/etape.log"}, false)
	want := []string{"-config", "/c.toml", "-log", "/var/log/etape.log", "-no-open"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("childArgs log:\n got=%v\nwant=%v", got, want)
	}
}

// TestChildArgsDemo covers the StartDemo relaunch: a UI-triggered demo entry
// takes no knobs, so it must produce exactly -demo and nothing else.
func TestChildArgsDemo(t *testing.T) {
	got := childArgs(baseFlags{ConfigPath: "/c.toml"}, true)
	want := []string{"-config", "/c.toml", "-no-open", "-demo"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("childArgs demo:\n got=%v\nwant=%v", got, want)
	}
}
