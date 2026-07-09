// Package venueprobe orchestrates broker-agnostic "test connection" probes
// for eTape's Venues & credentials settings UI. Given a broker name, an env,
// and either typed-but-unsaved credentials or the name of a saved one, it
// calls the already-built read-only helpers in broker/alpaca
// (VerifyCredentials) and broker/tradezero (FetchAccounts) and normalizes
// whatever they return into one Result shape the UI can render, regardless
// of which broker was probed.
//
// This package does no HTTP itself — every probe is a call to a Task 1/2
// read-only helper — and applies no timeout of its own beyond honoring the
// ctx its caller passes in; a single budget for the whole TestConnection
// call is the caller's job (a later uihub command handler), not this
// package's.
package venueprobe

import (
	"context"
	"strings"

	"github.com/earlisreal/eTape/engine/internal/broker/alpaca"
	"github.com/earlisreal/eTape/engine/internal/broker/tradezero"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/creds"
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
	clk       clock.Clock

	// alpacaVerify and tzFetchAccounts are injectable seams for tests (no
	// network); New wires them to the real broker helpers below.
	alpacaVerify    func(ctx context.Context, env string, cr creds.Pair, clk clock.Clock) (string, error)
	tzFetchAccounts func(ctx context.Context, cr creds.Pair, clk clock.Clock) ([]tradezero.AccountInfo, error)
}

// New builds a Prober wired to the real alpaca/tradezero read-only helpers.
func New(credsPath string, clk clock.Clock) *Prober {
	return &Prober{
		credsPath:       credsPath,
		clk:             clk,
		alpacaVerify:    alpaca.VerifyCredentials,
		tzFetchAccounts: tradezero.FetchAccounts,
	}
}

// TestConnection probes broker with a single read-only call and reports the
// outcome. accountID is accepted for parity with the wire args but unused by
// any probe today: Alpaca needs no account id, and TradeZero's probe lists
// every account visible to the key rather than targeting one.
//
// moomoo and any unrecognized broker name are rejected before credentials
// are even looked at — neither one ever calls a broker helper, so there is
// nothing for a missing/typed credential to gate; requiring a saved
// credential just to learn "not supported" would be a needless and
// confusing failure mode for those two cases.
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
		return Result{OK: false, Message: "connection testing is not supported for moomoo"}
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
