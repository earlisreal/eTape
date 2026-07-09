# Test connection + auto-detect env/account in Venues settings

## Context

Configuring an execution venue in **Settings → Venues & creds** today requires the user to
manually pick the environment (`paper`/`live`) and, for TradeZero, type an account id — with
no way to confirm the entered API key even works until the engine is restarted and tries to
boot the adapter. This is error-prone (wrong env = wrong-account trading; wrong account id =
boot failure) and gives no feedback loop.

This change adds a **Test connection** button per credentialed venue that authenticates against
the broker read-only, and uses that probe to **auto-detect** the environment and account id so
the user no longer types them by hand:

- **Alpaca** — paper and live are *different base URLs* with env-specific keys. The probe tries
  both `GET /v2/account` endpoints; whichever authenticates is the detected env. Alpaca needs no
  account id in eTape (identity = the key pair).
- **TradeZero** — one host for both envs; env is carried by each account's `accountType`. The
  probe calls the (currently unimplemented) `GET /v1/api/accounts`, which returns the account
  id(s) + `accountType` — giving us both the account id and the env in one read-only call.
- **moomoo** — a reject-all stub (execution v1.x); testing is not supported and returns a clear
  message.

**Chosen UX (confirmed with user): "Derive & hide, test-gated".** For TradeZero/Alpaca the manual
`env` and `account id` inputs are hidden; Test connection fills them (shown read-only). Saving a
credentialed venue requires it to be *verified* — either a successful Test this session, or a
pre-existing complete config whose key is unchanged.

**Safety.** Every probe endpoint is read-only (`GET /v2/account`, `GET /v2/clock`,
`GET /v1/api/accounts`). Per `CLAUDE.md`, read-only verification endpoints are explicitly allowed
even with live keys; the safety rule only bars placing/modifying/canceling real orders. The probe
issues **no** order actions, so no per-session authorization is needed.

## Design overview

New WS command `TestConnection` follows the existing command→ack pattern (correlated by `corrId`,
result returned in `AckMsg.Value`, exactly like `GetVenueSetup`). A new file-agnostic engine
package `venueprobe` constructs a short-lived **REST-only** broker client from the submitted
credentials (or, if the secret fields are left blank, the saved credential), probes it, and
returns the detected env + account id(s). The probe never goes through `broker.New` (which would
also build a WebSocket client / need an account id up front) — it calls new REST-only helpers.

Wire types (`TestConnectionArgs`, `TestConnectionResult`, `TestAccount`) are added to
`wsmsg/payloads.go` and regenerated to TS via tygo.

## Global constraints (binding on every task)

- Every probe call is READ-ONLY (`GET /v2/account`, `GET /v2/clock`, `GET /v1/api/accounts`).
  No task may add a code path that submits, replaces, cancels, or flattens an order, or that
  calls any `Trd_*`/write endpoint. This is a hard safety boundary, not a style preference.
  Read-only account/clock/accounts probes are explicitly fine per `CLAUDE.md` even against live
  keys — do not add extra "are you sure" gating beyond what the plan specifies.
- The probe path must NEVER go through `broker.New`/`alpaca.New`/`tradezero.New` (which build a
  full adapter with a WebSocket client and, for TradeZero, require a non-empty AccountID up
  front). Use new, narrowly-scoped REST-only helper functions instead.
- `venueprobe` must not import `uihub`; `uihub` imports `venueprobe` (one-directional, mirrors
  how `uihub` already imports `config` for the `venueAdmin` seam). Do not introduce a cycle.
- The engine `TestConnection` handler must bound its own context with
  `context.WithTimeout(ctx, 8*time.Second)` — the UI's `sendCommand` promise has no client-side
  timeout, so a hung outbound HTTP call would hang the UI forever without this.
  `venueprobe.Prober.TestConnection` itself must place NO further timeout wrapping of its own
  beyond honoring the ctx passed in (a single 8s budget for the whole call, not per-sub-probe).
- Wire types added to `wsmsg/payloads.go` are the ONLY source of truth for the TS shapes —
  regenerate `ui/src/gen/wsmsg.ts` via tygo rather than hand-editing it, and run
  `make gen-ts-check` to confirm no drift before finishing any task that touches wire DTOs.
