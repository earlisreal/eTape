# Settings Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Redesign the unified settings modal so every order-template parameter is editable (including the price offset and sizing amount that today have no input), add a Venues & credentials editor that rewrites `config.toml`/`credentials.json` from the UI, and give the Sounds section discoverable per-event enable toggles.

**Architecture:** UI changes extend the existing order-template model (`priceOffsetUnit`, position-percent sizing) behind one `normalizeOrderConfig` migration point, then rebuild the Orders grid, add a Venues section, and add sound toggles — all inside the existing `SettingsModal` shell. Engine changes add four validated WS commands (`GetVenueSetup`, `SetVenueSetup`, `PutCredential`, `DeleteCredential`) behind a new narrow `venueAdmin` seam that reads/writes the two config files atomically; new wire DTOs are tygo-generated into `ui/src/gen/wsmsg.ts`.

**Tech Stack:** Go 1.26 (`BurntSushi/toml`, stdlib), TypeScript + React (vitest, Playwright), tygo for Go→TS wire types.

## Global Constraints

- **No AI attribution in commits.** Commit message body only — never add `Co-Authored-By`, "Generated with", or any Claude/AI trailer (user's global rule).
- **Wire-type drift gate.** Any change to `engine/internal/uihub/wsmsg/*.go` requires `cd engine && make gen-ts` and committing the regenerated `ui/src/gen/wsmsg.ts` in the same commit; `make gen-ts-check` must pass (it is the authoritative drift gate — there is no CI).
- **Secrets never surface.** `keyId`/`secretKey` are never logged, never returned in any ack/result, and never sent from engine to UI. UI credential inputs are write-only and cleared after a successful save.
- **No new runtime order authority.** Nothing in settings arms a venue, places, or cancels. Venue/gate/credential edits are file-only and take effect on the next engine restart. The running gate and arm state are untouched by every new command.
- **`live ⇒ auto_arm=false` is enforced in engine validation**, not only in UI state.
- **Hotkey-capture inertness is preserved verbatim.** The capture field's `readOnly`, `normalizeCombo`, and the load-bearing `e.stopPropagation()` (plus its comment) move unchanged — the settings screen has zero order-safety authority.
- **Palette discipline.** All colors come from the existing `Palette` (`ui/src/render/palette.ts`) via `useTheme()`; both LIGHT and DARK must render correctly. No hardcoded hex.
- **Preserve these `data-testid`s** (tests depend on them): `tmpl-label-*`, `tmpl-hotkey-*`, `add-template`, `save`, `sound-enabled`, `sound-fill`, `sound-reject`, `sound-scanner`, `sound-place`, `sound-volume`, `sound-preview-fill`, `sound-preview-reject`, `sound-preview-scanner`, `open-settings`.
- **Test commands.** Engine: `cd engine && go test -race ./internal/<pkg>/...` (all: `make test`); `go vet ./...`; `golangci-lint run`. UI: `cd ui && npx vitest run <path>` (all: `npm test`); `npm run typecheck`; `npm run lint`. E2E: `cd ui && npm run e2e`.
- **Test matchers.** `@testing-library/jest-dom` is NOT installed (no `setupFiles` extends `expect`). Use plain vitest matchers + DOM properties — e.g. `expect((el as HTMLButtonElement).disabled).toBe(true)`, never `.toBeDisabled()`. Follow the convention in `AccountPanel.test.tsx` / `OrderTicketPanel.test.tsx`.
- **Spec:** `docs/superpowers/specs/2026-07-09-settings-redesign-design.md` is the source of truth for behavior; this plan is the source of truth for code.

---

## File Structure

**Engine — new files**
- `engine/internal/atomicfile/atomicfile.go` — `Write(path, data, perm)` temp-file + rename helper (+ test). Shared by config and creds writers; no such helper exists today.
- `engine/internal/venueadmin/venueadmin.go` — `Admin` implementing the `venueAdmin` seam over `config` + `creds` (+ test).

**Engine — modified files**
- `engine/internal/config/config.go` — add `VenueConfig` type, `ReadVenueConfig`, `ValidateVenueConfig`, `WriteVenueConfig` (+ new `config_venue_test.go`).
- `engine/internal/creds/creds.go` — add `Put`, `Delete`, `Keys` (+ new `creds_write_test.go`).
- `engine/internal/uihub/wsmsg/payloads.go` — add venue/credential DTOs.
- `engine/internal/uihub/commands.go` — add the `venueAdmin` seam interface, four command cases, and config↔wire mappers.
- `engine/internal/uihub/api.go` — thread `venueAdmin` through `New`.
- `engine/cmd/etape/main.go` — build `venueadmin.Admin` (capturing the booted venue config) and pass it to `uihub.New`.
- `ui/src/gen/wsmsg.ts` — regenerated (do not hand-edit).

**UI — new files**
- `ui/src/chrome/exec/Keycap.tsx` — keycap-chip visual primitive (+ test).
- `ui/src/chrome/exec/VenuesSection.tsx` — Venues & credentials editor (+ `VenuesSection.test.tsx`).

**UI — modified/renamed files**
- `ui/src/chrome/exec/priceSource.ts` — `PriceOffsetUnit`, offset unit in `resolvePrice`.
- `ui/src/chrome/exec/sizing.ts` — `PositionFraction` reads `pct`.
- `ui/src/chrome/exec/actionTemplate.ts` — `priceOffsetUnit`, updated defaults, `normalizeOrderConfig`.
- `ui/src/chrome/exec/resolveTemplate.ts` — pass offset unit through.
- `ui/src/chrome/exec/useOrderConfig.tsx` — normalize on load + seed.
- `ui/src/chrome/panels/OrderTicketPanel.tsx` — `Pos` mode reads the amount input.
- `ui/src/chrome/panels/AccountPanel.tsx` — flatten uses `pct: 100`.
- `ui/src/chrome/exec/OrderSettingsModal.tsx` → **rename** `ui/src/chrome/exec/OrderSettingsSection.tsx` (test file too) — grid rewrite.
- `ui/src/sound/SoundsSection.tsx` — per-event enable toggles.
- `ui/src/chrome/SettingsModal.tsx` — 920px shell, venues nav, `commands` prop.
- `ui/src/chrome/AppShell.tsx` — pass `commands` to `SettingsModal`, drop `status`.
- `ui/tests/*` (Playwright) — one new smoke spec.

---

## Task 1: `atomicfile` + config venue read/validate/write

**Files:**
- Create: `engine/internal/atomicfile/atomicfile.go`
- Create: `engine/internal/atomicfile/atomicfile_test.go`
- Modify: `engine/internal/config/config.go` (append new types + functions; existing content unchanged)
- Test: `engine/internal/config/config_venue_test.go`

**Interfaces:**
- Consumes: existing `config.Config`, `config.Venue`, `config.Gate`, `config.Load` (all present).
- Produces:
  - `atomicfile.Write(path string, data []byte, perm os.FileMode) error`
  - `config.VenueConfig{ Venues []Venue; Gate Gate }`
  - `config.ReadVenueConfig(path string) (VenueConfig, error)`
  - `config.ValidateVenueConfig(vc VenueConfig, credKeys []string) error`
  - `config.WriteVenueConfig(path string, vc VenueConfig) error`

- [ ] **Step 1: Write the failing test for `atomicfile.Write`**

Create `engine/internal/atomicfile/atomicfile_test.go`:

```go
package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteCreatesFileWithPerm(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "out.txt")
	if err := Write(p, []byte("hello"), 0o600); err != nil {
		t.Fatalf("Write: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil || string(b) != "hello" {
		t.Fatalf("read back: %q err=%v", b, err)
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %v, want 0600", fi.Mode().Perm())
	}
}

func TestWriteOverwritesAtomically(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "out.txt")
	_ = os.WriteFile(p, []byte("old"), 0o644)
	if err := Write(p, []byte("new-and-longer"), 0o644); err != nil {
		t.Fatalf("Write: %v", err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "new-and-longer" {
		t.Fatalf("got %q", b)
	}
	// No temp files left behind in the dir.
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("expected 1 file, found %d", len(ents))
	}
}
```

- [ ] **Step 2: Run it to confirm it fails**

Run: `cd engine && go test ./internal/atomicfile/...`
Expected: FAIL — build error, `undefined: Write`.

- [ ] **Step 3: Implement `atomicfile.Write`**

Create `engine/internal/atomicfile/atomicfile.go`:

```go
// Package atomicfile writes a file atomically: a temp file in the same
// directory, fsync, then rename over the target. Callers use it for the
// config.toml and credentials.json writers so a crash mid-write never leaves a
// half-written file.
package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
)

// Write atomically replaces path with data, applying perm to the final file.
// The temp file is created in path's directory so the rename is atomic (same
// filesystem). On any error the temp file is removed.
func Write(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("atomicfile: create temp: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = tmp.Close(); _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("atomicfile: write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("atomicfile: sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("atomicfile: close temp: %w", err)
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("atomicfile: chmod temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("atomicfile: rename: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run the test to confirm it passes**

Run: `cd engine && go test ./internal/atomicfile/...`
Expected: PASS.

- [ ] **Step 5: Write failing tests for the config venue functions**

Create `engine/internal/config/config_venue_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func validVC() VenueConfig {
	return VenueConfig{
		Venues: []Venue{
			{ID: "alpaca-paper", Broker: "alpaca", Env: "paper", Credentials: "alpaca", AccountID: "", AutoArm: true},
			{ID: "tz-live", Broker: "tradezero", Env: "live", Credentials: "tradeZero", AccountID: "TZ123", AutoArm: false},
			{ID: "sim", Broker: "sim", Env: "paper", AutoArm: true},
		},
		Gate: Gate{
			Global: GateGlobal{MaxDayLoss: 1000},
			Venue:  map[string]GateVenue{"alpaca-paper": {MaxOrderValue: 5000, MaxOpenOrders: 3}},
		},
	}
}

func TestValidateVenueConfigAccepts(t *testing.T) {
	if err := ValidateVenueConfig(validVC(), []string{"alpaca", "tradeZero"}); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestValidateVenueConfigRejects(t *testing.T) {
	keys := []string{"alpaca", "tradeZero"}
	cases := map[string]func(vc *VenueConfig){
		"empty id":            func(vc *VenueConfig) { vc.Venues[0].ID = "" },
		"bad id chars":        func(vc *VenueConfig) { vc.Venues[0].ID = "Alpaca_Paper" },
		"duplicate id":        func(vc *VenueConfig) { vc.Venues[1].ID = "alpaca-paper" },
		"bad broker":          func(vc *VenueConfig) { vc.Venues[0].Broker = "etrade" },
		"bad env":             func(vc *VenueConfig) { vc.Venues[0].Env = "demo" },
		"live auto-arm":       func(vc *VenueConfig) { vc.Venues[1].AutoArm = true },
		"missing cred key":    func(vc *VenueConfig) { vc.Venues[0].Credentials = "nope" },
		"tz missing account":  func(vc *VenueConfig) { vc.Venues[1].AccountID = "" },
		"negative gate cap":   func(vc *VenueConfig) { vc.Gate.Global.MaxDayLoss = -1 },
		"gate key unknown id": func(vc *VenueConfig) { vc.Gate.Venue["ghost"] = GateVenue{} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			vc := validVC()
			mutate(&vc)
			if err := ValidateVenueConfig(vc, keys); err == nil {
				t.Fatalf("expected rejection for %q", name)
			}
		})
	}
}

func TestWriteVenueConfigRoundTripAndBak(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	original := `# hand-written, keep me
[md]
session_anchor = "09:30"

[[venue]]
id = "old"
broker = "sim"
env = "paper"
`
	if err := os.WriteFile(p, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	vc := validVC()
	if err := WriteVenueConfig(p, vc); err != nil {
		t.Fatalf("WriteVenueConfig: %v", err)
	}

	// .bak holds the ORIGINAL bytes (comments intact).
	bak, err := os.ReadFile(p + ".bak")
	if err != nil || string(bak) != original {
		t.Fatalf(".bak not the original: err=%v\n%s", err, bak)
	}

	// Reloading the rewritten file yields the venues+gate we set, and the
	// non-venue section value survives.
	got, err := ReadVenueConfig(p)
	if err != nil {
		t.Fatalf("ReadVenueConfig: %v", err)
	}
	if len(got.Venues) != 3 || got.Venues[1].ID != "tz-live" || got.Venues[1].AccountID != "TZ123" {
		t.Fatalf("venues not round-tripped: %+v", got.Venues)
	}
	if got.Gate.Venue["alpaca-paper"].MaxOrderValue != 5000 {
		t.Fatalf("gate not round-tripped: %+v", got.Gate)
	}
	full, err := Load(p)
	if err != nil || full.MD.SessionAnchor != "09:30" {
		t.Fatalf("non-venue section lost: anchor=%q err=%v", full.MD.SessionAnchor, err)
	}

	// Second write does NOT overwrite .bak.
	vc.Venues = vc.Venues[:1]
	if err := WriteVenueConfig(p, vc); err != nil {
		t.Fatal(err)
	}
	bak2, _ := os.ReadFile(p + ".bak")
	if string(bak2) != original {
		t.Fatalf(".bak overwritten on second save")
	}
}
```

- [ ] **Step 6: Run to confirm failure**

Run: `cd engine && go test ./internal/config/...`
Expected: FAIL — `undefined: VenueConfig`, `ValidateVenueConfig`, `WriteVenueConfig`, `ReadVenueConfig`.

- [ ] **Step 7: Implement the config venue functions**

Append to `engine/internal/config/config.go` (add `regexp`, `strings`, and `github.com/earlisreal/eTape/engine/internal/atomicfile` to imports; `BurntSushi/toml` is already imported):

```go
// VenueConfig is the file-writable subset of Config the settings UI edits.
type VenueConfig struct {
	Venues []Venue
	Gate   Gate
}

var venueIDRe = regexp.MustCompile(`^[a-z0-9-]+$`)

var validBrokers = map[string]bool{"tradezero": true, "alpaca": true, "moomoo": true, "sim": true}

// ReadVenueConfig parses the TOML file fresh and returns its venue+gate subset.
// A missing file yields the defaults (not an error), matching boot semantics.
func ReadVenueConfig(path string) (VenueConfig, error) {
	c, err := Load(path)
	if err != nil {
		return VenueConfig{}, err
	}
	return VenueConfig{Venues: c.Venues, Gate: c.Gate}, nil
}

// ValidateVenueConfig enforces the settings-UI write rules. credKeys is the set
// of credential names currently present in credentials.json. It returns a
// field-naming error on the first violation and writes nothing.
func ValidateVenueConfig(vc VenueConfig, credKeys []string) error {
	keys := map[string]bool{}
	for _, k := range credKeys {
		keys[k] = true
	}
	seen := map[string]bool{}
	ids := map[string]bool{}
	for _, v := range vc.Venues {
		if v.ID == "" || !venueIDRe.MatchString(v.ID) {
			return fmt.Errorf("venue id %q: must be non-empty and match [a-z0-9-]", v.ID)
		}
		if seen[v.ID] {
			return fmt.Errorf("venue id %q: duplicate", v.ID)
		}
		seen[v.ID] = true
		ids[v.ID] = true
		if !validBrokers[v.Broker] {
			return fmt.Errorf("venue %q: broker %q must be one of tradezero, alpaca, moomoo, sim", v.ID, v.Broker)
		}
		if v.Env != "paper" && v.Env != "live" {
			return fmt.Errorf("venue %q: env %q must be paper or live", v.ID, v.Env)
		}
		if v.Env == "live" && v.AutoArm {
			return fmt.Errorf("venue %q: live venues cannot auto-arm", v.ID)
		}
		if v.Broker == "tradezero" || v.Broker == "alpaca" {
			if !keys[v.Credentials] {
				return fmt.Errorf("venue %q: credentials %q names no existing key", v.ID, v.Credentials)
			}
		}
		if v.Broker == "tradezero" && v.AccountID == "" {
			return fmt.Errorf("venue %q: tradezero requires account_id", v.ID)
		}
	}
	g := vc.Gate.Global
	if g.MaxDayLoss < 0 || g.MaxSymbolPositionValue < 0 || g.MaxSymbolPositionShares < 0 {
		return fmt.Errorf("gate.global: caps must be >= 0 (0 = off)")
	}
	for id, gv := range vc.Gate.Venue {
		if !ids[id] {
			return fmt.Errorf("gate.venue.%s: no venue with that id", id)
		}
		if gv.MaxOrderValue < 0 || gv.MaxPositionValue < 0 || gv.MaxPositionShares < 0 || gv.MaxOpenOrders < 0 {
			return fmt.Errorf("gate.venue.%s: caps must be >= 0 (0 = off)", id)
		}
	}
	return nil
}

// WriteVenueConfig re-reads path into a full Config, replaces its Venues and
// Gate, and re-encodes the whole file atomically. On the FIRST UI-driven write
// it copies the original file to path+".bak" (only if that .bak is absent), so
// the hand-written original — comments and all — is preserved forever.
// Decode→encode loses comments/ordering and any keys unknown to Config; that is
// the accepted trade-off (Config is the engine's entire config surface).
func WriteVenueConfig(path string, vc VenueConfig) error {
	if orig, err := os.ReadFile(path); err == nil {
		bak := path + ".bak"
		if _, statErr := os.Stat(bak); errors.Is(statErr, os.ErrNotExist) {
			if err := atomicfile.Write(bak, orig, 0o644); err != nil {
				return fmt.Errorf("config: write .bak: %w", err)
			}
		}
	}
	c, err := Load(path)
	if err != nil {
		return err
	}
	c.Venues = vc.Venues
	c.Gate = vc.Gate
	var buf strings.Builder
	if err := toml.NewEncoder(&buf).Encode(c); err != nil {
		return fmt.Errorf("config: encode: %w", err)
	}
	if err := atomicfile.Write(path, []byte(buf.String()), 0o644); err != nil {
		return fmt.Errorf("config: write: %w", err)
	}
	return nil
}
```

- [ ] **Step 8: Run tests to confirm they pass**

Run: `cd engine && go test ./internal/config/... ./internal/atomicfile/...`
Expected: PASS.

- [ ] **Step 9: Vet + commit**

Run: `cd engine && go vet ./internal/config/... ./internal/atomicfile/... && gofmt -l internal/config internal/atomicfile`
Expected: no output.

```bash
git add engine/internal/atomicfile engine/internal/config
git commit -m "feat(config): venue config read/validate/write with atomic file + .bak"
```

---

## Task 2: `creds` Put / Delete / Keys

**Files:**
- Modify: `engine/internal/creds/creds.go` (append; existing `Load`/`Get`/`DefaultPath` unchanged)
- Test: `engine/internal/creds/creds_write_test.go`

**Interfaces:**
- Consumes: `atomicfile.Write` (Task 1), existing `creds.Pair`, `creds.File`.
- Produces:
  - `creds.Put(path, name, keyID, secretKey string) error`
  - `creds.Delete(path, name string) error`
  - `creds.Keys(path string) ([]string, error)` — sorted names; missing file → `(nil, nil)`

- [ ] **Step 1: Write the failing test**

Create `engine/internal/creds/creds_write_test.go`:

```go
package creds

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPutPreservesSiblingsByteForByte(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "credentials.json")
	// A sibling entry eTape doesn't own, with an extra unknown field.
	seed := `{"eJournalThing":{"keyId":"K","secretKey":"S","futureField":42}}`
	if err := os.WriteFile(p, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Put(p, "alpaca", "AK", "AS"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	var raw map[string]json.RawMessage
	b, _ := os.ReadFile(p)
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["eJournalThing"]) != `{"keyId":"K","secretKey":"S","futureField":42}` {
		t.Fatalf("sibling mutated: %s", raw["eJournalThing"])
	}
	got, _ := Load(p)
	if p := got["alpaca"]; p.KeyID != "AK" || p.SecretKey != "AS" {
		t.Fatalf("put entry wrong: %+v", p)
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %v, want 0600", fi.Mode().Perm())
	}
}

func TestPutReplacesOnlyTarget(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "credentials.json")
	_ = os.WriteFile(p, []byte(`{"alpaca":{"keyId":"old","secretKey":"old"}}`), 0o600)
	if err := Put(p, "alpaca", "new", "new"); err != nil {
		t.Fatal(err)
	}
	got, _ := Load(p)
	if got["alpaca"].KeyID != "new" {
		t.Fatalf("replace failed: %+v", got["alpaca"])
	}
}

func TestPutCreatesMissingFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "credentials.json")
	if err := Put(p, "alpaca", "AK", "AS"); err != nil {
		t.Fatalf("Put on missing file: %v", err)
	}
	got, _ := Load(p)
	if got["alpaca"].KeyID != "AK" {
		t.Fatalf("not created: %+v", got)
	}
}

