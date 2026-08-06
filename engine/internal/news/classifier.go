package news

import (
	"sort"
	"strings"
	"time"

	"github.com/earlisreal/eTape/engine/internal/session"
)

type catalystInput struct {
	Headline, Source, Type string
	PublishedAt            time.Time
	PublishedPrecision     string
	SeenAt                 time.Time
	UsedRelatedSymbols     bool
}
type catalystResult struct {
	Category string
	Score    int
	Reasons  []string
}

var categoryPatterns = []struct {
	category string
	base     int
	patterns []string
}{
	{"halt", 90, []string{"trading halt", "halted", "resumes trading"}},
	{"offering", 80, []string{"public offering", "registered direct", "at-the-market", "atm offering", "private placement", "shelf registration", "stock offering", "share offering", "warrants", "dilution", "prospectus supplement"}},
	{"fda_clinical", 75, []string{"fda", "clinical trial", "phase 1", "phase 2", "phase 3", "topline data", "primary endpoint", "regulatory approval", "complete response letter", " crl", " nda", " bla"}},
	{"merger_acquisition", 75, []string{"merger", "acquisition", "acquired by", "takeover", "buyout", "tender offer", "definitive agreement", "strategic alternatives"}},
	{"bankruptcy", 75, []string{"bankruptcy", "chapter 11", "restructuring", "going concern", "liquidation", "insolvency", "default notice"}},
	{"guidance", 70, []string{"guidance", "outlook", "forecast", "raises full-year", "lowers full-year", "withdraws guidance"}},
	{"earnings", 65, []string{"earnings", "quarterly results", "financial results", "revenue", " eps", "profit", "loss widens", "beats estimates", "misses estimates"}},
	{"contract", 55, []string{"awarded contract", "contract award", "purchase order", "strategic agreement", "supply agreement", "partnership", "collaboration", "selected by", "government contract"}},
	{"regulatory", 55, []string{"sec investigation", "subpoena", "doj", "ftc", "lawsuit", "class action", "compliance notice", "delisting notice", "nasdaq deficiency", "exchange deficiency"}},
	{"financing", 50, []string{"debt financing", "credit facility", "convertible notes", "loan agreement", "securities purchase agreement"}},
	{"analyst", 40, []string{"upgrade", "downgrade", "initiates coverage", "price target", "reiterates", "maintains rating"}},
	{"corporate_action", 35, []string{"reverse split", "stock split", "dividend", "special dividend", "buyback", "repurchase authorization", "name change", "ticker change"}},
}
var genericPatterns = []string{"stocks to watch", "top gainers", "top losers", "market recap", "morning update", "midday update", "closing update", "trending stocks", "most active stocks", "why shares are moving"}

func classifyCatalyst(in catalystInput) catalystResult {
	h := strings.ToLower(in.Headline)
	r := catalystResult{Category: "other"}
	for _, c := range categoryPatterns {
		for _, p := range c.patterns {
			if strings.Contains(h, p) {
				r.Category, r.Score = c.category, c.base
				r.Reasons = append(r.Reasons, "category:"+c.category)
				goto matched
			}
		}
	}
matched:
	if r.Category == "other" {
		return r
	}
	source := strings.ToLower(in.Source)
	matchedSource := false
	for _, s := range []struct {
		match, reason string
		bonus         int
	}{{"sec", "filing", 15}, {"exchange", "filing", 15}, {"business wire", "press-wire", 12}, {"globenewswire", "press-wire", 12}, {"pr newswire", "press-wire", 12}, {"benzinga", "newswire", 8}, {"mt newswires", "newswire", 8}} {
		if strings.Contains(source, s.match) {
			r.Score += s.bonus
			r.Reasons = append(r.Reasons, "source:"+s.reason)
			matchedSource = true
			break
		}
	}
	if source != "" && !matchedSource {
		r.Score += 3
		r.Reasons = append(r.Reasons, "source:known")
	}
	if in.UsedRelatedSymbols {
		r.Score += 10
		r.Reasons = append(r.Reasons, "symbol:related-security")
	}
	if in.PublishedPrecision != "unknown" && !in.PublishedAt.IsZero() {
		age := in.SeenAt.Sub(in.PublishedAt)
		bonus, label := 0, ""
		switch {
		case age <= 15*time.Minute:
			bonus, label = 20, "under-15m"
		case age <= time.Hour:
			bonus, label = 15, "under-60m"
		case age <= 4*time.Hour:
			bonus, label = 10, "under-4h"
		case in.PublishedAt.In(session.Loc()).YearDay() == in.SeenAt.In(session.Loc()).YearDay():
			bonus, label = 5, "same-day"
		}
		if bonus > 0 {
			r.Score += bonus
			r.Reasons = append(r.Reasons, "recency:"+label)
		}
	}
	for _, p := range genericPatterns {
		if strings.Contains(h, p) {
			r.Score -= 25
			r.Reasons = append(r.Reasons, "penalty:generic-roundup")
			break
		}
	}
	if r.Score < 0 {
		r.Score = 0
	}
	if r.Score > 100 {
		r.Score = 100
	}
	sort.Strings(r.Reasons)
	return r
}
