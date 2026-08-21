package md

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"time"

	"github.com/earlisreal/eTape/engine/internal/session"
)

// The registry is a reviewed, checked-in snapshot. Updating it is a manual
// source-and-review operation: capture the dated constituent/ETP workbooks,
// record their URLs and review date in luld_registry.json, then run the
// registry tests. There is intentionally no runtime fetch or classifier.
//
//go:embed luld_registry.json
var embeddedLULDRegistry []byte

type luldTier string

const (
	luldTier1 luldTier = "T1"
	luldTier2 luldTier = "T2"
)

type luldRegistryEntry struct {
	Tier       luldTier `json:"tier"`
	Multiplier float64  `json:"multiplier,omitempty"`
	Provenance string   `json:"provenance"`
}

type luldRegistry struct {
	asOf         time.Time
	validThrough time.Time
	sources      []string
	symbols      map[string]luldRegistryEntry
}

type luldRegistryDocument struct {
	AsOf         string                       `json:"as_of"`
	ValidThrough string                       `json:"valid_through"`
	Sources      []string                     `json:"sources"`
	Symbols      map[string]luldRegistryEntry `json:"symbols"`
}

var registrySymbolPattern = regexp.MustCompile(`^US\.[A-Z][A-Z0-9.-]*$`)

func loadLULDRegistry(data []byte) (luldRegistry, error) {
	var doc luldRegistryDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return luldRegistry{}, fmt.Errorf("luld registry JSON: %w", err)
	}
	const dateLayout = "2006-01-02"
	asOf, err := time.Parse(dateLayout, doc.AsOf)
	if err != nil {
		return luldRegistry{}, fmt.Errorf("luld registry as_of: %w", err)
	}
	validThrough, err := time.Parse(dateLayout, doc.ValidThrough)
	if err != nil {
		return luldRegistry{}, fmt.Errorf("luld registry valid_through: %w", err)
	}
	if asOf.After(validThrough) {
		return luldRegistry{}, fmt.Errorf("luld registry as_of is after valid_through")
	}
	if len(doc.Sources) == 0 {
		return luldRegistry{}, fmt.Errorf("luld registry has no sources")
	}
	for i, source := range doc.Sources {
		if source == "" {
			return luldRegistry{}, fmt.Errorf("luld registry source %d is empty", i)
		}
	}
	if len(doc.Symbols) == 0 {
		return luldRegistry{}, fmt.Errorf("luld registry has no symbols")
	}
	for symbol, entry := range doc.Symbols {
		if !registrySymbolPattern.MatchString(symbol) {
			return luldRegistry{}, fmt.Errorf("luld registry invalid symbol %q", symbol)
		}
		if entry.Tier != luldTier1 && entry.Tier != luldTier2 {
			return luldRegistry{}, fmt.Errorf("luld registry %s invalid tier %q", symbol, entry.Tier)
		}
		if entry.Provenance == "" {
			return luldRegistry{}, fmt.Errorf("luld registry %s missing provenance", symbol)
		}
		if entry.Multiplier == 0 {
			entry.Multiplier = 1
		}
		if !math.IsNaN(entry.Multiplier) && (!math.IsInf(entry.Multiplier, 0)) && entry.Multiplier >= 1 {
			// Valid explicit multiplier.
		} else {
			return luldRegistry{}, fmt.Errorf("luld registry %s invalid multiplier %v", symbol, entry.Multiplier)
		}
		doc.Symbols[symbol] = entry
	}
	return luldRegistry{
		asOf: asOf, validThrough: validThrough,
		sources: append([]string(nil), doc.Sources...),
		symbols: doc.Symbols,
	}, nil
}

// lookup is an allowlist lookup. The valid-through date is inclusive; records
// disappear on the following calendar date instead of becoming stale.
func (r luldRegistry) lookup(symbol string, now time.Time) (luldRegistryEntry, bool) {
	y, m, d := now.In(session.Loc()).Date()
	vy, vm, vd := r.validThrough.Date()
	if y > vy || (y == vy && (m > vm || (m == vm && d > vd))) {
		return luldRegistryEntry{}, false
	}
	e, ok := r.symbols[symbol]
	return e, ok
}

var defaultLULDRegistry = func() luldRegistry {
	r, err := loadLULDRegistry(embeddedLULDRegistry)
	if err != nil {
		panic(err)
	}
	return r
}()