func TestDeleteRemovesEntry(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "credentials.json")
	_ = os.WriteFile(p, []byte(`{"a":{"keyId":"1","secretKey":"1"},"b":{"keyId":"2","secretKey":"2"}}`), 0o600)
	if err := Delete(p, "a"); err != nil {
		t.Fatal(err)
	}
	got, _ := Load(p)
	if _, ok := got["a"]; ok {
		t.Fatalf("a not deleted")
	}
	if _, ok := got["b"]; !ok {
		t.Fatalf("b wrongly removed")
	}
}

func TestKeysSortedAndMissingFileEmpty(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "credentials.json")
	if ks, err := Keys(p); err != nil || len(ks) != 0 {
		t.Fatalf("missing file: %v %v", ks, err)
	}
	_ = os.WriteFile(p, []byte(`{"z":{"keyId":"1","secretKey":"1"},"a":{"keyId":"2","secretKey":"2"}}`), 0o600)
	ks, err := Keys(p)
	if err != nil || len(ks) != 2 || ks[0] != "a" || ks[1] != "z" {
		t.Fatalf("keys: %v %v", ks, err)
	}
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `cd engine && go test ./internal/creds/...`
Expected: FAIL — `undefined: Put`, `Delete`, `Keys`.

- [ ] **Step 3: Implement the writers**

Append to `engine/internal/creds/creds.go` (add `errors`, `sort`, and `github.com/earlisreal/eTape/engine/internal/atomicfile` to imports):

```go
// readRaw reads the file as an order-agnostic name→raw-entry map, preserving
// each entry's exact JSON bytes. A missing file yields an empty map.
func readRaw(path string) (map[string]json.RawMessage, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("creds: %w", err)
	}
	m := map[string]json.RawMessage{}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, fmt.Errorf("creds %s: %w", path, err)
		}
	}
	return m, nil
}

func writeRaw(path string, m map[string]json.RawMessage) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("creds: marshal: %w", err)
	}
	return atomicfile.Write(path, b, 0o600)
}

// Put upserts one credential entry, preserving every sibling entry
// byte-for-byte (the file is shared with eJournal). The file is created 0600 if
// absent. Secrets are never logged.
func Put(path, name, keyID, secretKey string) error {
	m, err := readRaw(path)
	if err != nil {
		return err
	}
	entry, err := json.Marshal(Pair{KeyID: keyID, SecretKey: secretKey})
	if err != nil {
		return fmt.Errorf("creds: marshal entry: %w", err)
	}
	m[name] = entry
	return writeRaw(path, m)
}

// Delete removes one entry, preserving all siblings. A missing entry or missing
// file is a no-op success.
func Delete(path, name string) error {
	m, err := readRaw(path)
	if err != nil {
		return err
	}
	delete(m, name)
	return writeRaw(path, m)
}

// Keys returns the sorted credential names. A missing file yields nil, nil.
func Keys(path string) ([]string, error) {
	m, err := readRaw(path)
	if err != nil {
		return nil, err
	}
	if len(m) == 0 {
		return nil, nil
	}
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks, nil
}
```

- [ ] **Step 4: Run tests to confirm they pass**

Run: `cd engine && go test ./internal/creds/...`
Expected: PASS.

- [ ] **Step 5: Vet + commit**

Run: `cd engine && go vet ./internal/creds/... && gofmt -l internal/creds`
Expected: no output.

```bash
git add engine/internal/creds
git commit -m "feat(creds): atomic Put/Delete/Keys preserving sibling entries"
```

---

## Task 3: `venueadmin` package

**Files:**
- Create: `engine/internal/venueadmin/venueadmin.go`
- Create: `engine/internal/venueadmin/venueadmin_test.go`

**Interfaces:**
- Consumes: `config.VenueConfig`, `config.ReadVenueConfig`, `config.ValidateVenueConfig`, `config.WriteVenueConfig` (Task 1); `creds.Put`, `creds.Delete`, `creds.Keys` (Task 2).
- Produces (this is exactly the `venueAdmin` seam uihub will depend on in Task 4):
  - `venueadmin.New(cfgPath, credsPath string, booted config.VenueConfig) *Admin`
  - `(*Admin) GetVenueSetup() (file, running config.VenueConfig, credKeys []string, err error)`
  - `(*Admin) SetVenueSetup(vc config.VenueConfig) error`
  - `(*Admin) PutCredential(name, keyID, secretKey string) error`
  - `(*Admin) DeleteCredential(name string) error`

- [ ] **Step 1: Write the failing test**

Create `engine/internal/venueadmin/venueadmin_test.go`:

```go
package venueadmin

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/config"
)

func setup(t *testing.T) (*Admin, string, string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	credsPath := filepath.Join(dir, "credentials.json")
	_ = os.WriteFile(cfgPath, []byte("[md]\nsession_anchor = \"09:30\"\n"), 0o644)
	_ = os.WriteFile(credsPath, []byte(`{"alpaca":{"keyId":"K","secretKey":"S"}}`), 0o600)
	booted := config.VenueConfig{Venues: []config.Venue{{ID: "boot", Broker: "sim", Env: "paper", AutoArm: true}}}
	return New(cfgPath, credsPath, booted), cfgPath, credsPath
}

func TestSetVenueSetupValidatesBeforeWriting(t *testing.T) {
	a, cfgPath, _ := setup(t)
	// live + auto_arm must be rejected AND leave the file untouched.
	bad := config.VenueConfig{Venues: []config.Venue{{ID: "x", Broker: "sim", Env: "live", AutoArm: true}}}
	if err := a.SetVenueSetup(bad); err == nil {
		t.Fatal("expected validation error")
	}
	got, _ := config.ReadVenueConfig(cfgPath)
	if len(got.Venues) != 0 {
		t.Fatalf("file mutated on invalid set: %+v", got.Venues)
	}
}

func TestSetVenueSetupWritesValid(t *testing.T) {
	a, cfgPath, _ := setup(t)
	vc := config.VenueConfig{Venues: []config.Venue{{ID: "alpaca-paper", Broker: "alpaca", Env: "paper", Credentials: "alpaca", AutoArm: true}}}
	if err := a.SetVenueSetup(vc); err != nil {
		t.Fatalf("SetVenueSetup: %v", err)
	}
	got, _ := config.ReadVenueConfig(cfgPath)
	if len(got.Venues) != 1 || got.Venues[0].ID != "alpaca-paper" {
		t.Fatalf("not written: %+v", got.Venues)
	}
}

func TestGetVenueSetupReturnsFileRunningKeys(t *testing.T) {
	a, _, _ := setup(t)
	file, running, keys, err := a.GetVenueSetup()
	if err != nil {
		t.Fatal(err)
	}
	if len(running.Venues) != 1 || running.Venues[0].ID != "boot" {
		t.Fatalf("running should be booted: %+v", running.Venues)
	}
	if len(file.Venues) != 0 {
		t.Fatalf("file should have no venues yet: %+v", file.Venues)
	}
	if len(keys) != 1 || keys[0] != "alpaca" {
		t.Fatalf("keys: %v", keys)
	}
}

func TestDeleteCredentialBlockedWhileReferenced(t *testing.T) {
	a, cfgPath, credsPath := setup(t)
	_ = os.WriteFile(cfgPath, []byte(`[[venue]]
