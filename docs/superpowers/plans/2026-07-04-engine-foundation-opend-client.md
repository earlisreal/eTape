# eTape Engine — Plan 1 of 6: Foundation & OpenD Protocol Client

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stand up the `engine/` Go module and the lowest layer of the market-data plane — a raw-TCP OpenD protocol client that connects to `127.0.0.1:11111`, speaks the verified 44-byte framing + protobuf, completes the `InitConnect`/`KeepAlive` lifecycle, correlates request/response by serial number, dispatches pushes by protoID, and survives disconnects with jittered backoff — all validated against golden frames captured from the Python SDK and an in-process mock OpenD server.

**Architecture:** One Go module in `engine/`, sibling of `ui/`. Strict dependency direction: domain packages (`feed`, `md`, `exec`, `session` — later plans) never import adapters (`feed/opend`), `uihub`, or `store`. This plan builds the `feed/opend` adapter bottom-up: a pure byte codec (`frame.go`), the generated protobuf bindings (`pb/`), then the connection client that composes them. Everything time-dependent takes a `clock.Clock` so replay (Plan 3) is a seam, not a rewrite. Encryption is never implemented — localhost OpenD runs plaintext by default, and a plaintext-only codec keeps the client structurally simple and trade-incapable.

**Tech Stack:** Go 1.23+, `google.golang.org/protobuf` (proto2 runtime), `protoc` + `protoc-gen-go` (generation), `github.com/BurntSushi/toml` (bootstrap config), `golangci-lint`, `go test -race`. Python 3 (pyenv) + the installed `moomoo` SDK for one-time golden-frame capture only.

## Global Constraints

Copied verbatim (or tightly paraphrased) from the three approved specs. Every task's requirements implicitly include this section.

