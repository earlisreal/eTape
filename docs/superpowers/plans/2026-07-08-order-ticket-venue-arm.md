# Order Ticket Compact Redesign, Linked Venue Selection, Auto-Arm — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Shrink the order ticket to a 5-strip dense layout, make venue (broker account) selection a link-group concern shared by the ticket/Account panel/hotkeys, auto-arm paper venues at boot, and register moomoo as a disconnected stub venue so boot no longer errors.

**Architecture:** Engine changes (Go) add a per-venue `auto_arm` config flag, seed boot arm state from it in both the exec `Core` (for the gate) and the uihub `mirror` (for the UI snapshot), and replace the moomoo boot hard-error with a reject-all stub broker. UI changes (TypeScript/React) add per-group focused-venue state to `LinkGroups`, a shared `resolveVenue`/`useVenueSelection` helper, rewrite `OrderTicketPanel` to dense strips (dropping KILL/Cancel All/armed chrome), add a group-aware venue dropdown to both panels, scope the Account panel to the selected venue, and persist the venue link state in a new `linkVenues` workspace key.

**Tech Stack:** Go 1.26.4 (engine), TypeScript + React 18 + Vite + vitest (ui), BurntSushi TOML, dockview.

## Global Constraints

- **Engine tests:** from `engine/`, `make test` (= `go test -race ./...`); also `make vet` and `make lint` (golangci-lint 2.12.2) before commit. Per-package/per-test while iterating: `go test ./internal/exec/ -run TestName -v`.
- **UI tests:** from `ui/`, `npm test` (= `vitest run`); single file while iterating: `npx vitest run <path>`. If the vitest forks-pool reuses one process and a canvas-touching file interferes, run the file individually (per project note — not expected here since ticket/account panels are non-canvas).
- **No wire-contract change.** `wsmsg.VenueStatus` already has `venueArmed`, `connected`, and `note`; `AccountRow`/`PositionRow` already carry `venue`. **Do NOT edit `ui/src/gen/*` and do NOT run `make gen-ts-check` as part of this feature** — no `wsmsg` payload struct changes here.
- **Safety (from CLAUDE.md, standing):** the config in Task 12 adds live venues (`tradezero`, `alpaca-live`). Booting authenticates to them for **read-only** account/position polling, which is permitted. **Never place, modify, or cancel real orders on live venues** unless Earl explicitly authorizes it in the running conversation. Live venues MUST boot **disarmed** — never add `auto_arm = true` to a live venue.
- **Color discipline:** arm/venue UI uses `palette.accent` / `palette.textMuted`, never market-direction `palette.up`/`palette.down`/`palette.warn`. Bid is `palette.up`, ask is `palette.down` (these are quote values, not arm state).
- **Commit after every task** with a `feat:`/`refactor:`/`test:` message. Do not add a `Co-Authored-By` trailer.

## File Structure

**Engine (Go, module `github.com/earlisreal/eTape/engine`):**
- `internal/config/config.go` — add `AutoArm` to `Venue` (Task 1).
- `internal/exec/core.go` — `CoreConfig.AutoArm` + `applyBootArm` in `NewCore` (Task 2).
- `internal/exec/state.go` — (no change; `applyBootArm` lives in core.go, tested whitebox).
- `internal/broker/stub/stub.go` — **new**, the moomoo reject-all stub broker (Task 3).
- `cmd/etape/boot.go` — moomoo stub wiring + `autoArmVenues` (Task 4); `venueMetas` note/auto-arm (Task 5).
- `cmd/etape/main.go` — pass `AutoArm` into `CoreConfig` (Task 4).
- `internal/uihub/api.go` + `internal/uihub/mirror.go` — `VenueMeta.AutoArm`/`.Note` + boot seed (Task 5).

**UI (TypeScript, under `ui/src/`):**
- `chrome/linkGroups.ts` — venue focus (`focusVenue`/`venueFor`/venue hydrate/snapshot) (Task 6).
- `chrome/exec/venueSelection.ts` — **new**, `resolveVenue` + `useVenueSelection` (Task 7).
- `chrome/panels/OrderTicketPanel.tsx` — dense-strips rewrite (Task 8).
- `chrome/panels/registry.tsx` — panel description (Task 8).
- `chrome/exec/useHotkeys.ts` — full venue resolution chain (Task 9).
- `chrome/panels/AccountPanel.tsx` — venue dropdown + venue scoping + remove master button (Task 10).
- `chrome/workspace.ts` + `chrome/AppShell.tsx` — `linkVenues` persistence (Task 11).
- `~/.eTape/config.toml` — **local, not in repo** — four venues + gate caps (Task 12).

Tasks 1–5 (engine) and 6–11 (UI) are two independent groups: nothing in the UI imports the engine changes (the wire contract is unchanged), and the engine compiles/tests without the UI. They may be executed and reviewed as two batches. Within each group, execute in order.

---

### Task 1: `config.Venue.AutoArm` flag

**Files:**
- Modify: `engine/internal/config/config.go:56-62`
- Test: `engine/internal/config/config_test.go`

**Interfaces:**
- Produces: `config.Venue.AutoArm bool` (TOML key `auto_arm`), consumed by Tasks 4 (`autoArmVenues`) and 5 (`venueMetas`).

- [ ] **Step 1: Write the failing test**

Add to `engine/internal/config/config_test.go` (new test after `TestVenueAndGateParse`):

```go
func TestVenueAutoArmParse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := `
[[venue]]
id = "alpaca-paper"
broker = "alpaca"
env = "paper"
credentials = "alpaca"
auto_arm = true