id = "a"
broker = "alpaca"
env = "paper"
credentials = "alpaca"
`), 0o644)
	if err := a.DeleteCredential("alpaca"); err == nil {
		t.Fatal("expected block: alpaca is referenced")
	}
	// Still present.
	ks, _ := a.PutCredential("alpaca", "K", "S"), error(nil)
	_ = ks
	if _, err := os.Stat(credsPath); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `cd engine && go test ./internal/venueadmin/...`
Expected: FAIL — build error, `undefined: New`.

- [ ] **Step 3: Implement `Admin`**

Create `engine/internal/venueadmin/venueadmin.go`:

```go
// Package venueadmin implements the settings-UI seam that reads and writes the
// two config files (config.toml venues+gate, credentials.json) behind the
// engine's WS commands. It captures the venue config the engine BOOTED with so
// the UI can show a "restart required" banner when the file drifts from it.
// Nothing here touches the running gate or arm state.
package venueadmin

import (
	"fmt"

	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/creds"
)

type Admin struct {
	cfgPath   string
	credsPath string
	booted    config.VenueConfig
}

func New(cfgPath, credsPath string, booted config.VenueConfig) *Admin {
	return &Admin{cfgPath: cfgPath, credsPath: credsPath, booted: booted}
}

// GetVenueSetup returns the file state (parsed fresh), the running state (what
// the engine booted with), and the credential key NAMES. A missing/unreadable
// credentials file yields no keys, not an error.
func (a *Admin) GetVenueSetup() (file, running config.VenueConfig, credKeys []string, err error) {
	file, err = config.ReadVenueConfig(a.cfgPath)
	if err != nil {
		return config.VenueConfig{}, config.VenueConfig{}, nil, err
	}
	keys, kerr := creds.Keys(a.credsPath)
	if kerr != nil {
		keys = nil // credentials are optional for read; never fail the setup fetch
	}
	return file, a.booted, keys, nil
}

// SetVenueSetup validates against the current credential keys, then rewrites
// config.toml. Nothing is written on any validation failure.
func (a *Admin) SetVenueSetup(vc config.VenueConfig) error {
	keys, _ := creds.Keys(a.credsPath)
	if err := config.ValidateVenueConfig(vc, keys); err != nil {
		return err
	}
	return config.WriteVenueConfig(a.cfgPath, vc)
}

func (a *Admin) PutCredential(name, keyID, secretKey string) error {
	return creds.Put(a.credsPath, name, keyID, secretKey)
}

// DeleteCredential refuses while any venue in the current FILE config
// references the name.
func (a *Admin) DeleteCredential(name string) error {
	file, err := config.ReadVenueConfig(a.cfgPath)
	if err != nil {
		return err
	}
	for _, v := range file.Venues {
		if v.Credentials == name {
			return fmt.Errorf("credential %q is in use by venue %q", name, v.ID)
		}
	}
	return creds.Delete(a.credsPath, name)
}
```

- [ ] **Step 4: Run tests to confirm they pass**

Run: `cd engine && go test ./internal/venueadmin/...`
Expected: PASS.

- [ ] **Step 5: Vet + commit**

Run: `cd engine && go vet ./internal/venueadmin/... && gofmt -l internal/venueadmin`
Expected: no output.

```bash
git add engine/internal/venueadmin
git commit -m "feat(venueadmin): file-only venue/gate/credential admin seam"
```

---

## Task 4: wire DTOs, WS commands, wiring, tygo regen

**Files:**
- Modify: `engine/internal/uihub/wsmsg/payloads.go` (append DTOs)
- Modify: `engine/internal/uihub/commands.go` (seam interface + 4 cases + mappers + struct field + constructor)
- Modify: `engine/internal/uihub/api.go` (thread `venueAdmin` through `New`)
- Modify: `engine/cmd/etape/main.go` (build + inject `venueadmin.Admin`)
- Modify: `ui/src/gen/wsmsg.ts` (regenerated via `make gen-ts`)
- Test: `engine/internal/uihub/commands_test.go` (append cases + spy; update all existing `newCommands(` call sites)
- Test: `engine/internal/uihub/export_test.go` (thread `va` through `NewCommandsForTest`)
- Test: `engine/internal/uihub/server_test.go`, `engine/internal/uihub/api_test.go`, `engine/internal/uihubtest/e2e_test.go` (add the new arg to every `NewCommandsForTest`/`uihub.New` call — these break otherwise)

**Interfaces:**
- Consumes: `venueadmin.Admin` (Task 3), `config.VenueConfig`/`Venue`/`Gate`/`GateGlobal`/`GateVenue`.
- Produces wire DTOs `wsmsg.Venue`, `wsmsg.Gate`, `wsmsg.VenueConfig`, `wsmsg.VenueSetup`, `wsmsg.SetVenueSetupArgs`, `wsmsg.PutCredentialArgs`, `wsmsg.DeleteCredentialArgs`, and WS commands `GetVenueSetup`/`SetVenueSetup`/`PutCredential`/`DeleteCredential`.

- [ ] **Step 1: Add the wire DTOs**

Append to `engine/internal/uihub/wsmsg/payloads.go` (reusing existing `GlobalLimitsView` and `GateLimitsView`, which have the exact gate-cap fields):

```go
// ---- venue & credentials config DTOs (settings "Venues & credentials") ----

// Venue mirrors config.Venue (no secret material — Credentials is a key NAME).
type Venue struct {
	ID          string `json:"id"`
	Broker      string `json:"broker"`
	Env         string `json:"env"`
	Credentials string `json:"credentials"`
	AccountID   string `json:"accountId"`
	AutoArm     bool   `json:"autoArm"`
}

// Gate mirrors config.Gate; reuses the existing limit-view shapes.
type Gate struct {
	Global GlobalLimitsView          `json:"global"`
	Venue  map[string]GateLimitsView `json:"venue"`
}

type VenueConfig struct {
	Venues []Venue `json:"venues"`
	Gate   Gate    `json:"gate"`
}

// VenueSetup is the GetVenueSetup result. file = parsed from config.toml,
// running = what the engine booted with; the restart banner shows when they
// differ. credKeys = credential NAMES only.
type VenueSetup struct {
	File     VenueConfig `json:"file"`
	Running  VenueConfig `json:"running"`
	CredKeys []string    `json:"credKeys"`
}

type SetVenueSetupArgs struct {
	Venues []Venue `json:"venues"`
	Gate   Gate    `json:"gate"`
}

type PutCredentialArgs struct {
	Name      string `json:"name"`
	KeyID     string `json:"keyId"`
	SecretKey string `json:"secretKey"`
}

type DeleteCredentialArgs struct {
	Name string `json:"name"`
}
```

- [ ] **Step 2: Add the seam, mappers, struct field, and constructor arg in `commands.go`**

In `engine/internal/uihub/commands.go`, add `"github.com/earlisreal/eTape/engine/internal/config"` to the import block. Add the seam interface next to the others (after `demandCtl`):

```go
// venueAdmin is the file-only settings seam (satisfied by *venueadmin.Admin).
// It never touches the running gate/arm state — edits apply at next boot.
type venueAdmin interface {
	GetVenueSetup() (file, running config.VenueConfig, credKeys []string, err error)
	SetVenueSetup(vc config.VenueConfig) error
	PutCredential(name, keyID, secretKey string) error
	DeleteCredential(name string) error
}
```

Change the `commands` struct and `newCommands` to carry it:

```go
type commands struct {
	ex   execDoer
	cfg  configStore
	ind  indicatorCtl
	dem  demandCtl
	va   venueAdmin
	feed func() Feed
}

func newCommands(ex execDoer, cfg configStore, ind indicatorCtl, dem demandCtl, va venueAdmin, feed func() Feed) *commands {
	return &commands{ex: ex, cfg: cfg, ind: ind, dem: dem, va: va, feed: feed}
}
```

Add the config↔wire mappers at the bottom of the file:

```go
func venueToWire(v config.Venue) wsmsg.Venue {
	return wsmsg.Venue{ID: v.ID, Broker: v.Broker, Env: v.Env, Credentials: v.Credentials, AccountID: v.AccountID, AutoArm: v.AutoArm}
}

func gateToWire(g config.Gate) wsmsg.Gate {
	vm := map[string]wsmsg.GateLimitsView{}
	for id, gv := range g.Venue {
		vm[id] = wsmsg.GateLimitsView{MaxOrderValue: gv.MaxOrderValue, MaxPositionValue: gv.MaxPositionValue, MaxPositionShares: gv.MaxPositionShares, MaxOpenOrders: gv.MaxOpenOrders}
	}
	return wsmsg.Gate{
		Global: wsmsg.GlobalLimitsView{MaxDayLoss: g.Global.MaxDayLoss, MaxSymbolPositionValue: g.Global.MaxSymbolPositionValue, MaxSymbolPositionShares: g.Global.MaxSymbolPositionShares},
		Venue:  vm,
	}
}

func venueConfigToWire(vc config.VenueConfig) wsmsg.VenueConfig {
	vs := make([]wsmsg.Venue, 0, len(vc.Venues))
	for _, v := range vc.Venues {
		vs = append(vs, venueToWire(v))
	}
	return wsmsg.VenueConfig{Venues: vs, Gate: gateToWire(vc.Gate)}
}

func venueConfigFromWire(venues []wsmsg.Venue, gate wsmsg.Gate) config.VenueConfig {
	vs := make([]config.Venue, 0, len(venues))
	for _, v := range venues {
		vs = append(vs, config.Venue{ID: v.ID, Broker: v.Broker, Env: v.Env, Credentials: v.Credentials, AccountID: v.AccountID, AutoArm: v.AutoArm})
	}
	vm := map[string]config.GateVenue{}
	for id, gv := range gate.Venue {
		vm[id] = config.GateVenue{MaxOrderValue: gv.MaxOrderValue, MaxPositionValue: gv.MaxPositionValue, MaxPositionShares: gv.MaxPositionShares, MaxOpenOrders: gv.MaxOpenOrders}
	}
	return config.VenueConfig{
		Venues: vs,
		Gate:   config.Gate{Global: config.GateGlobal{MaxDayLoss: gate.Global.MaxDayLoss, MaxSymbolPositionValue: gate.Global.MaxSymbolPositionValue, MaxSymbolPositionShares: gate.Global.MaxSymbolPositionShares}, Venue: vm},
	}
}
```

- [ ] **Step 3: Add the four command cases**

In `commands.go`, inside the `handle` switch (before `default:`):

```go
	case "GetVenueSetup":
		file, running, keys, err := cd.va.GetVenueSetup()
		if err != nil {
			return blocked("venue read error")
		}
		return wsmsg.AckMsg{Status: "accepted", Value: wsmsg.VenueSetup{
			File: venueConfigToWire(file), Running: venueConfigToWire(running), CredKeys: keys,
		}}
	case "SetVenueSetup":
		var a wsmsg.SetVenueSetupArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return blocked("bad args")
		}
		if err := cd.va.SetVenueSetup(venueConfigFromWire(a.Venues, a.Gate)); err != nil {
			return blocked(err.Error())
		}
		return wsmsg.AckMsg{Status: "accepted"}
	case "PutCredential":
		var a wsmsg.PutCredentialArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return blocked("bad args")
		}
		if a.Name == "" || a.KeyID == "" || a.SecretKey == "" {
			return blocked("name, keyId, and secretKey are required")
		}
		if err := cd.va.PutCredential(a.Name, a.KeyID, a.SecretKey); err != nil {
			return blocked(err.Error())
		}
		return wsmsg.AckMsg{Status: "accepted"}
	case "DeleteCredential":
		var a wsmsg.DeleteCredentialArgs
		if err := json.Unmarshal(args, &a); err != nil || a.Name == "" {
			return blocked("bad args")
		}
		if err := cd.va.DeleteCredential(a.Name); err != nil {
			return blocked(err.Error())
		}
		return wsmsg.AckMsg{Status: "accepted"}
```

- [ ] **Step 4: Thread `venueAdmin` through `api.go`**

In `engine/internal/uihub/api.go`, change `New`'s signature and the `newCommands` call:

```go
func New(clk clock.Clock, cfg Config, ex ExecCore, st Stores, ind Indicators, va venueAdmin) (*Hub, *Server) {
```

and (line ~96):

```go
	cmd := newCommands(ex, st, ind, h, va, h.feed)
```

- [ ] **Step 5: Build + inject the admin in `main.go`**

In `engine/cmd/etape/main.go`, add imports `"github.com/earlisreal/eTape/engine/internal/config"` (already present) and `"github.com/earlisreal/eTape/engine/internal/venueadmin"`. Immediately before the `uihub.New(...)` call (line ~148), capture the booted venue config and build the admin:

```go
	venueAdm := venueadmin.New(*cfgPath, creds.DefaultPath(), config.VenueConfig{Venues: cfg.Venues, Gate: cfg.Gate})
```

Add `venueAdm` as the final argument to `uihub.New(...)`:

```go
	}, execCore, st, core, venueAdm)