- **Module path:** `github.com/earlisreal/eTape/engine`. All imports are `github.com/earlisreal/eTape/engine/...`. (repo remote: `github.com/earlisreal/eTape`)
- **Dependency rule:** domain packages (`feed`, `md`, `exec`, `session`) never import adapters (`feed/opend`, `broker/*`), `uihub`, or `store`. Adapters translate the outside world into domain events; a new data source or broker is a new adapter only. (go-engine-design §Dependency rule)
- **Single-writer core (future plans):** exactly one goroutine owns market-data / exec domain state; everything I/O-shaped is its own goroutine that only passes messages. `go test -race` enforces "no shared domain state" as a checked invariant, not a convention. This plan's client is pure I/O-edge code; it holds no domain state. (go-engine-design §Single-writer core)
- **Trade-incapability rule:** the feed connection implements **no `Trd_*` protocols** and **never** implements `Trd_UnlockTrade` (2005) — the trade password never touches eTape. Order writes live only in `broker/moomoo` (a later plan's separate connection). This plan touches only quote/system protoIDs. (CLAUDE.md; multi-broker-execution-design §Ripple effects)
- **Encryption:** OpenD runs plaintext on localhost by default (verified: `is_encrypt=False`, `INIT_RSA_FILE=''`). eTape never implements body encryption or RSA/AES. The header layout is identical either way; only `body_len`/body bytes would differ. (CLAUDE.md; go-engine-design §feed/opend)
- **Wire framing (verified 2026-07-03, byte-exact from SDK `common/constant.py:319`, `common/utils.py`):** 44-byte little-endian, `pack(1)` (no padding) header: `"FT"` magic (two bytes `0x46 0x54`) · u32 protoID · u8 fmtType (**0 = protobuf**, 1 = JSON) · u8 protoVer (**0**) · u32 serialNo · u32 bodyLen · 20-byte **raw** SHA1 of the (plaintext) body · 8 reserved bytes (**all zero**) — then the body. SHA1 is `sha1(body_bytes).digest()` (20 raw bytes, not hex), computed over the exact serialized protobuf bytes. (go-engine-design §feed/opend)
- **Determinism / testability:** components that need time take a `clock.Clock`, never `time.Now` directly. (go-engine-design §Clock)
- **Generated code is committed:** the protobuf bindings under `internal/feed/opend/pb/` and the vendored `.proto` sources are checked in; regeneration is scripted and reproducible. (go-engine-design §feed/opend, §Open items)
- **Repo is PUBLIC; sensitive-sweep every commit.** No account identifiers or credentials in checked-in fixtures. In particular `InitConnect` **S2C** carries `loginUserID` and `connID` — do **not** commit a real `InitConnect` S2C frame; capture it only for local verification, or synthesize one. Market-data frames and `KeepAlive` carry no account data and are safe to commit. Credentials stay in `~/.eJournal/credentials.json` (untouched by this plan). (memory: repo public; go-engine-design §store)
- **Config:** bootstrap config is a TOML file at `~/.eTape/config.toml`; this plan reads only the `[opend]` section (host/port). The file grows section-by-section in later plans; a missing file yields defaults. (go-engine-design §store)
- **CI gates:** `go build ./...`, `go vet ./...`, `go test -race ./...`, and `golangci-lint run` must all pass. (go-engine-design §Testing)

---

## Plan sequence (6 plans)

Each plan produces working, independently-testable software and depends only on the ones before it. The engine has two halves — the **market-data plane** and the **execution subsystem** — that meet only at `uihub` and at the price-mark events md sends exec; the sequence builds shared foundations first, then each half, then the hub that joins them.

1. **Foundation & OpenD Protocol Client** (this plan) — `engine/` module, CI, bootstrap config, `clock`, generated protobuf, the 44-byte frame codec, golden-frame corpus + mock OpenD server, and the connection client (InitConnect/KeepAlive lifecycle, serial correlation, push dispatch, backoff reconnect). **Deliverable:** `etape` connects to live OpenD, handshakes, keepalives, logs pushes, and reconnects on drop; codec is byte-validated against real captured frames.
2. **Market-Data Core** — `feed` interface + `FeedEvent` union (wrapping Plan 1's client), `session` ET calendar, the subscription/quota manager (refcounted demands, 1-slot-per-(symbol,subtype), 60 s min, hysteresis, LRU eviction, batched `Qot_Sub`), backfill (current-kline / recent-ticks / order-book caches + `request_history_kline`), and the single-writer `md` core: books, tape ring, quotes, bars (10s-from-ticks port of `prototypes/tick_to_10s_bars.py`, 1m-from-`K_1M`, 5m/15m/30m/60m from 1m, daily fetched), and indicators (VWAP/EMA/SMA/MACD/volume/buy-sell delta). **Deliverable:** engine ingests live OpenD → maintains books/tape/bars/indicators, race-free and deterministic (`replay(events)==state`), verified by unit + property tests.
3. **Store, Journal & Replay** — one SQLite/WAL writer goroutine; the journal tee at the `Feed` boundary; `bars_1m`/`bars_daily` archives; `config`/`sys_events` tables; retention/prune-at-boot; and the `replay` package (journal-backed `Feed` + replay `clock` driven by event timestamps × speed). **Deliverable:** recording is always-on; `etape --replay <day> --speed N` reconstructs a session with byte-identical bars/indicators.
4. **Execution Core (multi-venue)** — `exec` domain (venue-keyed `Order`/`Fill`/`Position`/`AccountSnapshot`, ULID order IDs), the append-only event log, the venue-keyed single-writer fold (`replay(log)==state`) + cross-venue aggregates, the two-layer gate (master/venue arm → duplicate → per-venue caps → global caps → day-loss auto-disarm), the `Broker` interface + `Capabilities`, `exec_events`/`fills` tables (venue column), and `SimBroker`. **Deliverable:** exec core accepts commands, gates them, folds events, and persists — verified end-to-end against SimBroker with table-driven fold+gate tests.
5. **Broker Adapters** — `broker/tradezero` (REST+WS, normalization, fill derivation, reconnect/staleness, rate buckets, routes, account-refresh polling, emulated `ReplaceOrder`) and `broker/alpaca` (REST + `trade_updates` WS, binary/text + JSON/msgpack frames, native replace, `CancelAll`, `GET by client_order_id`). `broker/moomoo` is designed (multi-broker spec) but **deferred to v1.x** — its framing reuses Plan 1's codec. **Deliverable:** TZ-paper + Alpaca-paper adapters drive the exec core through a scripted lifecycle (limit → replace → cancel → kill) with golden-corpus adapter tests.
6. **uihub, Pollers & Main Wiring** — the `uihub` WS server (topics with snapshot-then-delta, per-class coalescing, correlation-ID commands, static `ui/dist` serving), `uihub/wsmsg` + `tygo` generation in CI (contract drift fails the build), the `scan`/`news`/`health` pollers, and `cmd/etape` full boot sequence (config → store → uihub → OpenD → pre-subscribe → exec) with per-stage backoff and graceful shutdown. **Deliverable:** the complete engine binary the UI plans' mock engine can be swapped for; enables replay+SimBroker+Playwright E2E.

**Cross-plan dependencies to flag:**

- **`opend.Client` → `feed.Feed`:** Plan 1 exposes a low-level client (`Request`, `Pushes()`, `State()` with `ConnUp`/`ConnDown`, `ConnID()`). Plan 2 wraps it in the adapter-agnostic `feed.Feed` interface (`Events() <-chan FeedEvent`, subscription demands, history/snapshot queries). The `ConnUp`/`ConnDown`/`Resynced` signals and push channel established here are the raw material for that wrapper.
- **Generated `pb/`:** Plan 1 generates and commits bindings for all 167 protos. Plans 2/5(-moomoo)/etc. consume additional message types from the same committed `pb/` — no regeneration needed unless the SDK is upgraded (re-run `scripts/vendor_protos.py` + `scripts/gen_proto.sh`).
- **Golden corpus:** Plan 1 establishes `scripts/capture_golden_frames.py` and the JSONL fixture format under `internal/feed/opend/testdata/golden/`. Plan 2 extends the corpus with subscribe/push/backfill frames captured the same way; Plan 5's moomoo adapter (v1.x) reuses the harness for `Trd_*` order frames.
- **`clock.Clock`:** Plan 1's interface is consumed by every later time-dependent component (session, coalescing tickers, delayed unsubscribe, pollers) and is the replay seam in Plan 3. The `FakeClock` needed for fully-deterministic timer tests lands in Plan 3; Plan 1's timer tests use the real clock with short, bounded intervals.

---

## File Structure (Plan 1)

```
engine/
  go.mod, go.sum
  .golangci.yml
  Makefile                                  build/test/lint/proto targets
  cmd/etape/
    main.go                       cmd     — minimal harness: load config, run client, log pushes (Plan 6 replaces)
  internal/
    config/
      config.go                   config  — TOML bootstrap loader; [opend] section only (grows later)
      config_test.go
    clock/
      clock.go                    clock   — Clock interface + System (real) impl
    feed/opend/
      pb/
        proto/*.proto             (vendored 167 protos, go_package rewritten — committed)
        <pkg>/*.pb.go             (generated bindings, committed: initconnect, keepalive, common, qotcommon, …)
        pb_smoke_test.go          — imports generated pkgs, round-trips a message
      protoid.go                  opend   — v1 protoID constants (documented from SDK constant.py)
      frame.go                    opend   — Frame type, Encode, Decode, FrameReader, codec errors
      frame_test.go
      golden_test.go              opend   — table test over testdata/golden/*.jsonl
      serial.go                   opend   — serialGen (atomic u32)
      pending.go                  opend   — request/response correlation registry
      pending_test.go
      client.go                   opend   — Client: New, Run (backoff reconnect), session, Request, send, Pushes, State
      lifecycle.go                opend   — initConnect, keepAliveLoop
      backoff.go                  opend   — jittered backoff
      client_test.go              opend   — tests against mockOpenD
      mock_opend_test.go          opend   — in-process mock OpenD server (test helper)
      testdata/golden/
        *.jsonl                   (checked-in real frames: KeepAlive c2s/s2c, InitConnect c2s, market-data frames)
        manifest.json             (provenance: SDK/OpenD version, capture date, encryption=off, proto_fmt=protobuf)
  scripts/
    vendor_protos.py                        copy SDK .proto → pb/proto/, rewrite go_package (re-runnable)
    gen_proto.sh                            protoc over pb/proto/ → pb/<pkg>/ (re-runnable)
    capture_golden_frames.py                one-time golden capture via SDK monkeypatch (needs live OpenD)
```

---

## Task 1: Bootstrap the `engine/` module + CI scaffold

**Files:**
- Create: `engine/go.mod`, `engine/.golangci.yml`, `engine/Makefile`
- Create: `engine/internal/buildinfo/buildinfo.go`, `engine/internal/buildinfo/buildinfo_test.go` (a trivial package so the toolchain has something to build/test/lint from step one)

**Interfaces:**
- Produces: a compiling, testable, lintable module rooted at `github.com/earlisreal/eTape/engine`.

- [ ] **Step 1: Verify / install the toolchain**

Run and confirm versions (install any that are missing — Go and protoc are not yet on PATH per repo check):

```bash
go version              # want go1.23 or newer
protoc --version        # want libprotoc 3.21+ (any 3.x/2x that supports proto2 — all do)
which protoc-gen-go     # installed below if absent
```

Install `protoc-gen-go` into the Go bin and ensure it is on PATH:

```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
export PATH="$PATH:$(go env GOPATH)/bin"   # add to shell profile if not already
```

On macOS, Go and protoc install cleanly via Homebrew (`brew install go protobuf`). Record the exact versions used in `engine/Makefile`'s header comment for reproducibility.

- [ ] **Step 2: Initialize the module**

```bash
mkdir -p engine && cd engine
go mod init github.com/earlisreal/eTape/engine
go get google.golang.org/protobuf@latest
go get github.com/BurntSushi/toml@latest
```

- [ ] **Step 3: Write a trivial package with a failing test**

`engine/internal/buildinfo/buildinfo.go`:

```go
// Package buildinfo exposes basic engine identity, and exists so the module
// has a real package to build, test, and lint from the first commit.
package buildinfo

// Name is the engine binary's canonical name.
const Name = "etape-engine"
```

`engine/internal/buildinfo/buildinfo_test.go`:

```go
package buildinfo

import "testing"

func TestNameIsSet(t *testing.T) {
	if Name != "etape-engine" {
		t.Fatalf("Name = %q, want %q", Name, "etape-engine")
	}
}
```

- [ ] **Step 4: Add the golangci-lint config**

`engine/.golangci.yml`:

```yaml
run:
  timeout: 3m
linters:
  enable:
    - errcheck
    - govet
    - ineffassign
    - staticcheck
    - unused
    - gofmt
    - goimports
    - misspell
issues:
  exclude-dirs:
    - internal/feed/opend/pb   # generated protobuf code is not linted
```

- [ ] **Step 5: Add a Makefile**

`engine/Makefile`:

```make
# Toolchain (record exact versions used):
#   go 1.23.x, protoc 3.21.x, protoc-gen-go (google.golang.org/protobuf) latest
.PHONY: build test lint vet proto vendor-proto run
build:
	go build ./...
test:
	go test -race ./...
vet:
	go vet ./...
lint:
	golangci-lint run
proto:
	./scripts/gen_proto.sh
vendor-proto:
	python3 ../scripts/vendor_protos.py   # see note: scripts live under repo-root scripts/
run:
	go run ./cmd/etape
```

> Note on script location: `.claude/skills/` is gitignored, so capture/generation scripts live under the repo (`engine/scripts/` or repo-root `scripts/`). This plan places them at `engine/scripts/` — adjust the Makefile paths to `./scripts/...` accordingly if you keep them under `engine/`.

- [ ] **Step 6: Run build, test, vet, lint — all green**

```bash
cd engine
go build ./... && go vet ./... && go test -race ./... && golangci-lint run
```

Expected: PASS (one test passes), no lint errors.

- [ ] **Step 7: Commit**

```bash
git add engine/go.mod engine/go.sum engine/.golangci.yml engine/Makefile engine/internal/buildinfo/
git commit -m "feat(engine): bootstrap Go module + CI scaffold"
```

---

## Task 2: Bootstrap config loader (TOML)

**Files:**
- Create: `engine/internal/config/config.go`
- Test: `engine/internal/config/config_test.go`

**Interfaces:**
- Produces: `config.Config{ OpenD config.OpenD }`; `config.OpenD{ Host string; Port int }` with `Addr() string`; `config.Default() Config`; `config.Load(path string) (Config, error)` — a missing file returns defaults, a malformed file returns an error.

- [ ] **Step 1: Write the failing test**

`engine/internal/config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "does-not-exist.toml"))
	if err != nil {
		t.Fatalf("Load: unexpected error %v", err)
	}
	if got := cfg.OpenD.Addr(); got != "127.0.0.1:11111" {
		t.Fatalf("default OpenD addr = %q, want 127.0.0.1:11111", got)
	}
}

func TestLoadOverridesOpenD(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte("[opend]\nhost = \"10.0.0.5\"\nport = 22222\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.OpenD.Addr(); got != "10.0.0.5:22222" {
		t.Fatalf("OpenD addr = %q, want 10.0.0.5:22222", got)
	}
}

func TestLoadMalformedFileErrors(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.toml")
	if err := os.WriteFile(p, []byte("[opend\nhost = "), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("Load: expected error for malformed TOML, got nil")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestLoad -v`
Expected: FAIL — `config.Load` / `config.OpenD` undefined.

- [ ] **Step 3: Write the implementation**

`engine/internal/config/config.go`:

```go
// Package config loads eTape's bootstrap TOML config (~/.eTape/config.toml).
// Only the sections the current plan needs are defined; the struct grows in
// later plans. A missing file yields defaults; a malformed file is an error.
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"

	"github.com/BurntSushi/toml"
)

// OpenD locates the local OpenD gateway.
type OpenD struct {
	Host string `toml:"host"`
	Port int    `toml:"port"`
}

// Addr returns host:port for net.Dial.
func (o OpenD) Addr() string { return net.JoinHostPort(o.Host, strconv.Itoa(o.Port)) }

// Config is the engine's bootstrap configuration.
type Config struct {
	OpenD OpenD `toml:"opend"`
}

// Default returns the built-in defaults used when a field or the whole file is absent.
func Default() Config {
	return Config{OpenD: OpenD{Host: "127.0.0.1", Port: 11111}}
}

// Load reads the TOML file at path over the defaults. A non-existent file is
// not an error (defaults are returned); a malformed file is.
func Load(path string) (Config, error) {
	cfg := Default()
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -v`
Expected: PASS (all three tests).

- [ ] **Step 5: Commit**

```bash
git add engine/internal/config/
git commit -m "feat(engine/config): TOML bootstrap loader with [opend] section"
```

---

## Task 3: Clock interface + System implementation

**Files:**
- Create: `engine/internal/clock/clock.go`
- Test: `engine/internal/clock/clock_test.go`

**Interfaces:**
- Produces: `clock.Clock` interface (`Now() time.Time`, `After(d) <-chan time.Time`, `NewTicker(d) Ticker`); `clock.Ticker` interface (`C() <-chan time.Time`, `Stop()`); `clock.System{}` — the real-time impl consumed by the client and every later time-dependent component. (`FakeClock` lands in Plan 3.)

- [ ] **Step 1: Write the failing test**

`engine/internal/clock/clock_test.go`:

```go
package clock

import (
	"testing"
	"time"
)

func TestSystemNowAdvances(t *testing.T) {
	var c System
	t0 := c.Now()
	time.Sleep(2 * time.Millisecond)
	if !c.Now().After(t0) {
		t.Fatal("System.Now did not advance")
	}
}

func TestSystemAfterFires(t *testing.T) {
	var c System
	select {
	case <-c.After(5 * time.Millisecond):
	case <-time.After(time.Second):
		t.Fatal("System.After did not fire within 1s")
	}
}

func TestSystemTickerFiresAndStops(t *testing.T) {
	var c System
	tk := c.NewTicker(5 * time.Millisecond)
	select {
	case <-tk.C():
	case <-time.After(time.Second):
		t.Fatal("ticker did not fire")
	}
	tk.Stop()
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/clock/ -v`
Expected: FAIL — `clock.System` / `Ticker` undefined.

- [ ] **Step 3: Write the implementation**

`engine/internal/clock/clock.go`:

```go
// Package clock abstracts wall-clock time behind an interface so time-dependent
// components (keepalive tickers, request timeouts, pollers, coalescing, replay)
// are deterministic under test. It is deliberately dependency-free so every
// domain and adapter package can import it without cycles.
//
// Spec note: go-engine-design lists Clock under feed/; it is hoisted here to its
// own package so time-dependent packages need not import the feed event types.
package clock

import "time"

// Ticker abstracts *time.Ticker.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

// Clock abstracts the parts of the time package the engine uses.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
	NewTicker(d time.Duration) Ticker
}

// System is the real-time Clock.
type System struct{}

func (System) Now() time.Time                         { return time.Now() }
func (System) After(d time.Duration) <-chan time.Time { return time.After(d) }
func (System) NewTicker(d time.Duration) Ticker       { return sysTicker{time.NewTicker(d)} }

type sysTicker struct{ t *time.Ticker }

func (s sysTicker) C() <-chan time.Time { return s.t.C }
func (s sysTicker) Stop()               { s.t.Stop() }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/clock/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add engine/internal/clock/
git commit -m "feat(engine/clock): Clock interface + System impl"
```

---

## Task 4: Vendor + generate protobuf bindings (the generation spike)

This is the spec's flagged first-implementation spike ("protoc-gen-go run over all 167 protos — verify no proto2/proto3 or naming surprises"). Facts verified from the SDK: all 167 protos are **proto2**; 150 carry `option go_package = "github.com/futuopen/ftapi4go/pb/<pkg>"` (a third-party path we must override) and 17 (all options-family, none in our v1 path) lack it; imports are flat filenames (`import "Common.proto";`) so `protoc -I` must point at the proto dir; dependency roots are `Common → Qot_Common → Trd_Common`.

**Files:**
- Create: `engine/scripts/vendor_protos.py`, `engine/scripts/gen_proto.sh`
- Create (generated + committed): `engine/internal/feed/opend/pb/proto/*.proto`, `engine/internal/feed/opend/pb/<pkg>/*.pb.go`
- Create: `engine/internal/feed/opend/pb/doc.go` (a real `package pb` so the external smoke test compiles)
- Test: `engine/internal/feed/opend/pb/pb_smoke_test.go`

**Interfaces:**
- Produces: importable Go packages `.../pb/initconnect`, `.../pb/keepalive`, `.../pb/common`, `.../pb/qotcommon`, `.../pb/qotsub`, … (one per proto `package`, lowercased with underscores removed). Enum success value is `common.RetType_RetType_Succeed` (== 0). `common.PacketID{ConnID, SerialNo}` is available for later plans.

- [ ] **Step 1: Write the vendoring script**

`engine/scripts/vendor_protos.py` — copies the SDK protos into the repo and rewrites every `go_package` to our module path (inserting one where missing), so `gen_proto.sh` is a pure `protoc` call over committed, self-consistent sources:

```python
#!/usr/bin/env python3
"""Vendor moomoo OpenD .proto files into the engine repo with go_package rewritten
to eTape's module path. Run once, and again whenever the moomoo SDK is upgraded.
Output (internal/feed/opend/pb/proto/*.proto) is committed."""
import os, re, shutil, sys
import moomoo

SDK_PB = os.path.join(os.path.dirname(moomoo.__file__), "common", "pb")
ENGINE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))  # engine/
DEST = os.path.join(ENGINE, "internal", "feed", "opend", "pb", "proto")
MODULE_PB = "github.com/earlisreal/eTape/engine/internal/feed/opend/pb"

def go_dir(pkg: str) -> str:
    # futu convention: lowercase, drop underscores (Qot_Common -> qotcommon)
    return pkg.lower().replace("_", "")

def main() -> None:
    if os.path.isdir(DEST):
        shutil.rmtree(DEST)
    os.makedirs(DEST)
    count = 0
    for name in sorted(os.listdir(SDK_PB)):
        if not name.endswith(".proto"):
            continue
        text = open(os.path.join(SDK_PB, name), encoding="utf-8").read()
        m = re.search(r"^package\s+([A-Za-z0-9_]+)\s*;", text, re.M)
        if not m:
            sys.exit(f"no package declaration in {name}")
        gp = f'option go_package = "{MODULE_PB}/{go_dir(m.group(1))}";'
        if re.search(r"^option\s+go_package", text, re.M):
            text = re.sub(r"^option\s+go_package\s*=.*$", gp, text, count=1, flags=re.M)
        else:
            text = re.sub(r"^(package\s+[A-Za-z0-9_]+\s*;.*)$", r"\1\n" + gp, text, count=1, flags=re.M)
        open(os.path.join(DEST, name), "w", encoding="utf-8").write(text)
        count += 1
    print(f"vendored {count} protos into {DEST}")

if __name__ == "__main__":
    main()
```

- [ ] **Step 2: Write the generation script**

`engine/scripts/gen_proto.sh` — with every `go_package` set to our module and `--go_opt=module=<module>`, protoc-gen-go writes each package to `internal/feed/opend/pb/<pkg>/` under the engine root:

```bash
#!/usr/bin/env bash
set -euo pipefail
ENGINE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PROTO_DIR="$ENGINE_DIR/internal/feed/opend/pb/proto"
MODULE="github.com/earlisreal/eTape/engine"

command -v protoc >/dev/null || { echo "protoc not found on PATH" >&2; exit 1; }
command -v protoc-gen-go >/dev/null || { echo "protoc-gen-go not found on PATH (go install google.golang.org/protobuf/cmd/protoc-gen-go@latest)" >&2; exit 1; }

cd "$ENGINE_DIR"
protoc \
  -I "$PROTO_DIR" \
  --go_out=. \
  --go_opt=module="$MODULE" \
  "$PROTO_DIR"/*.proto
echo "generated pb bindings under internal/feed/opend/pb/"
```

- [ ] **Step 3: Run vendoring + generation**

```bash
cd engine
chmod +x scripts/gen_proto.sh
python3 scripts/vendor_protos.py     # expect: "vendored 167 protos ..."
./scripts/gen_proto.sh               # expect: no protoc errors; *.pb.go appear under pb/<pkg>/
go build ./internal/feed/opend/pb/...
```

Expected: 167 protos vendored; `.pb.go` files under `internal/feed/opend/pb/{initconnect,keepalive,common,qotcommon,qotsub,...}/`; `go build` of the pb tree succeeds. If protoc errors on the 17 go_package-less files, confirm `vendor_protos.py` inserted `go_package` into them (grep one, e.g. `SkillWrapAPI.proto`).

- [ ] **Step 4: Write the smoke test (proves generation + runtime work together)**

First add a base package so `pb/` is a real (documentation-only) package — otherwise `pb/pb_smoke_test.go` (an external `pb_test` package) has no base package to attach to and won't compile.

`engine/internal/feed/opend/pb/doc.go`:

```go
// Package pb is the parent of the generated OpenD protobuf bindings, one Go
// package per proto file under pb/<pkg>/. It contains no code itself; it exists
// so this directory is a buildable package (and to host the generation smoke test).
package pb
```

`engine/internal/feed/opend/pb/pb_smoke_test.go`:

```go
package pb_test

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/common"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/initconnect"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/keepalive"
)

func TestInitConnectMessageRoundTrips(t *testing.T) {
	req := &initconnect.Request{C2S: &initconnect.C2S{
		ClientVer: proto.Int32(100),
		ClientID:  proto.String("etape-test"),
	}}
	b, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got initconnect.Request
	if err := proto.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.GetC2S().GetClientID() != "etape-test" {
		t.Fatalf("clientID = %q, want etape-test", got.GetC2S().GetClientID())
	}
}

func TestCommonAndKeepAliveGenerated(t *testing.T) {
	// success enum is available and zero-valued
	if common.RetType_RetType_Succeed != 0 {
		t.Fatalf("RetType_Succeed = %d, want 0", common.RetType_RetType_Succeed)
	}
	// keepalive message compiles and round-trips
	ka := &keepalive.Request{C2S: &keepalive.C2S{Time: proto.Int64(123)}}
	if ka.GetC2S().GetTime() != 123 {
		t.Fatal("keepalive time getter mismatch")
	}
}
```

- [ ] **Step 5: Run the smoke test**

Run: `go test ./internal/feed/opend/pb/ -v`
Expected: PASS.

- [ ] **Step 6: Commit (scripts + vendored protos + generated code)**

```bash
git add engine/scripts/vendor_protos.py engine/scripts/gen_proto.sh
git add engine/internal/feed/opend/pb/          # includes doc.go, proto/, generated <pkg>/, and the smoke test
git commit -m "feat(engine/opend): vendor + generate protobuf bindings for 167 OpenD protos"
```

---

## Task 5: Frame codec — Encode / Decode (byte-exact header)

**Files:**
- Create: `engine/internal/feed/opend/frame.go`
- Test: `engine/internal/feed/opend/frame_test.go`

**Interfaces:**
- Produces: `opend.Frame{ProtoID, SerialNo uint32; FmtType, ProtoVer uint8; Body []byte}`; `opend.Encode(protoID, serialNo uint32, body []byte) []byte` (44-byte header + body, SHA1 over body); `opend.Decode(frame []byte) (Frame, error)` (parses one complete frame, verifies length + SHA1); errors `ErrShortHeader`, `ErrBadMagic`, `ErrBodyLen`, `ErrBadSHA1`; exported `HeaderLen = 44`. `FmtProtobuf`/`FmtJSON` constants.

- [ ] **Step 1: Write the failing test**

`engine/internal/feed/opend/frame_test.go`:

```go
package opend

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"testing"
)

func TestEncodeLayoutMatchesSpec(t *testing.T) {
	body := []byte("hello-opend")
	frame := Encode(1001, 42, body)

	if len(frame) != HeaderLen+len(body) {
		t.Fatalf("frame len = %d, want %d", len(frame), HeaderLen+len(body))
	}
	if frame[0] != 'F' || frame[1] != 'T' {
		t.Fatalf("magic = %q, want FT", frame[0:2])
	}
	if got := binary.LittleEndian.Uint32(frame[2:6]); got != 1001 {
		t.Fatalf("protoID = %d, want 1001", got)
	}
	if frame[6] != FmtProtobuf {
		t.Fatalf("fmtType = %d, want %d", frame[6], FmtProtobuf)
	}
	if frame[7] != 0 {
		t.Fatalf("protoVer = %d, want 0", frame[7])
	}
	if got := binary.LittleEndian.Uint32(frame[8:12]); got != 42 {
		t.Fatalf("serialNo = %d, want 42", got)
	}
	if got := binary.LittleEndian.Uint32(frame[12:16]); got != uint32(len(body)) {
		t.Fatalf("bodyLen = %d, want %d", got, len(body))
	}
	sum := sha1.Sum(body)
	if !bytes.Equal(frame[16:36], sum[:]) {
		t.Fatal("sha1 mismatch in header")
	}
	for i := 36; i < 44; i++ {
		if frame[i] != 0 {
			t.Fatalf("reserved byte %d = %d, want 0", i, frame[i])
		}
	}
	if !bytes.Equal(frame[HeaderLen:], body) {
		t.Fatal("body not appended verbatim")
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	body := []byte{0x0a, 0x03, 0x66, 0x6f, 0x6f}
	f, err := Decode(Encode(3001, 7, body))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if f.ProtoID != 3001 || f.SerialNo != 7 || f.FmtType != FmtProtobuf {
		t.Fatalf("decoded header = %+v", f)
	}
	if !bytes.Equal(f.Body, body) {
		t.Fatalf("decoded body = %x, want %x", f.Body, body)
	}
}

func TestDecodeErrors(t *testing.T) {
	good := Encode(1001, 1, []byte("abc"))

	if _, err := Decode(good[:10]); err != ErrShortHeader {
		t.Fatalf("short header err = %v, want ErrShortHeader", err)
	}
	bad := append([]byte(nil), good...)
	bad[0] = 'X'
	if _, err := Decode(bad); err != ErrBadMagic {
		t.Fatalf("bad magic err = %v, want ErrBadMagic", err)
	}
	short := append([]byte(nil), good...)
	short = short[:len(short)-1] // body one byte short of header's bodyLen
	if _, err := Decode(short); err != ErrBodyLen {
		t.Fatalf("body len err = %v, want ErrBodyLen", err)
	}
	corrupt := append([]byte(nil), good...)
	corrupt[HeaderLen] ^= 0xFF // flip a body byte → SHA1 no longer matches
	if _, err := Decode(corrupt); err != ErrBadSHA1 {
		t.Fatalf("sha1 err = %v, want ErrBadSHA1", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/feed/opend/ -run TestEncode -v`
Expected: FAIL — `Encode`/`Decode`/`Frame` undefined.

- [ ] **Step 3: Write the implementation**

`engine/internal/feed/opend/frame.go`:

```go
// Package opend is the moomoo OpenD adapter: raw-TCP framing, generated protobuf,
// and the connection client. It is the only package that knows moomoo exists.
package opend

import (
	"crypto/sha1"
	"encoding/binary"
	"errors"
)

// HeaderLen is the fixed OpenD frame header size (verified from the SDK:
// struct fmt "<1s1sI2B2I20s8s", pack(1), little-endian).
const HeaderLen = 44

// Protocol format types (Common.ProtoFmt). eTape only ever sends Protobuf.
const (
	FmtProtobuf uint8 = 0
	FmtJSON     uint8 = 1
)

const (
	magic0   = 'F'
	magic1   = 'T'
	protoVer = 0 // API_PROTO_VER
)

// Codec errors.
var (
	ErrShortHeader = errors.New("opend: frame shorter than 44-byte header")
	ErrBadMagic    = errors.New("opend: bad frame magic (want \"FT\")")
	ErrBodyLen     = errors.New("opend: frame length does not match header bodyLen")
	ErrBadSHA1     = errors.New("opend: body SHA1 does not match header")
)

// Frame is one decoded OpenD message: header identity + raw protobuf body.
type Frame struct {
	ProtoID  uint32
	FmtType  uint8
	ProtoVer uint8
	SerialNo uint32
	Body     []byte
}

// Encode builds a complete wire frame: 44-byte header (fmtType=protobuf,
// protoVer=0, SHA1 over body, 8 zero reserved bytes) followed by body.
func Encode(protoID, serialNo uint32, body []byte) []byte {
	sum := sha1.Sum(body)
	buf := make([]byte, HeaderLen+len(body))
	buf[0] = magic0
	buf[1] = magic1
	binary.LittleEndian.PutUint32(buf[2:6], protoID)
	buf[6] = FmtProtobuf
	buf[7] = protoVer
	binary.LittleEndian.PutUint32(buf[8:12], serialNo)
	binary.LittleEndian.PutUint32(buf[12:16], uint32(len(body)))
	copy(buf[16:36], sum[:])
	// buf[36:44] reserved — left zero.
	copy(buf[HeaderLen:], body)
	return buf
}

type header struct {
	protoID  uint32
	fmtType  uint8
	protoVer uint8
	serialNo uint32
	bodyLen  uint32
	sha20    [20]byte
}

// parseHeader decodes the fixed 44-byte header. It does not touch the body.
func parseHeader(b []byte) (header, error) {
	if len(b) < HeaderLen {
		return header{}, ErrShortHeader
	}
	if b[0] != magic0 || b[1] != magic1 {
		return header{}, ErrBadMagic
	}
	var h header
	h.protoID = binary.LittleEndian.Uint32(b[2:6])
	h.fmtType = b[6]
	h.protoVer = b[7]
	h.serialNo = binary.LittleEndian.Uint32(b[8:12])
	h.bodyLen = binary.LittleEndian.Uint32(b[12:16])
	copy(h.sha20[:], b[16:36])
	return h, nil
}

// Decode parses one complete frame (header immediately followed by its body)
// and verifies the body length and SHA1.
func Decode(frame []byte) (Frame, error) {
	h, err := parseHeader(frame)
	if err != nil {
		return Frame{}, err
	}
	if uint32(len(frame)-HeaderLen) != h.bodyLen {
		return Frame{}, ErrBodyLen
	}
	body := frame[HeaderLen : HeaderLen+int(h.bodyLen)]
	if sha1.Sum(body) != h.sha20 {
		return Frame{}, ErrBadSHA1
	}
	return Frame{
		ProtoID:  h.protoID,
		FmtType:  h.fmtType,
		ProtoVer: h.protoVer,
		SerialNo: h.serialNo,
		Body:     append([]byte(nil), body...),
	}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/feed/opend/ -run 'TestEncode|TestDecode' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add engine/internal/feed/opend/frame.go engine/internal/feed/opend/frame_test.go
git commit -m "feat(engine/opend): byte-exact frame codec (Encode/Decode + SHA1)"
```

---

## Task 6: Streaming FrameReader (partial + pipelined frames)

The live socket delivers arbitrary byte chunks — a single `recv` may hold several frames or a partial one. `io.ReadFull` over a buffered reader reproduces the SDK's read-header-then-read-body sequence with correct blocking behavior.

**Files:**
- Modify: `engine/internal/feed/opend/frame.go` (add `FrameReader`)
- Test: `engine/internal/feed/opend/frame_test.go` (add reader tests)

**Interfaces:**
- Produces: `opend.NewFrameReader(r io.Reader) *FrameReader`; `(*FrameReader).ReadFrame() (Frame, error)` — reads exactly one frame, blocking until it is complete; returns the underlying read error (e.g. `io.EOF`) on connection close, or `ErrBadMagic`/`ErrBadSHA1` on corruption.

- [ ] **Step 1: Write the failing test**

Add to `engine/internal/feed/opend/frame_test.go`:

```go
func TestFrameReaderPipelinedAndPartial(t *testing.T) {
	// Two frames concatenated, fed through a reader that yields tiny chunks,
	// exercising both pipelining (>1 frame buffered) and partial reads.
	a := Encode(1001, 1, []byte("first"))
	b := Encode(1004, 2, []byte("second-frame-body"))
	stream := append(append([]byte(nil), a...), b...)

	fr := NewFrameReader(&chunkyReader{data: stream, chunk: 3})

	f1, err := fr.ReadFrame()
	if err != nil {
		t.Fatalf("frame 1: %v", err)
	}
	if f1.ProtoID != 1001 || string(f1.Body) != "first" {
		t.Fatalf("frame 1 = %+v", f1)
	}
	f2, err := fr.ReadFrame()
	if err != nil {
		t.Fatalf("frame 2: %v", err)
	}
	if f2.ProtoID != 1004 || string(f2.Body) != "second-frame-body" {
		t.Fatalf("frame 2 = %+v", f2)
	}
	if _, err := fr.ReadFrame(); err != io.EOF {
		t.Fatalf("after last frame err = %v, want io.EOF", err)
	}
}

func TestFrameReaderRejectsCorruptBody(t *testing.T) {
	f := Encode(3001, 9, []byte("payload"))
	f[HeaderLen+1] ^= 0xFF // corrupt a body byte
	if _, err := NewFrameReader(bytes.NewReader(f)).ReadFrame(); err != ErrBadSHA1 {
		t.Fatalf("err = %v, want ErrBadSHA1", err)
	}
}

// chunkyReader yields at most `chunk` bytes per Read to simulate a dribbling socket.
type chunkyReader struct {
	data  []byte
	chunk int
	pos   int
}

func (c *chunkyReader) Read(p []byte) (int, error) {
	if c.pos >= len(c.data) {
		return 0, io.EOF
	}
	n := c.chunk
	if n > len(p) {
		n = len(p)
	}
	if n > len(c.data)-c.pos {
		n = len(c.data) - c.pos
	}
	copy(p, c.data[c.pos:c.pos+n])
	c.pos += n
	return n, nil
}
```

Add `"io"` to the test file's imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/feed/opend/ -run TestFrameReader -v`
Expected: FAIL — `NewFrameReader` undefined.

- [ ] **Step 3: Add the implementation to `frame.go`**

Append to `engine/internal/feed/opend/frame.go` (and add `"bufio"` and `"io"` to its imports):

```go
// FrameReader reads whole frames from a byte stream (e.g. a TCP connection),
// blocking until each frame is complete. It is used by exactly one reader
// goroutine per connection.
type FrameReader struct {
	r *bufio.Reader
}

// NewFrameReader wraps r with a 128 KiB buffer (matching the SDK's recv size).
func NewFrameReader(r io.Reader) *FrameReader {
	return &FrameReader{r: bufio.NewReaderSize(r, 128*1024)}
}

// ReadFrame reads exactly one frame. It returns the underlying read error
// (io.EOF/io.ErrUnexpectedEOF on close) or a codec error on corruption.
func (fr *FrameReader) ReadFrame() (Frame, error) {
	var head [HeaderLen]byte
	if _, err := io.ReadFull(fr.r, head[:]); err != nil {
		return Frame{}, err
	}
	h, err := parseHeader(head[:])
	if err != nil {
		return Frame{}, err
	}
	body := make([]byte, h.bodyLen)
	if _, err := io.ReadFull(fr.r, body); err != nil {
		return Frame{}, err
	}
	if sha1.Sum(body) != h.sha20 {
		return Frame{}, ErrBadSHA1
	}
	return Frame{
		ProtoID:  h.protoID,
		FmtType:  h.fmtType,
		ProtoVer: h.protoVer,
		SerialNo: h.serialNo,
		Body:     body,
	}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/feed/opend/ -run 'TestFrame' -v`
Expected: PASS (codec + reader tests).

- [ ] **Step 5: Commit**

```bash
git add engine/internal/feed/opend/frame.go engine/internal/feed/opend/frame_test.go
git commit -m "feat(engine/opend): streaming FrameReader for partial/pipelined frames"
```

---

## Task 7: Golden-frame corpus + capture script + validation test

Validate the codec against **real** OpenD bytes. The capture harness monkeypatches the SDK's own frame functions (`pack_pb_req` for c2s, `parse_rsp` for s2c), so captured bytes are exactly what the SDK put on / took off the wire, keyed by an already-parsed protoID/serialNo. The critical caveat: protobuf serialization is **not canonical**, so the round-trip byte target is always the **stored raw body bytes**, never a re-marshaled message — `Encode(protoID, serialNo, storedBody)` reproduces the stored frame because SHA1 and header packing are deterministic over identical bytes.

**Files:**
- Create: `engine/scripts/capture_golden_frames.py`
- Create (checked-in fixtures): `engine/internal/feed/opend/testdata/golden/*.jsonl`, `engine/internal/feed/opend/testdata/golden/manifest.json`
- Test: `engine/internal/feed/opend/golden_test.go`

**Interfaces:**
- Consumes: `opend.Decode`, `opend.Encode` (Task 5); generated pb packages (Task 4).
- Produces: a checked-in golden corpus + a table test that fails if the codec ever diverges from real wire bytes.

- [ ] **Step 1: Write the capture harness**

`engine/scripts/capture_golden_frames.py` — requires **live OpenD** (verified reachable on `127.0.0.1:11111`). It hooks the SDK, runs a scripted sequence of read-only market-data calls, dedupes by `(proto_id, direction)`, and writes JSONL. It asserts the two capture invariants (encryption off, protobuf format) and **skips committing PII-bearing frames** (InitConnect S2C):

```python
#!/usr/bin/env python3
"""Capture real OpenD wire frames for the Go codec golden corpus.
Requires OpenD running locally. Read-only market-data calls ONLY — never trades.
Encryption must be OFF (SDK default on localhost) and format Protobuf (default)."""
import binascii, hashlib, json, os, time
import moomoo
from moomoo.common import utils
from moomoo.common.constant import ProtoId, SysConfig

assert not SysConfig.is_proto_encrypt(), "encryption must be OFF for golden capture"

OUT = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
                   "internal", "feed", "opend", "testdata", "golden")
os.makedirs(OUT, exist_ok=True)

# loginUserID lives in InitConnect S2C — do not commit that frame (PII on a public repo).
PII_PROTO_IDS = {ProtoId.InitConnect}  # only the S2C direction is redacted below
captured = {}  # (proto_id, direction) -> record

def _record(proto_id, direction, frame_bytes, body_bytes, decoded_json):
    key = (proto_id, direction)
    if key in captured:
        return
    captured[key] = {
        "proto_id": int(proto_id),
        "direction": direction,          # c2s | s2c
        "is_push": bool(ProtoId.is_proto_id_push(proto_id)),
        "proto_fmt_type": 0,
        "proto_ver": 0,
        "serial_no": int.from_bytes(frame_bytes[8:12], "little"),
        "body_len": len(body_bytes),
        "body_sha1_hex": hashlib.sha1(body_bytes).hexdigest(),
        "frame_hex": binascii.hexlify(frame_bytes).decode(),
        "body_hex": binascii.hexlify(body_bytes).decode(),
        "decoded_json": decoded_json,    # semantic oracle only, NOT a byte target
    }

_orig_pack = utils.pack_pb_req
def _pack_hook(pb, proto_id, conn_id=0, serial_no=None):
    frame = _orig_pack(pb, proto_id, conn_id, serial_no)
    body = pb.SerializeToString()
    _record(proto_id, "c2s", frame, body, None)
    return frame
utils.pack_pb_req = _pack_hook

_orig_parse = utils.parse_rsp
def _parse_hook(data, conn_id, is_encrypt):
    res = _orig_parse(data, conn_id, is_encrypt)
    hd = getattr(res, "head_dict", None)
    if hd and getattr(res, "total_len", 0) and getattr(res, "err", None) is None:
        pid = hd["proto_id"]
        frame = bytes(data[:res.total_len])
        body = frame[44:]
        if pid in PII_PROTO_IDS:
            return res  # skip InitConnect S2C (loginUserID)
        _record(pid, "s2c", frame, body, None)
    return res
utils.parse_rsp = _parse_hook

def main():
    ctx = moomoo.OpenQuoteContext(host="127.0.0.1", port=11111)  # InitConnect c2s captured here
    try:
        ctx.get_global_state()                                   # 1002
        ctx.get_market_snapshot(["US.AAPL"])                     # 3203 snapshot
        ctx.get_stock_quote(["US.AAPL"])                         # requires a prior sub; ok if it errors
        ctx.subscribe(["US.AAPL"], [moomoo.SubType.QUOTE, moomoo.SubType.TICKER, moomoo.SubType.ORDER_BOOK])  # 3001
        time.sleep(3)                                            # let pushes arrive (3005/3011/3013)
        ctx.get_order_book("US.AAPL")                            # 3012
        ctx.get_cur_kline("US.AAPL", 10, moomoo.KLType.K_1M)     # 3006
        ctx.get_rt_ticker("US.AAPL", 20)                         # 3010
        time.sleep(9)                                            # >=1 KeepAlive (1004 c2s + s2c) — free
    finally:
        ctx.close()

    manifest = {
        "sdk_version": getattr(moomoo, "__version__", "unknown"),
        "captured_frames": len(captured),
        "encryption": "off",
        "proto_fmt": "protobuf",
        "note": "InitConnect S2C intentionally omitted (loginUserID PII). "
                "body_hex is the byte-exact round-trip target; decoded_json is a semantic oracle only.",
    }
    with open(os.path.join(OUT, "manifest.json"), "w") as f:
        json.dump(manifest, f, indent=2)
    with open(os.path.join(OUT, "frames.jsonl"), "w") as f:
        for rec in sorted(captured.values(), key=lambda r: (r["proto_id"], r["direction"])):
            f.write(json.dumps(rec) + "\n")
    print(f"wrote {len(captured)} frames to {OUT}")

if __name__ == "__main__":
    main()
```

- [ ] **Step 2: Run the capture (requires live OpenD)**

```bash
cd engine
python3 scripts/capture_golden_frames.py
head -c 400 internal/feed/opend/testdata/golden/frames.jsonl
```

Expected: `frames.jsonl` contains at least KeepAlive c2s+s2c, InitConnect c2s, and several market-data frames (snapshot/quote/ticker/book/kline, plus any pushes). Confirm **no** InitConnect s2c line is present. If OpenD is down or a market call errors (out of hours), the KeepAlive + InitConnect-c2s + whatever succeeded is still a valid minimum corpus — the codec is protoID-agnostic, so any 3+ diverse frames validate it.

- [ ] **Step 3: Write the validation test**

`engine/internal/feed/opend/golden_test.go`:

```go
package opend

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/keepalive"
)

type goldenFrame struct {
	ProtoID  uint32 `json:"proto_id"`
	Dir      string `json:"direction"`
	SerialNo uint32 `json:"serial_no"`
	BodyLen  int    `json:"body_len"`
	FrameHex string `json:"frame_hex"`
	BodyHex  string `json:"body_hex"`
}

func loadGolden(t *testing.T) []goldenFrame {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "golden", "frames.jsonl"))
	if err != nil {
		t.Skipf("no golden corpus (run scripts/capture_golden_frames.py against live OpenD): %v", err)
	}
	defer f.Close()
	var out []goldenFrame
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var g goldenFrame
		if err := json.Unmarshal(sc.Bytes(), &g); err != nil {
			t.Fatalf("golden line: %v", err)
		}
		out = append(out, g)
	}
	return out
}

// The codec must decode every real frame and re-encode it byte-for-byte.
func TestGoldenDecodeReencode(t *testing.T) {
	frames := loadGolden(t)
	if len(frames) == 0 {
		t.Skip("empty golden corpus")
	}
	for _, g := range frames {
		t.Run(g.Dir+"/"+itoa(g.ProtoID), func(t *testing.T) {
			raw, err := hex.DecodeString(g.FrameHex)
			if err != nil {
				t.Fatal(err)
			}
			f, err := Decode(raw)
			if err != nil {
				t.Fatalf("Decode real frame: %v", err)
			}
			if f.ProtoID != g.ProtoID || f.SerialNo != g.SerialNo || len(f.Body) != g.BodyLen {
				t.Fatalf("header mismatch: got protoID=%d serial=%d bodyLen=%d", f.ProtoID, f.SerialNo, len(f.Body))
			}
			wantBody, _ := hex.DecodeString(g.BodyHex)
			if !bytes.Equal(f.Body, wantBody) {
				t.Fatal("decoded body != stored body")
			}
			// Re-encode from the STORED body bytes (not a re-marshaled message):
			// must reproduce the exact wire frame.
			if got := Encode(g.ProtoID, g.SerialNo, wantBody); !bytes.Equal(got, raw) {
				t.Fatal("re-encoded frame != real frame bytes")
			}
		})
	}
}

// Spot-check that a real KeepAlive body decodes with the generated pb type.
func TestGoldenKeepAliveDecodesSemantically(t *testing.T) {
	for _, g := range loadGolden(t) {
		if g.ProtoID != 1004 || g.Dir != "s2c" {
			continue
		}
		body, _ := hex.DecodeString(g.BodyHex)
		var resp keepalive.Response
		if err := proto.Unmarshal(body, &resp); err != nil {
			t.Fatalf("keepalive decode: %v", err)
		}
		if resp.GetRetType() != 0 {
			t.Fatalf("keepalive retType = %d, want 0", resp.GetRetType())
		}
		return // one is enough
	}
	t.Skip("no KeepAlive s2c frame in corpus")
}

func itoa(u uint32) string {
	if u == 0 {
		return "0"
	}
	var b [10]byte
	i := len(b)
	for u > 0 {
		i--
		b[i] = byte('0' + u%10)
		u /= 10
	}
	return string(b[i:])
}
```

- [ ] **Step 4: Run the validation test**

Run: `go test ./internal/feed/opend/ -run TestGolden -v`
Expected: PASS (or SKIP with a clear message if the corpus is absent — the test never silently no-ops).

- [ ] **Step 5: Sensitive-sweep, then commit**

Confirm no account identifiers leaked into the corpus (public repo). `manifest.json` should say `encryption: off`; grep the corpus for any InitConnect s2c line:

```bash
grep -c '"proto_id": 1001, "direction": "s2c"' engine/internal/feed/opend/testdata/golden/frames.jsonl  # expect 0
git add engine/scripts/capture_golden_frames.py engine/internal/feed/opend/testdata/golden/ engine/internal/feed/opend/golden_test.go
git commit -m "feat(engine/opend): golden-frame corpus + codec validation against real OpenD bytes"
```

---

## Task 8: Serial-number generator + pending-response registry

**Files:**
- Create: `engine/internal/feed/opend/serial.go`, `engine/internal/feed/opend/pending.go`
- Test: `engine/internal/feed/opend/pending_test.go`

**Interfaces:**
- Produces: `serialGen` with `next() uint32` (atomic, monotonic, wraps benignly); `pending` registry with `register(serial) chan Frame`, `resolve(serial, Frame) bool` (true if a waiter matched — false ⇒ caller treats the frame as a push), `cancel(serial)` (idempotent), `failAll()` (closes all waiters ⇒ in-flight `Request`s see `ErrNotConnected`). Race-safe: `resolve` and `cancel` remove from the map under lock before touching the channel, so `failAll` never closes a channel another path is sending on.

- [ ] **Step 1: Write the failing test**

`engine/internal/feed/opend/pending_test.go`:

```go
package opend

import (
	"sync"
	"testing"
)

func TestSerialGenMonotonic(t *testing.T) {
	var g serialGen
	a, b, c := g.next(), g.next(), g.next()
	if !(a == 1 && b == 2 && c == 3) {
		t.Fatalf("serials = %d,%d,%d want 1,2,3", a, b, c)
	}
}

func TestPendingResolveDeliversToWaiter(t *testing.T) {
	p := newPending()
	ch := p.register(7)
	if ok := p.resolve(7, Frame{ProtoID: 1001, SerialNo: 7}); !ok {
		t.Fatal("resolve returned false for a registered serial")
	}
	f := <-ch
	if f.SerialNo != 7 {
		t.Fatalf("delivered serial = %d, want 7", f.SerialNo)
	}
}

func TestPendingResolveUnknownIsPush(t *testing.T) {
	p := newPending()
	if ok := p.resolve(99, Frame{SerialNo: 99}); ok {
		t.Fatal("resolve of unregistered serial returned true (should be treated as push)")
	}
}

func TestPendingCancelThenResolveIsPush(t *testing.T) {
	p := newPending()
	_ = p.register(5)
	p.cancel(5)
	if ok := p.resolve(5, Frame{SerialNo: 5}); ok {
		t.Fatal("resolve after cancel returned true")
	}
	p.cancel(5) // idempotent, must not panic
}

func TestPendingFailAllClosesWaiters(t *testing.T) {
	p := newPending()
	ch := p.register(3)
	p.failAll()
	if _, ok := <-ch; ok {
		t.Fatal("expected channel closed after failAll")
	}
}

func TestPendingConcurrentResolveCancel(t *testing.T) {
	// -race must stay clean under concurrent register/resolve/cancel/failAll.
	p := newPending()
	var wg sync.WaitGroup
	for i := uint32(1); i <= 200; i++ {
		wg.Add(1)
		go func(s uint32) {
			defer wg.Done()
			ch := p.register(s)
			go p.resolve(s, Frame{SerialNo: s})
			select {
			case <-ch:
			default:
			}
			p.cancel(s)
		}(i)
	}
	wg.Wait()
	p.failAll()
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/feed/opend/ -run 'TestSerial|TestPending' -v`
Expected: FAIL — `serialGen`/`newPending` undefined.

- [ ] **Step 3: Write the implementations**

`engine/internal/feed/opend/serial.go`:

```go
package opend

import "sync/atomic"

// serialGen produces per-connection request serial numbers. A u32 that wraps at
// 2^32 is fine: correlation is by serialNo within a single live connection, and
// the in-flight window is tiny, so a wrap never collides in practice.
type serialGen struct{ n atomic.Uint32 }

func (g *serialGen) next() uint32 { return g.n.Add(1) }
```

`engine/internal/feed/opend/pending.go`:

```go
package opend

import "sync"

// pending correlates responses to in-flight requests by serial number.
// Every method removes its entry from the map under the lock before touching
// the channel, so failAll can never close a channel that resolve/cancel is
// about to use.
type pending struct {
	mu sync.Mutex
	m  map[uint32]chan Frame
}

func newPending() *pending { return &pending{m: make(map[uint32]chan Frame)} }

// register reserves a slot for serial and returns the (buffered) delivery channel.
func (p *pending) register(serial uint32) chan Frame {
	ch := make(chan Frame, 1)
	p.mu.Lock()
	p.m[serial] = ch
	p.mu.Unlock()
	return ch
}

// resolve delivers f to the waiter for serial. It returns false if no waiter is
// registered — the caller then treats f as a push.
func (p *pending) resolve(serial uint32, f Frame) bool {
	p.mu.Lock()
	ch, ok := p.m[serial]
	if ok {
		delete(p.m, serial)
	}
	p.mu.Unlock()
	if ok {
		ch <- f // cap-1 buffer: never blocks
	}
	return ok
}

// cancel drops a waiter (e.g. after a timeout). Idempotent.
func (p *pending) cancel(serial uint32) {
	p.mu.Lock()
	delete(p.m, serial)
	p.mu.Unlock()
}

// failAll closes every outstanding waiter — used on disconnect so blocked
// Request calls unblock and return ErrNotConnected.
func (p *pending) failAll() {
	p.mu.Lock()
	for s, ch := range p.m {
		close(ch)
		delete(p.m, s)
	}
	p.mu.Unlock()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/feed/opend/ -run 'TestSerial|TestPending' -race -v`
Expected: PASS, no race warnings.

- [ ] **Step 5: Commit**

```bash
git add engine/internal/feed/opend/serial.go engine/internal/feed/opend/pending.go engine/internal/feed/opend/pending_test.go
git commit -m "feat(engine/opend): serial generator + race-safe pending-response registry"
```

---

## Task 9: protoID constants + client core (session, Request, push dispatch) + mock OpenD server

**Files:**
- Create: `engine/internal/feed/opend/protoid.go`, `engine/internal/feed/opend/client.go`, `engine/internal/feed/opend/backoff.go`
- Test: `engine/internal/feed/opend/mock_opend_test.go`, `engine/internal/feed/opend/client_test.go`

**Interfaces:**
- Produces: `opend.New(opt Options) *Client`; `Options{Addr, ClientID string; ClientVer int32; RequestTimeout, DialTimeout, ReconnectMin, ReconnectMax time.Duration; Clock clock.Clock}`; `(*Client).Request(ctx, protoID uint32, req proto.Message) (Frame, error)`; `(*Client).Pushes() <-chan Frame`; `(*Client).State() <-chan ConnState`; `(*Client).ConnID() uint64`; `ConnState` (`ConnDown`, `ConnUp`); errors `ErrRequestTimeout`, `ErrNotConnected`. `Run` and lifecycle land in Tasks 10–11; this task builds the session plumbing and a mock server, testing `Request`/push routing over a manually-driven session.
- Consumes: `Encode`/`FrameReader` (Tasks 5–6), `serialGen`/`pending` (Task 8), `clock` (Task 3).

- [ ] **Step 1: Write the protoID constants**

`engine/internal/feed/opend/protoid.go`:

```go
package opend

// v1 protocol surface (protoIDs from the SDK's common/constant.py ProtoId class).
// Only InitConnect + KeepAlive are used in this plan; the rest are declared for
// the market-data plane (Plan 2) and are documented here as the single source of
// truth. The feed connection NEVER sends Trd_* protos (trade-incapability rule).
const (
	ProtoInitConnect uint32 = 1001
	ProtoKeepAlive   uint32 = 1004
	ProtoGetGlobalState uint32 = 1002

	ProtoQotSub            uint32 = 3001
	ProtoQotRegQotPush     uint32 = 3002
	ProtoQotGetSubInfo     uint32 = 3003
	ProtoQotGetBasicQot    uint32 = 3004
	ProtoQotUpdateBasicQot uint32 = 3005 // push
	ProtoQotGetKL          uint32 = 3006
	ProtoQotUpdateKL       uint32 = 3007 // push
	ProtoQotGetRT          uint32 = 3008
	ProtoQotUpdateRT       uint32 = 3009 // push
	ProtoQotGetTicker      uint32 = 3010
	ProtoQotUpdateTicker   uint32 = 3011 // push
	ProtoQotGetOrderBook   uint32 = 3012
	ProtoQotUpdateOrderBook uint32 = 3013 // push

	ProtoQotRequestHistoryKL      uint32 = 3103
	ProtoQotRequestHistoryKLQuota uint32 = 3104
	ProtoQotGetSecuritySnapshot   uint32 = 3203
	ProtoQotStockFilter           uint32 = 3215
	ProtoQotGetSearchNews         uint32 = 3263
	ProtoQotGetUSPreMarketRank    uint32 = 3410
)
```

- [ ] **Step 2: Write the backoff helper**

`engine/internal/feed/opend/backoff.go`:

```go
package opend

import (
	"math/rand/v2"
	"time"
)

// backoff yields jittered exponential delays capped at max, per the engine
// spec's OpenD-disconnect policy (1 s → 30 s, jittered).
type backoff struct {
	min, max time.Duration
	cur      time.Duration
}

func newBackoff(min, max time.Duration) *backoff { return &backoff{min: min, max: max} }

func (b *backoff) reset() { b.cur = 0 }

func (b *backoff) next() time.Duration {
	if b.cur == 0 {
		b.cur = b.min
	} else {
		b.cur *= 2
		if b.cur > b.max {
			b.cur = b.max
		}
	}
	// full jitter within [min, cur]
	span := b.cur - b.min
	if span <= 0 {
		return b.min
	}
	return b.min + rand.N(span)
}
```

- [ ] **Step 3: Write the client core**

`engine/internal/feed/opend/client.go`:

```go
package opend

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/clock"
)

// ConnState is the connection lifecycle signal on State().
type ConnState int

const (
	ConnDown ConnState = iota
	ConnUp
)

func (s ConnState) String() string {
	if s == ConnUp {
		return "up"
	}
	return "down"
}

var (
	ErrRequestTimeout = errors.New("opend: request timed out")
	ErrNotConnected   = errors.New("opend: not connected")
)

// Options configures a Client. Zero values are filled with defaults in New.
type Options struct {
	Addr           string
	ClientID       string
	ClientVer      int32
	RequestTimeout time.Duration
	DialTimeout    time.Duration
	ReconnectMin   time.Duration
	ReconnectMax   time.Duration
	Clock          clock.Clock
}

// Client is the OpenD connection: a supervised TCP session with request/response
// correlation and push dispatch. It holds no market-data domain state.
type Client struct {
	opt Options
	clk clock.Clock

	mu     sync.Mutex
	conn   net.Conn // current live conn; nil when down
	connID uint64
	kaInt  time.Duration
	sendMu sync.Mutex

	serial  serialGen
	pending *pending

	pushes chan Frame
	state  chan ConnState
}

// New builds a Client, filling defaults.
func New(opt Options) *Client {
	if opt.RequestTimeout == 0 {
		opt.RequestTimeout = 5 * time.Second
	}
	if opt.DialTimeout == 0 {
		opt.DialTimeout = 5 * time.Second
	}
	if opt.ReconnectMin == 0 {
		opt.ReconnectMin = time.Second
	}
	if opt.ReconnectMax == 0 {
		opt.ReconnectMax = 30 * time.Second
	}
	if opt.Clock == nil {
		opt.Clock = clock.System{}
	}
	if opt.ClientVer == 0 {
		opt.ClientVer = 100
	}
	if opt.ClientID == "" {
		opt.ClientID = "etape-engine"
	}
	return &Client{
		opt:     opt,
		clk:     opt.Clock,
		pending: newPending(),
		pushes:  make(chan Frame, 1024),
		state:   make(chan ConnState, 8),
	}
}

// Pushes yields frames with no matching in-flight request (dispatched by protoID
// by consumers). Plan 2 wraps this into typed FeedEvents.
func (c *Client) Pushes() <-chan Frame { return c.pushes }

// State yields connection up/down transitions.
func (c *Client) State() <-chan ConnState { return c.state }

// ConnID returns the OpenD-assigned connection ID (0 until InitConnect succeeds).
func (c *Client) ConnID() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connID
}

// Request sends req as protoID and waits for the correlated response.
func (c *Client) Request(ctx context.Context, protoID uint32, req proto.Message) (Frame, error) {
	body, err := proto.Marshal(req)
	if err != nil {
		return Frame{}, err
	}
	serial := c.serial.next()
	ch := c.pending.register(serial)
	defer c.pending.cancel(serial) // no-op if already resolved

	if err := c.send(Encode(protoID, serial, body)); err != nil {
		return Frame{}, err
	}

	select {
	case f, ok := <-ch:
		if !ok {
			return Frame{}, ErrNotConnected // failAll closed it
		}
		return f, nil
	case <-c.clk.After(c.opt.RequestTimeout):
		return Frame{}, ErrRequestTimeout
	case <-ctx.Done():
		return Frame{}, ctx.Err()
	}
}

func (c *Client) send(frame []byte) error {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return ErrNotConnected
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	_, err := conn.Write(frame)
	return err
}

// serveConn runs one connection to completion: it spawns the reader, performs the
// InitConnect handshake, runs the keepalive loop, and returns the error that ended
// the session. Lifecycle helpers (initConnect, keepAliveLoop) are in lifecycle.go;
// the supervising Run loop is added in Task 11.
func (c *Client) serveConn(ctx context.Context, conn net.Conn) error {
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()

	c.setConn(conn)
	defer c.clearConn()

	readErr := make(chan error, 1)
	fr := NewFrameReader(conn)
	go func() {
		for {
			f, err := fr.ReadFrame()
			if err != nil {
				readErr <- err
				return
			}
			if !c.pending.resolve(f.SerialNo, f) {
				select {
				case c.pushes <- f:
				case <-sctx.Done():
					return
				default:
					// push buffer full: drop. Plan 2's feed wrapper owns
					// coalescing/backpressure and forces a re-snapshot instead.
				}
			}
		}
	}()

	if err := c.initConnect(sctx); err != nil {
		return err
	}
	c.emit(ConnUp)
	defer c.emit(ConnDown)

	kaErr := make(chan error, 1)
	go c.keepAliveLoop(sctx, kaErr)

	select {
	case <-sctx.Done():
		return sctx.Err()
	case err := <-readErr:
		return err
	case err := <-kaErr:
		return err
	}
}

func (c *Client) setConn(conn net.Conn) {
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
}

func (c *Client) clearConn() {
	c.mu.Lock()
	c.conn = nil
	c.connID = 0
	c.mu.Unlock()
	c.pending.failAll()
}

func (c *Client) setConnInfo(connID uint64, kaInterval time.Duration) {
	c.mu.Lock()
	c.connID = connID
	c.kaInt = kaInterval
	c.mu.Unlock()
}

func (c *Client) emit(s ConnState) {
	select {
	case c.state <- s:
	default:
	}
}
```

- [ ] **Step 4: Write the mock OpenD server (test helper)**

`engine/internal/feed/opend/mock_opend_test.go`:

```go
package opend

import (
	"net"
	"sync"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/initconnect"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/keepalive"
)

// mockOpenD is an in-process OpenD server for client tests. Its handler decides,
// per inbound frame, what to reply (or to stay silent / close the conn).
type mockOpenD struct {
	ln      net.Listener
	handler func(m *mockOpenD, conn net.Conn, f Frame) // custom behavior per frame

	mu       sync.Mutex
	requests []Frame // every frame received (for assertions)
	dials    int
}

func newMockOpenD(t *testing.T) *mockOpenD {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	m := &mockOpenD{ln: ln}
	m.handler = m.defaultHandler
	go m.acceptLoop()
	t.Cleanup(func() { _ = ln.Close() })
	return m
}

func (m *mockOpenD) addr() string { return m.ln.Addr().String() }

func (m *mockOpenD) acceptLoop() {
	for {
		conn, err := m.ln.Accept()
		if err != nil {
			return
		}
		m.mu.Lock()
		m.dials++
		m.mu.Unlock()
		go m.handleConn(conn)
	}
}

func (m *mockOpenD) handleConn(conn net.Conn) {
	defer conn.Close()
	fr := NewFrameReader(conn)
	for {
		f, err := fr.ReadFrame()
		if err != nil {
			return
		}
		m.mu.Lock()
		m.requests = append(m.requests, f)
		m.mu.Unlock()
		m.handler(m, conn, f)
	}
}

// defaultHandler answers InitConnect and KeepAlive with success replies.
func (m *mockOpenD) defaultHandler(_ *mockOpenD, conn net.Conn, f Frame) {
	switch f.ProtoID {
	case ProtoInitConnect:
		resp := &initconnect.Response{
			RetType: proto.Int32(0),
			S2C: &initconnect.S2C{
				ServerVer:         proto.Int32(900),
				LoginUserID:       proto.Uint64(1),
				ConnID:            proto.Uint64(0xABCDEF),
				ConnAESKey:        proto.String("0000000000000000"),
				KeepAliveInterval: proto.Int32(10),
			},
		}
		m.reply(conn, f, resp)
	case ProtoKeepAlive:
		resp := &keepalive.Response{RetType: proto.Int32(0), S2C: &keepalive.S2C{Time: proto.Int64(1)}}
		m.reply(conn, f, resp)
	}
}

func (m *mockOpenD) reply(conn net.Conn, req Frame, msg proto.Message) {
	body, _ := proto.Marshal(msg)
	_, _ = conn.Write(Encode(req.ProtoID, req.SerialNo, body))
}

// push sends an unsolicited frame (serialNo 0 → no waiter → routed as a push).
func (m *mockOpenD) push(conn net.Conn, protoID uint32, msg proto.Message) {
	body, _ := proto.Marshal(msg)
	_, _ = conn.Write(Encode(protoID, 0, body))
}

func (m *mockOpenD) dialCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dials
}

func (m *mockOpenD) requestCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.requests)
}
```

- [ ] **Step 5: Write the client-core test (Request + push routing over a manual session)**

`engine/internal/feed/opend/client_test.go`:

```go
package opend

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/keepalive"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotsub"
)

func dialClient(t *testing.T, m *mockOpenD) (*Client, net.Conn) {
	t.Helper()
	c := New(Options{Addr: m.addr(), Clock: clock.System{}, RequestTimeout: 200 * time.Millisecond})
	conn, err := net.Dial("tcp", m.addr())
	if err != nil {
		t.Fatal(err)
	}
	c.setConn(conn)
	// Start the reader/dispatch loop by hand (serveConn's inner goroutine) so we
	// can test Request/push without the full lifecycle (Tasks 10-11).
	fr := NewFrameReader(conn)
	go func() {
		for {
			f, err := fr.ReadFrame()
			if err != nil {
				return
			}
			if !c.pending.resolve(f.SerialNo, f) {
				c.pushes <- f
			}
		}
	}()
	t.Cleanup(func() { _ = conn.Close() })
	return c, conn
}

func TestRequestGetsCorrelatedResponse(t *testing.T) {
	m := newMockOpenD(t)
	c, _ := dialClient(t, m)

	f, err := c.Request(context.Background(), ProtoKeepAlive,
		&keepalive.Request{C2S: &keepalive.C2S{Time: proto.Int64(1)}})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	var resp keepalive.Response
	if err := proto.Unmarshal(f.Body, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.GetRetType() != 0 {
		t.Fatalf("retType = %d, want 0", resp.GetRetType())
	}
}

func TestRequestTimesOutWhenServerSilent(t *testing.T) {
	m := newMockOpenD(t)
	m.handler = func(_ *mockOpenD, _ net.Conn, _ Frame) {} // never reply
	c, _ := dialClient(t, m)

	_, err := c.Request(context.Background(), ProtoKeepAlive,
		&keepalive.Request{C2S: &keepalive.C2S{Time: proto.Int64(1)}})
	if err != ErrRequestTimeout {
		t.Fatalf("err = %v, want ErrRequestTimeout", err)
	}
}

func TestUnsolicitedFrameRoutesToPushes(t *testing.T) {
	m := newMockOpenD(t)
	// Reply to the request AND emit a push on the same conn.
	m.handler = func(mm *mockOpenD, conn net.Conn, f Frame) {
		mm.defaultHandler(mm, conn, f)
		mm.push(conn, ProtoQotUpdateTicker, &qotsub.Response{RetType: proto.Int32(0)})
	}
	c, _ := dialClient(t, m)

	if _, err := c.Request(context.Background(), ProtoKeepAlive,
		&keepalive.Request{C2S: &keepalive.C2S{Time: proto.Int64(1)}}); err != nil {
		t.Fatalf("Request: %v", err)
	}
	select {
	case p := <-c.Pushes():
		if p.ProtoID != ProtoQotUpdateTicker {
			t.Fatalf("push protoID = %d, want %d", p.ProtoID, ProtoQotUpdateTicker)
		}
	case <-time.After(time.Second):
		t.Fatal("no push routed within 1s")
	}
}
```

> Note: `qotsub.Response` is used only as a convenient generated message with a `RetType` field for the push-body payload; the test asserts routing, not decoding. If its field names differ after generation, substitute any generated message with a settable field.

- [ ] **Step 6: Run the tests**

Run: `go test ./internal/feed/opend/ -run 'TestRequest|TestUnsolicited' -race -v`
Expected: PASS, no races.

- [ ] **Step 7: Commit**

```bash
git add engine/internal/feed/opend/protoid.go engine/internal/feed/opend/backoff.go engine/internal/feed/opend/client.go engine/internal/feed/opend/mock_opend_test.go engine/internal/feed/opend/client_test.go
git commit -m "feat(engine/opend): client core — Request correlation, push dispatch, mock server"
```

---

## Task 10: Lifecycle — InitConnect + KeepAlive

**Files:**
- Create: `engine/internal/feed/opend/lifecycle.go`
- Test: `engine/internal/feed/opend/client_test.go` (add lifecycle tests)

**Interfaces:**
- Produces: `(*Client).initConnect(ctx) error` (sends `InitConnect`, checks `retType==0`, stores `connID` + `keepAliveInterval`); `(*Client).keepAliveLoop(ctx, errc)` (ticks at the negotiated interval, sends `KeepAlive`, feeds `errc` on failure so the session ends → reconnect).
- Consumes: `Request` (Task 9), generated `initconnect`/`keepalive`/`common` (Task 4), `clock` (Task 3).

- [ ] **Step 1: Write the failing tests**

Add to `engine/internal/feed/opend/client_test.go`:

```go
func TestInitConnectStoresConnID(t *testing.T) {
	m := newMockOpenD(t)
	c, _ := dialClient(t, m)
	if err := c.initConnect(context.Background()); err != nil {
		t.Fatalf("initConnect: %v", err)
	}
	if c.ConnID() != 0xABCDEF {
		t.Fatalf("connID = %#x, want 0xABCDEF", c.ConnID())
	}
	if c.kaInt != 10*time.Second {
		t.Fatalf("keepAlive interval = %v, want 10s", c.kaInt)
	}
}

func TestInitConnectFailsOnNonZeroRetType(t *testing.T) {
	m := newMockOpenD(t)
	m.handler = func(_ *mockOpenD, conn net.Conn, f Frame) {
		if f.ProtoID == ProtoInitConnect {
			resp := &initconnect.Response{RetType: proto.Int32(-1), RetMsg: proto.String("nope")}
			m.reply(conn, f, resp)
		}
	}
	c, _ := dialClient(t, m)
	if err := c.initConnect(context.Background()); err == nil {
		t.Fatal("expected error on retType=-1")
	}
}

func TestKeepAliveLoopSendsHeartbeats(t *testing.T) {
	m := newMockOpenD(t)
	c, _ := dialClient(t, m)
	if err := c.initConnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.kaInt = 20 * time.Millisecond // fast heartbeat for the test
	errc := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go c.keepAliveLoop(ctx, errc)
	defer cancel()

	// Expect several KeepAlive requests to arrive at the mock within ~200ms.
	deadline := time.After(500 * time.Millisecond)
	for {
		kaCount := 0
		m.mu.Lock()
		for _, r := range m.requests {
			if r.ProtoID == ProtoKeepAlive {
				kaCount++
			}
		}
		m.mu.Unlock()
		if kaCount >= 3 {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("only %d keepalives seen, want >=3", kaCount)
		case <-time.After(10 * time.Millisecond):
		}
	}
}
```

Add `"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/initconnect"` to the test imports.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/feed/opend/ -run 'TestInitConnect|TestKeepAliveLoop' -v`
Expected: FAIL — `initConnect`/`keepAliveLoop` undefined.

- [ ] **Step 3: Write the implementation**

`engine/internal/feed/opend/lifecycle.go`:

```go
package opend

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/common"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/initconnect"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/keepalive"
)

// initConnect performs the 1001 handshake and records connID + keepalive interval.
func (c *Client) initConnect(ctx context.Context) error {
	req := &initconnect.Request{C2S: &initconnect.C2S{
		ClientVer:           proto.Int32(c.opt.ClientVer),
		ClientID:            proto.String(c.opt.ClientID),
		RecvNotify:          proto.Bool(true),
		ProgrammingLanguage: proto.String("Go"),
	}}
	f, err := c.Request(ctx, ProtoInitConnect, req)
	if err != nil {
		return fmt.Errorf("initconnect: %w", err)
	}
	var resp initconnect.Response
	if err := proto.Unmarshal(f.Body, &resp); err != nil {
		return fmt.Errorf("initconnect decode: %w", err)
	}
	if resp.GetRetType() != int32(common.RetType_RetType_Succeed) {
		return fmt.Errorf("initconnect failed: retType=%d msg=%q", resp.GetRetType(), resp.GetRetMsg())
	}
	s2c := resp.GetS2C()
	c.setConnInfo(s2c.GetConnID(), time.Duration(s2c.GetKeepAliveInterval())*time.Second)
	return nil
}

// keepAliveLoop sends a KeepAlive every interval; any failure ends the session.
func (c *Client) keepAliveLoop(ctx context.Context, errc chan<- error) {
	interval := c.kaInt
	if interval <= 0 {
		interval = 10 * time.Second
	}
	ticker := c.clk.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
			req := &keepalive.Request{C2S: &keepalive.C2S{Time: proto.Int64(c.clk.Now().Unix())}}
			rctx, cancel := context.WithTimeout(ctx, c.opt.RequestTimeout)
			_, err := c.Request(rctx, ProtoKeepAlive, req)
			cancel()
			if err != nil {
				select {
				case errc <- fmt.Errorf("keepalive: %w", err):
				default:
				}
				return
			}
		}
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/feed/opend/ -run 'TestInitConnect|TestKeepAliveLoop' -race -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add engine/internal/feed/opend/lifecycle.go engine/internal/feed/opend/client_test.go
git commit -m "feat(engine/opend): InitConnect handshake + KeepAlive heartbeat loop"
```

---

## Task 11: Supervised Run loop — connect, serve, backoff reconnect

**Files:**
- Modify: `engine/internal/feed/opend/client.go` (add `Run` + `dialOnce`)
- Test: `engine/internal/feed/opend/client_test.go` (add reconnect tests)

**Interfaces:**
- Produces: `(*Client).Run(ctx context.Context) error` — dials, serves one session, and on any session-ending error backs off (jittered, `ReconnectMin`→`ReconnectMax`) and redials, until `ctx` is cancelled (returns `ctx.Err()`). Emits `ConnUp`/`ConnDown` across the cycle.
- Consumes: `serveConn` (Task 9), `initConnect`/`keepAliveLoop` (Task 10), `backoff` (Task 9).

- [ ] **Step 1: Write the failing tests**

Add to `engine/internal/feed/opend/client_test.go`:

```go
func TestRunConnectsAndSignalsUp(t *testing.T) {
	m := newMockOpenD(t)
	c := New(Options{
		Addr: m.addr(), Clock: clock.System{},
		RequestTimeout: 200 * time.Millisecond, ReconnectMin: time.Millisecond, ReconnectMax: 5 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.Run(ctx) }()

	select {
	case st := <-c.State():
		if st != ConnUp {
			t.Fatalf("first state = %v, want ConnUp", st)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no ConnUp within 2s")
	}
	if c.ConnID() != 0xABCDEF {
		t.Fatalf("connID = %#x after connect", c.ConnID())
	}
}

func TestRunReconnectsAfterDrop(t *testing.T) {
	m := newMockOpenD(t)
	// Drop the connection right after the InitConnect reply, forcing reconnects.
	m.handler = func(mm *mockOpenD, conn net.Conn, f Frame) {
		if f.ProtoID == ProtoInitConnect {
			mm.defaultHandler(mm, conn, f)
			_ = conn.Close()
		}
	}
	c := New(Options{
		Addr: m.addr(), Clock: clock.System{},
		RequestTimeout: 100 * time.Millisecond, ReconnectMin: time.Millisecond, ReconnectMax: 5 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.Run(ctx) }()

	// The client should dial repeatedly as the server keeps dropping it.
	deadline := time.After(2 * time.Second)
	for {
		if m.dialCount() >= 3 {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("only %d dials, want >=3 (reconnect not happening)", m.dialCount())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestRunStopsOnContextCancel(t *testing.T) {
	m := newMockOpenD(t)
	c := New(Options{
		Addr: m.addr(), Clock: clock.System{},
		ReconnectMin: time.Millisecond, ReconnectMax: 5 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("Run returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/feed/opend/ -run TestRun -v`
Expected: FAIL — `Run` undefined.

- [ ] **Step 3: Add `Run` + `dialOnce` to `client.go`**

Append to `engine/internal/feed/opend/client.go` (imports already include `context`, `net`, `time`):

```go
// Run supervises the connection: dial → serve → (on any error) backoff → redial,
// until ctx is cancelled. It blocks; callers run it in a goroutine.
func (c *Client) Run(ctx context.Context) error {
	bo := newBackoff(c.opt.ReconnectMin, c.opt.ReconnectMax)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		conn, err := c.dialOnce(ctx)
		if err == nil {
			bo.reset()
			// serveConn emits ConnUp on handshake and ConnDown on exit.
			_ = c.serveConn(ctx, conn)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.clk.After(bo.next()):
		}
	}
}

func (c *Client) dialOnce(ctx context.Context) (net.Conn, error) {
	d := net.Dialer{Timeout: c.opt.DialTimeout}
	return d.DialContext(ctx, "tcp", c.opt.Addr)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/feed/opend/ -run TestRun -race -v`
Expected: PASS.

- [ ] **Step 5: Run the whole package under race**

Run: `go test ./internal/feed/opend/... -race`
Expected: PASS (all tests; golden test may SKIP if no corpus).

- [ ] **Step 6: Commit**

```bash
git add engine/internal/feed/opend/client.go engine/internal/feed/opend/client_test.go
git commit -m "feat(engine/opend): supervised Run loop with jittered backoff reconnect"
```

---

## Task 12: `cmd/etape` minimal harness + live smoke verification

**Files:**
- Create: `engine/cmd/etape/main.go`

**Interfaces:**
- Consumes: `config` (Task 2), `clock` (Task 3), `opend.New`/`Run`/`Pushes`/`State` (Tasks 9–11).
- Produces: a runnable `etape` binary that connects to OpenD, logs connection state + pushes, and shuts down cleanly on SIGINT. Plan 6 replaces this with the full boot sequence.

- [ ] **Step 1: Write `main.go`**

`engine/cmd/etape/main.go`:

```go
// Command etape is the eTape engine. In this plan it is a minimal harness that
// connects to OpenD and logs connection state + pushes; Plan 6 replaces main with
// the full boot sequence (store → uihub → OpenD → pre-subscribe → exec).
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
)

func main() {
	home, _ := os.UserHomeDir()
	cfgPath := flag.String("config", filepath.Join(home, ".eTape", "config.toml"), "path to config.toml")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Error("load config", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client := opend.New(opend.Options{
		Addr:      cfg.OpenD.Addr(),
		ClientID:  "etape-engine",
		ClientVer: 100,
		Clock:     clock.System{},
	})

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case st := <-client.State():
				log.Info("opend connection", "state", st)
			case f := <-client.Pushes():
				log.Info("opend push", "protoID", f.ProtoID, "serialNo", f.SerialNo, "bodyLen", len(f.Body))
			}
		}
	}()

	log.Info("connecting to OpenD", "addr", cfg.OpenD.Addr())
	if err := client.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Error("opend client stopped", "err", err)
		os.Exit(1)
	}
	log.Info("shutdown complete")
}
```

- [ ] **Step 2: Build**

Run: `cd engine && go build ./cmd/etape`
Expected: builds a binary with no errors.

- [ ] **Step 3: Live smoke test against OpenD**

With OpenD running (verified reachable on `127.0.0.1:11111`):

```bash
cd engine && go run ./cmd/etape
```

Expected within a few seconds:
- `connecting to OpenD addr=127.0.0.1:11111`
- `opend connection state=up`  (InitConnect + keepalive succeeded)
- periodic `opend connection` stays `up`; no repeated up/down churn.
- Ctrl-C → `shutdown complete`, clean exit (exit code 0).

If it logs `state=down` and redials repeatedly, OpenD is unreachable or the handshake failed — check OpenD is logged in and the port matches config.

(No pushes are expected in this plan — the harness does not subscribe; `Qot_Sub` is Plan 2. Seeing `state=up` and a stable connection is the success criterion. If you want to see push logs now, temporarily run the Python `scripts/capture_golden_frames.py` in parallel to generate market-data activity on a *separate* SDK connection — the engine won't see those pushes since they're on a different connection; this is expected and confirms the two-connection model.)

- [ ] **Step 4: Full CI gate**

```bash
cd engine
go build ./... && go vet ./... && go test -race ./... && golangci-lint run
```
Expected: all green.

- [ ] **Step 5: Commit**

```bash
git add engine/cmd/etape/main.go
git commit -m "feat(engine/cmd): minimal etape harness — connect to OpenD, log state + pushes"
```

---

## Definition of done (Plan 1)

- `engine/` is a Go module (`github.com/earlisreal/eTape/engine`) that builds, vets, tests (`-race`), and lints clean.
- Generated protobuf bindings for all 167 OpenD protos are committed and importable; regeneration is a two-script, reproducible step.
- The frame codec is byte-exact: unit round-trips **and** a decode/re-encode match against **real** OpenD frames captured from the Python SDK (golden corpus committed, PII-free).
- `opend.Client` connects to live OpenD, completes InitConnect + KeepAlive, correlates request/response by serial number, dispatches pushes by protoID, and reconnects with jittered backoff on drop — all covered by tests against an in-process mock server.
- `etape` runs, reaches `state=up` against live OpenD, holds the connection, and shuts down cleanly.
- The seams Plan 2 needs are in place: `Client.Request`, `Client.Pushes()`, `Client.State()`, `Client.ConnID()`, the v1 protoID constants, and `clock.Clock`.

## Self-review notes (author checklist, completed)

- **Spec coverage:** Plan 1 covers go-engine-design §feed/opend "Wire protocol" (framing, SHA1, InitConnect/KeepAlive lifecycle, serialNo correlation, push routing, reconnect backoff), the "generated Go protobuf (all 167 protos)" and "golden frames" open items, the `engine/` repo layout + dependency rule, the `clock` seam, and the bootstrap TOML config. The subscription manager, `Feed` interface, `md` core, `session`, `store`, `uihub`, pollers, and all `exec`/broker work are explicitly deferred to Plans 2–6 (see the roadmap). This matches the approved scope split (6 plans).
- **Placeholder scan:** every code step contains complete, runnable code; every command has an expected result; no "TBD"/"add error handling"/"similar to Task N" placeholders.
- **Type consistency:** `Frame`, `Encode`/`Decode`, `FrameReader`, `serialGen`, `pending`, `Client`/`Options`/`ConnState`, `initConnect`/`keepAliveLoop`, and the `pb/<pkg>` import paths are used consistently across tasks. Generated enum/const names (`common.RetType_RetType_Succeed`, proto2 pointer fields via `proto.Int32`/`Bool`/`Int64`/`String`) follow protoc-gen-go's proto2 conventions verified in the SDK protos.
- **Execution risks flagged inline:** (a) the protoc generation step is the known spike — the vendoring script normalizes `go_package` across all 167 files (150 override + 17 insert) so protoc is a pure call; (b) golden capture needs live OpenD and is written to degrade to a valid minimum corpus (and to SKIP, never silently pass, if absent); (c) generated message field names (e.g. `qotsub.Response`) should be confirmed post-generation and swapped if protoc names them differently.

## Execution options

**Plan complete and saved to `docs/superpowers/plans/2026-07-04-engine-foundation-opend-client.md`. Two execution options:**

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

**Which approach?** (Or: shall I write Plan 2 next instead of executing?)

---

## Post-implementation corrections (executed 2026-07-04/05, subagent-driven)

Plan 1 was implemented in full and merged. All 12 tasks passed per-task review; the
final whole-branch review (opus) returned **ready to merge**. The following
corrections were made during execution and supersede the code shown above where
they differ — recorded here so the plan matches what shipped:

1. **Task 4 (protoc generation):** `golangci-lint` on the machine is **v2**, whose
   config format differs (top-level `version: "2"`; `gofmt`/`goimports` live under a
   separate `formatters:` key). The `.golangci.yml` was written in v2 format
   (Task 1). `protoc-gen-go` lives at `~/go/bin` (added to PATH for generation).
2. **Task 7 (golden capture):** the capture script's SDK monkeypatch targets in the
   draft above are wrong (they hook `pack_pb_req`/`parse_rsp` in the wrong namespace
   and test `err is None` when success is `ParseRspErr.OK`, capturing nothing). The
   shipped `scripts/capture_golden_frames.py` instead hooks **`NetManager.send`** for
   c2s and **`open_context_base.parse_rsp`** (a bound global via `from .utils import *`)
   gated on `ParseRspErr.OK` for s2c. It excludes **InitConnect (1001)** *and*
   **GetGlobalState (1002)** from the committed corpus (loginUserID PII and moomoo
   upstream server IPs respectively — public repo). Corpus = 17 real frames.
3. **Task 10 (keepalive) — race fix:** reading `c.kaInt` directly in `keepAliveLoop`
   races `setConnInfo`'s locked write across reconnects (caught by `-race`). Shipped
   code adds a mutex-guarded `keepAliveInterval()` accessor (mirrors `ConnID()`).
4. **Task 9/11 (serveConn) — Critical leak fix:** `serveConn` must
   `defer func() { _ = conn.Close() }()` immediately after `setConn(conn)`. Without
   it the reader goroutine (blocked in `io.ReadFull`, no deadline) and its fd leak on
   the ctx-cancel / keepalive-timeout / handshake-timeout exit paths. A regression
   test (`TestRunReconnectsAfterKeepAliveTimeout`) drives the wedged-OpenD path and
   asserts goroutines settle.

**Carried to Plan 2 (required before subscriptions):** response correlation is
currently serialNo-only. Real OpenD **pushes carry an independent, nonzero
server-side serial** (observed: pushes at serial ~1517–1519 while client requests
were at ~2752–2758). Once Plan 2 subscribes and pushes flow, a push serial could
collide with an in-flight request serial and be mis-delivered to that request's
waiter. **Fix in Plan 2's feed wrapper: route known push protoIDs
(3005/3007/3009/3011/3013/…) to `Pushes()` regardless of serial; only match a
pending waiter when protoID matches too.** Also make the mock emit pushes with a
realistic nonzero serial.

**Hardening backlog (not blocking v1):** add a per-write deadline in `send()` (a
non-reading-but-open OpenD can wedge `conn.Write` before the keepalive timeout
fires); cap `bodyLen` before allocating in `ReadFrame`/`Decode` (guards against a
corrupt length from a compromised gateway); reset reconnect backoff on successful
handshake (`ConnUp`) rather than on successful dial; emit an initial `ConnDown` /
dial-failure signal on `State()` so Plan 2 can distinguish "connecting" from
"never started"; pin the `protoc-gen-go` version in `gen_proto.sh`'s error hint.
