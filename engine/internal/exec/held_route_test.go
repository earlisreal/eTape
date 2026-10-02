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

func TestResolveDeferredStopSellRoute(t *testing.T) {
	pre := time.Date(2026, 9, 30, 8, 0, 0, 0, session.Loc())
	rth := time.Date(2026, 9, 30, 10, 0, 0, 0, session.Loc())
	post := time.Date(2026, 9, 30, 17, 0, 0, 0, session.Loc())
	closed := time.Date(2026, 9, 30, 22, 0, 0, 0, session.Loc())
	tests := []struct {
		name      string
		now       time.Time
		tif       TIF
		session   OrderSession
		want      HeldRoute
		effective OrderSession
		deadline  time.Time
		blocked   string
	}{
		{"premarket AUTO", pre, TIFDay, SessionAuto, RouteEngineHeld, SessionExtended, session.Schedule(pre).Open, ""},
		{"RTH AUTO", rth, TIFDay, SessionAuto, RouteEngineHeld, SessionRTH, session.Schedule(rth).Close, ""},
		{"RTH EXTENDED", rth, TIFDay, SessionExtended, RouteEngineHeld, SessionExtended, session.Schedule(rth).Close, ""},
		{"postmarket EXTENDED", post, TIFDay, SessionExtended, RouteEngineHeld, SessionExtended, session.Schedule(post).DataClose, ""},
		{"pre-market explicit RTH", pre, TIFDay, SessionRTH, RouteUnsupported, SessionRTH, time.Time{}, "deferred RTH orders can only be placed during RTH"},
		{"postmarket explicit RTH", post, TIFDay, SessionRTH, RouteUnsupported, SessionRTH, time.Time{}, "deferred RTH orders can only be placed during RTH"},
		{"GTC", rth, TIFGTC, SessionRTH, RouteUnsupported, SessionRTH, time.Time{}, "deferred position sizing requires a DAY order"},
		{"closed", closed, TIFDay, SessionAuto, RouteUnsupported, SessionAuto, time.Time{}, "deferred stop-sell is unavailable outside market sessions"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, effective, deadline, blocked := ResolveDeferredStopSellRoute(tt.now, tt.tif, tt.session)
			if got != tt.want || effective != tt.effective || !deadline.Equal(tt.deadline) || blocked != tt.blocked {
				t.Fatalf("route = %s/%s/%v/%q, want %s/%s/%v/%q", got, effective, deadline, blocked, tt.want, tt.effective, tt.deadline, tt.blocked)
			}
		})
	}
}

