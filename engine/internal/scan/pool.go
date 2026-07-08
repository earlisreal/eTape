package scan

import (
	"sort"
	"time"

	"github.com/earlisreal/eTape/engine/internal/session"
)

const (
	poolTrackN = 10 // top-N filtered rank symbols tracked live each poll
	poolCap    = 30 // max sticky members per pool day (cap > N: always an evictable off-board member)
)

// Delta is the subscription change one Update produces. Admitted symbols get a
// watch-tier Ensure; Backfill (a subset of Admitted, first admission this pool
// day) additionally get a deep-history seed; Evicted symbols get a Release.
type Delta struct {
	Admitted []string // newly pooled this poll, rank order
	Backfill []string // first admission this pool day (subset of Admitted)
	Evicted  []string // removed this poll, sorted
}

// Pool is the sticky top-N scanner subscription pool: pure logic, no I/O. The
// poller feeds it the filtered rank symbols each poll and executes the returned
// delta against the feed. See docs/superpowers/specs/2026-07-08-scanner-driven-
// subscription-pool-design.md.
type Pool struct {
	members    map[string]int64 // symbol -> last-seen-in-top-N poll time (UnixMilli)
	backfilled map[string]bool  // symbols already backfilled this pool day
	day        int64            // current pool-day key (0 = uninitialized)
}

func NewPool() *Pool {
	return &Pool{members: map[string]int64{}, backfilled: map[string]bool{}}
}

// Update feeds the current filtered rank symbols (rank order) and the poll time,
// and returns the demand delta. Symbols beyond the top-N are ignored for
// admission but existing members stay pooled (sticky) until cap eviction or the
// 20:00-ET pool-day reset.
func (p *Pool) Update(ranked []string, now time.Time) Delta {
	var d Delta

	// Pool-day reset (20:00 ET): release everything and start fresh.
	if day := session.PoolDay(now); day != p.day {
		for s := range p.members {
			d.Evicted = append(d.Evicted, s)
		}
		sort.Strings(d.Evicted)
		p.members = map[string]int64{}
		p.backfilled = map[string]bool{}
		p.day = day
	}

	n := poolTrackN
	if len(ranked) < n {
		n = len(ranked)
	}
	topN := ranked[:n]
	inTop := make(map[string]bool, n)
	for _, s := range topN {
		inTop[s] = true
	}

	ts := now.UnixMilli()
	for _, s := range topN {
		if _, ok := p.members[s]; ok {
			p.members[s] = ts // refresh last-seen-in-top-N
			continue
		}
		// New admission: enforce cap by evicting the longest-off-board member.
		if len(p.members) >= poolCap {
			if victim := p.oldestOffBoard(inTop); victim != "" {
				delete(p.members, victim)
				d.Evicted = append(d.Evicted, victim)
			}
		}
		p.members[s] = ts
		d.Admitted = append(d.Admitted, s)
		if !p.backfilled[s] {
			p.backfilled[s] = true
			d.Backfill = append(d.Backfill, s)
		}
	}
	return d
}

// oldestOffBoard returns the pooled member NOT currently in the top-N with the
// oldest last-seen-in-top-N timestamp (ties broken by symbol for determinism).
// cap (30) > N (10) guarantees an off-board member exists whenever the pool is
// full, so a current top-N member is never chosen.
func (p *Pool) oldestOffBoard(inTop map[string]bool) string {
	cands := make([]string, 0, len(p.members))
	for s := range p.members {
		if !inTop[s] {
			cands = append(cands, s)
		}
	}
	sort.Strings(cands) // deterministic tiebreak
	var victim string
	var oldest int64
	for _, s := range cands {
		if ls := p.members[s]; victim == "" || ls < oldest {
			victim, oldest = s, ls
		}
	}
	return victim
}

// Symbols returns the current pool members, sorted. Used to compose the news
// rotation set.
func (p *Pool) Symbols() []string {
	out := make([]string, 0, len(p.members))
	for s := range p.members {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