```

- [ ] **Step 6: Fix every call site of the changed signatures (build-breakers)**

Adding a param to `newCommands` and `uihub.New` breaks existing tests that call them. Update all of these first (the existing spy types are `spyExec`, `spyCfg`, `spyInd`, `spyDemandCtl` — verified names):

- `engine/internal/uihub/commands_test.go`: insert the new 5th arg (`&spyVenueAdmin{}`, defined below) into **every** `newCommands(...)` call — lines **44, 63, 72, 82, 91, 108, 120, and 165**. Note line 165 is inside the `newCmdWith` helper and reads `newCommands(nil, nil, nil, dem, getter)` (the last arg is the variable `getter`, not a literal `func() Feed`) → make it `newCommands(nil, nil, nil, dem, &spyVenueAdmin{}, getter)`. A naive "insert before `func() Feed`" edit misses this one.
- `engine/internal/uihub/export_test.go:24-26`: `NewCommandsForTest` calls `newCommands` directly. Add a `va venueAdmin` param and thread it:
  ```go
  func NewCommandsForTest(ex execDoer, c configStore, i indicatorCtl, d demandCtl, va venueAdmin, f func() Feed) commandHandler {
  	return newCommands(ex, c, i, d, va, f)
  }
  ```
- `engine/internal/uihub/server_test.go` (lines **55, 129, 211, 290, 421**): add a 6th arg `nil` to each `uihub.NewCommandsForTest(...)` call (package `uihub_test` cannot name the unexported `venueAdmin`, but `nil` for an interface param is fine).
- `engine/internal/uihub/api_test.go:31` and `engine/internal/uihubtest/e2e_test.go` (lines **92 and 181**): add a 6th arg `nil` to each `uihub.New(...)` call.

Build after this step to confirm the tree compiles before adding new tests: `cd engine && go build ./... && go vet ./internal/uihub/...`

- [ ] **Step 6b: Add command tests + spy**

In `engine/internal/uihub/commands_test.go`, append the spy and cases (add `errors`, `strings`, and `github.com/earlisreal/eTape/engine/internal/config` to the import block — none of the three is currently imported):

```go
type spyVenueAdmin struct {
	setCalled  bool
	putCalled  bool
	delErr     error
	setErr     error
	lastPutSec string
}

func (s *spyVenueAdmin) GetVenueSetup() (config.VenueConfig, config.VenueConfig, []string, error) {
	return config.VenueConfig{Venues: []config.Venue{{ID: "file-v", Broker: "sim", Env: "paper"}}},
		config.VenueConfig{Venues: []config.Venue{{ID: "run-v", Broker: "sim", Env: "paper"}}},
		[]string{"alpaca"}, nil
}
func (s *spyVenueAdmin) SetVenueSetup(config.VenueConfig) error { s.setCalled = true; return s.setErr }
func (s *spyVenueAdmin) PutCredential(_, _, sec string) error   { s.putCalled = true; s.lastPutSec = sec; return nil }
func (s *spyVenueAdmin) DeleteCredential(string) error          { return s.delErr }

func TestGetVenueSetupResultHasNoSecrets(t *testing.T) {
	va := &spyVenueAdmin{}
	cd := newCommands(&spyExec{}, &spyCfg{}, &spyInd{}, &spyDemandCtl{}, va, func() Feed { return nil })
	ack := cd.handle(context.Background(), "GetVenueSetup", json.RawMessage(`{}`), 0)
	if ack.Status != "accepted" {
		t.Fatalf("status %v", ack.Status)
	}
	b, _ := json.Marshal(ack)
	if strings.Contains(string(b), "secretKey") || strings.Contains(string(b), "keyId") {
		t.Fatalf("setup result leaked secret material: %s", b)
	}
}

func TestSetVenueSetupBlocksOnError(t *testing.T) {
	va := &spyVenueAdmin{setErr: errors.New("venue \"x\": env \"live\" cannot auto-arm")}
	cd := newCommands(&spyExec{}, &spyCfg{}, &spyInd{}, &spyDemandCtl{}, va, func() Feed { return nil })
	ack := cd.handle(context.Background(), "SetVenueSetup", json.RawMessage(`{"venues":[],"gate":{"global":{},"venue":{}}}`), 0)
	if ack.Status != "blocked" || ack.Reason == "" {
		t.Fatalf("want blocked with reason, got %+v", ack)
	}
}

func TestPutCredentialRequiresAllFields(t *testing.T) {
	va := &spyVenueAdmin{}
	cd := newCommands(&spyExec{}, &spyCfg{}, &spyInd{}, &spyDemandCtl{}, va, func() Feed { return nil })
	ack := cd.handle(context.Background(), "PutCredential", json.RawMessage(`{"name":"a","keyId":"","secretKey":"s"}`), 0)
	if ack.Status != "blocked" || va.putCalled {
		t.Fatalf("empty keyId must block before calling admin: %+v", ack)
	}
}
```

(All three of `errors`, `strings`, and `config` must be added to the import block — none is present today.)

- [ ] **Step 7: Run engine tests**

Run: `cd engine && go test -race ./internal/uihub/... && go vet ./... && go build ./...`
Expected: PASS / no output.

- [ ] **Step 8: Regenerate wire types**

Run: `cd engine && make gen-ts && make gen-ts-check`
Expected: `gen-ts` regenerates `ui/src/gen/wsmsg.ts` (now containing `Venue`, `Gate`, `VenueConfig`, `VenueSetup`, `SetVenueSetupArgs`, `PutCredentialArgs`, `DeleteCredentialArgs`); `gen-ts-check` prints nothing and exits 0.

- [ ] **Step 9: Verify the UI still typechecks against the regenerated types**

Run: `cd ui && npm run typecheck`
Expected: PASS (no consumers yet, so this only confirms the generated file is valid TS).

- [ ] **Step 10: Commit**

```bash
git add engine/internal/uihub engine/cmd/etape/main.go ui/src/gen/wsmsg.ts
git commit -m "feat(uihub): GetVenueSetup/SetVenueSetup/PutCredential/DeleteCredential commands"
```

---

## Task 5: Template model — offset unit, position-percent sizing, normalization

**Files:**
- Modify: `ui/src/chrome/exec/priceSource.ts`
- Modify: `ui/src/chrome/exec/sizing.ts`
- Modify: `ui/src/chrome/exec/actionTemplate.ts`
- Modify: `ui/src/chrome/exec/resolveTemplate.ts:18`
- Modify: `ui/src/chrome/exec/useOrderConfig.tsx` (lines 17, 24)
- Test (all three ALREADY EXIST — edit, don't overwrite): `ui/src/chrome/exec/sizing.test.ts`, `ui/src/chrome/exec/priceSource.test.ts`, `ui/src/chrome/exec/actionTemplate.test.ts`
- Test (existing tests that break and MUST be updated in this task): `ui/src/chrome/exec/resolveTemplate.test.ts` (line 24 `fraction: "all"` → `pct: 100`), `ui/src/chrome/exec/useOrderConfig.test.tsx` (the "no value → defaults" case expects raw `DEFAULT_ORDER_CONFIG`; after seeding with `normalizeOrderConfig` it must expect `normalizeOrderConfig(DEFAULT_ORDER_CONFIG)`)

**Interfaces:**
- Produces:
  - `priceSource.ts`: `export type PriceOffsetUnit = "$" | "%"`; `resolvePrice(source: PriceSource, offset: number, unit: PriceOffsetUnit | undefined, quote: Quote): number`
  - `sizing.ts`: `resolveShares` `PositionFraction` branch reads `spec.pct`
  - `actionTemplate.ts`: `PlaceOrderTemplate.priceOffsetUnit?: PriceOffsetUnit`; `normalizeOrderConfig(config: OrderConfig): OrderConfig`
- Consumes: existing `SizingSpec`, `OrderConfig`, `DEFAULT_TEMPLATES`, `DEFAULT_ORDER_CONFIG`.

> **Note — breaking the resolver contract:** after this task `resolveShares` for `PositionFraction` reads `pct`, not `fraction`. Every inline `PositionFraction` spec must carry `pct` or it sizes to 0. `DEFAULT_TEMPLATES` is fixed here; the two runtime call sites (`OrderTicketPanel`, `AccountPanel`) are fixed in Task 6.

- [ ] **Step 1: Write failing tests for `resolvePrice` unit + `resolveShares` pct**

In `ui/src/chrome/exec/priceSource.test.ts` (it exists with a 3-arg `describe("resolvePrice", ...)` block calling `resolvePrice("Bid", 0, q)` — those calls become an arity error once the signature is 4-arg), **replace the existing `describe("resolvePrice", ...)` block** with:

```ts
import { describe, it, expect } from "vitest";
import { resolvePrice } from "./priceSource";

const q = { symbol: "X", bid: 100, ask: 102, last: 101, ts: "" };

describe("resolvePrice", () => {
  it("dollar offset (default when unit undefined) adds absolute", () => {
    expect(resolvePrice("Ask", 0.05, undefined, q)).toBeCloseTo(102.05);
    expect(resolvePrice("Bid", -0.05, "$", q)).toBeCloseTo(99.95);
  });
  it("percent offset scales with base, signed both ways", () => {
    expect(resolvePrice("Ask", 1, "%", q)).toBeCloseTo(102 + 1.02); // +1% of 102
    expect(resolvePrice("Bid", -2, "%", q)).toBeCloseTo(100 - 2); // -2% of 100
  });
});
```

In `ui/src/chrome/exec/sizing.test.ts`, **delete the existing `it("PositionFraction all/half of |held|", ...)` block** (it asserts `fraction: "all"`→428, `"half"`→214, and a `-100`→100 short case; all of these size to 0 after this change) and add this block in its place:

```ts
import { describe, it, expect } from "vitest";
import { resolveShares } from "./sizing";

describe("resolveShares PositionFraction reads pct", () => {
  const ctx = { price: 10, buyingPower: 0, positionQty: 300 };
  it("100 pct = full position", () => {
    expect(resolveShares({ mode: "PositionFraction", pct: 100 }, ctx)).toBe(300);
  });
  it("50 pct = half, floored", () => {
    expect(resolveShares({ mode: "PositionFraction", pct: 50 }, { ...ctx, positionQty: 3 })).toBe(1);
  });
  it("missing pct = 0 shares", () => {
    expect(resolveShares({ mode: "PositionFraction" }, ctx)).toBe(0);
  });
  it("uses absolute position for shorts", () => {
    expect(resolveShares({ mode: "PositionFraction", pct: 100 }, { ...ctx, positionQty: -300 })).toBe(300);
  });
});
```

- [ ] **Step 2: Run to confirm failure**

Run: `cd ui && npx vitest run src/chrome/exec/priceSource.test.ts src/chrome/exec/sizing.test.ts`
Expected: FAIL — `resolvePrice` arity mismatch; `PositionFraction` returns full/half via `fraction`, not `pct`.

- [ ] **Step 3: Update `priceSource.ts`**

Replace the file body with:

```ts
import type { Quote } from "../../wire/contract";

export type PriceSource = "Bid" | "Ask" | "Last" | "Mid";
export type PriceOffsetUnit = "$" | "%";

// base = the chosen quote leg; the template's signed offset is added on top.
// unit "$" (or absent) adds an absolute amount; "%" adds base * offset / 100 —
// so the offset scales with price (the marketable-limit lesson from the venue
// latency benchmarks). (ui-design §Order entry.)
export function resolvePrice(source: PriceSource, offset: number, unit: PriceOffsetUnit | undefined, quote: Quote): number {
  const base =
    source === "Bid" ? quote.bid :
    source === "Ask" ? quote.ask :
    source === "Last" ? quote.last :
    (quote.bid + quote.ask) / 2;
  return unit === "%" ? base + (base * offset) / 100 : base + offset;
}
```

- [ ] **Step 4: Update `sizing.ts` `PositionFraction` branch**

Replace the `PositionFraction` case in `resolveShares` (keep the `fraction` field on the type as legacy input-only):

```ts
    case "PositionFraction": {
      const held = Math.abs(ctx.positionQty);
      return Math.max(0, Math.floor((held * (spec.pct ?? 0)) / 100));
    }
```

- [ ] **Step 5: Run those two test files to confirm they pass**

Run: `cd ui && npx vitest run src/chrome/exec/priceSource.test.ts src/chrome/exec/sizing.test.ts`
Expected: PASS.

- [ ] **Step 6: Write the failing `normalizeOrderConfig` test**

`ui/src/chrome/exec/actionTemplate.test.ts` already exists with 3 tests (unique ids, sizing/price recipe, `DEFAULT_ORDER_CONFIG` shape) — **append** this `describe` block, keep the existing ones (add the `normalizeOrderConfig` import to the existing import line):

```ts
import { describe, it, expect } from "vitest";
import { normalizeOrderConfig, type OrderConfig } from "./actionTemplate";

describe("normalizeOrderConfig", () => {
  it("migrates fraction all/half to pct 100/50 and defaults offset unit", () => {
    const raw: OrderConfig = {
      activeVenue: "",
      templates: [
        { kind: "place", id: "a", label: "A", side: "SELL", type: "LIMIT", tif: "DAY", priceSource: "Bid", priceOffset: 0, sizing: { mode: "PositionFraction", fraction: "all" } },
        { kind: "place", id: "b", label: "B", side: "SELL", type: "LIMIT", tif: "DAY", priceSource: "Bid", priceOffset: 0, sizing: { mode: "PositionFraction", fraction: "half" } },
      ] as OrderConfig["templates"],
    };
    const out = normalizeOrderConfig(raw);
    const a = out.templates[0];
    const b = out.templates[1];
    expect(a.kind === "place" && a.priceOffsetUnit).toBe("$");
    expect(a.kind === "place" && a.sizing.pct).toBe(100);
    expect(b.kind === "place" && b.sizing.pct).toBe(50);
  });
  it("is idempotent", () => {
    const raw: OrderConfig = {
      activeVenue: "v",
      templates: [{ kind: "place", id: "a", label: "A", side: "BUY", type: "LIMIT", tif: "DAY", priceSource: "Ask", priceOffset: 0.1, priceOffsetUnit: "%", sizing: { mode: "PositionFraction", pct: 50 } }] as OrderConfig["templates"],
    };
    expect(normalizeOrderConfig(normalizeOrderConfig(raw))).toEqual(normalizeOrderConfig(raw));
  });
  it("passes manage templates through untouched", () => {
    const raw: OrderConfig = { activeVenue: "", templates: [{ kind: "manage", id: "k", label: "KILL", action: "KillSwitch", hotkey: "Ctrl+Shift+K" }] as OrderConfig["templates"] };
    expect(normalizeOrderConfig(raw).templates[0]).toEqual(raw.templates[0]);
  });
});
```

- [ ] **Step 7: Run to confirm failure**

Run: `cd ui && npx vitest run src/chrome/exec/actionTemplate.test.ts`
Expected: FAIL — `normalizeOrderConfig` is not exported.

- [ ] **Step 8: Update `actionTemplate.ts`**

Add the import and field, update the two `PositionFraction` defaults, and add `normalizeOrderConfig`:

```ts
import type { PriceSource, PriceOffsetUnit } from "./priceSource";
```

In `PlaceOrderTemplate`, add after `priceSource: PriceSource; priceOffset: number;`:

```ts
  priceOffsetUnit?: PriceOffsetUnit;   // absent => "$" (every persisted config is already valid)
