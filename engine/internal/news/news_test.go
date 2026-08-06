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
	for _, tc := range []struct{ symbol, headline string }{{"US.A", "Apple reports earnings"}, {"US.ON", "ONcology trial results"}, {"US.AI", "AIming for growth"}} {
		if got := normalizeArticles([]searchNews{{Title: tc.headline}}, tc.symbol, plan, now, 96*time.Hour); len(got) != 0 {
			t.Fatalf("%s matched %q", tc.symbol, tc.headline)
		}
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
	s.record(plan, "US.A", now)
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

func TestSchedulerReservesScannerSlot(t *testing.T) {
	s := newScheduler()
	plan := SymbolPlan{Active: []string{"US.A", "US.B", "US.C", "US.D"}, Scanner: []string{"US.S"}}
	now := time.Now()
	scannerRuns := 0
	for i := 0; i < 40; i++ {
		symbol := s.next(plan, now, 10*time.Second, time.Minute)
		if symbol == "" {
			t.Fatal("no symbol due")
		}
		s.record(plan, symbol, now)
		if i == 3 && symbol != "US.S" {
			t.Fatalf("scanner starved after active burst: %s", symbol)
		}
		if symbol == "US.S" {
			scannerRuns++
		}
		now = now.Add(3100 * time.Millisecond)
	}
	if scannerRuns < 2 {
		t.Fatalf("scanner polled %d times over two minutes", scannerRuns)
	}
}

func TestURLLessArticleIDAllowsTimeUpgrade(t *testing.T) {
	p := &Poller{seen: map[string]seenArticle{}}
	now := time.Now()
	base := wsmsg.NewsItem{Headline: "Result", Source: "Wire", Type: "news", Symbols: []string{"US.AAPL"}, PublishedPrecision: "unknown"}
	base.ID = articleID(base, "")
	if got := p.upsert([]normalizedArticle{{item: base}}, now); len(got) != 1 {
		t.Fatal("initial item not emitted")
	}
	updated := base
	updated.PublishedAt, updated.PublishedPrecision = "2026-08-06T13:31:00.000Z", "second"
	if got := p.upsert([]normalizedArticle{{item: updated}}, now); len(got) != 1 || got[0].ID != base.ID || got[0].PublishedPrecision != "second" {
		t.Fatalf("time upgrade = %+v", got)
	}
}

func TestArticleReconciliationUpgradesOptionalMetadata(t *testing.T) {
	p := &Poller{seen: map[string]seenArticle{}}
	now := time.Now()
	base := wsmsg.NewsItem{Headline: "Company Announces Public Offering", Type: "news", Symbols: []string{"US.AAPL"}, PublishedPrecision: "unknown"}
	base.ID = articleID(base, "")
	p.upsert([]normalizedArticle{{item: base}}, now)
	updated := base
	updated.Source, updated.URL = "GlobeNewswire", "https://example.com/aapl"
	updated.ID = articleID(updated, "")
	got := p.upsert([]normalizedArticle{{item: updated}}, now)
	if len(got) != 1 || len(p.seen) != 1 || got[0].ID != base.ID || got[0].URL == "" || got[0].Source == "" {
		t.Fatalf("optional metadata created duplicate: %+v", got)
	}
}

func TestURLLessStoriesDoNotMergeAcrossSymbols(t *testing.T) {
	p := &Poller{seen: map[string]seenArticle{}}
	now := time.Now()
	first := wsmsg.NewsItem{Headline: "Company Announces Public Offering", Source: "GlobeNewswire", Type: "news", Symbols: []string{"US.AAAA"}}
	second := first
	second.Symbols = []string{"US.BBBB"}
	first.ID, second.ID = articleID(first, ""), articleID(second, "")
	got := p.upsert([]normalizedArticle{{item: first}, {item: second}}, now)
	if len(got) != 2 || len(p.seen) != 2 || got[0].ID == got[1].ID {
		t.Fatalf("distinct URL-less stories merged: %+v", got)
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
