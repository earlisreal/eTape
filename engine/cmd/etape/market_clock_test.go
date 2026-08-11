package main

import (
	"testing"
	"time"

	getglobalstate "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/getglobalstate"
	"google.golang.org/protobuf/proto"
)

func TestReconstructedMarketTimeUsesOpenDFraction(t *testing.T) {
	s2c := &getglobalstate.S2C{
		Time:      proto.Int64(1_700_000_100),
		LocalTime: proto.Float64(1_700_000_098.250),
	}
	got, ok := reconstructedMarketTimeMs(s2c)
	if !ok || got != 1_700_000_100_250 {
		t.Fatalf("reconstructed market time=(%d,%v), want (1700000100250,true)", got, ok)
	}
}

func TestMarketClockOffsetCompensatesRequestMidpoint(t *testing.T) {
	s2c := &getglobalstate.S2C{
		Time:      proto.Int64(1_700_000_100),
		LocalTime: proto.Float64(1_700_000_098.250),
	}
	midpoint := time.UnixMilli(1_700_000_098_260)
	got, ok := marketClockOffset(s2c, midpoint, 20*time.Millisecond)
	if !ok || got != 1_990 {
		t.Fatalf("offset=(%d,%v), want (1990,true)", got, ok)
	}
}

func TestMarketClockOffsetRejectsInvalidAndSlowSamples(t *testing.T) {
	valid := &getglobalstate.S2C{
		Time:      proto.Int64(1_700_000_100),
		LocalTime: proto.Float64(1_700_000_098.250),
	}
	midpoint := time.UnixMilli(1_700_000_098_260)
	if _, ok := marketClockOffset(valid, midpoint, 2*time.Second+time.Millisecond); ok {
		t.Fatal("slow sample must be rejected")
	}
	if _, ok := marketClockOffset(&getglobalstate.S2C{Time: proto.Int64(0), LocalTime: proto.Float64(1)}, midpoint, time.Millisecond); ok {
		t.Fatal("missing server time must be rejected")
	}
	if _, ok := marketClockOffset(&getglobalstate.S2C{Time: proto.Int64(1), LocalTime: proto.Float64(0)}, midpoint, time.Millisecond); ok {
		t.Fatal("missing local time must be rejected")
	}
}

func TestMedianInt64UsesMiddleOfRollingWindow(t *testing.T) {
	if got := medianInt64([]int64{2_050, 1_980, 2_000, 2_020, 1_990}); got != 2_000 {
		t.Fatalf("median=%d, want 2000", got)
	}
}