```

Update the two `PositionFraction` entries in `DEFAULT_TEMPLATES`:

```ts
  { kind: "place", id: "sell-half", label: "Sell ½", side: "SELL", type: "LIMIT", tif: "DAY", priceSource: "Bid", priceOffset: 0, sizing: { mode: "PositionFraction", pct: 50 }, hotkey: "Ctrl+3" },
  { kind: "place", id: "flatten", label: "Flatten", side: "SELL", type: "LIMIT", tif: "DAY", priceSource: "Bid", priceOffset: 0, sizing: { mode: "PositionFraction", pct: 100 }, hotkey: "Ctrl+4" },
```

Append the normalizer:

```ts
// normalizeOrderConfig is the single migration point applied where a config
// enters the app (OrderConfigProvider on load, and to DEFAULT_ORDER_CONFIG).
// It converts legacy PositionFraction `fraction` to `pct` and defaults a
// missing price-offset unit to "$". Idempotent; manage templates pass through.
function normalizeTemplate(t: ActionTemplate): ActionTemplate {
  if (t.kind !== "place") return t;
  let sizing = t.sizing;
  if (sizing.mode === "PositionFraction" && sizing.pct === undefined) {
    sizing = { ...sizing, pct: sizing.fraction === "half" ? 50 : 100 };
  }
  return { ...t, priceOffsetUnit: t.priceOffsetUnit ?? "$", sizing };
}

export function normalizeOrderConfig(config: OrderConfig): OrderConfig {
  return { ...config, templates: config.templates.map(normalizeTemplate) };
}
```

- [ ] **Step 9: Wire the normalizer + offset unit into the resolver and provider**

In `resolveTemplate.ts:18`, pass the unit:

```ts
  const price = resolvePrice(t.priceSource, t.priceOffset, t.priceOffsetUnit, ctx.quote);
```

In `useOrderConfig.tsx`, import the normalizer and apply it at both entry points:

```ts
import { DEFAULT_ORDER_CONFIG, ORDER_CONFIG_KEY, normalizeOrderConfig, type OrderConfig } from "./actionTemplate";
```

Line 17 seed:

```ts
  const [config, setConfig] = useState<OrderConfig>(() => normalizeOrderConfig(DEFAULT_ORDER_CONFIG));
```

Line 24 load:

```ts
      if (ack.status === "accepted" && ack.value && typeof ack.value === "object") setConfig(normalizeOrderConfig(ack.value as OrderConfig));
