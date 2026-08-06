package news

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

const articleRetention = 7 * 24 * time.Hour
const maxArticles = 5000

type normalizedArticle struct {
	item        wsmsg.NewsItem
	publishedAt time.Time
	usedRelated bool
}
type seenArticle struct {
	Item       wsmsg.NewsItem
	LastSeenAt time.Time
}

func normalizeArticles(raw []searchNews, queried string, plan SymbolPlan, now time.Time, maxAge time.Duration) []normalizedArticle {
	tracked := trackedSymbols(plan)
	out := make([]normalizedArticle, 0, len(raw))
	for _, n := range raw {
		symbols, usedRelated := relatedTracked(n.RelatedSecurities, tracked), len(n.RelatedSecurities) > 0
		if usedRelated && len(symbols) == 0 {
			continue
		}
		if !usedRelated {
			q, ok := normalizeMoomooSecurity(queried)
			if !ok || !headlineHasTicker(n.Title, q) {
				continue
			}
			if _, ok := tracked[q]; !ok {
				continue
			}
			symbols = []string{q}
		}
		pt := parsePublishTime(n.PublishTime, now)
		published, _ := parseISO(pt.At)
		if pt.OK && now.Sub(published) > maxAge {
			continue
		}
		item := wsmsg.NewsItem{Symbols: symbols, Headline: n.Title, Source: n.Source, URL: n.URL, SeenAt: iso(now), PublishedAt: pt.At, PublishedPrecision: pt.Precision, ViewCount: n.ViewCount, Type: mapNewsType(n.NewsSubType)}
		item.ID = articleID(item, n.PublishTime)
		c := classifyCatalyst(catalystInput{Headline: item.Headline, Source: item.Source, Type: item.Type, PublishedAt: published, PublishedPrecision: item.PublishedPrecision, SeenAt: now, UsedRelatedSymbols: usedRelated})
		item.CatalystCategory, item.CatalystScore, item.CatalystReasons = c.Category, c.Score, c.Reasons
		out = append(out, normalizedArticle{item: item, publishedAt: published, usedRelated: usedRelated})
	}
	return out
}

func articleID(item wsmsg.NewsItem, rawPublished string) string {
	identity := canonicalURL(item.URL)
	if identity == "" {
		identity = strings.ToLower(strings.TrimSpace(item.Headline)) + "|" + strings.ToLower(strings.TrimSpace(item.Source)) + "|" + strings.TrimSpace(rawPublished) + "|" + item.Type
	}
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:16])
}

func canonicalURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Fragment = ""
	return u.String()
}

func (p *Poller) upsert(items []normalizedArticle, now time.Time) []wsmsg.NewsItem {
	out := make([]wsmsg.NewsItem, 0, len(items))
	for _, a := range items {
		prior, exists := p.seen[a.item.ID]
		if !exists {
			p.seen[a.item.ID] = seenArticle{Item: a.item, LastSeenAt: now}
			out = append(out, a.item)
			continue
		}
		merged, changed := mergeArticle(prior.Item, a.item)
		prior.LastSeenAt = now
		prior.Item = merged
		p.seen[a.item.ID] = prior
		if changed {
			out = append(out, merged)
		}
	}
	return out
}

func mergeArticle(old, next wsmsg.NewsItem) (wsmsg.NewsItem, bool) {
	merged := old
	changed := false
	set := map[string]struct{}{}
	for _, s := range old.Symbols {
		set[s] = struct{}{}
	}
	for _, s := range next.Symbols {
		set[s] = struct{}{}
	}
	syms := make([]string, 0, len(set))
	for s := range set {
		syms = append(syms, s)
	}
	sort.Strings(syms)
	if strings.Join(syms, "|") != strings.Join(old.Symbols, "|") {
		merged.Symbols, changed = syms, true
	}
	if old.PublishedPrecision == "unknown" && next.PublishedPrecision != "unknown" || old.PublishedPrecision == "date" && next.PublishedPrecision == "second" {
		merged.PublishedAt, merged.PublishedPrecision, changed = next.PublishedAt, next.PublishedPrecision, true
	}
	if old.Source == "" && next.Source != "" {
		merged.Source, changed = next.Source, true
	}
	if old.URL == "" && next.URL != "" {
		merged.URL, changed = next.URL, true
	}
	if changed {
		merged.CatalystCategory, merged.CatalystScore, merged.CatalystReasons = next.CatalystCategory, next.CatalystScore, next.CatalystReasons
	}
	merged.ViewCount = next.ViewCount
	return merged, changed
}

func (p *Poller) prune(now time.Time) {
	for id, a := range p.seen {
		if now.Sub(a.LastSeenAt) > articleRetention {
			delete(p.seen, id)
		}
	}
	if len(p.seen) <= maxArticles {
		return
	}
	items := make([]struct {
		id string
		at time.Time
	}, 0, len(p.seen))
	for id, a := range p.seen {
		items = append(items, struct {
			id string
			at time.Time
		}{id, a.LastSeenAt})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].at.Before(items[j].at) })
	for _, x := range items[:len(items)-maxArticles] {
		delete(p.seen, x.id)
	}
}
