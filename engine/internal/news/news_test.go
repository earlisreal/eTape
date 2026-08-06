package news

import (
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

func TestNormalizeMoomooSecurity(t *testing.T) {
	for _, tc := range []struct {
		raw, want string
		ok        bool
	}{{"US.LITE", "US.LITE", true}, {" lite.us ", "US.LITE", true}, {"HK.00700", "", false}, {"AAPL", "", false}, {"US.A A", "", false}} {
		got, ok := normalizeMoomooSecurity(tc.raw)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("%q = %q,%v", tc.raw, got, ok)
		}
	}
	got := normalizeRelatedSecurities([]string{"US.AAPL", "aapl.us", "HK.1"})
	if len(got) != 1 || got[0] != "US.AAPL" {
		t.Fatalf("normalized = %v", got)
	}
}

func TestAssociationIsConservative(t *testing.T) {
	plan := SymbolPlan{Active: []string{"US.AAPL", "US.NVDA", "US.ON", "US.AI"}}
	now := time.Date(2026, 7, 6, 14, 0, 0, 0, time.UTC)
	items := normalizeArticles([]searchNews{
		{Title: "Apple result", RelatedSecurities: []string{"NVDA.US", "US.AAPL"}},
		{Title: "Wrong result", RelatedSecurities: []string{"US.TSLA"}},
		{Title: "AAPL Reports Quarterly Results"},
		{Title: "Economy turns on a dime"},
		{Title: "AI reports results"},
	}, "US.AAPL", plan, now, 96*time.Hour)
	if len(items) != 2 || len(items[0].item.Symbols) != 2 || items[1].item.Symbols[0] != "US.AAPL" {
		t.Fatalf("associations = %+v", items)
	}
	if got := normalizeArticles([]searchNews{{Title: "Economy turns on a dime"}}, "US.ON", plan, now, 96*time.Hour); len(got) != 0 {
		t.Fatal("short ticker matched inside word")
	}
	if got := normalizeArticles([]searchNews{{Title: "AI reports results"}}, "US.AI", plan, now, 96*time.Hour); len(got) != 1 {
		t.Fatal("standalone short ticker did not match")
	}
}

func TestParsePublishTime(t *testing.T) {
	now := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct{ raw, at, precision string }{{"2026-07-06 09:31:00", "2026-07-06T13:31:00.000Z", "second"}, {"2026/01/06 09:31:00", "2026-01-06T14:31:00.000Z", "second"}, {"5/13", "2025-05-13T04:00:00.000Z", "date"}, {"12/31", "2025-12-31T05:00:00.000Z", "date"}} {
		got := parsePublishTime(tc.raw, now)
		if got.At != tc.at || got.Precision != tc.precision {
			t.Fatalf("%q = %+v", tc.raw, got)
		}
	}
	if got := parsePublishTime("bad", now); got.OK || got.Precision != "unknown" {
		t.Fatalf("bad=%+v", got)
	}
}

func TestArticleIDAndUpsert(t *testing.T) {
	a := wsmsg.NewsItem{Headline: "A", Source: "S", URL: " HTTPS://Example.com/x#one ", Type: "news"}
	b := a
	b.URL = "https://example.com/x#two"
	if articleID(a, "") != articleID(b, "") {
		t.Fatal("fragment changed ID")
	}
	p := &Poller{seen: map[string]seenArticle{}}
	now := time.Now()
	a.ID = articleID(a, "")
	a.Symbols = []string{"US.AAPL"}
	a.PublishedPrecision = "unknown"
	if got := p.upsert([]normalizedArticle{{item: a}}, now); len(got) != 1 {
		t.Fatal("new article not emitted")
	}
	b = a
	b.Symbols = []string{"US.AAPL", "US.NVDA"}
	b.ViewCount = 9
	if got := p.upsert([]normalizedArticle{{item: b}}, now); len(got) != 1 || len(got[0].Symbols) != 2 {
		t.Fatalf("symbol expansion=%+v", got)
	}
	if got := p.upsert([]normalizedArticle{{item: b}}, now); len(got) != 0 {
		t.Fatal("view-only change emitted")
	}
}

func TestRetentionAndLimit(t *testing.T) {
	now := time.Now()
	p := &Poller{seen: map[string]seenArticle{"old": {LastSeenAt: now.Add(-articleRetention - time.Second)}}}
	p.prune(now)
	if len(p.seen) != 0 {
		t.Fatal("old item retained")
	}
	for i := 0; i <= maxArticles; i++ {
		p.seen[string(rune(i))] = seenArticle{LastSeenAt: now.Add(time.Duration(i) * time.Second)}
	}
	p.prune(now)
	if len(p.seen) != maxArticles {
		t.Fatalf("len=%d", len(p.seen))
	}
}

func TestSchedulerAndLimiter(t *testing.T) {
	s := newScheduler()
	now := time.Now()
	plan := SymbolPlan{Active: []string{"US.A"}, Scanner: []string{"US.B"}}
	if got := s.next(plan, now, time.Minute, time.Hour); got != "US.A" {
		t.Fatalf("got %s", got)
	}
	s.record("US.A", now)
	if got := s.next(plan, now.Add(time.Second), time.Minute, time.Hour); got != "US.B" {
		t.Fatalf("got %s", got)
	}
	var l limiter
	for i := 0; i < 10; i++ {
		if !l.allow(now.Add(time.Duration(i) * 3 * time.Second)) {
			t.Fatal("premature quota block")
		}
	}
	if l.allow(now.Add(29 * time.Second)) {
		t.Fatal("quota exceeded")
	}
	if !l.allow(now.Add(31 * time.Second)) {
		t.Fatal("quota did not expire")
	}
}

func TestClassifyCatalyst(t *testing.T) {
	now := time.Now()
	got := classifyCatalyst(catalystInput{Headline: "Company announces public offering", Source: "Business Wire", PublishedAt: now.Add(-time.Hour), PublishedPrecision: "second", SeenAt: now, UsedRelatedSymbols: true})
	if got.Category != "offering" || got.Score != 100 {
		t.Fatalf("classifier=%+v", got)
	}
	if got := classifyCatalyst(catalystInput{Headline: "Top gainers: company announces earnings", SeenAt: now}); got.Category != "earnings" || got.Score != 40 {
		t.Fatalf("generic concrete=%+v", got)
	}
}
