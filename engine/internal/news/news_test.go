package news

import (
	"testing"

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

func TestNormalizeStampsSeenAt(t *testing.T) {
	items := normalize([]searchNews{{Title: "PR drops", Source: "Newswire", URL: "http://x/9"}}, "US.AAPL", "2026-07-06T13:31:00.000Z")
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	it := items[0]
	if it.Symbol != "US.AAPL" || it.Headline != "PR drops" || it.Source != "Newswire" || it.URL != "http://x/9" || it.SeenAt != "2026-07-06T13:31:00.000Z" {
		t.Fatalf("normalize wrong: %+v", it)
	}
}