```

- [ ] **Step 9b: Update the two existing tests the resolver/seed change breaks**

- `ui/src/chrome/exec/resolveTemplate.test.ts:24` — the `"PositionFraction=all resolves from the live position (flatten)"` test builds `sizing: { mode: "PositionFraction", fraction: "all" }` and expects `r.args.qty` `300`. Change that sizing to `{ mode: "PositionFraction", pct: 100 }` (qty stays 300).
- `ui/src/chrome/exec/useOrderConfig.test.tsx` — the `"falls back to defaults when the store has no value"` test asserts `expect(result.current.config).toEqual(DEFAULT_ORDER_CONFIG)`. Because the seed is now `normalizeOrderConfig(DEFAULT_ORDER_CONFIG)` (place templates gain an explicit `priceOffsetUnit: "$"`), change the expectation to `toEqual(normalizeOrderConfig(DEFAULT_ORDER_CONFIG))` (import `normalizeOrderConfig`).

- [ ] **Step 10: Run the exec suite + typecheck**

Run: `cd ui && npx vitest run src/chrome/exec && npm run typecheck`
Expected: PASS. (The two tests updated in Step 9b now pass; `useHotkeys` and preset tests are unaffected because already-normalized configs resolve identically.)

- [ ] **Step 11: Commit**

```bash
git add ui/src/chrome/exec/priceSource.ts ui/src/chrome/exec/sizing.ts ui/src/chrome/exec/actionTemplate.ts ui/src/chrome/exec/resolveTemplate.ts ui/src/chrome/exec/useOrderConfig.tsx ui/src/chrome/exec/*.test.ts ui/src/chrome/exec/useOrderConfig.test.tsx
git commit -m "feat(exec): price offset unit and position-percent sizing with one migration point"
```

---

## Task 6: Order ticket & flatten honor position-percent

**Files:**
- Modify: `ui/src/chrome/panels/OrderTicketPanel.tsx:56-59` (the `submitManual` sizing ternary — match on the code text, not the line number)
- Modify: `ui/src/chrome/panels/AccountPanel.tsx:145` (the flatten template `sizing`)
- Test: `ui/src/chrome/panels/OrderTicketPanel.test.tsx` (exists — extend)

**Interfaces:**
- Consumes: `resolveShares` (now pct-based, Task 5).

- [ ] **Step 1: Write the failing test**

Add to `ui/src/chrome/panels/OrderTicketPanel.test.tsx` a test that selects `Pos` mode, types `50` into the amount input, submits, and asserts the submitted qty is 50% of the held position. Mirror the file's existing harness exactly: `mkProps()` builds `commands.sendCommand` as a `vi.fn()` that pushes `{ name, args }` into a `sent` array; a position of 200 shares is seeded via the exec store the harness already uses. After clicking submit, `await waitFor(() => sent.some((s) => s.name === "SubmitOrder"))` and read the captured args:

```ts
// with positionQty = 200 and mode "Pos" (data-testid="mode" set to "PositionFraction"), amount "50":
// submitted qty should be 100 (50% of 200), NOT 200 (the old hardcoded "all").
const submitted = sent.find((s) => s.name === "SubmitOrder");
expect(submitted.args.qty).toBe(100);
```

- [ ] **Step 2: Run to confirm failure**

Run: `cd ui && npx vitest run src/chrome/panels/OrderTicketPanel.test.tsx`
Expected: FAIL — qty is 200 (Pos mode ignores the amount, hardcodes `fraction: "all"`).

- [ ] **Step 3: Fix `submitManual`**

In `OrderTicketPanel.tsx`, replace the final branch of the sizing ternary (lines 56-59; the `: { mode, fraction: "all" as const };` line) so `Pos` reads the amount as a position percent:

```ts
    const spec = mode === "Shares" ? { mode, shares: Number(amount) || 0 }
      : mode === "Dollar" ? { mode, dollar: Number(amount) || 0 }
      : mode === "BuyingPowerPct" ? { mode, pct: Number(amount) || 0 }
      : { mode, pct: Number(amount) || 0 };
```

(The `Pos` branch now builds `{ mode: "PositionFraction", pct }`; 100 = flatten, consistent with the grid.)

- [ ] **Step 4: Fix `AccountPanel` flatten**

In `AccountPanel.tsx:145`, change the flatten template's sizing so it still means "close 100%":

```ts
      sizing: { mode: "PositionFraction", pct: 100 },
```

- [ ] **Step 5: Run panel tests + typecheck**

Run: `cd ui && npx vitest run src/chrome/panels && npm run typecheck`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add ui/src/chrome/panels/OrderTicketPanel.tsx ui/src/chrome/panels/AccountPanel.tsx ui/src/chrome/panels/OrderTicketPanel.test.tsx
git commit -m "fix(exec): Pos sizing honors the amount input; flatten uses pct:100"
```

---

## Task 7: Keycap primitive + Orders grid rewrite

**Files:**
- Create: `ui/src/chrome/exec/Keycap.tsx`
- Create: `ui/src/chrome/exec/Keycap.test.tsx`
- Rename: `ui/src/chrome/exec/OrderSettingsModal.tsx` → `ui/src/chrome/exec/OrderSettingsSection.tsx`
- Rename: `ui/src/chrome/exec/OrderSettingsModal.test.tsx` → `ui/src/chrome/exec/OrderSettingsSection.test.tsx`
- Modify: `ui/src/chrome/SettingsModal.tsx` (import path; drop `status` from the `OrderSettingsSection` call; drop `status` prop)
- Modify: `ui/src/chrome/AppShell.tsx:345` (drop `status={execStatus}` from `<SettingsModal>`)

**Interfaces:**
- Produces: `Keycap({ combo, danger? })`; the rewritten `OrderSettingsSection({ config, onSave })` (no `status` prop) with an editable CSS-grid; helpers `sizingValue`, `setSizingValue`, `modeToSpec` (module-local).
- Consumes: `PriceOffsetUnit` (Task 5), `ManagementAction`, `normalizeCombo`.

- [ ] **Step 1: Write the Keycap test**

Create `ui/src/chrome/exec/Keycap.test.tsx`:

```tsx
import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { Keycap } from "./Keycap";

describe("Keycap", () => {
  it("splits a combo into one kbd per key with symbol labels", () => {
    render(<ThemeProvider><Keycap combo="Ctrl+Shift+Backspace" /></ThemeProvider>);
    const kbds = screen.getAllByRole("group")[0].querySelectorAll("kbd");
    expect(kbds.length).toBe(3);
    expect(kbds[1].textContent).toBe("⇧");
    expect(kbds[2].textContent).toBe("⌫");
  });
});
```

- [ ] **Step 2: Run to confirm failure**

Run: `cd ui && npx vitest run src/chrome/exec/Keycap.test.tsx`
Expected: FAIL — `Cannot find module './Keycap'`.

- [ ] **Step 3: Implement `Keycap.tsx`**

```tsx
// The keycap chip: a normalized combo ("Ctrl+Shift+K") rendered as physical
// key caps. The one new visual primitive of the settings redesign — derived
// entirely from existing palette tokens (surface fill, borderStrong border,
// mono face, a 1px thicker bottom border for the key-cap read).
import { useTheme } from "../ThemeProvider";

const KEY_LABEL: Record<string, string> = {
  Ctrl: "Ctrl", Alt: "⌥", Shift: "⇧", Meta: "⌘", Backspace: "⌫", Enter: "⏎", Escape: "Esc",
};

export function Keycap({ combo, danger }: { combo: string; danger?: boolean }): JSX.Element {
  const { palette } = useTheme();
  const keys = combo.split("+").filter(Boolean);
  const color = danger ? palette.danger : palette.text;
  const border = danger ? palette.danger : palette.borderStrong;
  return (
    <span role="group" style={{ display: "inline-flex", gap: 3, alignItems: "center" }}>
      {keys.map((k, i) => (
        <kbd key={i} className="mono" style={{
          fontSize: 11, lineHeight: "14px", padding: "1px 5px", background: palette.surface,
          color, border: `1px solid ${border}`, borderBottomWidth: 2, borderRadius: 3,
        }}>{KEY_LABEL[k] ?? k}</kbd>
      ))}
    </span>
  );
}
```

- [ ] **Step 4: Run to confirm it passes**

Run: `cd ui && npx vitest run src/chrome/exec/Keycap.test.tsx`
Expected: PASS.

- [ ] **Step 5: Rename the section files with git**

```bash
cd /Users/earl.savadera/Projects/eTape
git mv ui/src/chrome/exec/OrderSettingsModal.tsx ui/src/chrome/exec/OrderSettingsSection.tsx
git mv ui/src/chrome/exec/OrderSettingsModal.test.tsx ui/src/chrome/exec/OrderSettingsSection.test.tsx
```

Update the test file's import to `from "./OrderSettingsSection"` and drop any `status` prop it passes when rendering (render as `<OrderSettingsSection config={...} onSave={...} />`). Remove the gate-cap read-only assertion test case (that block moves to Venues in Task 10).

- [ ] **Step 6: Rewrite `OrderSettingsSection.tsx`**

Replace the whole file with the editable grid (drops the `status` prop and the read-only gate block; keeps the hotkey-capture verbatim; preserves `tmpl-label-*`, `tmpl-hotkey-*`, `add-template`, `save`; adds `add-place`, `add-manage`, and `tmpl-unbind-*`):

```tsx
// Task 11 history: this was OrderSettingsModal (a standalone overlay). Since the
// settings unification it is a plain section body embedded in SettingsModal's
// "Orders & hotkeys" tab. This revision makes every template parameter editable
// — including price offset (value + $/%) and sizing amount, which had no input
// before — and lets management templates be created.
import { useState } from "react";
import { useTheme } from "../ThemeProvider";
import type { Side, OrderType, TIF } from "../../wire/contract";
import type { PriceSource, PriceOffsetUnit } from "./priceSource";
import type { SizingSpec, SizingMode } from "./sizing";
import {
  DEFAULT_TEMPLATES, normalizeOrderConfig, type ActionTemplate, type ManagementAction,
  type OrderConfig, type PlaceOrderTemplate,
} from "./actionTemplate";
import { normalizeCombo } from "./hotkeys";
import { Keycap } from "./Keycap";

const SIDES: Side[] = ["BUY", "SELL", "SHORT", "COVER"];
const TYPES: OrderType[] = ["LIMIT", "MARKET", "STOP", "STOP_LIMIT"];
const TIFS: TIF[] = ["DAY", "GTC", "IOC", "FOK"];
const SOURCES: PriceSource[] = ["Bid", "Ask", "Last", "Mid"];
const MODES: SizingMode[] = ["Dollar", "BuyingPowerPct", "Shares", "PositionFraction"];
const MODE_LABEL: Record<SizingMode, string> = { Dollar: "Dollar", BuyingPowerPct: "BP %", Shares: "Shares", PositionFraction: "Pos %" };
const MANAGE_ACTIONS: ManagementAction[] = ["CancelLast", "CancelAllFocused", "CancelAllEverything", "KillSwitch"];
const COLS = "110px 68px 78px 58px 62px 118px 150px 130px 26px";

function sizingValue(s: SizingSpec): string {
  switch (s.mode) {
    case "Dollar": return String(s.dollar ?? 0);
    case "Shares": return String(s.shares ?? 0);
    case "BuyingPowerPct":
    case "PositionFraction": return String(s.pct ?? 0);
  }
}
function setSizingValue(s: SizingSpec, v: string): SizingSpec {
  const n = Number(v) || 0;
  switch (s.mode) {
    case "Dollar": return { mode: "Dollar", dollar: n };
    case "Shares": return { mode: "Shares", shares: n };
    case "BuyingPowerPct": return { mode: "BuyingPowerPct", pct: n };
    case "PositionFraction": return { mode: "PositionFraction", pct: n };
  }
}
function modeToSpec(mode: SizingMode): SizingSpec {
  switch (mode) {
    case "Dollar": return { mode, dollar: 0 };
    case "Shares": return { mode, shares: 100 };
    case "BuyingPowerPct": return { mode, pct: 25 };
    case "PositionFraction": return { mode, pct: 100 };
  }
}

export function OrderSettingsSection({ config, onSave }: { config: OrderConfig; onSave: (next: OrderConfig) => void }): JSX.Element {
  const { palette } = useTheme();
  const [templates, setTemplates] = useState<ActionTemplate[]>(() => config.templates.map((t) => ({ ...t })));
  const [addOpen, setAddOpen] = useState(false);
  const [confirmReset, setConfirmReset] = useState(false);

  const patch = (id: string, over: Partial<ActionTemplate>) =>
    setTemplates((ts) => ts.map((t) => (t.id === id ? ({ ...t, ...over } as ActionTemplate) : t)));
  const removeTemplate = (id: string) => setTemplates((ts) => ts.filter((t) => t.id !== id));
  const uid = (p: string) => `${p}-${templates.length + 1}-${Math.max(0, ...templates.map((_, i) => i)) + 1}`;
  const addPlace = () => setTemplates((ts) => [...ts, { kind: "place", id: uid("tmpl"), label: "New", side: "BUY", type: "LIMIT", tif: "DAY", priceSource: "Ask", priceOffset: 0, priceOffsetUnit: "$", sizing: { mode: "Shares", shares: 100 } } as PlaceOrderTemplate]);
  const addManage = () => setTemplates((ts) => [...ts, { kind: "manage", id: uid("mng"), label: "New action", action: "CancelLast" }]);
  const doReset = () => { setTemplates(normalizeOrderConfig({ ...config, templates: DEFAULT_TEMPLATES.map((t) => ({ ...t })) }).templates); setConfirmReset(false); };

  const inp = { background: palette.bg, color: palette.text, border: `1px solid ${palette.border}`, fontSize: 12, padding: "1px 4px", width: "100%", boxSizing: "border-box" } as const;
  const cell = { display: "grid", gridTemplateColumns: COLS, gap: 4, alignItems: "center", padding: "3px 0", borderTop: `1px solid ${palette.border}` } as const;
  const head = { ...cell, color: palette.textMuted, fontSize: 10, letterSpacing: 0.4 };

  const places = templates.filter((t): t is PlaceOrderTemplate => t.kind === "place");

  return (
    <div style={{ color: palette.text }}>
      <div style={head}>
        <span>LABEL</span><span>SIDE</span><span>TYPE</span><span>TIF</span><span>PRICE</span><span>OFFSET</span><span>SIZE</span><span>KEY</span><span />
      </div>

      {templates.map((t) => (
        <div key={t.id} style={cell}>
          <input data-testid={`tmpl-label-${t.id}`} value={t.label} onChange={(e) => patch(t.id, { label: e.target.value })} style={inp} />
          {t.kind === "place" ? (
            <>
              <select value={t.side} onChange={(e) => patch(t.id, { side: e.target.value as Side })} style={inp}>{SIDES.map((s) => <option key={s}>{s}</option>)}</select>
              <select value={t.type} onChange={(e) => patch(t.id, { type: e.target.value as OrderType })} style={inp}>{TYPES.map((x) => <option key={x}>{x}</option>)}</select>
              <select value={t.tif} onChange={(e) => patch(t.id, { tif: e.target.value as TIF })} style={inp}>{TIFS.map((x) => <option key={x}>{x}</option>)}</select>
              <select value={t.priceSource} onChange={(e) => patch(t.id, { priceSource: e.target.value as PriceSource })} style={inp}>{SOURCES.map((x) => <option key={x}>{x}</option>)}</select>
              <span style={{ display: "flex", gap: 3 }}>
                <input aria-label={`offset-${t.id}`} value={String(t.priceOffset)} onChange={(e) => patch(t.id, { priceOffset: Number(e.target.value) || 0 })} style={{ ...inp, width: 62 }} />
                <select aria-label={`offset-unit-${t.id}`} value={t.priceOffsetUnit ?? "$"} onChange={(e) => patch(t.id, { priceOffsetUnit: e.target.value as PriceOffsetUnit })} style={{ ...inp, width: 46 }}>
                  <option value="$">$</option><option value="%">%</option>
                </select>
              </span>
              <span style={{ display: "flex", gap: 3 }}>
                <select aria-label={`size-mode-${t.id}`} value={t.sizing.mode} onChange={(e) => patch(t.id, { sizing: modeToSpec(e.target.value as SizingMode) })} style={{ ...inp, width: 84 }}>
                  {MODES.map((m) => <option key={m} value={m}>{MODE_LABEL[m]}</option>)}
                </select>
                <input aria-label={`size-value-${t.id}`} value={sizingValue(t.sizing)} onChange={(e) => patch(t.id, { sizing: setSizingValue(t.sizing, e.target.value) })} style={{ ...inp, width: 60 }} />
              </span>
            </>
          ) : (
            <select aria-label={`action-${t.id}`} value={t.action} onChange={(e) => patch(t.id, { action: e.target.value as ManagementAction })} style={{ ...inp, gridColumn: "2 / 8" }}>
              {MANAGE_ACTIONS.map((a) => <option key={a}>{a}</option>)}
            </select>
          )}
          <span style={{ display: "flex", gap: 3, alignItems: "center" }}>
            <input
              data-testid={`tmpl-hotkey-${t.id}`} readOnly value={t.hotkey ?? ""} placeholder="press keys"
              onKeyDown={(e) => {
                // Must stop propagation, not just preventDefault: the real hotkey
                // engine (useHotkeys, mounted globally in AppShell) listens on
                // `window` in the bubble phase. Without this, a candidate combo
                // typed here while capturing a binding can also be a *live* combo
                // (e.g. default Ctrl+Shift+K = KillSwitch, Ctrl+1..4 = place
                // templates) and fire the real action — this settings screen must
                // stay inert with zero order-safety authority.
                e.preventDefault();
                e.stopPropagation();
                const c = normalizeCombo(e);
                if (c) patch(t.id, { hotkey: c });
              }}
              style={{ ...inp, width: 96 }}
            />
            {t.hotkey ? <button data-testid={`tmpl-unbind-${t.id}`} title="unbind" onClick={() => patch(t.id, { hotkey: "" })} style={{ ...inp, width: 22, cursor: "pointer", color: palette.textMuted }}>×</button> : null}
          </span>
          <button title="remove" onClick={() => removeTemplate(t.id)} style={{ ...inp, width: 22, color: palette.danger, cursor: "pointer" }}>×</button>
        </div>
      ))}

      <div style={{ display: "flex", gap: 6, marginTop: 10, alignItems: "center", position: "relative" }}>
        <button data-testid="add-template" onClick={() => setAddOpen((v) => !v)} style={{ ...inp, width: "auto", cursor: "pointer" }}>+ Add ▾</button>
        {addOpen && (
          <>
            <button data-testid="add-place" onClick={() => { addPlace(); setAddOpen(false); }} style={{ ...inp, width: "auto", cursor: "pointer" }}>Order template</button>
            <button data-testid="add-manage" onClick={() => { addManage(); setAddOpen(false); }} style={{ ...inp, width: "auto", cursor: "pointer" }}>Management action</button>
          </>
        )}
        {confirmReset
          ? <button data-testid="reset-confirm" onClick={doReset} style={{ ...inp, width: "auto", color: palette.danger, cursor: "pointer" }}>Confirm reset</button>
          : <button data-testid="reset-defaults" onClick={() => setConfirmReset(true)} style={{ ...inp, width: "auto", cursor: "pointer" }}>Reset to defaults</button>}
      </div>

      <div style={{ display: "flex", justifyContent: "flex-end", gap: 6, marginTop: 12 }}>
        <button data-testid="save" onClick={() => onSave({ ...config, templates })} style={{ ...inp, width: "auto", background: palette.accent, color: palette.bg, fontWeight: 700, cursor: "pointer" }}>Save</button>
      </div>
    </div>
  );
}
```

- [ ] **Step 7: Drop `status` at the two call sites**

In `SettingsModal.tsx`: change the import to `import { OrderSettingsSection } from "./exec/OrderSettingsSection";`, change the render to `{section === "orders" && <OrderSettingsSection config={oc.config} onSave={oc.save} />}`, and remove `status` from the component's destructured props and from its prop type. **Also delete `import type { ExecStatus } from "../wire/contract";`** — it was the only use of `ExecStatus` in the file, and `tsconfig` has `noUnusedLocals: true`, so leaving it fails typecheck (`TS6133`). In `AppShell.tsx:345`, delete the `status={execStatus}` line from `<SettingsModal ...>` (`execStatus` stays used elsewhere in AppShell, so no orphan).

- [ ] **Step 8: Update the section test for the new fields**

In `OrderSettingsSection.test.tsx`, keep the preserved-testid tests (`tmpl-label-buy-5k` edit + save, hotkey capture + kill-leak regression, add/remove count) and add: editing `offset-buy-5k` + `offset-unit-buy-5k`, editing `size-mode-*` to `PositionFraction` then `size-value-*` to `50`, and asserting the saved config round-trips those values; `add-place`/`add-manage` create the right kind; `tmpl-unbind-*` clears the hotkey; `reset-defaults`→`reset-confirm` restores `DEFAULT_TEMPLATES`.

- [ ] **Step 9: Run the exec + chrome suites + typecheck**

Run: `cd ui && npx vitest run src/chrome/exec/OrderSettingsSection.test.tsx src/chrome/exec/Keycap.test.tsx src/chrome/SettingsModal.test.tsx && npm run typecheck`
Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add ui/src/chrome/exec/Keycap.tsx ui/src/chrome/exec/Keycap.test.tsx ui/src/chrome/exec/OrderSettingsSection.tsx ui/src/chrome/exec/OrderSettingsSection.test.tsx ui/src/chrome/SettingsModal.tsx ui/src/chrome/AppShell.tsx
git commit -m "feat(settings): editable order-template grid with offset/size inputs and manage rows"
```

---

## Task 8: Cheat-sheet strip + conflict detection

**Files:**
- Modify: `ui/src/chrome/exec/OrderSettingsSection.tsx`
- Modify: `ui/src/chrome/exec/OrderSettingsSection.test.tsx`

**Interfaces:**
- Consumes: `Keycap` (Task 7), the `templates` draft state.

- [ ] **Step 1: Write the failing tests**

Add to `OrderSettingsSection.test.tsx`:
- Binding two templates to the same combo (capture `Ctrl+1` on a second row via keyDown) marks both `tmpl-hotkey-*` cells and disables `save` (assert the `save` button has `disabled`).
- Unbinding one (`tmpl-unbind-*`) re-enables `save`.
- The cheat-sheet strip (`data-testid="cheat-sheet"`) renders a bound template's label and reflects a label edit live.

```tsx
// duplicate binding disables save (plain DOM property — jest-dom is not installed)
const save = () => screen.getByTestId("save") as HTMLButtonElement;
fireEvent.keyDown(screen.getByTestId("tmpl-hotkey-buy-25pct"), { key: "1", ctrlKey: true });
expect(save().disabled).toBe(true);
fireEvent.click(screen.getByTestId("tmpl-unbind-buy-25pct"));
expect(save().disabled).toBe(false);
```

- [ ] **Step 2: Run to confirm failure**

Run: `cd ui && npx vitest run src/chrome/exec/OrderSettingsSection.test.tsx`
Expected: FAIL — no `cheat-sheet`, `save` not disabled on conflict.

- [ ] **Step 3: Add conflict computation + cheat-sheet + Save gate**

In `OrderSettingsSection.tsx`, after `const places = ...`, add:

```tsx
  const combos = templates.map((t) => t.hotkey ?? "").filter((c) => c !== "");
  const dupes = new Set(combos.filter((c, i) => combos.indexOf(c) !== i));
  const isDup = (t: ActionTemplate) => !!t.hotkey && dupes.has(t.hotkey);
  const hasConflict = dupes.size > 0;
  const manages = templates.filter((t) => t.kind === "manage");
```

Render the cheat-sheet strip just inside the top `<div>`, before the header row:

```tsx
      <div data-testid="cheat-sheet" style={{ border: `1px solid ${palette.border}`, borderRadius: 4, padding: "6px 8px", marginBottom: 10 }}>
        <div style={{ color: palette.textMuted, fontSize: 10, letterSpacing: 0.4, marginBottom: 4 }}>CHEAT SHEET</div>
        {[{ label: "Place", rows: places }, { label: "Manage", rows: manages }].map((grp) => (
          <div key={grp.label} style={{ display: "flex", flexWrap: "wrap", gap: 12, alignItems: "center", marginBottom: 2 }}>
            <span style={{ width: 52, color: palette.textMuted }}>{grp.label}</span>
            {grp.rows.filter((t) => t.hotkey).map((t) => (
              <span key={t.id} style={{ display: "inline-flex", gap: 5, alignItems: "center" }}>
                <Keycap combo={t.hotkey as string} danger={isDup(t) || (t.kind === "manage" && t.action === "KillSwitch")} />
                <span style={{ color: isDup(t) ? palette.danger : palette.text }}>{t.label}</span>
              </span>
            ))}
          </div>
        ))}
      </div>
```

Mark the KEY cell danger on conflict — wrap the capture input's `border` so a dup row reads red. Change the hotkey `<input>` style to:

```tsx
              style={{ ...inp, width: 96, borderColor: isDup(t) ? palette.danger : palette.border }}
```

and after the key `<span>` add a note when this row is a dup (optional inline):

```tsx
            {isDup(t) ? <span style={{ color: palette.danger, fontSize: 10 }}>dup</span> : null}
```

Disable Save while conflicted:

```tsx
        <button data-testid="save" disabled={hasConflict} onClick={() => onSave({ ...config, templates })} style={{ ...inp, width: "auto", background: hasConflict ? palette.border : palette.accent, color: palette.bg, fontWeight: 700, cursor: hasConflict ? "not-allowed" : "pointer" }}>Save</button>
```

- [ ] **Step 4: Run the section tests + typecheck**

Run: `cd ui && npx vitest run src/chrome/exec/OrderSettingsSection.test.tsx && npm run typecheck`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add ui/src/chrome/exec/OrderSettingsSection.tsx ui/src/chrome/exec/OrderSettingsSection.test.tsx
git commit -m "feat(settings): cheat-sheet strip and duplicate-hotkey conflict detection"
```

---

## Task 9: Sounds section — per-event enable toggles

**Files:**
- Modify: `ui/src/sound/SoundsSection.tsx`
- Test: `ui/src/sound/SoundsSection.test.tsx` (create if absent)

**Interfaces:**
- Consumes: existing `SoundConfig`, `DEFAULT_SOUND_CONFIG`, `sanitizeSoundConfig`, `soundEngine.preview`, `useSoundConfig` (whatever the section uses today to read/save; keep the same `save` path). **No `SoundEngine` or `sanitizeSoundConfig` changes** — unchecked ⇔ persisted `"off"`.

- [ ] **Step 1: Write the failing tests**

`SoundsSection` takes **no props** — it reads `{ config, save }` from `useSoundConfig()`, which `SoundConfigProvider` builds from a `commands` spy. Mirror the existing `SoundsSection.test.tsx` harness: render inside `<ThemeProvider><SoundConfigProvider commands={spy}>…` and assert against `spy.sendCommand` calls with `("SetConfig", { key: "soundConfig", value: expect.objectContaining({...}) })`. Use `.disabled` DOM property (no jest-dom). Cases:
- Unchecking `sound-scanner-on` fires `SetConfig` with `value` containing `scannerSound: "off"`; the Scanner `<select>` (`sound-scanner`) and `sound-preview-scanner` have `.disabled === true`.
- Re-checking `sound-scanner-on` fires `SetConfig` with `scannerSound` set to the previously selected sound (e.g. `arpeggio`), not `"off"`.
- Fill/Reject behave the same via `sound-fill-on`/`sound-reject-on`.
- The `"off"` `<option>` is absent from all three dropdowns (query options, assert none has value `"off"`).

- [ ] **Step 2: Run to confirm failure**

Run: `cd ui && npx vitest run src/sound/SoundsSection.test.tsx`
Expected: FAIL — no `sound-scanner-on` etc.

- [ ] **Step 3: Rewrite `SoundsSection.tsx` rows**

Convert each sound-picker row (Fill, Reject, Scanner) to an enable checkbox + dropdown + preview, keeping the `placeClick` row and volume slider. Track the last non-off selection so re-checking restores it. Add the new imports the rewrite needs: `import { useState } from "react";` and the types `FillSoundId`, `RejectSoundId`, `ScannerSoundId` from `./SoundConfig` (alongside the existing value imports `DEFAULT_SOUND_CONFIG`, `FILL_SOUND_IDS/LABELS`, etc.). Sketch (apply to all three events — Fill shown; Reject/Scanner identical with their ids/defaults):

```tsx
// remembered picks for re-checking after an unchecked (off) state
const [lastPick, setLastPick] = useState<{ fill: FillSoundId; reject: RejectSoundId; scanner: ScannerSoundId }>({
  fill: config.fillSound === "off" ? DEFAULT_SOUND_CONFIG.fillSound : config.fillSound,
  reject: config.rejectSound === "off" ? DEFAULT_SOUND_CONFIG.rejectSound : config.rejectSound,
  scanner: config.scannerSound === "off" ? DEFAULT_SOUND_CONFIG.scannerSound : config.scannerSound,
});
const fillOn = config.fillSound !== "off";
// row:
<div style={row}>
  <input data-testid="sound-fill-on" type="checkbox" checked={fillOn}
    onChange={(e) => save({ ...config, fillSound: e.target.checked ? lastPick.fill : "off" })} />
  <span style={{ width: 90 }}>Fill</span>
  <select data-testid="sound-fill" disabled={!fillOn} value={fillOn ? config.fillSound : lastPick.fill} style={inp}
    onChange={(e) => { const v = e.target.value as FillSoundId; setLastPick((p) => ({ ...p, fill: v })); save({ ...config, fillSound: v }); }}>
    {FILL_SOUND_IDS.map((id) => <option key={id} value={id}>{FILL_SOUND_LABELS[id]}</option>)}
  </select>
  <button data-testid="sound-preview-fill" disabled={!fillOn} style={{ ...inp, cursor: fillOn ? "pointer" : "not-allowed" }}
    onClick={() => soundEngine.preview("fill", fillOn ? config.fillSound : lastPick.fill)}>▶</button>
</div>
```

Keep the master `sound-enabled` checkbox, the `placeClick` (`sound-place`) row, and the `sound-volume` slider exactly as they are. Remove every `<option value="off">off</option>` from the three dropdowns.

- [ ] **Step 4: Run the sounds tests + typecheck**

Run: `cd ui && npx vitest run src/sound && npm run typecheck`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add ui/src/sound/SoundsSection.tsx ui/src/sound/SoundsSection.test.tsx
git commit -m "feat(sounds): per-event enable toggles (unchecked = persisted off)"
```

---

## Task 10: VenuesSection component

**Files:**
- Create: `ui/src/chrome/exec/VenuesSection.tsx`
- Create: `ui/src/chrome/exec/VenuesSection.test.tsx`

**Interfaces:**
- Consumes wire types from `../../wire/contract`: `Venue`, `Gate`, `VenueConfig`, `VenueSetup`, `AckMsg` (all now generated, Task 4).
- Produces: `VenuesSection({ commands })` where `commands: { sendCommand(name: string, args: unknown): Promise<AckMsg> }`.

- [ ] **Step 1: Write the failing tests**

Create `ui/src/chrome/exec/VenuesSection.test.tsx`. Provide a spy `commands.sendCommand` that answers `GetVenueSetup` with a fixture (`file` = `running` initially, one paper + one live venue, `credKeys: ["alpaca","tradeZero"]`). Assert:
- A `live` venue row shows a `LIVE` badge and its expanded auto-arm toggle is `disabled`.
- The restart banner (`data-testid="restart-banner"`) is absent when file == running; after a `SetVenueSetup` save the section re-fetches and, given a fixture where the second fetch's `file` differs from `running`, the banner appears.
- Delete on a referenced credential is disabled (`credential-delete-tradeZero` has `disabled`); an unreferenced one is enabled.
- After a credential save the masked inputs (`cred-keyid`, `cred-secret`) are cleared and never rendered with prior values.
- A `SetVenueSetup` ack of `{status:"blocked", reason:"venue \"x\": ..."}` renders the reason inline (`data-testid="venues-error"`).

- [ ] **Step 2: Run to confirm failure**

Run: `cd ui && npx vitest run src/chrome/exec/VenuesSection.test.tsx`
Expected: FAIL — `Cannot find module './VenuesSection'`.

- [ ] **Step 3: Implement `VenuesSection.tsx`**

```tsx
// Venues & credentials editor. Edits are FILE-ONLY: SetVenueSetup rewrites
// config.toml and credential ops rewrite credentials.json; nothing here arms a
// venue or changes the running gate — changes apply at the next engine restart
// (hence the restart banner). Secrets are write-only: keyId/secretKey are typed
// here, sent once on save, and never read back from the engine.
import { useCallback, useEffect, useMemo, useState } from "react";
import { useTheme } from "../ThemeProvider";
import { useToasts } from "../Toast";
import type { AckMsg, Venue, Gate, VenueConfig, VenueSetup } from "../../wire/contract";

interface Commands { sendCommand(name: string, args: unknown): Promise<AckMsg>; }
const BROKERS = ["tradezero", "alpaca", "moomoo", "sim"];
const ENVS = ["paper", "live"];
const emptyGate = (): Gate => ({ global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 }, venue: {} });

export function VenuesSection({ commands }: { commands: Commands }): JSX.Element {
  const { palette } = useTheme();
  const toast = useToasts();
  const [setup, setSetup] = useState<VenueSetup | null>(null);
  const [draft, setDraft] = useState<VenueConfig>({ venues: [], gate: emptyGate() });
  const [err, setErr] = useState("");
  const [credName, setCredName] = useState("");
  const [credKeyId, setCredKeyId] = useState("");
  const [credSecret, setCredSecret] = useState("");

  const refresh = useCallback(() => {
    void commands.sendCommand("GetVenueSetup", {}).then((ack) => {
      if (ack.status === "accepted" && ack.value) {
        const s = ack.value as VenueSetup;
        setSetup(s);
        setDraft({ venues: s.file.venues.map((v) => ({ ...v })), gate: { global: { ...s.file.gate.global }, venue: { ...s.file.gate.venue } } });
      }
    }).catch(() => toast.push({ level: "danger", text: "Could not load venue setup." }));
  }, [commands, toast]);
  useEffect(refresh, [refresh]);

  const restartNeeded = useMemo(
    () => setup !== null && JSON.stringify(setup.file) !== JSON.stringify(setup.running),
    [setup],
  );
  const usedBy = (key: string) => draft.venues.filter((v) => v.credentials === key).map((v) => v.id);

  const patchVenue = (i: number, over: Partial<Venue>) =>
    setDraft((d) => ({ ...d, venues: d.venues.map((v, j) => (j === i ? { ...v, ...over } : v)) }));
  const addVenue = () => setDraft((d) => ({ ...d, venues: [...d.venues, { id: "", broker: "sim", env: "paper", credentials: "", accountId: "", autoArm: false }] }));
  const removeVenue = (i: number) => setDraft((d) => ({ ...d, venues: d.venues.filter((_, j) => j !== i) }));

  const saveVenues = () => {
    setErr("");
    void commands.sendCommand("SetVenueSetup", { venues: draft.venues, gate: draft.gate }).then((ack) => {
      if (ack.status !== "accepted") { setErr(ack.reason || "rejected"); return; }
      refresh();
    }).catch(() => toast.push({ level: "danger", text: "Save failed (transport)." }));
  };
  const putCred = () => {
    void commands.sendCommand("PutCredential", { name: credName, keyId: credKeyId, secretKey: credSecret }).then((ack) => {
      if (ack.status !== "accepted") { setErr(ack.reason || "rejected"); return; }
      setCredName(""); setCredKeyId(""); setCredSecret(""); // clear write-only inputs
      refresh();
    }).catch(() => toast.push({ level: "danger", text: "Credential save failed (transport)." }));
  };
  const delCred = (name: string) => {
    void commands.sendCommand("DeleteCredential", { name }).then((ack) => {
      if (ack.status !== "accepted") { setErr(ack.reason || "rejected"); return; }
      refresh();
    }).catch(() => toast.push({ level: "danger", text: "Credential delete failed (transport)." }));
  };

  const inp = { background: palette.bg, color: palette.text, border: `1px solid ${palette.border}`, fontSize: 12, padding: "1px 4px" } as const;
  const badge = (env: string) => ({ padding: "0 6px", borderRadius: 3, fontSize: 10, fontWeight: 700, color: env === "live" ? palette.danger : palette.textMuted, border: `1px solid ${env === "live" ? palette.danger : palette.border}` });

  return (
    <div style={{ color: palette.text }}>
      {restartNeeded && (
        <div data-testid="restart-banner" style={{ background: palette.bg, border: `1px solid ${palette.accent}`, color: palette.accent, padding: "6px 8px", borderRadius: 4, marginBottom: 10 }}>
          ⚠ Engine restart required — saved venue config differs from the running engine.
        </div>
      )}
      {err && <div data-testid="venues-error" style={{ color: palette.danger, marginBottom: 8 }}>{err}</div>}

      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
        <div style={{ fontWeight: 700 }}>Venues</div>
        <button data-testid="add-venue" onClick={addVenue} style={{ ...inp, cursor: "pointer" }}>+ Add venue</button>
      </div>

      {draft.venues.map((v, i) => (
        <div key={i} style={{ borderTop: `1px solid ${palette.border}`, padding: "4px 0" }}>
          <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
            <input data-testid={`venue-id-${i}`} value={v.id} onChange={(e) => patchVenue(i, { id: e.target.value })} placeholder="id" style={{ ...inp, width: 120 }} className="mono" />
            <select data-testid={`venue-broker-${i}`} value={v.broker} onChange={(e) => patchVenue(i, { broker: e.target.value })} style={inp}>{BROKERS.map((b) => <option key={b}>{b}</option>)}</select>
            <select data-testid={`venue-env-${i}`} value={v.env} onChange={(e) => patchVenue(i, { env: e.target.value, autoArm: e.target.value === "live" ? false : v.autoArm })} style={inp}>{ENVS.map((x) => <option key={x}>{x}</option>)}</select>
            <span style={badge(v.env)}>{v.env.toUpperCase()}</span>
            <select data-testid={`venue-cred-${i}`} value={v.credentials} onChange={(e) => patchVenue(i, { credentials: e.target.value })} style={inp}>
              <option value="">—</option>{(setup?.credKeys ?? []).map((k) => <option key={k}>{k}</option>)}
            </select>
            <input data-testid={`venue-account-${i}`} value={v.accountId} onChange={(e) => patchVenue(i, { accountId: e.target.value })} placeholder="account id" style={{ ...inp, width: 100 }} />
            <label style={{ display: "flex", gap: 4, alignItems: "center", opacity: v.env === "live" ? 0.5 : 1 }}>
              <input data-testid={`venue-autoarm-${i}`} type="checkbox" disabled={v.env === "live"} checked={v.autoArm} onChange={(e) => patchVenue(i, { autoArm: e.target.checked })} />
              auto-arm
            </label>
            <button data-testid={`venue-remove-${i}`} onClick={() => removeVenue(i)} style={{ ...inp, color: palette.danger, cursor: "pointer" }}>remove</button>
          </div>
          <div style={{ display: "flex", gap: 8, marginTop: 3 }}>
            {(["maxOrderValue", "maxPositionValue", "maxPositionShares", "maxOpenOrders"] as const).map((cap) => (
              <label key={cap} style={{ fontSize: 11, color: palette.textMuted }}>{cap}{" "}
                <input value={String(draft.gate.venue[v.id]?.[cap] ?? 0)} onChange={(e) => setDraft((d) => ({ ...d, gate: { ...d.gate, venue: { ...d.gate.venue, [v.id]: { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0, ...d.gate.venue[v.id], [cap]: Number(e.target.value) || 0 } } } }))} style={{ ...inp, width: 70 }} />
              </label>
            ))}
          </div>
        </div>
      ))}

      <div style={{ marginTop: 8, display: "flex", gap: 8 }}>
        <span style={{ fontWeight: 700 }}>Global limits</span>
        {(["maxDayLoss", "maxSymbolPositionValue", "maxSymbolPositionShares"] as const).map((k) => (
          <label key={k} style={{ fontSize: 11, color: palette.textMuted }}>{k}{" "}
            <input data-testid={`global-${k}`} value={String(draft.gate.global[k])} onChange={(e) => setDraft((d) => ({ ...d, gate: { ...d.gate, global: { ...d.gate.global, [k]: Number(e.target.value) || 0 } } }))} style={{ ...inp, width: 80 }} />
          </label>
        ))}
      </div>

      <div style={{ marginTop: 14, display: "flex", justifyContent: "space-between", alignItems: "center" }}>
        <div style={{ fontWeight: 700 }}>Credentials</div>
      </div>
      {(setup?.credKeys ?? []).map((k) => {
        const refs = usedBy(k);
        return (
          <div key={k} style={{ display: "flex", gap: 10, alignItems: "center", borderTop: `1px solid ${palette.border}`, padding: "3px 0" }}>
            <span className="mono" style={{ width: 140 }}>{k}</span>
            <span style={{ color: palette.textMuted, flex: 1 }}>used by: {refs.length ? refs.join(", ") : "—"}</span>
            <button data-testid={`credential-delete-${k}`} disabled={refs.length > 0} onClick={() => delCred(k)} style={{ ...inp, color: palette.danger, cursor: refs.length ? "not-allowed" : "pointer" }}>delete</button>
          </div>
        );
      })}
      <div style={{ display: "flex", gap: 6, marginTop: 6, alignItems: "center" }}>
        <input data-testid="cred-name" value={credName} onChange={(e) => setCredName(e.target.value)} placeholder="name" style={{ ...inp, width: 120 }} />
        <input data-testid="cred-keyid" value={credKeyId} onChange={(e) => setCredKeyId(e.target.value)} placeholder="key id" type="password" style={{ ...inp, width: 140 }} />
        <input data-testid="cred-secret" value={credSecret} onChange={(e) => setCredSecret(e.target.value)} placeholder="secret key" type="password" style={{ ...inp, width: 180 }} />
        <button data-testid="cred-save" onClick={putCred} style={{ ...inp, cursor: "pointer" }}>Add / replace key</button>
      </div>

      <div style={{ display: "flex", justifyContent: "flex-end", marginTop: 14 }}>
        <button data-testid="save-venues" onClick={saveVenues} style={{ ...inp, background: palette.accent, color: palette.bg, fontWeight: 700, cursor: "pointer" }}>Save venues & limits</button>
      </div>
    </div>
  );
}
```

> **Confirmed imports:** `useToasts` lives in `../Toast` (singular — verified against `AccountPanel.tsx`/`OrderTicketPanel.tsx`; its `push({ level, text })` signature matches). `AckMsg`/`Venue`/`Gate`/`VenueConfig`/`VenueSetup` resolve from `../../wire/contract` once Task 4's DTOs are generated. `AckMsg` fields used: `status`, `reason`, `value`.

- [ ] **Step 4: Run the venues tests + typecheck**

Run: `cd ui && npx vitest run src/chrome/exec/VenuesSection.test.tsx && npm run typecheck`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add ui/src/chrome/exec/VenuesSection.tsx ui/src/chrome/exec/VenuesSection.test.tsx
git commit -m "feat(settings): Venues & credentials editor (file-only, write-only secrets)"
```

---

## Task 11: Settings shell redesign + venues wiring

**Files:**
- Modify: `ui/src/chrome/SettingsModal.tsx`
- Modify: `ui/src/chrome/AppShell.tsx:342-344` (pass `commands`)
- Modify: `ui/src/chrome/SettingsModal.test.tsx`

**Interfaces:**
- Consumes: `VenuesSection` (Task 10). `commands` comes from `AppShell` (already in scope, line 37).
- Produces: `SettingsSection` union gains `"venues"`; `SettingsModal` gains a `commands` prop.

- [ ] **Step 1: Write the failing test**

In `SettingsModal.test.tsx`: `commands` becomes a **required** prop, so first update the three existing tests (`returns null when closed`, `shows the three sections and switches`, `appearance toggles theme`) to pass a `commands` spy — otherwise they fail to typecheck with "Property 'commands' is missing". Add a shared `mkCommands = () => ({ sendCommand: vi.fn().mockResolvedValue({ kind: "ack", corrId: "", status: "accepted" }) })` helper. If the `shows the three sections` test asserts a nav-item count of 3 (or its name), update it to 4 and rename it. Then add a new test: assert the nav has four items, clicking "Venues & creds" routes to a section that fires `GetVenueSetup` (assert `spy.sendCommand` called with `"GetVenueSetup"`), and the modal panel's inline `width` is `920`.

- [ ] **Step 2: Run to confirm failure**

Run: `cd ui && npx vitest run src/chrome/SettingsModal.test.tsx`
Expected: FAIL — no venues nav item / no `commands` prop.

- [ ] **Step 3: Update `SettingsModal.tsx`**

- Extend the union and nav:

```ts
export type SettingsSection = "appearance" | "orders" | "venues" | "sounds";
const NAV: { id: SettingsSection; label: string }[] = [
  { id: "appearance", label: "Appearance" },
  { id: "orders", label: "Orders & hotkeys" },
  { id: "venues", label: "Venues & creds" },
  { id: "sounds", label: "Sounds" },
];
```

- Add `commands` to the props type and destructure it (type: `{ sendCommand(name: string, args: unknown): Promise<AckMsg> }`; import `AckMsg` from `../wire/contract`).
- Widen the panel: `width: 920, maxHeight: "min(640px, 85vh)"`, `gridTemplateColumns: "180px 1fr"`.
- Restyle the active nav item to a bronze left rule instead of the background/border swap:

```tsx
    <button key={n.id} className="btn" aria-label={n.label} onClick={() => onSection(n.id)}
      style={{ display: "block", width: "100%", textAlign: "left", marginBottom: 4, background: "transparent",
        borderColor: "transparent", borderLeft: `3px solid ${section === n.id ? palette.accent : "transparent"}`,
        color: section === n.id ? palette.text : palette.textMuted, paddingLeft: 8 }}>
      {n.label}
    </button>
```

- Render the venues branch:

```tsx
  {section === "venues" && <VenuesSection commands={commands} />}
```

(Add `import { VenuesSection } from "./exec/VenuesSection";`.)

- [ ] **Step 4: Pass `commands` from AppShell**

In `AppShell.tsx`, add `commands={commands}` to the `<SettingsModal ...>` element (line ~342). Confirm `status` was already removed in Task 7; if not, remove it now.

- [ ] **Step 5: Run chrome suite + typecheck + lint**

Run: `cd ui && npx vitest run src/chrome && npm run typecheck && npm run lint`
Expected: PASS / no errors.

- [ ] **Step 6: Commit**

```bash
git add ui/src/chrome/SettingsModal.tsx ui/src/chrome/SettingsModal.test.tsx ui/src/chrome/AppShell.tsx
git commit -m "feat(settings): 920px four-section shell with Venues nav and bronze active rule"
```

---

## Task 12: E2E smoke (Playwright)

**Files:**
- Create: `ui/tests/settings-redesign.spec.ts` (match the existing E2E test dir/naming — check `ui/tests/` or `ui/e2e/` and mirror an existing spec's boot + replay-engine harness)

**Interfaces:**
- Consumes: the replay-engine E2E harness the repo already uses (`--replay-hold`). Copy the setup block from an existing spec.

- [ ] **Step 1: Locate the E2E harness pattern**

Run: `cd ui && ls tests 2>/dev/null; ls e2e 2>/dev/null; sed -n '1,40p' $(git ls-files 'ui/**/*.spec.ts' | head -1)`
Expected: prints an existing spec's harness (baseURL, how the engine replay is launched, how to fire a hotkey). Mirror it.

