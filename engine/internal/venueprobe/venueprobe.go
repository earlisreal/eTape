// Package venueprobe orchestrates broker-agnostic "test connection" probes
// for eTape's Venues & credentials settings UI. Given a broker name, an env,
// and either typed-but-unsaved credentials or the name of a saved one, it
// calls the already-built read-only helpers in broker/alpaca
// (VerifyCredentials), broker/tradezero (FetchAccounts), and broker/moomoo
// (VerifyAccount) and normalizes whatever they return into one Result shape
// the UI can render, regardless of which broker was probed. moomoo has no
// key/secret to verify — it dials the local OpenD gateway directly and
// validates the configured account id instead.
//
// This package does no HTTP/TCP itself — every probe is a call to a broker
// package's own read-only helper — and applies no timeout of its own beyond
// honoring the ctx its caller passes in; a single budget for the whole
// TestConnection call is the caller's job (a later uihub command handler),
// not this package's.
package venueprobe

import (
	"context"
	"strconv"
	"strings"

	"github.com/earlisreal/eTape/engine/internal/broker/alpaca"
	"github.com/earlisreal/eTape/engine/internal/broker/moomoo"
	"github.com/earlisreal/eTape/engine/internal/broker/tradezero"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/creds"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/trdcommon"
)

// Account is one candidate account a probe discovered. TradeZero can return
// more than one account for a single key pair; the UI offers a picker when
// len(Result.Accounts) > 1.
type Account struct {
	AccountID   string
	AccountType string
	Env         string
}

// Result is the outcome of one TestConnection call.
type Result struct {
	OK          bool
	Env         string
	AccountID   string
	AccountType string
	Message     string
	Accounts    []Account
}

// Prober orchestrates connection tests across brokers. Build one via New for
// real use; tests construct a Prober by hand with alpacaVerify/
// tzFetchAccounts fakes injected so no probe ever makes a real network call.
type Prober struct {
	credsPath string
	// openDAddr is the local OpenD gateway's host:port — a fixed,
	// process-wide value (like credsPath), used only by the moomoo probe.
	openDAddr string
	clk       clock.Clock

	// alpacaVerify, tzFetchAccounts, moomooVerify, and moomooList are
	// injectable seams for tests (no network); New wires them to the real
	// broker helpers below.
	alpacaVerify    func(ctx context.Context, env string, cr creds.Pair, clk clock.Clock) (string, error)
	tzFetchAccounts func(ctx context.Context, cr creds.Pair, clk clock.Clock) ([]tradezero.AccountInfo, error)
	moomooVerify    func(ctx context.Context, addr string, accountID uint64, env string, clk clock.Clock) (*trdcommon.TrdAcc, error)
	moomooList      func(ctx context.Context, addr, clientID string, clk clock.Clock) ([]*trdcommon.TrdAcc, error)
}

// moomooProbeClientID is the OpenD client identity BOTH moomoo probe paths
// (validate via moomooVerify/VerifyAccount and discovery via
// moomooList/ListAccounts) use — a fixed, throwaway-connection identity
// distinct from the long-lived adapter's "etape-trade" and venueseed's
// "etape-seed", so a concurrent probe/seed/adapter connection are each
// individually visible to OpenD.
const moomooProbeClientID = "etape-trade-probe"

// New builds a Prober wired to the real alpaca/tradezero/moomoo read-only
// helpers. openDAddr is the local OpenD gateway address (config.OpenD.Addr())
// the moomoo probe dials.
func New(credsPath, openDAddr string, clk clock.Clock) *Prober {
	return &Prober{
		credsPath:       credsPath,
		openDAddr:       openDAddr,
		clk:             clk,
		alpacaVerify:    alpaca.VerifyCredentials,
		tzFetchAccounts: tradezero.FetchAccounts,
		moomooVerify:    moomoo.VerifyAccount,
		moomooList:      moomoo.ListAccounts,
	}
}

