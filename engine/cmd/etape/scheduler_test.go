package main

import (
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/session"
)

func TestNextSealFire(t *testing.T) {
	loc := session.Loc()
	cases := []struct {
		name         string
		now          time.Time
		wantY, wantD int
		wantM        time.Month
	}{
		{"late evening rolls to next 00:30", time.Date(2026, 7, 6, 23, 0, 0, 0, loc), 2026, 7, time.July},
		{"just before fires same day", time.Date(2026, 7, 6, 0, 15, 0, 0, loc), 2026, 6, time.July},
		{"exactly at fire rolls forward", time.Date(2026, 7, 6, 0, 30, 0, 0, loc), 2026, 7, time.July},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := nextSealFire(c.now)
			if got.Year() != c.wantY || got.Month() != c.wantM || got.Day() != c.wantD ||
				got.Hour() != sealHourET || got.Minute() != sealMinET {
				t.Fatalf("nextSealFire(%v) = %v, want %04d-%02d-%02d %02d:%02d ET",
					c.now, got, c.wantY, c.wantM, c.wantD, sealHourET, sealMinET)
			}
			if !got.After(c.now) {
				t.Fatalf("nextSealFire(%v) = %v is not strictly after now", c.now, got)
			}
		})
	}
}