- [ ] **Step 2: Write the smoke spec**

Create `ui/tests/settings-redesign.spec.ts` using that harness. Two flows:

```ts
import { test, expect } from "@playwright/test";
// ...reuse the project's boot/replay fixture...

test("orders: dollar-amount edit round-trips and fires the new size", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: /settings/i }).click();
  await page.getByRole("button", { name: /orders & hotkeys/i }).click();
  await page.getByLabel("size-value-buy-5k").fill("7000"); // was $5000
  await page.getByTestId("save").click();
  // close settings, arm, fire Ctrl+1 against the replay engine, assert the flash / order qty
  // reflects floor(7000 / price) rather than floor(5000 / price). Use the flash-capture
  // pattern the existing exec E2E spec uses.
});

test("venues: add-venue validation error surfaces inline", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: /settings/i }).click();
  await page.getByRole("button", { name: /venues & creds/i }).click();
  await page.getByTestId("add-venue").click();
  await page.getByTestId("venue-id-0").fill("Bad Id!"); // illegal chars
  await page.getByTestId("save-venues").click();
  await expect(page.getByTestId("venues-error")).toBeVisible();
});
```

- [ ] **Step 3: Run the E2E**

Run: `cd ui && npm run e2e -- settings-redesign`
Expected: PASS (both flows). If the replay engine needs a rebuilt engine binary, build it first per the existing spec's instructions.