// TestConnection probes broker with a single read-only call and reports the
// outcome. accountID is accepted for parity with the wire args but only
// moomoo's probe uses it today: Alpaca needs no account id, and TradeZero's
// probe lists every account visible to the key rather than targeting one.
//
// moomoo has no key/secret to verify, so its case skips resolveCreds
// entirely and goes straight to testMoomoo, which dials OpenD directly and
// is bimodal on accountID: non-empty validates that account, empty runs
// discovery (lists every eligible live US account instead). Only a truly
// unrecognized broker name is rejected before anything is looked at, with
// the generic "not supported" message.
func (p *Prober) TestConnection(ctx context.Context, broker, env, credName, keyID, secretKey, accountID string) Result {
	switch broker {
	case "alpaca":
		cr, ok := p.resolveCreds(credName, keyID, secretKey)
		if !ok {
			return Result{OK: false, Message: "no credentials to test"}
		}
		return p.testAlpaca(ctx, env, cr)
	case "tradezero":
		cr, ok := p.resolveCreds(credName, keyID, secretKey)
		if !ok {
			return Result{OK: false, Message: "no credentials to test"}
		}
		return p.testTradeZero(ctx, cr)
	case "moomoo":
		return p.testMoomoo(ctx, env, accountID)
	default:
		return Result{OK: false, Message: "connection testing is not supported for " + broker}
	}
}

// resolveCreds returns the credential pair to probe with: the typed values
// when both are non-empty (the "just typed, not yet saved" case), else the
// saved credential named credName loaded from p.credsPath. The second
// return is false when neither source yields a usable pair — callers must
// never invoke a broker helper in that case.
func (p *Prober) resolveCreds(credName, keyID, secretKey string) (creds.Pair, bool) {
	if keyID != "" && secretKey != "" {
		return creds.Pair{KeyID: keyID, SecretKey: secretKey}, true
	}
	file, err := creds.Load(p.credsPath)
	if err != nil {
		return creds.Pair{}, false
	}
	cr, err := file.Get(credName)
	if err != nil {
		return creds.Pair{}, false
	}
	return cr, true
}

// otherEnv returns the opposite Alpaca environment: "live" for anything
// that isn't "live" (including "paper" or an unrecognized value), else
// "paper".
func otherEnv(env string) string {
	if env == "live" {
		return "paper"
	}
	return "live"
}

// testAlpaca tries env first, then the other env on failure — a wrong-env
// key gets a 401/403 from VerifyCredentials, so trying both is how this
// package distinguishes a paper key from a live key without asking the user
// which one they typed. Whichever call succeeds first is the detected env;
// if both fail, the second (fallback) call's error is the most relevant one
// to surface, since it is the one for the env this key is more likely NOT
// meant for if the caller's stated env was already wrong.
func (p *Prober) testAlpaca(ctx context.Context, env string, cr creds.Pair) Result {
	acctNum, err := p.alpacaVerify(ctx, env, cr, p.clk)
	if err == nil {
		return Result{OK: true, Env: env, Message: "account " + acctNum}
	}

	fallback := otherEnv(env)
	acctNum, err = p.alpacaVerify(ctx, fallback, cr, p.clk)
	if err == nil {
		return Result{OK: true, Env: fallback, Message: "account " + acctNum}
	}
	return Result{OK: false, Message: err.Error()}
}

// testTradeZero lists every account visible to cr and maps the result into
// either a top-level Env/AccountID/AccountType (exactly one account) or an
// Accounts list for the UI to offer a picker (more than one) — TradeZero,
// unlike Alpaca, can have several accounts behind one key pair.
func (p *Prober) testTradeZero(ctx context.Context, cr creds.Pair) Result {
	infos, err := p.tzFetchAccounts(ctx, cr, p.clk)
	if err != nil {
		return Result{OK: false, Message: err.Error()}
	}
	if len(infos) == 0 {
		return Result{OK: false, Message: "no accounts (check credentials)"}
	}

	accts := make([]Account, len(infos))
	for i, info := range infos {
		accts[i] = Account{
			AccountID:   info.AccountID,
			AccountType: info.AccountType,
			Env:         tzEnv(info.AccountType),
		}
	}
	if len(accts) == 1 {
		a := accts[0]
		return Result{OK: true, Env: a.Env, AccountID: a.AccountID, AccountType: a.AccountType}
	}
	return Result{OK: true, Accounts: accts}
}