func TestResolveLimitIfTouchedRoute(t *testing.T) {
	pre := time.Date(2026, 9, 30, 8, 0, 0, 0, session.Loc())
	rth := time.Date(2026, 9, 30, 10, 0, 0, 0, session.Loc())
	post := time.Date(2026, 9, 30, 17, 0, 0, 0, session.Loc())
	closed := time.Date(2026, 9, 30, 22, 0, 0, 0, session.Loc())
	earlyCloseRTH := time.Date(2026, 11, 27, 10, 0, 0, 0, session.Loc())
	holiday := time.Date(2026, 11, 26, 10, 0, 0, 0, session.Loc())
	tests := []struct {
		name      string
		now       time.Time
		tif       TIF
		session   OrderSession
		want      HeldRoute
		effective OrderSession
		deadline  time.Time
		blocked   string
	}{
		{"premarket AUTO", pre, TIFDay, SessionAuto, RouteEngineHeld, SessionExtended, session.Schedule(pre).Open, ""},
		{"premarket EXTENDED", pre, TIFDay, SessionExtended, RouteEngineHeld, SessionExtended, session.Schedule(pre).Open, ""},
		{"RTH AUTO", rth, TIFDay, SessionAuto, RouteEngineHeld, SessionRTH, session.Schedule(rth).Close, ""},
		{"RTH only during RTH", rth, TIFDay, SessionRTH, RouteEngineHeld, SessionRTH, session.Schedule(rth).Close, ""},
		{"RTH EXTENDED", rth, TIFDay, SessionExtended, RouteEngineHeld, SessionExtended, session.Schedule(rth).Close, ""},
		{"postmarket EXTENDED", post, TIFDay, SessionExtended, RouteEngineHeld, SessionExtended, session.Schedule(post).DataClose, ""},
		{"postmarket AUTO", post, TIFDay, SessionAuto, RouteEngineHeld, SessionExtended, session.Schedule(post).DataClose, ""},
		{"reject PRE RTH", pre, TIFDay, SessionRTH, RouteUnsupported, SessionRTH, time.Time{}, "limit-if-touched order RTH orders can only be placed during RTH"},
		{"reject POST RTH", post, TIFDay, SessionRTH, RouteUnsupported, SessionRTH, time.Time{}, "limit-if-touched order RTH orders can only be placed during RTH"},
		{"reject GTC", rth, TIFGTC, SessionAuto, RouteUnsupported, SessionAuto, time.Time{}, "limit-if-touched order requires a DAY order"},
		{"reject IOC", rth, TIFIOC, SessionAuto, RouteUnsupported, SessionAuto, time.Time{}, "limit-if-touched order requires a DAY order"},
		{"reject FOK", rth, TIFFOK, SessionAuto, RouteUnsupported, SessionAuto, time.Time{}, "limit-if-touched order requires a DAY order"},
		{"reject overnight session", rth, TIFDay, SessionOvernight, RouteUnsupported, SessionOvernight, time.Time{}, "limit-if-touched order is unavailable outside market sessions"},
		{"reject closed", closed, TIFDay, SessionAuto, RouteUnsupported, SessionAuto, time.Time{}, "limit-if-touched order is unavailable outside market sessions"},
		{"reject holiday", holiday, TIFDay, SessionAuto, RouteUnsupported, SessionAuto, time.Time{}, "limit-if-touched order is unavailable outside market sessions"},
		{"early close RTH deadline", earlyCloseRTH, TIFDay, SessionAuto, RouteEngineHeld, SessionRTH, session.Schedule(earlyCloseRTH).Close, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, effective, deadline, blocked := ResolveLimitIfTouchedRoute(tt.now, tt.tif, tt.session)
			if got != tt.want || effective != tt.effective || !deadline.Equal(tt.deadline) || blocked != tt.blocked {
				t.Fatalf("route = %s/%s/%v/%q, want %s/%s/%v/%q", got, effective, deadline, blocked, tt.want, tt.effective, tt.deadline, tt.blocked)
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

func TestHeldOrderTriggerDirection(t *testing.T) {
	tests := []struct {
		typ         OrderType
		side        Side
		price, stop float64
		want        bool
	}{
		{TypeLimitIfTouched, SideBuy, 9, 10, true},
		{TypeLimitIfTouched, SideBuy, 10, 10, true},
		{TypeLimitIfTouched, SideBuy, 11, 10, false},
		{TypeLimitIfTouched, SideCover, 9, 10, true},
		{TypeLimitIfTouched, SideCover, 10, 10, true},
		{TypeLimitIfTouched, SideCover, 11, 10, false},
		{TypeLimitIfTouched, SideSell, 9, 10, false},
		{TypeLimitIfTouched, SideSell, 10, 10, true},
		{TypeLimitIfTouched, SideSell, 11, 10, true},
		{TypeLimitIfTouched, SideShort, 9, 10, false},
		{TypeLimitIfTouched, SideShort, 10, 10, true},
		{TypeLimitIfTouched, SideShort, 11, 10, true},
		{TypeStopLimit, SideBuy, 11, 10, true},
		{TypeStopLimit, SideSell, 9, 10, true},
	}
	for _, tt := range tests {
		if got := HeldOrderTriggered(tt.typ, tt.side, tt.price, tt.stop); got != tt.want {
			t.Errorf("trigger(%s,%s,%v,%v)=%v want %v", tt.typ, tt.side, tt.price, tt.stop, got, tt.want)
		}
	}
}