[[venue]]
id = "alpaca-live"
broker = "alpaca"
env = "live"
credentials = "alpaca-live"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Venues[0].AutoArm {
		t.Fatalf("alpaca-paper should have auto_arm=true: %+v", cfg.Venues[0])
	}
	if cfg.Venues[1].AutoArm {
		t.Fatalf("alpaca-live should default auto_arm=false: %+v", cfg.Venues[1])
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd engine && go test ./internal/config/ -run TestVenueAutoArmParse -v`
Expected: FAIL — compile error `unknown field AutoArm` (the field does not exist yet).

- [ ] **Step 3: Add the field**

In `engine/internal/config/config.go`, the `Venue` struct becomes:

```go
// Venue is one configured execution venue.  ->  [[venue]]
type Venue struct {
	ID          string `toml:"id"`          // slug used in events, topics, commands, gate config
	Broker      string `toml:"broker"`      // tradezero | alpaca | moomoo | sim
	Env         string `toml:"env"`         // paper | live
	Credentials string `toml:"credentials"` // key into ~/.eJournal/credentials.json
	AccountID   string `toml:"account_id"`  // broker-specific (TZ accountId, moomoo accID)
	AutoArm     bool   `toml:"auto_arm"`    // boot this venue armed (paper); live venues keep the manual arm click
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd engine && go test ./internal/config/ -v`
Expected: PASS (all config tests, including the new one).

- [ ] **Step 5: Commit**

```bash
cd engine && git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add per-venue auto_arm flag"
```

---

### Task 2: exec `Core` boot auto-arm

**Files:**
- Modify: `engine/internal/exec/core.go:88-118` (`CoreConfig` + `NewCore`)
- Test: `engine/internal/exec/state_test.go` (whitebox, `package exec`)

**Interfaces:**
- Consumes: `State.SetVenueArmed(v VenueID, on bool)`, `State.SetMasterArmed(on bool)`, `NewState([]VenueID)` (existing, `internal/exec/state.go` / `reconcile.go`).
- Produces: `CoreConfig.AutoArm map[VenueID]bool` (populated by main.go in Task 4) and unexported `applyBootArm(s *State, venues []VenueID, autoArm map[VenueID]bool)`.

- [ ] **Step 1: Write the failing test**

Add to `engine/internal/exec/state_test.go` (it is `package exec` — whitebox):

```go
func TestApplyBootArm(t *testing.T) {
	s := NewState([]VenueID{"paper", "live"})
	applyBootArm(s, []VenueID{"paper", "live"}, map[VenueID]bool{"paper": true})
	if !s.MasterArmed {
		t.Fatal("master should arm when at least one venue auto-arms")
	}
	if !s.Venue("paper").Armed {
		t.Fatal("paper venue should boot armed")
	}
	if s.Venue("live").Armed {
		t.Fatal("live venue should boot disarmed")
	}
}

func TestApplyBootArmNoneStaysDisarmed(t *testing.T) {
	s := NewState([]VenueID{"live"})
	applyBootArm(s, []VenueID{"live"}, nil)
	if s.MasterArmed || s.Venue("live").Armed {
		t.Fatal("with no auto-arm venue, boot must stay fully disarmed")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd engine && go test ./internal/exec/ -run TestApplyBootArm -v`
Expected: FAIL — `undefined: applyBootArm`.

- [ ] **Step 3: Add `applyBootArm` and wire `CoreConfig`/`NewCore`**

In `engine/internal/exec/core.go`, add the `AutoArm` field to `CoreConfig`:

```go
type CoreConfig struct {
	Venues  []VenueID
	Gate    GateConfig
	Store   EventStore
	Brokers map[VenueID]Broker
	Clock   clock.Clock
	IDGen   *OrderIDGen
	SysLog  func(kind, detail string) // optional; store.AppendSysEvent in prod
	AutoArm map[VenueID]bool          // venues that boot armed (paper); nil/false => disarmed
}
```

At the end of `NewCore`, apply boot arm state before returning. Change the `return &Core{...}` to assign to a local, apply, then return:

```go
func NewCore(cfg CoreConfig) *Core {
	sl := cfg.SysLog
	if sl == nil {
		sl = func(string, string) {}
	}
	c := &Core{
		venues:  cfg.Venues,
		gate:    cfg.Gate,
		store:   cfg.Store,
		brokers: cfg.Brokers,
		clk:     cfg.Clock,
		idgen:   cfg.IDGen,
		syslog:  sl,
		cmds:    make(chan cmdReq),
		bevents: make(chan BrokerEvent, 1024),
		markCh:  make(chan Mark, 256),
		updates: make(chan Update, 4096),
		state:   NewState(cfg.Venues),
		marks:   markState{},
	}
	// Auto-arm is the ONLY boot path that starts armed; Recover never touches
	// arm state, so a restart with no auto-arm venue is still fully disarmed.
	applyBootArm(c.state, cfg.Venues, cfg.AutoArm)
	return c
}

// applyBootArm seeds boot arm state: venues flagged auto_arm start armed, and
// master arms iff at least one venue auto-arms. Live venues (no auto_arm) stay
// disarmed and keep the deliberate arm click.
func applyBootArm(s *State, venues []VenueID, autoArm map[VenueID]bool) {
	for _, v := range venues {
		if autoArm[v] {
			s.SetVenueArmed(v, true)
			s.SetMasterArmed(true)
		}
	}
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd engine && go test ./internal/exec/ -v`
Expected: PASS. In particular `TestApplyBootArm*` pass **and** the existing `core_lifecycle_test.go` "recovered state should boot disarmed" assertion still passes (its venues carry no `auto_arm`, so `applyBootArm` with a nil map is a no-op).

- [ ] **Step 5: Commit**

```bash
cd engine && git add internal/exec/core.go internal/exec/state_test.go
git commit -m "feat(exec): seed boot arm state from per-venue auto_arm"
```

---

### Task 3: moomoo stub broker

**Files:**
- Create: `engine/internal/broker/stub/stub.go`
- Test: `engine/internal/broker/stub/stub_test.go`

**Interfaces:**
- Consumes: the `exec.Broker` interface (`Capabilities`, `SubmitOrder`, `ReplaceOrder`, `CancelOrder`, `CancelAll`, `Flatten`, `Snapshot`, `Events`) from `internal/exec/broker.go`.
- Produces: `stub.New() *stub.Broker`, used by boot.go in Task 4.

- [ ] **Step 1: Write the failing test**

Create `engine/internal/broker/stub/stub_test.go`:

```go
package stub_test

import (
	"context"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/broker/stub"
	"github.com/earlisreal/eTape/engine/internal/exec"
)

func TestStubRejectsSubmit(t *testing.T) {
	b := stub.New()
	if _, err := b.SubmitOrder(context.Background(), exec.OrderRequest{Venue: "moomoo"}); err == nil {
		t.Fatal("stub venue must reject SubmitOrder")
	}
	if err := b.ReplaceOrder(context.Background(), "id", exec.ReplaceRequest{}); err == nil {
		t.Fatal("stub venue must reject ReplaceOrder")
	}
}

func TestStubCancelFlattenAreNoops(t *testing.T) {
	b := stub.New()
	if err := b.CancelAll(context.Background(), ""); err != nil {
		t.Fatalf("CancelAll should be a no-op, got %v", err)
	}
	if err := b.CancelOrder(context.Background(), "id"); err != nil {
		t.Fatalf("CancelOrder should be a no-op, got %v", err)
	}
	if err := b.Flatten(context.Background()); err != nil {
		t.Fatalf("Flatten should be a no-op, got %v", err)
	}
}

func TestStubEventsChannelIsClosed(t *testing.T) {
	b := stub.New()
	if _, ok := <-b.Events(); ok {
		t.Fatal("stub venue never connects — Events() must be a closed channel")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd engine && go test ./internal/broker/stub/ -v`
Expected: FAIL — package `stub` does not exist / `undefined: stub.New`.

- [ ] **Step 3: Create the stub broker**

Create `engine/internal/broker/stub/stub.go`:

```go
// Package stub provides a disconnected, reject-all execution venue for brokers
// whose live trading adapter is not yet implemented. It registers like any
// venue, never connects (Events() is closed), and rejects order placement;
// cancel and flatten are no-ops because exposure-reducing actions are never
// gated. moomoo trading is deferred to execution v1.x — when the real adapter
// lands, only boot.go's "moomoo" case changes.
package stub

import (
	"context"
	"errors"

	"github.com/earlisreal/eTape/engine/internal/exec"
)

// Broker is the reject-all venue. Compile-time interface check below.
type Broker struct {
	events chan exec.BrokerEvent
}

var _ exec.Broker = (*Broker)(nil)

// New builds a stub broker whose Events() channel is already closed, so exec's
// pump goroutine exits immediately and the venue reports as never-connected.
func New() *Broker {
	ch := make(chan exec.BrokerEvent)
	close(ch)
	return &Broker{events: ch}
}

var errUnavailable = errors.New("venue unavailable: moomoo trading is deferred to execution v1.x")

func (b *Broker) Capabilities() exec.Capabilities { return exec.Capabilities{} }

func (b *Broker) SubmitOrder(context.Context, exec.OrderRequest) (exec.OrderAck, error) {
	return exec.OrderAck{}, errUnavailable
}

func (b *Broker) ReplaceOrder(context.Context, string, exec.ReplaceRequest) error {
	return errUnavailable
}

func (b *Broker) CancelOrder(context.Context, string) error { return nil }
func (b *Broker) CancelAll(context.Context, string) error   { return nil }
func (b *Broker) Flatten(context.Context) error             { return nil }

// Snapshot errors so exec.Recover logs "not available" and creates no account
// row — the Account panel then shows "—" for this venue rather than a fake $0.
func (b *Broker) Snapshot(context.Context) (exec.AccountSnapshot, []exec.Position, []exec.Order, error) {
	return exec.AccountSnapshot{}, nil, nil, errUnavailable
}

func (b *Broker) Events() <-chan exec.BrokerEvent { return b.events }
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd engine && go test ./internal/broker/stub/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd engine && git add internal/broker/stub/
git commit -m "feat(broker): add reject-all stub venue for moomoo (execution v1.x)"
```

---

### Task 4: boot.go — moomoo stub wiring + auto-arm plumbing

**Files:**
- Modify: `engine/cmd/etape/boot.go:63-101` (`buildBrokers` moomoo case) + add `autoArmVenues`
- Modify: `engine/cmd/etape/main.go:135-139` (`CoreConfig.AutoArm`)
- Test: `engine/cmd/etape/boot_test.go`

**Interfaces:**
- Consumes: `config.Venue.AutoArm` (Task 1), `stub.New()` (Task 3), `exec.CoreConfig.AutoArm` (Task 2).
- Produces: `autoArmVenues(cfg config.Config) map[exec.VenueID]bool`.

- [ ] **Step 1: Update the failing/obsolete boot test**

Open `engine/cmd/etape/boot_test.go`. Replace the existing `TestBuildBrokersMoomooAndUnknownError` with the two tests below (moomoo now registers; only an unknown broker errors), and add `TestAutoArmVenues`:

```go
func TestBuildBrokersMoomooRegistersStub(t *testing.T) {
	cfg := config.Config{Venues: []config.Venue{{ID: "moomoo", Broker: "moomoo"}}}
	vbs, err := buildBrokers(cfg, creds.File{}, clock.System{}, false)
	if err != nil {
		t.Fatalf("moomoo venue should register a stub, not error: %v", err)
	}
	if len(vbs) != 1 || vbs[0].ID != "moomoo" {
		t.Fatalf("expected one moomoo venue, got %+v", vbs)
	}
	if vbs[0].Run != nil {
		t.Fatal("stub venue has no Run loop")
	}
	if _, err := vbs[0].Broker.SubmitOrder(context.Background(), exec.OrderRequest{Venue: "moomoo"}); err == nil {
		t.Fatal("moomoo stub must reject submits")
	}
}

func TestBuildBrokersUnknownErrors(t *testing.T) {
	cfg := config.Config{Venues: []config.Venue{{ID: "x", Broker: "nope"}}}
	if _, err := buildBrokers(cfg, creds.File{}, clock.System{}, false); err == nil {
		t.Fatal("unknown broker must error")
	}
}

func TestAutoArmVenues(t *testing.T) {
	cfg := config.Config{Venues: []config.Venue{
		{ID: "alpaca-paper", Broker: "alpaca", AutoArm: true},
		{ID: "alpaca-live", Broker: "alpaca"},
		{ID: "moomoo", Broker: "moomoo", AutoArm: true},
	}}
	got := autoArmVenues(cfg)
	if !got["alpaca-paper"] || !got["moomoo"] {
		t.Fatalf("auto-arm venues missing: %+v", got)
	}
	if got["alpaca-live"] {
		t.Fatalf("live venue must not auto-arm: %+v", got)
	}
}
```

> Note the imports these tests need: `context`, `github.com/earlisreal/eTape/engine/internal/exec`, `github.com/earlisreal/eTape/engine/internal/clock`, `github.com/earlisreal/eTape/engine/internal/creds`, `github.com/earlisreal/eTape/engine/internal/config`. Check the top of `boot_test.go` and add any that are missing. `clock.System{}` is the production clock; if the file uses a different constructor for a real clock in other tests, match it.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd engine && go test ./cmd/etape/ -run 'TestBuildBrokersMoomoo|TestBuildBrokersUnknown|TestAutoArmVenues' -v`
Expected: FAIL — moomoo case still errors, and `autoArmVenues` is undefined.

- [ ] **Step 3: Replace the moomoo case and add `autoArmVenues`**

In `engine/cmd/etape/boot.go`, add the `stub` import:

```go
	"github.com/earlisreal/eTape/engine/internal/broker/stub"
```

Replace the moomoo case in `buildBrokers`:

```go
		case "moomoo":
			// Stub venue: registers, never connects, rejects order placement.
			// The real moomoo trading adapter is execution v1.x; only this
			// case changes then. (Replay short-circuits to sim above.)
			out = append(out, venueBroker{ID: id, Broker: stub.New()})
```

Add `autoArmVenues` (e.g. right after `venueMetas`):

```go
// autoArmVenues maps venue id -> true for venues configured with auto_arm.
// Paper venues boot armed; live venues (absent here) keep the manual arm click.
// Built from config regardless of replay, so replay mode auto-arms identically.
func autoArmVenues(cfg config.Config) map[exec.VenueID]bool {
	out := make(map[exec.VenueID]bool, len(cfg.Venues))
	for _, v := range cfg.Venues {
		if v.AutoArm {
			out[exec.VenueID(v.ID)] = true
		}
	}
	return out
}
```

- [ ] **Step 4: Wire `AutoArm` into the `CoreConfig`**

In `engine/cmd/etape/main.go`, extend the `exec.NewCore` call:

```go
	execCore := exec.NewCore(exec.CoreConfig{
		Venues: venueIDs, Gate: buildGateConfig(cfg.Gate), Store: st,
		Brokers: brokers, Clock: execClk, IDGen: exec.NewOrderIDGen(execClk, rand.Reader),
		SysLog:  st.AppendSysEvent,
		AutoArm: autoArmVenues(cfg),
	})
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd engine && go build ./... && go test ./cmd/etape/ -v`
Expected: build succeeds; all boot tests pass.

- [ ] **Step 6: Commit**

```bash
cd engine && git add cmd/etape/boot.go cmd/etape/main.go cmd/etape/boot_test.go
git commit -m "feat(boot): register moomoo stub venue + plumb auto_arm into exec core"
```

---

### Task 5: uihub — seed boot arm state + moomoo note into the mirror

**Files:**
- Modify: `engine/internal/uihub/api.go:55-59` (`VenueMeta`) + `:75-84` (`New`)
- Modify: `engine/internal/uihub/mirror.go:24-28` (internal `venueMeta`) + `:77-80` (`newMirror`)
- Modify: `engine/cmd/etape/boot.go:39-52` (`venueMetas`)
- Test: `engine/internal/uihub/mirror_test.go`

**Interfaces:**
- Consumes: `config.Venue.AutoArm` (Task 1); the existing `wsmsg.VenueStatus{ Venue, Broker, Connected, VenueArmed, Note, Gate }` (unchanged).
- Produces: `uihub.VenueMeta.AutoArm bool` and `uihub.VenueMeta.Note string`; the mirror's initial `execStatus()` reflects boot arm state and the moomoo note.

Rationale: the UI's first `exec.status` comes from the mirror's seeded state (the exec `Core` does not emit status at boot). Seeding `masterArmed`/`venueArmed` here — from the same config `auto_arm` flag the `Core` reads in Task 2 — makes the boot snapshot correct without a `Core` boot-emit. This mirrors how gate limits are already fed to both the `Core` (`CoreConfig.Gate`) and the mirror (`VenueMeta.Gate`). moomoo's `connected` stays `false` (the stub never emits `BrokerConnUp`), so it needs no special connected handling — only the note.

- [ ] **Step 1: Write the failing test**

Add to `engine/internal/uihub/mirror_test.go` (it is `package uihub`):

```go
func TestNewMirrorSeedsAutoArmAndNote(t *testing.T) {
	m := newMirror([]venueMeta{
		{ID: "alpaca-paper", Broker: wsmsg.BrokerAlpaca, AutoArm: true},
		{ID: "alpaca-live", Broker: wsmsg.BrokerAlpaca},
		{ID: "moomoo", Broker: wsmsg.BrokerMoomoo, Note: "execution v1.x"},
	}, wsmsg.GlobalLimitsView{}, 10, 10, 10, 10)

	st := m.execStatus()
	if !st.MasterArmed {
		t.Fatal("master should seed armed when a venue auto-arms")
	}
	byID := map[string]wsmsg.VenueStatus{}
	for _, v := range st.Venues {
		byID[v.Venue] = v
	}
	if !byID["alpaca-paper"].VenueArmed {
		t.Fatal("alpaca-paper should seed armed")
	}
	if byID["alpaca-live"].VenueArmed {
		t.Fatal("alpaca-live should seed disarmed")
	}
	if byID["moomoo"].Connected {
		t.Fatal("moomoo stub should seed disconnected")
	}
	if byID["moomoo"].Note != "execution v1.x" {
		t.Fatalf("moomoo note wrong: %q", byID["moomoo"].Note)
	}
}
```

> `wsmsg.BrokerAlpaca` and `wsmsg.BrokerMoomoo` are both `Broker` string constants in `internal/uihub/wsmsg/wsmsg.go` — `testMirror()` in this file already uses `BrokerAlpaca`.

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd engine && go test ./internal/uihub/ -run TestNewMirrorSeedsAutoArmAndNote -v`
Expected: FAIL — `unknown field AutoArm`/`Note` in `venueMeta`.

- [ ] **Step 3: Add `AutoArm`/`Note` to both `VenueMeta` types and seed the mirror**

In `engine/internal/uihub/api.go`, extend the public `VenueMeta`:

```go
type VenueMeta struct {
	ID      string
	Broker  string
	AutoArm bool   // boot this venue armed (paper); reflected in the initial exec.status
	Note    string // e.g. "execution v1.x" for the moomoo stub
	Gate    GateLimits
}
```

In the same file's `New`, carry the two fields into the internal `venueMeta`:

```go
		vms = append(vms, venueMeta{
			ID:      v.ID,
			Broker:  wsmsg.Broker(v.Broker),
			AutoArm: v.AutoArm,
			Note:    v.Note,
			Gate: wsmsg.GateLimitsView{
				MaxOrderValue: v.Gate.MaxOrderValue, MaxPositionValue: v.Gate.MaxPositionValue,
				MaxPositionShares: v.Gate.MaxPositionShares, MaxOpenOrders: v.Gate.MaxOpenOrders,
			},
		})
```

In `engine/internal/uihub/mirror.go`, extend the internal `venueMeta`:

```go
type venueMeta struct {
	ID      string
	Broker  wsmsg.Broker
	AutoArm bool
	Note    string
	Gate    wsmsg.GateLimitsView
}
```

And seed arm state + note in `newMirror`'s loop:

```go
	for _, v := range venues {
		m.venueStatus[v.ID] = &wsmsg.VenueStatus{
			Venue: v.ID, Broker: v.Broker, Gate: v.Gate,
			VenueArmed: v.AutoArm, Note: v.Note,
		}
		m.venueOrder = append(m.venueOrder, v.ID)
		if v.AutoArm {
			m.masterArmed = true
		}
	}
```

- [ ] **Step 4: Populate `AutoArm`/`Note` in `venueMetas`**

In `engine/cmd/etape/boot.go`, update `venueMetas`:

```go
func venueMetas(cfg config.Config) []uihub.VenueMeta {
	out := make([]uihub.VenueMeta, 0, len(cfg.Venues))
	for _, v := range cfg.Venues {
		gv := cfg.Gate.Venue[v.ID]
		note := ""
		if v.Broker == "moomoo" {
			note = "execution v1.x"
		}
		out = append(out, uihub.VenueMeta{
			ID: v.ID, Broker: v.Broker, AutoArm: v.AutoArm, Note: note,
			Gate: uihub.GateLimits{
				MaxOrderValue: gv.MaxOrderValue, MaxPositionValue: gv.MaxPositionValue,
				MaxPositionShares: gv.MaxPositionShares, MaxOpenOrders: gv.MaxOpenOrders,
			},
		})
	}
	return out
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd engine && go build ./... && go test ./internal/uihub/ ./cmd/etape/ -v`
Expected: PASS.

- [ ] **Step 6: Run the full engine suite + lint**

Run: `cd engine && make test && make vet && make lint`
Expected: all pass. (This is the engine group's final gate — Tasks 1–5 are now complete and consistent.)

- [ ] **Step 7: Commit**

```bash
cd engine && git add internal/uihub/api.go internal/uihub/mirror.go internal/uihub/mirror_test.go cmd/etape/boot.go
git commit -m "feat(uihub): seed boot arm state and moomoo note into the exec.status snapshot"
```

---

### Task 6: `LinkGroups` — per-group focused venue

**Files:**
- Modify: `ui/src/chrome/linkGroups.ts`
- Test: `ui/src/chrome/linkGroups.test.ts`

**Interfaces:**
- Consumes: `VenueID` from `../wire/contract`.
- Produces: `LinkMsg` (now `{ group; symbol?; venue? }`), `LinkGroups.focusVenue(group, venue)`, `LinkGroups.venueFor(group): VenueID | undefined`, `LinkGroups.hydrateVenues(map)`, `LinkGroups.snapshotVenues()`. Consumed by Tasks 7, 11.

- [ ] **Step 1: Write the failing tests**

Add a new `describe("venue focus", ...)` block to `ui/src/chrome/linkGroups.test.ts`. The file already imports `{ LinkGroups }` and `{ FakeBus, FakeBusHub }` from `../../test/fakes`, and constructs instances as `new LinkGroups(new FakeBus(hub), onEcho)` — match that exactly (there is no `hub.bus()`; the constructor takes `new FakeBus(hub)`):

```ts
describe("venue focus", () => {
  it("focusVenue updates local state and posts to the bus without an engine echo", () => {
    const onEcho = vi.fn();
    const lg = new LinkGroups(new FakeBus(new FakeBusHub()), onEcho);
    lg.focusVenue("green", "alpaca-paper");
    expect(lg.venueFor("green")).toBe("alpaca-paper");
    expect(onEcho).not.toHaveBeenCalled(); // venue is UI-only state — never validated server-side
  });

  it("venueFor returns undefined for the pinned (null) group", () => {
    const lg = new LinkGroups(new FakeBus(new FakeBusHub()), () => {});
    expect(lg.venueFor(null)).toBeUndefined();
  });

  it("propagates a venue-only message across windows without touching symbol", () => {
    const hub = new FakeBusHub();
    const a = new LinkGroups(new FakeBus(hub), () => {});
    const b = new LinkGroups(new FakeBus(hub), () => {});
    a.focus("green", "US.AAPL");
    b.focusVenue("green", "tradezero");
    expect(a.venueFor("green")).toBe("tradezero"); // venue crossed the bus
    expect(a.symbolFor("green")).toBe("US.AAPL");   // symbol untouched by the venue message
  });

  it("hydrateVenues/snapshotVenues round-trip and skip falsy entries", () => {
    const lg = new LinkGroups(new FakeBus(new FakeBusHub()), () => {});
    lg.hydrateVenues({ green: "alpaca-paper", red: "" as never });
    expect(lg.snapshotVenues()).toEqual({ green: "alpaca-paper" });
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd ui && npx vitest run src/chrome/linkGroups.test.ts`
Expected: FAIL — `focusVenue`/`venueFor`/`snapshotVenues` are not functions.

- [ ] **Step 3: Implement venue focus in `LinkGroups`**

In `ui/src/chrome/linkGroups.ts`, update the import and `LinkMsg`:

```ts
import type { AckMsg, VenueID } from "../wire/contract";

export type LinkGroup = "red" | "green" | "blue" | "yellow" | null; // null = pinned
export interface LinkMsg { group: LinkGroup; symbol?: string; venue?: VenueID }
```

Add a `focusedVenues` map beside `focused`:

```ts
  private readonly focused = new Map<Exclude<LinkGroup, null>, string>();
  private readonly focusedVenues = new Map<Exclude<LinkGroup, null>, VenueID>();
```

Update the bus handler in the constructor to dispatch on whichever field is present:

```ts
    this.bus.onMessage((msg) => {
      if (!msg.group) return;
      if (msg.symbol !== undefined) this.setLocal(msg.group, msg.symbol);
      if (msg.venue !== undefined) this.setLocalVenue(msg.group, msg.venue);
    });
```

Add the venue methods (place them after `symbolFor`/`subscribe`, before `hydrate`):

```ts
  // Venue focus is UI-only state — unlike symbol focus it does NOT echo to the
  // engine (the engine has no per-group venue concept). It publishes cross-window
  // and notifies subscribers so grouped panels re-render.
  focusVenue(group: Exclude<LinkGroup, null>, venue: VenueID): void {
    this.setLocalVenue(group, venue);
    this.bus.post({ group, venue });
  }

  venueFor(group: LinkGroup): VenueID | undefined {
    return group ? this.focusedVenues.get(group) : undefined;
  }

  private setLocalVenue(group: Exclude<LinkGroup, null>, venue: VenueID): void {
    this.focusedVenues.set(group, venue);
    this.subs.forEach((cb) => cb());
  }
```

Add venue hydrate/snapshot after the existing symbol `hydrate`/`snapshot` (same silent semantics — a page-load restore, no bus/echo/notify):

```ts
  /** Seed per-group focused venues from a persisted workspace doc (silent — see hydrate). */
  hydrateVenues(map: Partial<Record<Exclude<LinkGroup, null>, VenueID>>): void {
    for (const [group, venue] of Object.entries(map) as [Exclude<LinkGroup, null>, VenueID][]) {
      if (venue) this.focusedVenues.set(group, venue);
    }
  }

  /** Plain-object snapshot of the focused-venue map, for the workspace doc's linkVenues key. */
  snapshotVenues(): Partial<Record<Exclude<LinkGroup, null>, VenueID>> {
    return Object.fromEntries(this.focusedVenues);
  }
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd ui && npx vitest run src/chrome/linkGroups.test.ts`
Expected: PASS (new venue tests + all existing symbol/hydrate tests).

- [ ] **Step 5: Commit**

```bash
cd ui && git add src/chrome/linkGroups.ts src/chrome/linkGroups.test.ts
git commit -m "feat(ui/linkGroups): per-group focused venue (focusVenue/venueFor + hydrate/snapshot)"
```

---

### Task 7: shared `venueSelection` helper

**Files:**
- Create: `ui/src/chrome/exec/venueSelection.ts`
- Test: `ui/src/chrome/exec/venueSelection.test.ts`

**Interfaces:**
- Consumes: `LinkGroups.venueFor` (Task 6), `useOrderConfig()` (`OrderConfigApi.config.activeVenue`, `setActiveVenue`), `stores.exec.status()`.
- Produces:
  - `resolveVenue(group: LinkGroup, linkGroups: LinkGroups, activeVenue: VenueID, status: ExecStatus | null): VenueID` — used by Tasks 8, 9, 10.
  - `useVenueSelection(group: LinkGroup, linkGroups: LinkGroups, stores: Stores): { venue: VenueID; venues: VenueID[]; selectVenue: (v: VenueID) => void }` — used by Tasks 8, 10.

- [ ] **Step 1: Write the failing test**

Create `ui/src/chrome/exec/venueSelection.test.ts` (pure `resolveVenue` unit tests — no jsdom needed):

```ts
import { describe, it, expect } from "vitest";
import { resolveVenue } from "./venueSelection";
import { LinkGroups, BroadcastChannelBus } from "../linkGroups";
import type { ExecStatus } from "../../wire/contract";

const status = (...ids: string[]): ExecStatus => ({
  masterArmed: false,
  global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 },
  venues: ids.map((venue) => ({
    venue, broker: "alpaca" as never, connected: true, venueArmed: false,
    reconcilePending: false, note: "", lastReconcileMs: null,
    gate: { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 },
  })),
});

describe("resolveVenue", () => {
  it("prefers the group's focused venue", () => {
    const lg = new LinkGroups(new BroadcastChannelBus(), () => {});
    lg.focusVenue("green", "tradezero");
    expect(resolveVenue("green", lg, "alpaca-paper", status("alpaca-paper", "tradezero"))).toBe("tradezero");
  });

  it("falls back to the active venue, then the first configured venue", () => {
    const lg = new LinkGroups(new BroadcastChannelBus(), () => {});
    expect(resolveVenue("green", lg, "alpaca-live", status("alpaca-paper", "alpaca-live"))).toBe("alpaca-live");
    // empty active venue (the default) falls through to the first venue
    expect(resolveVenue(null, lg, "", status("alpaca-paper", "alpaca-live"))).toBe("alpaca-paper");
  });

  it("returns empty string when nothing resolves", () => {
    const lg = new LinkGroups(new BroadcastChannelBus(), () => {});
    expect(resolveVenue(null, lg, "", null)).toBe("");
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd ui && npx vitest run src/chrome/exec/venueSelection.test.ts`
Expected: FAIL — module `./venueSelection` does not exist.

- [ ] **Step 3: Create the helper**

Create `ui/src/chrome/exec/venueSelection.ts`:

```ts
import { useSyncExternalStore } from "react";
import type { VenueID, ExecStatus } from "../../wire/contract";
import type { Stores } from "../../data/registry";
import type { LinkGroup, LinkGroups } from "../linkGroups";
import { useOrderConfig } from "./useOrderConfig";

// The venue-resolution chain shared by the order ticket, the Account panel, and
// the hotkey engine: a grouped panel's focused venue wins, else the global
// active venue, else the first configured venue, else none. `||` (not `??`) so
// the empty-string activeVenue default falls through to the first venue.
export function resolveVenue(
  group: LinkGroup,
  linkGroups: LinkGroups,
  activeVenue: VenueID,
  status: ExecStatus | null,
): VenueID {
  return linkGroups.venueFor(group) || activeVenue || status?.venues[0]?.venue || "";
}

// Hook form for panels: returns the resolved venue, the full venue-id list, and
// a setter that writes group-focus for grouped panels or the global active venue
// for pinned panels (group === null). Subscribes to both the link bus (venue
// re-pick) and the exec store (venue list changes) so the panel re-renders.
export function useVenueSelection(
  group: LinkGroup,
  linkGroups: LinkGroups,
  stores: Stores,
): { venue: VenueID; venues: VenueID[]; selectVenue: (v: VenueID) => void } {
  const { config: orderCfg, setActiveVenue } = useOrderConfig();
  useSyncExternalStore((cb) => linkGroups.subscribe(cb), () => linkGroups.venueFor(group));
  useSyncExternalStore((cb) => stores.exec.subscribe(cb), () => stores.exec.getSnapshot());
  const status = stores.exec.status();
  const venues = status?.venues.map((v) => v.venue) ?? [];
  const venue = resolveVenue(group, linkGroups, orderCfg.activeVenue, status);
  const selectVenue = (v: VenueID) => {
    if (group === null) setActiveVenue(v);   // pinned panels drive the global active venue
    else linkGroups.focusVenue(group, v);     // grouped panels write focusVenue only, leaving activeVenue untouched
  };
  return { venue, venues, selectVenue };
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd ui && npx vitest run src/chrome/exec/venueSelection.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd ui && git add src/chrome/exec/venueSelection.ts src/chrome/exec/venueSelection.test.ts
git commit -m "feat(ui/exec): shared resolveVenue + useVenueSelection helper"
```

---

### Task 8: `OrderTicketPanel` — dense-strips rewrite + group-aware venue

**Files:**
- Modify: `ui/src/chrome/panels/OrderTicketPanel.tsx` (full body rewrite of the returned JSX + venue wiring)
- Modify: `ui/src/chrome/panels/registry.tsx:174` (panel description)
- Test: `ui/src/chrome/panels/OrderTicketPanel.test.tsx`

**Interfaces:**
- Consumes: `useVenueSelection(group, linkGroups, stores)` (Task 7); existing `useOrderConfig` (for `templates`), `useThrottledQuote`, `useOrderCommands`, `resolveShares`, `preCheck`, `resolvePlaceTemplate`, `abbrevType`, `sideLabel`, `bareSymbol`, `formatPrice`, `QUOTE_DECIMALS`.
- Removes: `data-testid` `cancel-all`, `kill`, `ticket-armed-state`; the Bid/Ask button row; the labeled Price row; the full-width Submit row. Keeps `bid`, `ask`, `venue`, `open-settings`, `order-type`, `price`, `stop`, `amount`, `mode`, `submit`, `preset-*`.

- [ ] **Step 1: Update the tests (some assertions must change first)**

Edit `ui/src/chrome/panels/OrderTicketPanel.test.tsx`. Ground facts about this file: `mkProps()` takes no args, sets `config.group: "green"`, and returns `{ props, stores, sent, linkGroups }` (real `LinkGroups`); the status fixture is `const status = (): ExecStatus => (…)` (zero-arg, single armed `alpaca-paper` venue); quote payloads use `ts: ""` (not `tsMs`); `wrap` already nests `OrderConfigProvider`; a module-level `hexToRgb` helper exists at the bottom.

1. **Delete** these four tests whose subjects no longer exist: `"kill switch fires KillSwitch even without arming logic"`, `"shows a DISARMED badge when the active venue is disarmed"`, `"shows an ARMED badge when master and the active venue are armed, and exposes an order-type testid"`, and `"colors the armed indicator bronze/muted, never green/red"`. Then **delete the now-unused module-level `hexToRgb` function** at the bottom of the file (only the deleted color test used it) — leaving it triggers an unused-symbol lint/TS error.
2. **Keep** the two existing tests that still hold post-redesign: `"follows the link-group symbol and shows bid/ask"` (bid/ask are now header spans with the same testids and formatted text) and `"manual Shares submit sends a venue-tagged SubmitOrder"` (amount/price/submit testids unchanged).
3. **Add** a bid/ask click-to-fill test (the header spans replace the old Bid/Ask button row):

```ts
it("clicking the header bid/ask fills the price input", () => {
  const { props, stores, linkGroups } = mkProps();
  act(() => {
    stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status() });
    stores.quote.apply({ kind: "snapshot", topic: "md.quote" as never, payload: { symbol: "US.AAPL", bid: 3.4, ask: 3.5, last: 3.45, ts: "" } });
    linkGroups.focus("green", "US.AAPL");
  });
  wrap(props);
  fireEvent.click(screen.getByTestId("bid"));
  expect((screen.getByTestId("price") as HTMLInputElement).value).toBe("3.40");
  fireEvent.click(screen.getByTestId("ask"));
  expect((screen.getByTestId("price") as HTMLInputElement).value).toBe("3.50");
});
```

4. **Add** a stop-input-permanently-rendered test:

```ts
it("renders the stop input always, disabled unless type is STOP/STOP_LIMIT", () => {
  const { props, stores } = mkProps();
  act(() => { stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status() }); });
  wrap(props);
  expect((screen.getByTestId("stop") as HTMLInputElement).disabled).toBe(true); // default LIMIT
  fireEvent.change(screen.getByTestId("order-type"), { target: { value: "STOP" } });
  expect((screen.getByTestId("stop") as HTMLInputElement).disabled).toBe(false);
});
```

5. **Add** a venue→`focusVenue` group-sync test (`mkProps` already mounts on the green group; seed a two-venue status inline):

```ts
it("changing the venue dropdown writes the group's focused venue", () => {
  const { props, stores, linkGroups } = mkProps();
  const twoVenues: ExecStatus = { ...status(), venues: [status().venues[0], { ...status().venues[0], venue: "tradezero" }] };
  act(() => { stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: twoVenues }); });
  wrap(props);
  fireEvent.change(screen.getByTestId("venue"), { target: { value: "tradezero" } });
  expect(linkGroups.venueFor("green")).toBe("tradezero");
});
```

(`ExecStatus` is already imported in this file.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd ui && npx vitest run src/chrome/panels/OrderTicketPanel.test.tsx`
Expected: FAIL — the stop test throws (stop is conditionally rendered today, absent on the default LIMIT type), and the venue-sync test fails (today the venue `<select>` calls `setActiveVenue`, not `focusVenue`, so `linkGroups.venueFor("green")` stays `undefined`). The click-to-fill test may already pass (today's Bid/Ask buttons also fill the price); the rewrite makes it authoritative for the header spans.

- [ ] **Step 3: Rewrite `OrderTicketPanel.tsx`**

Replace the venue wiring and the entire returned JSX. Full file:

```tsx
import { useEffect, useMemo, useState } from "react";
import { useSyncExternalStore } from "react";
import type { PanelProps } from "./registry";
import type { Side, OrderType, TIF, SubmitOrderArgs } from "../../wire/contract";
import { useTheme } from "../ThemeProvider";
import { useToasts } from "../Toast";
import { useOrderCommands } from "../exec/useOrderCommands";
import { useOrderConfig } from "../exec/useOrderConfig";
import { useVenueSelection } from "../exec/venueSelection";
import { useThrottledQuote } from "../exec/useThrottledQuote";
import { resolveShares, type SizingMode } from "../exec/sizing";
import { preCheck, type DraftOrder } from "../exec/preChecks";
import { resolvePlaceTemplate } from "../exec/resolveTemplate";
import type { PlaceOrderTemplate } from "../exec/actionTemplate";
import { sideLabel, bareSymbol, abbrevType } from "../exec/orderStatus";
import { formatPrice, QUOTE_DECIMALS } from "../../render/format";
import { useOpenSettings } from "../OpenSettingsContext";

const SIDES: Side[] = ["BUY", "SELL", "SHORT", "COVER"];
const TYPES: OrderType[] = ["LIMIT", "MARKET", "STOP", "STOP_LIMIT"];
const TIFS: TIF[] = ["DAY", "GTC", "IOC", "FOK"];
const MODES: SizingMode[] = ["Shares", "Dollar", "BuyingPowerPct", "PositionFraction"];
const MODE_LABEL: Record<SizingMode, string> = { Shares: "Sh", Dollar: "$", BuyingPowerPct: "BP%", PositionFraction: "Pos" };

export function OrderTicketPanel({ config, stores, commands, linkGroups, group: groupProp }: PanelProps): JSX.Element {
  const { palette } = useTheme();
  const toast = useToasts();
  const oc = useOrderCommands(commands, stores.exec, toast);
  const { config: orderCfg } = useOrderConfig(); // presets/templates
  const openSettings = useOpenSettings();
  useSyncExternalStore((cb) => stores.exec.subscribe(cb), () => stores.exec.getSnapshot());

  const group = groupProp ?? config.group;
  const [symbol, setSymbol] = useState<string>(() => linkGroups.symbolFor(group) ?? (config.settings.symbol as string) ?? "US.AAPL");
  useEffect(() => {
    const apply = () => setSymbol(linkGroups.symbolFor(group) ?? (config.settings.symbol as string) ?? "US.AAPL");
    apply();
    return linkGroups.subscribe(apply);
  }, [linkGroups, group, config.settings.symbol]);

  const quote = useThrottledQuote(stores.quote, symbol);
  const { venue, venues, selectVenue } = useVenueSelection(group, linkGroups, stores);

  const [side, setSide] = useState<Side>("BUY");
  const [type, setType] = useState<OrderType>("LIMIT");
  const [tif, setTif] = useState<TIF>("DAY");
  const [mode, setMode] = useState<SizingMode>("Shares");
  const [amount, setAmount] = useState("100");
  const [price, setPrice] = useState("");
  const [stop, setStop] = useState("");

  const account = stores.exec.accounts().find((a) => a.venue === venue);
  const buyingPower = account?.buyingPower ?? 0;
  const positionQty = stores.exec.positions().filter((p) => p.symbol === symbol && p.venue === venue).reduce((s, p) => s + p.qty, 0);

  const presets = useMemo(() => orderCfg.templates.filter((t): t is PlaceOrderTemplate => t.kind === "place"), [orderCfg.templates]);
  const hasStop = type === "STOP" || type === "STOP_LIMIT";

  const submitManual = () => {
    if (venue === "") { toast.push({ level: "danger", text: "No venue configured." }); return; }
    const px = Number(price) || 0;
    const spec = mode === "Shares" ? { mode, shares: Number(amount) || 0 }
      : mode === "Dollar" ? { mode, dollar: Number(amount) || 0 }
      : mode === "BuyingPowerPct" ? { mode, pct: Number(amount) || 0 }
      : { mode, fraction: "all" as const };
    const qty = resolveShares(spec, { price: px, buyingPower, positionQty });
    const draft: DraftOrder = { symbol, side, type, tif, qty, limitPrice: type === "MARKET" ? 0 : px, stopPrice: hasStop ? Number(stop) || 0 : 0 };
    const pc = preCheck(draft, quote?.last ?? 0, Date.now());
    for (const n of pc.notices) toast.push({ level: "warn", text: n });
    if (!pc.ok) { toast.push({ level: "danger", text: pc.errors.join(" ") }); return; }
    const o = pc.order;
    const args: SubmitOrderArgs = { venue, symbol, side: o.side, type: o.type, tif: o.tif, qty: o.qty, limitPrice: o.limitPrice, stopPrice: o.stopPrice };
    const tail = o.type === "MARKET" ? "MKT" : `${o.limitPrice.toFixed(QUOTE_DECIMALS)} ${abbrevType(o.type)}`;
    const flash = `${sideLabel(o.side)} ${o.qty.toLocaleString("en-US")} ${bareSymbol(symbol)} @ ${tail}`;
    void oc.submit(args, flash);
  };

  const firePreset = (t: PlaceOrderTemplate) => {
    if (venue === "" || !quote) { toast.push({ level: "danger", text: "No venue/quote for preset." }); return; }
    const r = resolvePlaceTemplate(t, { venue, symbol, quote, buyingPower, positionQty, nowMs: Date.now() });
    for (const n of r.preCheck.notices) toast.push({ level: "warn", text: n });
    if (!r.preCheck.ok) { toast.push({ level: "danger", text: r.preCheck.errors.join(" ") }); return; }
    void oc.submit(r.args, r.flash);
  };

  // Clickable inline bid/ask in the header blotter line (replaces the old Bid/Ask
  // button row). No quote => em dash, click no-ops.
  const quoteFill = (value: number | undefined) => { if (value !== undefined) setPrice(value.toFixed(QUOTE_DECIMALS)); };
  const priceSpan = (testid: string, value: number | undefined, tone: string) => (
    <span data-testid={testid} onClick={() => quoteFill(value)}
      style={{ color: tone, cursor: value === undefined ? "default" : "pointer" }}>
      {value === undefined ? "—" : formatPrice(value, QUOTE_DECIMALS)}
    </span>
  );
  const sideClass = (s: Side) => `side${s !== side ? "" : s === "BUY" ? " side-selected-buy" : " side-selected"}`;
  const ctl = { flex: 1 } as const;

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 3, padding: 6, height: "100%", background: palette.surface, color: palette.text, fontSize: 12, overflow: "auto" }}>
      {/* Strip 1 — header blotter line */}
      <div style={{ display: "flex", alignItems: "baseline", gap: 6 }}>
        <strong className="serif" style={{ fontSize: 14 }}>{bareSymbol(symbol)}</strong>
        <span className="mono" style={{ fontSize: 12 }}>
          {priceSpan("bid", quote?.bid, palette.up)}
          <span style={{ color: palette.textMuted }}>/</span>
          {priceSpan("ask", quote?.ask, palette.down)}
        </span>
        <div style={{ flex: 1 }} />
        <select data-testid="venue" className="ctl mono" value={venue} onChange={(e) => selectVenue(e.target.value)}>
          {venues.map((v) => <option key={v} value={v}>{v}</option>)}
        </select>
        <button data-testid="open-settings" className="btn" onClick={() => openSettings?.openOrderSettings()}>⚙</button>
      </div>
      {/* Strip 2 — side row */}
      <div style={{ display: "flex", gap: 3 }}>
        {SIDES.map((s) => (
          <button key={s} type="button" className={sideClass(s)} onClick={() => setSide(s)}>{s}</button>
        ))}
      </div>
      {/* Strip 3 — type · tif · price · stop */}
      <div style={{ display: "flex", gap: 3 }}>
        <select data-testid="order-type" className="ctl mono" value={type} onChange={(e) => setType(e.target.value as OrderType)} style={ctl}>
          {TYPES.map((t) => <option key={t} value={t}>{abbrevType(t)}</option>)}
        </select>
        <select className="ctl mono" value={tif} onChange={(e) => setTif(e.target.value as TIF)} style={ctl}>
          {TIFS.map((t) => <option key={t} value={t}>{t}</option>)}
        </select>
        <input data-testid="price" className="ctl mono" value={price} onChange={(e) => setPrice(e.target.value)} disabled={type === "MARKET"} placeholder="price" style={ctl} />
        <input data-testid="stop" className="ctl mono" value={stop} onChange={(e) => setStop(e.target.value)} disabled={!hasStop} placeholder="stop" style={{ ...ctl, opacity: hasStop ? 1 : 0.4 }} />
      </div>
      {/* Strip 4 — qty · mode · submit */}
      <div style={{ display: "flex", gap: 3 }}>
        <input data-testid="amount" className="ctl mono" value={amount} onChange={(e) => setAmount(e.target.value)} style={{ width: 64 }} />
        <select data-testid="mode" className="ctl mono" value={mode} title={mode} onChange={(e) => setMode(e.target.value as SizingMode)} style={{ width: 56 }}>
          {MODES.map((m) => <option key={m} value={m} title={m}>{MODE_LABEL[m]}</option>)}
        </select>
        <button data-testid="submit" className="btn btn-primary" onClick={submitManual} style={{ flex: 1, fontWeight: 700 }}>
          {side} {bareSymbol(symbol)}
        </button>
      </div>
      {/* Strip 5 — preset chips (only when presets exist) */}
      {presets.length > 0 && (
        <div style={{ display: "flex", flexWrap: "wrap", gap: 3 }}>
          {presets.map((t) => (
            <button key={t.id} data-testid={`preset-${t.id}`} className="btn" onClick={() => firePreset(t)}>{t.label}</button>
          ))}
        </div>
      )}
    </div>
  );
}
```

Key removals vs the old file: the `VenueID` import and manual `venue`/`venues`/`setActiveVenue` derivation (now via `useVenueSelection`), `vStatus`/`armed`, the `quoteBtn` helper, the Bid/Ask button row, the labeled Price row, the `ticket-armed-state` block, the full-width Submit row, and the trailing Cancel All + KILL block.

- [ ] **Step 4: Update the registry description**

In `ui/src/chrome/panels/registry.tsx`, the `order-ticket` entry description:

```tsx
  description: "Compact entry, presets, sizing",
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd ui && npx vitest run src/chrome/panels/OrderTicketPanel.test.tsx`
Expected: PASS. Also typecheck: `cd ui && npx tsc --noEmit` (no unused `VenueID` import, no dangling refs).

- [ ] **Step 6: Commit**

```bash
cd ui && git add src/chrome/panels/OrderTicketPanel.tsx src/chrome/panels/OrderTicketPanel.test.tsx src/chrome/panels/registry.tsx
git commit -m "feat(ui/ticket): dense-strips redesign, group-aware venue, drop KILL/Cancel All/armed chrome"
```

---

### Task 9: `useHotkeys` — full venue resolution chain

**Files:**
- Modify: `ui/src/chrome/exec/useHotkeys.ts:26`
- Test: `ui/src/chrome/exec/useHotkeys.test.tsx`

**Interfaces:**
- Consumes: `resolveVenue` (Task 7).

- [ ] **Step 1: Write the failing test**

Add a self-contained test to `ui/src/chrome/exec/useHotkeys.test.tsx` proving the hotkey fires at the group's focused venue (the latent mismatch this closes). The file's existing `setup(...)` returns only `{ sent }` (it does **not** expose `linkGroups`) and seeds a single `alpaca-paper` venue, so rather than change `setup`'s signature (other tests depend on it), inline a two-venue harness that mirrors `setup`'s body. `beforeEach` already mocks `document.hasFocus` → true; the place-hotkey combo is `{ key: "1", ctrlKey: true }` (from the existing tests); `Harness`, `makeStores`, `LinkGroups`, `BroadcastChannelBus`, `OrderConfigProvider`, `ThemeProvider`, `ToastProvider`, `AckMsg`, `ExecStatus` are all already imported:

```ts
it("fires the place hotkey at the group's focused venue, not just the first venue", async () => {
  const stores = makeStores();
  const sent: Array<{ name: string; args: unknown }> = [];
  const commands = { sendCommand: vi.fn(async (n: string, a: unknown): Promise<AckMsg> => { sent.push({ name: n, args: a }); return { kind: "ack", corrId: "c", status: "accepted", orderId: "ETX", value: undefined }; }) };
  const linkGroups = new LinkGroups(new BroadcastChannelBus(), () => {});
  const twoArmed: ExecStatus = {
    masterArmed: true, global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 },
    venues: [
      { venue: "alpaca-paper", broker: "alpaca", connected: true, venueArmed: true, reconcilePending: false, note: "", lastReconcileMs: null, gate: { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 } },
      { venue: "tradezero", broker: "tradezero", connected: true, venueArmed: true, reconcilePending: false, note: "", lastReconcileMs: null, gate: { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 } },
    ],
  };
  stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: twoArmed });
  stores.exec.apply({ kind: "snapshot", topic: "exec.account" as never, key: "tradezero", payload: { venue: "tradezero", equity: 100, buyingPower: 100000, availableCash: 100, sodEquity: 100, realized: 0, dayPnl: 0, leverage: 4, tsMs: 1 } });
  stores.quote.apply({ kind: "snapshot", topic: "md.quote" as never, payload: { symbol: "US.AAPL", bid: 3.4, ask: 3.5, last: 3.45, ts: "" } });
  linkGroups.focus("green", "US.AAPL");
  linkGroups.focusVenue("green", "tradezero"); // green group's venue is the SECOND one
  render(
    <ThemeProvider><ToastProvider><OrderConfigProvider commands={commands}>
      <Harness stores={stores} commands={commands} linkGroups={linkGroups} group="green" />
    </OrderConfigProvider></ToastProvider></ThemeProvider>,
  );
  await act(async () => { fireEvent.keyDown(window, { key: "1", ctrlKey: true }); await Promise.resolve(); });
  const submit = sent.find((s) => s.name === "SubmitOrder");
  expect(submit).toBeTruthy();
  expect((submit!.args as { venue: string }).venue).toBe("tradezero");
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd ui && npx vitest run src/chrome/exec/useHotkeys.test.tsx`
Expected: FAIL — today the hotkey resolves `config.activeVenue || venues[0]`, ignoring the group's focused venue, so `SubmitOrder.venue` is `alpaca-paper`, not `tradezero`.

- [ ] **Step 3: Switch to the full chain**

In `ui/src/chrome/exec/useHotkeys.ts`, add the import:

```ts
import { resolveVenue } from "./venueSelection";
```

Replace the venue line inside `onKey` (line 26):

```ts
      const venue = resolveVenue(group, linkGroups, config.activeVenue, status);
```

(`group`, `linkGroups`, `config`, and `status` are all already in scope in the handler.)

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd ui && npx vitest run src/chrome/exec/useHotkeys.test.tsx`
Expected: PASS (new test + existing armed/disarmed/management tests).

- [ ] **Step 5: Commit**

```bash
cd ui && git add src/chrome/exec/useHotkeys.ts src/chrome/exec/useHotkeys.test.tsx
git commit -m "fix(ui/hotkeys): resolve venue via the shared chain (group venue wins)"
```

---

### Task 10: `AccountPanel` — venue dropdown, venue-scoped stats/positions, remove master button

**Files:**
- Modify: `ui/src/chrome/panels/AccountPanel.tsx`
- Test: `ui/src/chrome/panels/AccountPanel.test.tsx`

**Interfaces:**
- Consumes: `useVenueSelection(group, linkGroups, stores)` (Task 7). `AccountPanel` starts consuming the `linkGroups` and `group` props already present on `PanelProps`.
- Removes: `data-testid="arm-toggle"` (the duplicate master button — TopBar's `arm-chip` owns master arm). Keeps per-venue chips (`venue-arm-*`, all venues).

- [ ] **Step 1: Update the tests**

This is the largest test migration in the plan. Because the rewritten `AccountPanel` calls `useVenueSelection` (→ `useOrderConfig` → requires an `OrderConfigProvider`; → `linkGroups.subscribe`/`.venueFor`) and filters positions/stats by the resolved venue, three harness fixes are required or **every** test throws, plus several tests change. Edit `ui/src/chrome/panels/AccountPanel.test.tsx` as follows.

**A. Harness fixes (affect all tests):**

Add imports at the top:

```tsx
import { OrderConfigProvider } from "../exec/useOrderConfig";
import { LinkGroups } from "../linkGroups";
import { FakeBus, FakeBusHub } from "../../test/fakes";
import type { LinkGroup } from "../linkGroups";
```

Rewrite `mkProps` — replace the dead `over: Partial<PanelProps>` param (no current test uses it) with a `group` param, give `linkGroups` a real instance by default, and return it:

```tsx
function mkProps(group: LinkGroup = null) {
  const stores = makeStores();
  const sent: Array<{ name: string; args: unknown }> = [];
  const configChanges: Array<Record<string, unknown>> = [];
  const commands = {
    sendCommand: vi.fn(async (name: string, args: unknown): Promise<AckMsg> => { sent.push({ name, args }); return { kind: "ack", corrId: "c", status: "accepted" }; }),
    sendQuery: vi.fn(async () => []),
  };
  const linkGroups = new LinkGroups(new FakeBus(new FakeBusHub()), () => {});
  const props = {
    config: { id: "t-account", panelId: "account", group, settings: {} },
    stores, scheduler: {} as never, width: 800, height: 400, linkGroups, commands,
    onConfigChange: (s: Record<string, unknown>) => configChanges.push(s),
  } as PanelProps;
  return { props, stores, sent, configChanges, linkGroups };
}
```

Wrap with `OrderConfigProvider` (uses the props' own commands stub):

```tsx
function wrap(props: PanelProps) {
  return render(
    <ThemeProvider><ToastProvider><OrderConfigProvider commands={props.commands}>
      <AccountPanel {...props} />
    </OrderConfigProvider></ToastProvider></ThemeProvider>,
  );
}
```

Extend the `status` fixture to accept venue ids (default preserves existing single-venue callers):

```tsx
const status = (masterArmed: boolean, ...venueIds: string[]): ExecStatus => ({
  masterArmed, global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 },
  venues: (venueIds.length ? venueIds : ["alpaca-paper"]).map((venue) => ({
    venue, broker: "alpaca", connected: true, venueArmed: true, reconcilePending: false,
    note: "", lastReconcileMs: null, gate: { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 },
  })),
});
```

**B. Delete these four tests** (their premises contradict the new design):
- `"aggregates day P&L across venues and shows armed state"` — summed cross-venue P&L and the removed `arm-toggle` button.
- `"arm toggle sends Disarm when currently armed"` — `arm-toggle` removed.
- `"styles the master arm chip bronze (accent) when armed, muted when disarmed…"` — `arm-toggle` removed. (Keep `hexToRgb`/`LIGHT` — the per-venue-chip color test below still uses them.)
- `"renders per-venue and net rows with colored unrealized P&L"` — NET rows are now filtered out (replaced by the new filter test in C).

**C. Add these two tests:**

```tsx
it("scopes stats to the selected venue", () => {
  const { props, stores, linkGroups } = mkProps("green");
  act(() => {
    stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status(false, "alpaca-paper", "alpaca-live") });
    stores.exec.apply({ kind: "snapshot", topic: "exec.account" as never, key: "alpaca-paper", payload: acct("alpaca-paper", { equity: 99 }) });
    stores.exec.apply({ kind: "delta", topic: "exec.account" as never, key: "alpaca-live", payload: acct("alpaca-live", { equity: 12 }) });
    linkGroups.focusVenue("green", "alpaca-live");
  });
  wrap(props);
  expect(screen.getByTestId("acct-equity").textContent).toContain("12.00");
  fireEvent.change(screen.getByTestId("acct-venue"), { target: { value: "alpaca-paper" } });
  expect(screen.getByTestId("acct-equity").textContent).toContain("99.00");
});

it("filters positions to the selected venue and drops NET rows", () => {
  const { props, stores, linkGroups } = mkProps("green");
  act(() => {
    stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status(false, "alpaca-paper", "alpaca-live") });
    stores.exec.apply({ kind: "snapshot", topic: "exec.positions" as never, payload: [
      pos({ venue: "alpaca-paper", symbol: "US.AAPL" }),
      pos({ venue: "alpaca-live", symbol: "US.MSFT" }),
      pos({ venue: null, symbol: "US.AAPL" }), // NET aggregate
    ] });
    linkGroups.focusVenue("green", "alpaca-paper");
  });
  wrap(props);
  expect(screen.queryByTestId("pos-net")).toBeNull();
  expect(screen.getByText("AAPL")).toBeTruthy();
  expect(screen.queryByText("MSFT")).toBeNull();
});
```

**D. Seed a status in three kept tests that seed positions but no status** — with venue scoping, positions filter by the resolved venue, which is `""` (→ zero rows) unless a venue exists to resolve to. Add `stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status(true) })` to the `act(...)` block of each:
- `"flatten on a long row submits a SELL for the full qty (priced from the quote)"`
- `"defaults to sorting positions by unrealized P&L descending"`
- `"clicking the Qty column header sorts by qty and persists the sort via onConfigChange"`

**E. No change needed** (already seed `exec.status`, and per-venue chips are unchanged): `"shows — for equity before any account snapshot arrives"` (no status → venue `""` → equity `—`, still correct), `"arms a venue when its per-venue control is clicked"`, `"disarms a venue when its per-venue control is clicked while armed"`, `"clicking one venue's control does not affect another venue's state or dispatch"`, `"styles per-venue arm chips bronze/muted rather than green/amber"`, `"annotates Flatten with the venue's armed state but keeps it clickable when disarmed"`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd ui && npx vitest run src/chrome/panels/AccountPanel.test.tsx`
Expected: FAIL — the two new tests fail against the current (un-rewritten) panel: `getByTestId("acct-venue")` throws (no venue dropdown yet), and `queryByTestId("pos-net")` is non-null (NET rows still render). The harness fixes (OrderConfigProvider wrap, real `linkGroups`) and the status-seed additions keep the kept tests green.

- [ ] **Step 3: Rewrite the panel for venue scoping**

In `ui/src/chrome/panels/AccountPanel.tsx`:

Add the import:

```tsx
import { useVenueSelection } from "../exec/venueSelection";
```

Change `StatsStrip` to take the venue selection and scope to it. Replace the `StatsStrip` signature and its stats derivation + the master button:

```tsx
function StatsStrip({
  stores, oc, palette, venue, venues, selectVenue,
}: {
  stores: PanelProps["stores"];
  oc: ReturnType<typeof useOrderCommands>;
  palette: ReturnType<typeof useTheme>["palette"];
  venue: string;
  venues: string[];
  selectVenue: (v: string) => void;
}): JSX.Element {
  const status = stores.exec.status();
  const account = stores.exec.accounts().find((a) => a.venue === venue);
  const equity = account?.equity ?? null;
  const bp = account?.buyingPower ?? null;
  const dayPnl = account?.dayPnl ?? null;
  const realized = account?.realized ?? null;

  const cell = (label: string, testid: string, value: string, tone?: number) => (
    <div style={{ display: "flex", flexDirection: "column", padding: "2px 10px" }}>
      <span style={{ fontSize: 10, color: palette.textMuted }}>{label}</span>
      <span data-testid={testid} className="mono" style={{ fontSize: 13, color: tone === undefined ? palette.text : tone >= 0 ? palette.up : palette.down }}>{value}</span>
    </div>
  );
  const dot = (ok: boolean, title: string) => (
    <span title={title} style={{ width: 8, height: 8, borderRadius: 8, background: ok ? palette.ok : palette.danger, display: "inline-block" }} />
  );

  return (
    <div style={{ display: "flex", alignItems: "center", gap: 4, padding: "4px 8px", background: palette.surface, borderBottom: `1px solid ${palette.border}` }}>
      <select data-testid="acct-venue" className="ctl mono" value={venue} onChange={(e) => selectVenue(e.target.value)}>
        {venues.map((v) => <option key={v} value={v}>{v}</option>)}
      </select>
      {cell("Equity", "acct-equity", money(equity))}
      {cell("Buying Power", "acct-bp", money(bp))}
      {cell("Day P&L", "acct-daypnl", money(dayPnl), dayPnl ?? 0)}
      {cell("Realized", "acct-realized", money(realized), realized ?? 0)}
      <div style={{ flex: 1 }} />
      {/* Per-venue arm chips stay all-venue (TopBar's arm-chip owns master arm;
          the old duplicate master ARMED button is removed). */}
      <div style={{ display: "flex", gap: 6, alignItems: "center", padding: "0 8px" }}>
        {(status?.venues ?? []).map((v) => (
          <button key={v.venue} data-testid={`venue-arm-${v.venue}`} data-armed={v.venueArmed}
            title={`${v.venue}: ${v.connected ? "connected" : "disconnected"} — click to ${v.venueArmed ? "disarm" : "arm"}`}
            onClick={() => (v.venueArmed ? oc.disarm(v.venue) : oc.arm(v.venue))}
            style={{
              display: "flex", gap: 3, alignItems: "center", fontSize: 10, cursor: "pointer",
              background: "transparent", border: `1px solid ${v.venueArmed ? palette.accent : palette.borderStrong}`,
              borderRadius: 4, padding: "2px 6px", color: v.venueArmed ? palette.accent : palette.textMuted,
            }}>
            {dot(v.connected, `${v.venue}: ${v.connected ? "connected" : "disconnected"}`)}{v.venue}{v.venueArmed ? " ●" : " ○"}
          </button>
        ))}
      </div>
    </div>
  );
}
```

Change `PositionsTable` to filter to the selected venue. Update its signature to accept `venue` and filter `rows0`:

```tsx
function PositionsTable({
  stores, commands, oc, palette, config, onConfigChange, venue,
}: {
  stores: PanelProps["stores"];
  commands: PanelProps["commands"];
  oc: ReturnType<typeof useOrderCommands>;
  palette: ReturnType<typeof useTheme>["palette"];
  config: PanelProps["config"];
  onConfigChange: PanelProps["onConfigChange"];
  venue: string;
}): JSX.Element {
  const toast = useToasts();
  const rows0 = stores.exec.positions().filter((p) => p.venue === venue); // venue-scoped; NET (venue===null) rows drop out
  const status = stores.exec.status();
  // ...rest unchanged (sort, openCount = rows0.length, clickSort, flatten, render)
```

`openCount` becomes `rows0.length` (all filtered rows are single-venue). The `net`/`pos-net` branch in the render is now unreachable (no `venue===null` rows survive the filter) but harmless — leave it.

Finally, wire the venue selection in the top-level `AccountPanel`:

```tsx
export function AccountPanel({ config, stores, commands, onConfigChange, linkGroups, group: groupProp }: PanelProps): JSX.Element {
  const { palette } = useTheme();
  const toast = useToasts();
  const oc = useOrderCommands(commands, stores.exec, toast);
  useSyncExternalStore((cb) => stores.exec.subscribe(cb), () => stores.exec.getSnapshot());
  const group = groupProp ?? config.group;
  const { venue, venues, selectVenue } = useVenueSelection(group, linkGroups, stores);

  return (
    <div style={{ height: "100%", display: "flex", flexDirection: "column", background: palette.bg, color: palette.text, fontFamily: "inherit" }}>
      <StatsStrip stores={stores} oc={oc} palette={palette} venue={venue} venues={venues} selectVenue={selectVenue} />
      <PositionsTable stores={stores} commands={commands} oc={oc} palette={palette} config={config} onConfigChange={onConfigChange} venue={venue} />
    </div>
  );
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd ui && npx vitest run src/chrome/panels/AccountPanel.test.tsx`
Expected: PASS. Typecheck: `cd ui && npx tsc --noEmit`.

- [ ] **Step 5: Commit**

```bash
cd ui && git add src/chrome/panels/AccountPanel.tsx src/chrome/panels/AccountPanel.test.tsx
git commit -m "feat(ui/account): group-aware venue dropdown, venue-scoped stats/positions, drop duplicate master button"
```

---

### Task 11: persist venue link state (`linkVenues` workspace key)

**Files:**
- Modify: `ui/src/chrome/workspace.ts:9-18` (`Workspace` type)
- Modify: `ui/src/chrome/AppShell.tsx:64-74` (hydrate) + `:115-124` (persist)
- Test: covered by `linkGroups.test.ts` (Task 6, hydrateVenues/snapshotVenues round-trip). AppShell wiring is verified by the full-suite run + manual boot (Task 12 verification).

**Interfaces:**
- Consumes: `LinkGroups.hydrateVenues` / `.snapshotVenues` (Task 6).
- Produces: `Workspace.linkVenues?` persisted alongside `groups`. The existing `groups` symbol map shape is untouched, so old workspace docs load with no migration.

- [ ] **Step 1: Add the `linkVenues` field to the `Workspace` type**

In `ui/src/chrome/workspace.ts`, add a `VenueID` import (the `import type { LinkGroup } from "./linkGroups";` line already exists at line 1 — do NOT re-add it, that is a duplicate-identifier error):

```ts
import type { VenueID } from "../wire/contract";
```

Then extend the `Workspace` interface (add after `groups?`):

```ts
  groups?: Partial<Record<Exclude<LinkGroup, null>, string>>;
  // Per-link-group focused venue (LinkGroups.focusedVenues), persisted beside
  // `groups`. Optional: absent in any workspace doc saved before this field.
  linkVenues?: Partial<Record<Exclude<LinkGroup, null>, VenueID>>;
```

- [ ] **Step 2: Hydrate venues on load**

In `ui/src/chrome/AppShell.tsx`, in the load effect, hydrate venues right after the symbol hydrate:

```ts
      linkGroups.hydrate(w.groups ?? {});
      linkGroups.hydrateVenues(w.linkVenues ?? {});
      setWs(w);
```

- [ ] **Step 3: Persist venues on change**

In the same file's persist subscription effect, include `linkVenues` in the saved doc (the subscription already fires on any `linkGroups` change, symbol or venue):

```ts
      const next = { ...current, groups: linkGroups.snapshot(), linkVenues: linkGroups.snapshotVenues() };
```

- [ ] **Step 4: Verify typecheck + full UI suite**

Run: `cd ui && npx tsc --noEmit && npm test`
Expected: PASS (whole vitest suite). If the forks-pool interferes with any canvas file, re-run the affected file individually.

- [ ] **Step 5: Commit**

```bash
cd ui && git add src/chrome/workspace.ts src/chrome/AppShell.tsx
git commit -m "feat(ui/workspace): persist per-group focused venue in linkVenues"
```

---

### Task 12: configure venues in `~/.eTape/config.toml` (local, not committed)

**Files:**
- Modify: `~/.eTape/config.toml` (Earl's local machine — **not** in the repo; there is no committed example config to edit).

This step makes the feature actually work end-to-end: with zero `[[venue]]` entries the venue dropdowns are empty (the exact bug this design closes). It is manual because it touches live credentials and Earl's machine.

- [ ] **Step 1: Add the four venues + explicit gate caps**

Append to `~/.eTape/config.toml` (gate zero-values mean **unenforced**, so live venues carry deliberately tiny caps matching the standing 1-share live guardrail — edit to taste):

```toml
[[venue]]
id = "alpaca-paper"
broker = "alpaca"
env = "paper"
credentials = "alpaca"
auto_arm = true

[[venue]]
id = "alpaca-live"
broker = "alpaca"
env = "live"
credentials = "alpaca-live"

[[venue]]
id = "tradezero"
broker = "tradezero"
env = "live"
credentials = "tradeZero"
account_id = "<TZ accountId — required, adapter errors without it; verify via read-only accounts endpoint>"

[[venue]]
id = "moomoo"
broker = "moomoo"
auto_arm = true   # harmless: stub rejects submits regardless

[gate.global]
max_day_loss = 100.0

[gate.venue.alpaca-live]
max_order_value = 50.0
max_position_value = 50.0
max_position_shares = 5
max_open_orders = 2

[gate.venue.tradezero]
max_order_value = 50.0
max_position_value = 50.0
max_position_shares = 5
max_open_orders = 2
```

- [ ] **Step 2: Fill the TradeZero `account_id`**

Replace the `account_id` placeholder with the real TradeZero accountId. Verify it via a **read-only** accounts query (the moomoo skill scripts / TZ read-only accounts endpoint) — do not place any order. The adapter hard-errors on boot without it.

- [ ] **Step 3: Boot the engine and verify (read-only)**

Run the engine (`cd engine && go run ./cmd/etape` or the project's normal run path) with OpenD up. Confirm in logs/UI:
- Boot no longer errors on the moomoo venue (it registers as a disconnected stub).
- The order-ticket and Account-panel venue dropdowns list all four venues.
- The TopBar master chip shows **ARMED** at boot (because paper venues auto-arm), and the moomoo per-venue chip shows disconnected with an "execution v1.x" note; live venue chips show disarmed.
- Alpaca-live and TradeZero authenticate for read-only polling only.

**Do not place, modify, or cancel any live order.** Live venues boot disarmed by design; leave them disarmed unless Earl authorizes a live test in the running conversation.

- [ ] **Step 4: No commit** — this file is local and outside the repo.

---

## Self-Review

**Spec coverage** (each §, mapped to a task):
- §1 dense-strips ticket + removed KILL/Cancel All/armed chrome + registry description → Task 8. ✓ (5 strips: header blotter, side row, type/tif/price/stop, qty/mode/submit, preset chips; stop permanently rendered + disabled/dimmed; bid/ask click-to-fill moved to header; submit label `{side} {bareSymbol}`.)
- §2 link groups carry a venue (`LinkMsg` gains `venue?`, `focusVenue`/`venueFor`, no engine echo, resolution chain, pinned vs grouped) → Tasks 6, 7, 9. ✓
- §3 venue dropdown in both panels (from `status().venues`; ticket group-aware; Account panel stats/positions venue-scoped; per-venue chips stay all-venue; duplicate master button removed) → Tasks 8, 10. ✓
- §4 auto-arm paper / manual live (engine mechanism unchanged; `AutoArm` config; boot arm from it; ticket shows no arm UI; AccountPanel master button removed; TopBar chip owns master) → Tasks 1, 2, 5, 8, 10. ✓
- §5 moomoo stub venue (registers, `connected=false`, note "execution v1.x", rejects submit/cancel, no Run loop) → Tasks 3, 4, 5. ✓
- §6 config (four venues + gate caps + `auto_arm`) → Task 12. ✓
- §7 test impact (ticket kill/cancel/armed deletion + bid/ask retarget + stop-disabled + venue group-sync; AccountPanel master-button removal + venue-scoped; linkGroups focusVenue/venueFor + bus round-trip + linkVenues hydrate/snapshot + old-doc compat; engine auto_arm parse + boot arm state + moomoo stub listed/rejected + boot no longer errors) → Tasks 1–11 tests. ✓

**Placeholder scan:** no TBD/"add error handling"/"similar to Task N" — every code step shows complete code; test steps that adapt to existing fixtures name the exact fixtures/helpers and what to extend.

**Type consistency:** `AutoArm` (Go) / `auto_arm` (TOML) consistent across config → CoreConfig → boot → uihub. `focusVenue`/`venueFor`/`hydrateVenues`/`snapshotVenues` names match between Task 6 (definition) and Tasks 7/11 (use). `resolveVenue`/`useVenueSelection` signatures match between Task 7 (definition) and Tasks 8/9/10 (use). `VenueMeta.AutoArm`/`.Note` added to both the public (`api.go`) and internal (`mirror.go`) `venueMeta` types and copied between them in `New`. `wsmsg.VenueStatus` fields used (`venueArmed`, `connected`, `note`) already exist — no contract change.

**Known pre-existing behavior (out of scope, noted so a reviewer isn't surprised):** the exec `Core.emitStatus` hard-codes `Connected: true` for every venue, so the first master arm/disarm/kill after boot will flip the moomoo stub's `connected` to `true` in the UI. At boot (the state §5 describes) moomoo is correctly `connected=false`. Fixing the hard-coded connected flag is a separate concern not in this spec.

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-07-08-order-ticket-venue-arm.md`. Two execution options:

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