// tzEnv maps a TradeZero AccountType (e.g. "Live", "Paper") to eTape's env
// string via a case-insensitive Contains (not an exact match), since
// TradeZero's documented value could vary in case or wording.
func tzEnv(accountType string) string {
	if strings.Contains(strings.ToLower(accountType), "paper") {
		return "paper"
	}
	return "live"
}

// testMoomoo probes a moomoo venue by dialing OpenD directly — unlike
// alpaca/tradezero, there is no key/secret to verify. It is bimodal on
// accountID: a non-empty accountID validates that specific account, byte-
// for-byte the same behavior this had before discovery mode existed; an
// empty accountID switches to discovery mode (testMoomooDiscover), listing
// every eligible live US account OpenD's current login can see so the
// settings UI's account picker has something to offer.
func (p *Prober) testMoomoo(ctx context.Context, env, accountID string) Result {
	if accountID == "" {
		return p.testMoomooDiscover(ctx)
	}
	accID, err := strconv.ParseUint(accountID, 10, 64)
	if err != nil {
		return Result{OK: false, Message: "account_id must be numeric"}
	}
	_, err = p.moomooVerify(ctx, p.openDAddr, accID, env, p.clk)
	if err != nil {
		return Result{OK: false, Message: err.Error()}
	}
	return Result{OK: true, Env: env, AccountID: accountID}
}

// testMoomooDiscover lists every account OpenD's current login can see and
// filters with moomoo.EligibleLiveUS, mapping the survivors into Accounts the
// same way testTradeZero maps TradeZero's multi-account result. Unlike
// TradeZero (which can genuinely be either env), every account this filter
// keeps is by construction TrdEnv_Real, so Env is unconditionally "live" —
// both on the top-level Result and on each Account — rather than computed
// per account like tzEnv.
//
// Accounts is always populated (one entry per eligible account, even when
// there is exactly one) — a deliberate difference from testTradeZero's
// single-account case, which leaves Accounts empty. That is what lets the
// exactly-one branch below ADD the top-level AccountID/AccountType promotion
// (mirrors how testTradeZero promotes its own single-account case) without
// the UI having to choose between reading the top-level fields or the list.
//
// Zero eligible accounts is a definitive answer from the current OpenD
// login (the same account list a retry right now would also see), so it is
// reported as OK:false with a fixed, exact message rather than a transient
// error — a transport failure (dead OpenD) is the separate err != nil branch
// above it, and gets the same error-shape treatment validate mode's
// moomooVerify failure gets.
func (p *Prober) testMoomooDiscover(ctx context.Context) Result {
	accs, err := p.moomooList(ctx, p.openDAddr, moomooProbeClientID, p.clk)
	if err != nil {
		return Result{OK: false, Message: err.Error()}
	}

	var eligible []*trdcommon.TrdAcc
	for _, a := range accs {
		if moomoo.EligibleLiveUS(a) {
			eligible = append(eligible, a)
		}
	}
	if len(eligible) == 0 {
		return Result{OK: false, Message: "no live US-authorized account found on this OpenD login"}
	}

	accts := make([]Account, len(eligible))
	for i, a := range eligible {
		accts[i] = Account{
			AccountID:   strconv.FormatUint(a.GetAccID(), 10),
			AccountType: trdcommon.TrdAccType(a.GetAccType()).String(),
			Env:         "live",
		}
	}
	res := Result{OK: true, Env: "live", Accounts: accts}
	if len(accts) == 1 {
		res.AccountID = accts[0].AccountID
		res.AccountType = accts[0].AccountType
	}
	return res
}
