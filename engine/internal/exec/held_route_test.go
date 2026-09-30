package exec

import (
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/session"
)

func TestResolveHeldStopLimitRoute(t *testing.T) {
	pre := time.Date(2026, 9, 30, 8, 0, 0, 0, session.Loc())
	post := time.Date(2026, 9, 30, 17, 0, 0, 0, session.Loc())
	rth := time.Date(2026, 9, 30, 10, 0, 0, 0, session.Loc())
	earlyPost := time.Date(2026, 11, 27, 15, 0, 0, 0, session.Loc())
	tests := []struct {
		name     string
		now      time.Time
		tif      TIF
		session  OrderSession
		want     HeldRoute
		deadline time.Time
	}{
		{"premarket auto day", pre, TIFDay, SessionAuto, RouteEngineHeld, session.Schedule(pre).Open},
		{"postmarket extended day", post, TIFDay, SessionExtended, RouteEngineHeld, session.Schedule(post).DataClose},
		{"explicit rth", pre, TIFDay, SessionRTH, RouteNative, time.Time{}},
		{"gtc", pre, TIFGTC, SessionExtended, RouteNative, time.Time{}},
		{"rth hours", rth, TIFDay, SessionAuto, RouteNative, time.Time{}},
		{"early close deadline", earlyPost, TIFDay, SessionAuto, RouteEngineHeld, session.Schedule(earlyPost).DataClose},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _, deadline := ResolveStopLimitRoute(tt.now, tt.tif, tt.session)
			if got != tt.want || !deadline.Equal(tt.deadline) {
				t.Fatalf("route/deadline = %s/%v, want %s/%v", got, deadline, tt.want, tt.deadline)
			}
		})
	}
}

func TestStopTriggerCrossing(t *testing.T) {
	for _, tt := range []struct {
		side        Side
		price, stop float64
		want        bool
	}{
		{SideBuy, 10, 10, true}, {SideBuy, 11, 10, true}, {SideBuy, 9, 10, false},
		{SideCover, 10, 10, true}, {SideSell, 10, 10, true}, {SideSell, 9, 10, true},
		{SideShort, 11, 10, false},
	} {
		if got := StopLimitTriggered(tt.side, tt.price, tt.stop); got != tt.want {
			t.Errorf("trigger(%s,%v,%v)=%v want %v", tt.side, tt.price, tt.stop, got, tt.want)
		}
	}
}
