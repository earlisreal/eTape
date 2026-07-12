package news

import (
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

func TestDedupByURL(t *testing.T) {
	p := &Poller{seen: map[string]bool{}}
	in := []wsmsg.NewsItem{
		{Symbol: "US.AAPL", Headline: "A", URL: "http://x/1", SeenAt: "t1"},
		{Symbol: "US.AAPL", Headline: "A", URL: "http://x/1", SeenAt: "t2"}, // dup url
		{Symbol: "US.AAPL", Headline: "B", URL: "", SeenAt: "t3"},           // no url -> symbol|headline key
		{Symbol: "US.AAPL", Headline: "B", URL: "", SeenAt: "t4"},           // dup by fallback key
	}
	out := p.dedup(in)
	if len(out) != 2 {
		t.Fatalf("expected 2 unique items, got %d: %+v", len(out), out)
	}
	// second call: all already seen
	if again := p.dedup(in); len(again) != 0 {
		t.Fatalf("all should be seen on second pass, got %d", len(again))
	}
}

func TestSeenResetsAtDayBoundary(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC))
	p := &Poller{seen: map[string]bool{}, seenDay: session.DayMs(clk.Now().UnixMilli())}
	in := []wsmsg.NewsItem{
		{Symbol: "US.AAPL", Headline: "A", URL: "http://x/1", SeenAt: "t1"},
	}
	out := p.dedup(in)
	if len(out) != 1 {
		t.Fatalf("expected 1 unique item, got %d: %+v", len(out), out)
	}
	// same day, repeat: suppressed
	if again := p.dedup(in); len(again) != 0 {
		t.Fatalf("expected dedup suppression within the same ET day, got %d: %+v", len(again), again)
	}
	// advance past ET midnight into the next ET day
	clk.Advance(24 * time.Hour)
	p.resetIfNewDay(clk.Now())
	// seen-set was cleared: the same URL is emitted again
	if out := p.dedup(in); len(out) != 1 {
		t.Fatalf("expected seen-set cleared after ET-day rollover, got %d: %+v", len(out), out)
	}
}

func TestNormalizeStampsSeenAt(t *testing.T) {
	items := normalize([]searchNews{{
		Title: "PR drops", Source: "Newswire", URL: "http://x/9",
		NewsSubType: 2, PublishTime: "2026-07-06 09:31:00", ViewCount: 42,
	}}, "US.AAPL", "2026-07-06T13:31:00.000Z")
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	it := items[0]
	if it.Symbol != "US.AAPL" || it.Headline != "PR drops" || it.Source != "Newswire" || it.URL != "http://x/9" || it.SeenAt != "2026-07-06T13:31:00.000Z" {
		t.Fatalf("normalize wrong: %+v", it)
	}
	if wantPublished := parsePublishTime("2026-07-06 09:31:00"); it.PublishedAt != wantPublished {
		t.Fatalf("PublishedAt = %q, want %q", it.PublishedAt, wantPublished)
	}
	if it.ViewCount != 42 {
		t.Fatalf("ViewCount = %d, want 42", it.ViewCount)
	}
	if wantType := mapNewsType(2); it.Type != wantType {
		t.Fatalf("Type = %q, want %q", it.Type, wantType)
	}
}

func TestMapNewsType(t *testing.T) {
	cases := []struct {
		subType int32
		want    string
	}{
		{0, "news"},   // ALL -> falls back to the most common category
		{1, "news"},   // NEWS
		{2, "notice"}, // NOTICE
		{3, "rating"}, // RATING
		{99, "news"},  // unknown -> falls back to the most common category
	}
	for _, c := range cases {
		if got := mapNewsType(c.subType); got != c.want {
			t.Fatalf("mapNewsType(%d) = %q, want %q", c.subType, got, c.want)
		}
	}
}

func TestParsePublishTime(t *testing.T) {
	// 2026-07-06 is EDT (UTC-4) in America/New_York.
	if got, want := parsePublishTime("2026-07-06 09:31:00"), "2026-07-06T13:31:00.000Z"; got != want {
		t.Fatalf("parsePublishTime(valid) = %q, want %q", got, want)
	}
	if got := parsePublishTime(""); got != "" {
		t.Fatalf("parsePublishTime(empty) = %q, want \"\"", got)
	}
	if got := parsePublishTime("not-a-time"); got != "" {
		t.Fatalf("parsePublishTime(malformed) = %q, want \"\"", got)
	}
}