- Every new/changed `newCommands(...)` and `uihub.New(...)` call site (production AND test) must
  compile — this is a required-arg change, not optional; missed call sites are a build failure,
  not a runtime one, so `go build ./...` is the check.
- Follow existing package conventions exactly: Alpaca's `apiError`/`>=400` handling style in
  `rest.go`, TradeZero's decode-tolerant array/object handling (`decodeAccountDetails`'s pattern)
  in its own `rest.go`, and the existing per-endpoint token-bucket rate limiting
  (`rc.bAcct` for TradeZero, the pooled bucket for Alpaca) — new methods must go through the same
  `rc.do`/bucket-gated path as existing methods, not bypass it.
- UI: reuse the existing patterns already in `VenuesSection.tsx` — `useToasts()` for
  success/failure feedback, the two-click confirm/label-swap idiom for a transient button state,
  `data-testid` naming convention (`venue-<field>-${i}`), and the `rowKeys`-keyed state pattern
  (like `capsByRow`) for anything keyed per-venue-row rather than by mutable `id`.

## Task 1: Alpaca REST-only verify helper

Files: `engine/internal/broker/alpaca/rest.go`, `engine/internal/broker/alpaca/alpaca.go`,
`engine/internal/broker/alpaca/rest_test.go`.

- In `rest.go`, add `func (rc *restClient) verifyAccount(ctx context.Context) (accountNumber string, err error)`:
  issues `GET /v2/account` via the existing `rc.do` path (same as `snapshot`), decodes a minimal
  struct scoped to this function (e.g. `struct { AccountNumber string \`json:"account_number"\`; Status string \`json:"status"\` }`
  — independent of the existing `alpacaAccount` type, which has no id field), returns the account
  number. A `>=400` response is a real error via the existing `apiError` helper (this >=400 vs
  success is exactly how the caller will discriminate paper-vs-live: the right env's key
  authenticates, the wrong env's key gets a 401/403).
- In `alpaca.go`, add an exported package-level function:
  `func VerifyCredentials(ctx context.Context, env string, cr creds.Pair, clk clock.Clock) (string, error)`.
  It selects the base URL for `env` reusing the exact same paper/live selection logic `New`
  already uses (the `live := cfg.Env == "live"` / `defaultPaperRESTBase` / `defaultLiveRESTBase`
  branch around alpaca.go's `New`), builds a `newRESTClient` directly, and calls `verifyAccount`.
  It must NOT construct an `*Adapter`, NOT build a `wsClient`, and NOT start any goroutine — this
  is a bare synchronous REST call.
- Tests in `rest_test.go` (httptest, following this file's existing style — see
  `TestSubmit_HTTP200Rejected_R114`-style helpers in the tradezero package for the general
  pattern, or existing alpaca rest tests for this package's own idioms): a 200 response decodes
  the account number; a 401/403 response returns a non-nil error via `apiError`; a malformed body
  is a decode error, never a silent success.

Report file: `.claude/worktrees/test-connection-button/.superpowers/sdd/task-1-report.md`.

## Task 2: TradeZero list-accounts helper

Files: `engine/internal/broker/tradezero/rest.go`, `engine/internal/broker/tradezero/tradezero.go`,
`engine/internal/broker/tradezero/rest_test.go`, `engine/internal/broker/tradezero/testdata/accounts.json`.

- In `rest.go`, add `AccountType string \`json:"accountType"\`` to the existing `tzAccount` struct
  (used today by `decodeAccountDetails`). Update `testdata/accounts.json` if needed so its
  fixture includes an `accountType` value (e.g. `"Live"`) consistent with
  `docs/2026-07-03-tradezero-api.md`'s documented shape.
- Add `func (rc *restClient) listAccounts(ctx context.Context) ([]tzAccount, error)`:
  `GET /v1/api/accounts` (no account id in the path — unlike every other method in this file),
  rate-limited through the existing `rc.bAcct` bucket via `rc.do`, decode-tolerant of a bare JSON
  array (reuse/extract the array-decoding half of `decodeAccountDetails` rather than duplicating
  it — that function already knows how to decode `[]tzAccount`).
- In `tradezero.go`, add an exported `type AccountInfo struct { AccountID, AccountType string }`
  and package-level `func FetchAccounts(ctx context.Context, cr creds.Pair, clk clock.Clock) ([]AccountInfo, error)`:
  builds `newRESTClient(defaultRESTBase, "", cr.KeyID, cr.SecretKey, clk)` (the accounts-list
  endpoint takes no account id in its path, so an empty string here is correct — do not require
  or validate a non-empty account id, unlike `New`, which does) and maps `listAccounts`' rows to
  `AccountInfo`.
- Tests in `rest_test.go`: `listAccounts` against `testdata/accounts.json` returns the account
  with its `accountType`; an empty-array response returns an empty slice with no error; a >=400
  response is a real error.

Report file: `.claude/worktrees/test-connection-button/.superpowers/sdd/task-2-report.md`.

## Task 3: Wire DTOs for TestConnection

Files: `engine/internal/uihub/wsmsg/payloads.go`.

Append after the existing venue/credential DTOs (near the `DeleteCredentialArgs` struct at the
end of the "venue & credentials config DTOs" section):

```go
// ---- test-connection probe (settings "Venues & credentials" Test button) ----

// TestConnectionArgs carries the (possibly not-yet-saved) credential under
// test. KeyID/SecretKey are the typed-but-unsaved values from the UI form
// when non-empty; when both are empty the engine falls back to the saved
// credential named by Credentials.
type TestConnectionArgs struct {
	Broker      string `json:"broker"`
	Env         string `json:"env"`
	Credentials string `json:"credentials"`
	KeyID       string `json:"keyId"`
	SecretKey   string `json:"secretKey"`
	AccountID   string `json:"accountId"`
}

// TestAccount is one candidate account a probe discovered (TradeZero can
// return more than one; the UI offers a picker when len(Accounts) > 1).
type TestAccount struct {
	AccountID   string `json:"accountId"`
	AccountType string `json:"accountType"`
	Env         string `json:"env"`
}

// TestConnectionResult is the TestConnection command's AckMsg.Value payload.
// OK is the auth outcome (distinct from AckMsg.Status, which is the
// transport-level accepted/blocked outcome — a malformed-args request is
// "blocked" at the transport level; a bad API key is a transport-level
// "accepted" ack carrying OK:false here).
type TestConnectionResult struct {
	OK          bool          `json:"ok"`
	Env         string        `json:"env"`
	AccountID   string        `json:"accountId"`
	AccountType string        `json:"accountType"`
	Message     string        `json:"message"`
	Accounts    []TestAccount `json:"accounts"`
}
```

Then regenerate and verify: from `engine/`, run `make gen-ts` (writes
`ui/src/gen/wsmsg.ts`) and `make gen-ts-check` (confirms no drift). Confirm the three new types
are present and exported in the regenerated `ui/src/gen/wsmsg.ts`.

Report file: `.claude/worktrees/test-connection-button/.superpowers/sdd/task-3-report.md`.

## Task 4: `venueprobe` package

Files: `engine/internal/venueprobe/venueprobe.go`, `engine/internal/venueprobe/venueprobe_test.go`.

Depends on Task 1 (`alpaca.VerifyCredentials`) and Task 2 (`tradezero.FetchAccounts`,
`tradezero.AccountInfo`) already existing and merged into this worktree's history — this task's
implementer should read those two functions' final signatures directly from
`engine/internal/broker/alpaca/alpaca.go` and `engine/internal/broker/tradezero/tradezero.go`
before writing `venueprobe.go`.

New package, broker-agnostic orchestration only (no HTTP itself):

```go
package venueprobe

type Account struct { AccountID, AccountType, Env string }

type Result struct {
	OK          bool
	Env         string
	AccountID   string
	AccountType string
	Message     string
	Accounts    []Account
}

type Prober struct {
	credsPath string
	clk       clock.Clock
	// Injectable seams for tests (no network); default to the real broker
	// helpers in New.
	alpacaVerify    func(ctx context.Context, env string, cr creds.Pair, clk clock.Clock) (string, error)
	tzFetchAccounts func(ctx context.Context, cr creds.Pair, clk clock.Clock) ([]tradezero.AccountInfo, error)
}

func New(credsPath string, clk clock.Clock) *Prober {
	return &Prober{
		credsPath: credsPath, clk: clk,
		alpacaVerify:    alpaca.VerifyCredentials,
		tzFetchAccounts: tradezero.FetchAccounts,
	}
}

func (p *Prober) TestConnection(ctx context.Context, broker, env, credName, keyID, secretKey, accountID string) Result {
	...
}
```

`TestConnection` logic:

1. **Resolve credentials.** If `keyID` and `secretKey` are both non-empty, use them directly as a
   `creds.Pair{KeyID: keyID, SecretKey: secretKey}` — this is the "just typed, not yet saved"
   case. Otherwise load the saved credential: `creds.Load(p.credsPath)` then `.Get(credName)`; on
   any error (file missing, name missing, empty pair) return
   `Result{OK: false, Message: "no credentials to test"}` — never panic, never call a broker
   helper with an empty pair.
2. **Dispatch on `broker`:**
   - `"alpaca"`: call `p.alpacaVerify(ctx, env, cr, p.clk)`. If it errors, try the *other* env
     (`"live"` if env was `"paper"` or anything else, `"paper"` if env was `"live"`) via a second
     `p.alpacaVerify` call. Whichever call succeeds first is the detected env:
     `Result{OK: true, Env: detectedEnv, Message: "account " + accountNumber}`
     (`AccountID` is intentionally left empty in the result — Alpaca needs none). If BOTH calls
     error, return `Result{OK: false, Message: <the second/most-relevant error's message>}`.
   - `"tradezero"`: call `p.tzFetchAccounts(ctx, cr, p.clk)`. On error, return
     `Result{OK: false, Message: err.Error()}`. On an empty slice, return
     `Result{OK: false, Message: "no accounts (check credentials)"}`. Otherwise map each
     `tradezero.AccountInfo` to an `Account{AccountID, AccountType, Env}` where `Env` is
     `"paper"` when `AccountType` case-insensitively contains `"paper"`, else `"live"`. If exactly
     one account, return `Result{OK: true, Env: acct.Env, AccountID: acct.AccountID, AccountType: acct.AccountType}`.
     If more than one, return `Result{OK: true, Accounts: accts}` (env/accountId left empty —
     the UI resolves the choice) with `Message` left empty (the UI's `Accounts` picker is the
     the signal, not a message string).
   - `"moomoo"`: return `Result{OK: false, Message: "connection testing is not supported for moomoo"}`
     unconditionally — do not attempt any network call.
   - default (`"sim"` or anything unrecognized): return
     `Result{OK: false, Message: "connection testing is not supported for " + broker}`.

Unit tests (no network — inject fakes for `alpacaVerify`/`tzFetchAccounts` on a `Prober` built by
hand, not via `New`):
- Credential resolution: typed keyId/secret bypasses the creds file; empty typed fields fall back
  to `creds.Load` + `.Get` against a real temp file written by the test; a missing/absent name
  returns the "no credentials to test" result without calling either injected fake.
- Alpaca: fake that succeeds only for `"live"` when called with `env:"paper"` → result reports
  `Env:"live", OK:true`; fake that always errors → `OK:false` with a message.
- TradeZero: fake returning one account → single-account result shape; fake returning two
  accounts (one `"Live"`, one `"Paper"`) → `Accounts` populated with correct `Env` mapping for
  each, top-level `Env`/`AccountID` left empty; fake returning an empty slice → the
  "no accounts" message; fake returning an error → that error's message passed through.
- moomoo and an unrecognized broker string both return `OK:false` without touching either fake.

Report file: `.claude/worktrees/test-connection-button/.superpowers/sdd/task-4-report.md`.

## Task 5: Command dispatch wiring

Files: `engine/internal/uihub/commands.go`, `engine/internal/uihub/api.go`,
`engine/internal/uihub/commands_test.go`.

Depends on Task 3 (wire DTOs) and Task 4 (`venueprobe.Prober`/`Result`) already existing in this
worktree's history.

- In `commands.go`: add an interface (beside `venueAdmin`)
  ```go
  // venueTester is the read-only credential-probe seam (satisfied by
  // *venueprobe.Prober). Every implementation must be side-effect-free
  // against the broker (GET-only) — see the plan's Global Constraints.
  type venueTester interface {
      TestConnection(ctx context.Context, broker, env, credName, keyID, secretKey, accountID string) venueprobe.Result
  }
  ```
  Add a `probe venueTester` field to the `commands` struct, and thread it through
  `newCommands(ex execDoer, cfg configStore, ind indicatorCtl, dem demandCtl, va venueAdmin, feed func() Feed, probe venueTester) *commands`
  (new trailing parameter — append, don't reorder existing params, to keep the diff minimal at
  every non-test call site).
- Add `import "github.com/earlisreal/eTape/engine/internal/venueprobe"` to `commands.go`.
- Add a new case in `(*commands).handle`'s switch, placed near the other venue cases
  (after `"DeleteCredential"`, before `default`):
  ```go
  case "TestConnection":
      var a wsmsg.TestConnectionArgs
      if err := json.Unmarshal(args, &a); err != nil {
          return blocked("bad args")
      }
      pctx, cancel := context.WithTimeout(ctx, 8*time.Second)
      defer cancel()
      r := cd.probe.TestConnection(pctx, a.Broker, a.Env, a.Credentials, a.KeyID, a.SecretKey, a.AccountID)
      return wsmsg.AckMsg{Status: "accepted", Value: resultToWire(r)}
  ```
  (Note: `AckMsg.Status` stays `"accepted"` even when `r.OK` is false — a bad key is a valid,
  transport-successful probe outcome, not a malformed/rejected request. Only a JSON-unmarshal
  failure returns `blocked`.) Add the `time` import if not already present in this file (check
  first — it may already be imported for another reason; if not, add it).
- Add a `resultToWire` helper beside the existing `venueConfigToWire`/`gateToWire` helpers at the
  bottom of `commands.go`:
  ```go
  func resultToWire(r venueprobe.Result) wsmsg.TestConnectionResult {
      accts := make([]wsmsg.TestAccount, 0, len(r.Accounts))
      for _, a := range r.Accounts {
          accts = append(accts, wsmsg.TestAccount{AccountID: a.AccountID, AccountType: a.AccountType, Env: a.Env})
      }
      return wsmsg.TestConnectionResult{
          OK: r.OK, Env: r.Env, AccountID: r.AccountID, AccountType: r.AccountType,
          Message: r.Message, Accounts: accts,
      }
  }
  ```
- In `api.go`: add `probe venueTester` as a new trailing parameter to `uihub.New(...)` and pass it
  through to the `newCommands(...)` call inside (`newCommands(ex, st, ind, h, va, h.feed, probe)`).
- In `commands_test.go`: update every existing `newCommands(...)` call site (there are roughly 15)
  to pass a new trailing spy, e.g.:
  ```go
  type spyVenueTester struct{ result venueprobe.Result }
  func (s *spyVenueTester) TestConnection(context.Context, string, string, string, string, string, string) venueprobe.Result {
      return s.result
  }
  ```
  Passing `&spyVenueTester{}` (zero-value `Result`) at every pre-existing call site is sufficient
  — those tests don't exercise `TestConnection` and only need the signature to compile.
- Add new tests: bad/malformed args → `blocked("bad args")`, `cd.probe` never called (verify via
  a spy that records whether it was invoked); a spy returning a populated `venueprobe.Result`
  (`OK:true, Env:"live", AccountID:"2TZ1"`) → `ack.Status == "accepted"` and
  `ack.Value.(wsmsg.TestConnectionResult)` carries the same fields through `resultToWire`
  unchanged, including a non-empty `Accounts` slice mapped correctly.

Report file: `.claude/worktrees/test-connection-button/.superpowers/sdd/task-5-report.md`.

## Task 6: Boot wiring

Files: `engine/cmd/etape/main.go`.

Depends on Task 4 (`venueprobe.New`) and Task 5 (`uihub.New`'s new trailing parameter).

- Add `import "github.com/earlisreal/eTape/engine/internal/venueprobe"`.
- Beside the existing `venueAdm := venueadmin.New(...)` line, add:
  `venueProbe := venueprobe.New(creds.DefaultPath(), uihubClk)` (reuse the same `uihubClk` and
  `creds.DefaultPath()` the surrounding code already uses for `venueAdm` — do not introduce a
  second clock or a hardcoded path).
- Pass `venueProbe` as the new trailing argument to the existing `uihub.New(...)` call.
- Verify the whole engine still builds: `cd engine && go build ./...`.

Report file: `.claude/worktrees/test-connection-button/.superpowers/sdd/task-6-report.md`.

## Task 7: UI — Test connection button, auto-detected fields, save gating

Files: `ui/src/chrome/exec/VenuesSection.tsx`, `ui/src/chrome/exec/VenuesSection.test.tsx`.

Depends on Task 3's regenerated `ui/src/gen/wsmsg.ts` (for `TestConnectionArgs` /
`TestConnectionResult` / `TestAccount` types, re-exported via `../../wire/contract`) and Task 5/6
(the `TestConnection` command existing end-to-end) — this task's implementer should confirm those
types import cleanly before starting.

Current relevant code (for orientation — read the actual current file before editing, this
worktree's `VenuesSection.tsx` is the source of truth):
- `BROKERS`/`ENVS`/`CRED_REQUIRED_BROKERS` constants near the top.
- The venue-card render loop; the "Connection" field group renders `id`, `broker`, `env` (a plain
  `<select>` over `ENVS`), `account id` (a plain `<input>`, always shown), and `starting balance`
  (sim only).
- The "Credentials" field group (shown when `v.broker !== "sim"`) renders `key id`/`secret`
  password inputs plus a `key set`/`no key` chip.
- The `validation` memo drives `hasErrors`, which disables the Save button.

Changes:

1. **Constants.** Add `const TESTABLE_BROKERS = new Set(["tradezero", "alpaca"]);` near the
   existing broker constants.
2. **State.** Add
   ```ts
   type TestStatus = "idle" | "testing" | "ok" | "fail";
   const [testState, setTestState] = useState<Record<string, { status: TestStatus; message?: string }>>({});
   ```
   Key it by `rowKeys[i]` — the same stable per-row synthetic key `capsByRow` already uses —
   NOT by `v.id` or `v.credentials` (id can be edited mid-session; the row key can't).
3. **Invalidate stale test results.** Inside the existing `setSecretField` and `setBroker`
   functions, also clear that row's `testState` entry back to absent/`"idle"` — a Test result
   from a previous key or broker must not silently persist as "verified" once the input it was
   testing has changed. (`setSecretField` takes a credential name, not a row index; find the row
   index or key from context at each call site, or key `testState` off the row index passed
   alongside — pick whichever is more consistent with how `secretDrafts` is already threaded, and
   note the choice in your report.)
4. **Hide manual env / account-id inputs for testable brokers, add read-only detected display.**
   In the "Connection" field group:
   - Render the existing `env` `<select>` only when `!TESTABLE_BROKERS.has(v.broker)`.
   - Render the existing `account id` `<input>` only when `v.broker === "moomoo"` (moomoo keeps
     manual entry; alpaca doesn't use one; tradezero's is now auto-detected).
   - When `TESTABLE_BROKERS.has(v.broker)`, render a read-only summary instead: the detected env
     (reuse the existing LIVE/live-red styling already used for the venue-card header chip) and,
     for `v.broker === "tradezero"`, the detected account id. When the last Test result for this
     row returned more than one candidate account (`testState[...].accounts` — see point 5 below
     for where that comes from), render a `<select>` populated from those candidates instead of
     plain text, wired to `patchVenue(i, { accountId })`.
   - Validation must still require `v.accountId` for a saved tradezero venue (existing rule) — it
     is now populated by the auto-detect flow rather than typed, but the check itself is
     unchanged.
5. **Test connection button.** In the "Credentials" group, after the secret input, when
   `TESTABLE_BROKERS.has(v.broker)`, add a button (`data-testid="venue-test-${i}"`, class `btn`,
   disabled while `testState[rowKeys[i]]?.status === "testing"`) whose label is
   `"Testing…"`/`"Test connection"` based on that same status. On click:
   ```ts
   const rowKey = rowKeys[i];
   setTestState((s) => ({ ...s, [rowKey]: { status: "testing" } }));
   const typed = secretDrafts[v.credentials] ?? { keyId: "", secret: "" };
   try {
     const ack = await commands.sendCommand("TestConnection", {
       broker: v.broker, env: v.env, credentials: v.credentials,
       keyId: typed.keyId, secretKey: typed.secret, accountId: v.accountId,
     });
     const r = ack.value as TestConnectionResult | undefined;
     if (ack.status === "accepted" && r?.ok) {
       patchVenue(i, { env: r.env || v.env, accountId: r.accountId || v.accountId });
       setTestState((s) => ({ ...s, [rowKey]: { status: "ok", message: `Connected · ${(r.env || v.env).toUpperCase()}${r.accountId ? " · " + r.accountId : ""}` } }));
       toast.push({ level: "success", text: testState[rowKey]?.message ?? "Connection verified." }); // adapt to whichever message you just computed
     } else {
       setTestState((s) => ({ ...s, [rowKey]: { status: "fail", message: r?.message || ack.reason || "Test failed" } }));
       toast.push({ level: "danger", text: r?.message || ack.reason || "Test failed" });
     }
   } catch {
     setTestState((s) => ({ ...s, [rowKey]: { status: "fail", message: "Test failed (transport)." } }));
     toast.push({ level: "danger", text: "Test failed (transport)." });
   }
   ```
   Store the `TestAccount[]` from a multi-account TradeZero result somewhere reachable by the
   render code in point 4 (e.g. extend the `testState` entry's shape with an optional
   `accounts?: TestAccount[]` field) — do not introduce a second untracked piece of state for it.
   Render the `✓ <message>` / `✗ <message>` result inline near the button (reuse the existing
   `issueText`-style small colored text pattern already used elsewhere in this file), using
   `palette.accent`/success-ish color for `"ok"` and `palette.danger` for `"fail"`.
6. **Save gating.** Extend the `validation` memo: for a venue whose broker is in
   `TESTABLE_BROKERS`, compute
   ```ts
   const rowKey = rowKeys[i]; // available in the memo's existing per-venue loop, or thread it through
   const tested = testState[rowKey]?.status === "ok";
   const typedNewKeys = !!(secretDrafts[v.credentials]?.keyId && secretDrafts[v.credentials]?.secret);
   const preexistingComplete = keySet && !!v.env && (v.broker !== "tradezero" || !!v.accountId);
   // keySet is already computed per-row elsewhere in this file for the "key set" chip — reuse
   // that same derivation ((setup?.credKeys ?? []).includes(v.credentials)) inside the memo.
   const verified = tested || (preexistingComplete && !typedNewKeys);
   if (!verified) issues.cred = issues.cred ?? "test connection before saving";
   ```
   (Don't clobber an existing `issues.cred` message from the current required-credential check —
   only set this message when no stronger cred issue already applies.) This keeps a pure
   risk-limit edit on an already-configured, unchanged venue saveable without forcing a re-test,
   while requiring a test for any new venue or any venue whose key was just retyped.

Tests (`VenuesSection.test.tsx`, extend the existing test file, following its existing
render/interact/assert style):
- For a tradezero venue: typing key id + secret and clicking Test (with the `sendCommand` mock
  resolving an `accepted` ack whose `value` is a single-account `TestConnectionResult`) fills the
  read-only env/account-id display and enables Save; Save stays disabled before clicking Test.
- A `TestConnectionResult` with `accounts.length > 1` renders a picker; selecting one sets
  `accountId`.
- A failing test result (`ok:false`) shows the failure message and leaves Save disabled.
- The manual `env`/`account id` inputs are absent for `tradezero`/`alpaca` rows and present for
  `moomoo`/`sim` rows.
- Editing an already-verified row's secret fields resets its test status (Save becomes disabled
  again without a fresh Test).

Report file: `.claude/worktrees/test-connection-button/.superpowers/sdd/task-7-report.md`.

## Verification (final, whole-branch)

- `cd engine && make gen-ts && go build ./... && go test ./internal/broker/... ./internal/venueprobe/... ./internal/uihub/...`
- `cd engine && make gen-ts-check` (no TS drift).
- `cd ui && npm run test -- VenuesSection && npx tsc --noEmit`.
- Manual (live OpenD not required): open Settings → Venues & creds, add a TradeZero venue, enter
  the live keys from `~/.eTape/credentials.json`, click **Test connection** → expect `✓ Connected ·
  LIVE · 2TZ…`, env+acct filled read-only, Save enabled. Repeat for an Alpaca paper key (`alpaca`)
  → detects `PAPER`. Enter a bad key → `✗` with the broker error, Save stays disabled.
  (Read-only probe only — no orders placed.)