- [ ] **Step 4: Commit**

```bash
git add ui/tests/settings-redesign.spec.ts
git commit -m "test(e2e): settings orders round-trip + venues validation smoke"
```

---

## Final Verification (run before declaring the branch done)

- [ ] Engine: `cd engine && make test && go vet ./... && golangci-lint run && make gen-ts-check` — all green, no wire drift.
- [ ] UI: `cd ui && npm test && npm run typecheck && npm run lint` — all green.
- [ ] E2E: `cd ui && npm run e2e` — green.
- [ ] Manual smoke (per `superpowers:verification-before-completion`): boot the engine against a scratch `~/.eTape/config.toml` copy, open Settings → Venues, add a venue, Save, confirm the `.bak` was created and the running-engine banner appears; add a credential, confirm the masked inputs clear and no secret appears in the engine log or WS frames (watch the network tab / engine stdout).

---

## Self-Review (author checklist — completed during drafting)

**Spec coverage:** §1 modal shell → Task 11 (+ Keycap primitive Task 7). §2 orders grid (offset/size inputs, manage rows, add menu, conflict, reset, unbind) → Tasks 7–8. §3 sounds toggles → Task 9. §4 template model + resolution + one migration point + ticket Pos fix → Tasks 5–6. §5 venues section → Task 10. §6 four engine commands + TOML/creds writers + validation → Tasks 1–4. §7 safety invariants → enforced across Tasks 1 (validation), 4 (no-secret result test), 7 (stopPropagation verbatim). §8 tests → each task's test steps + Task 12 E2E. §9 out-of-scope items are not implemented (no hot-apply, no running-gate edit, no reserved-hotkey registry, no capture-UX overhaul, no moomoo unlock).

**Type consistency:** `resolvePrice(source, offset, unit, quote)` used identically in `resolveTemplate.ts` (Task 5). `PositionFraction` reads `pct` everywhere it is constructed (`DEFAULT_TEMPLATES` T5, `OrderTicketPanel`/`AccountPanel` T6) and consumed (`resolveShares` T5). `venueAdmin` seam signature matches `venueadmin.Admin` methods (Tasks 3 ↔ 4). `wsmsg.Venue`/`Gate`/`VenueConfig`/`VenueSetup` field json tags match the UI `VenueSetup`/`Venue`/`Gate` usage (Tasks 4 ↔ 10). `SettingsSection` gains `"venues"` in one place (Task 11).
