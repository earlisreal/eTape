package venueprobe

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/broker/tradezero"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/creds"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/trdcommon"
)

// failAlpaca returns an alpacaVerify fake that fails the test if invoked —
// used by cases that must never reach a broker helper.
func failAlpaca(t *testing.T) func(context.Context, string, creds.Pair, clock.Clock) (string, error) {
	return func(context.Context, string, creds.Pair, clock.Clock) (string, error) {
		t.Fatal("alpacaVerify should not have been called")
		return "", nil
	}
}

// failTZ returns a tzFetchAccounts fake that fails the test if invoked.
func failTZ(t *testing.T) func(context.Context, creds.Pair, clock.Clock) ([]tradezero.AccountInfo, error) {
	return func(context.Context, creds.Pair, clock.Clock) ([]tradezero.AccountInfo, error) {
		t.Fatal("tzFetchAccounts should not have been called")
		return nil, nil
	}
}

// failMoomoo returns a moomooVerify fake that fails the test if invoked.
func failMoomoo(t *testing.T) func(context.Context, string, uint64, string, clock.Clock) (*trdcommon.TrdAcc, error) {
	return func(context.Context, string, uint64, string, clock.Clock) (*trdcommon.TrdAcc, error) {
		t.Fatal("moomooVerify should not have been called")
		return nil, nil
	}
}

func fakeClock() clock.Clock { return clock.NewFake(time.Unix(0, 0)) }

// ---- credential resolution ----

func TestTestConnection_TypedCredentialsBypassFile(t *testing.T) {
	// credsPath points at a file with a DIFFERENT pair under the same name,
	// so if the code mistakenly fell back to the file, the fake below would
	// see the wrong pair (and this assertion would catch it).
	dir := t.TempDir()
	credsPath := filepath.Join(dir, "credentials.json")
	if err := os.WriteFile(credsPath, []byte(`{"myAlpaca":{"keyId":"wrong-key","secretKey":"wrong-secret"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var gotCr creds.Pair
	var calls int
	p := &Prober{
		credsPath: credsPath,
		clk:       fakeClock(),
		alpacaVerify: func(_ context.Context, env string, cr creds.Pair, _ clock.Clock) (string, error) {
			calls++
			gotCr = cr
			return "ACC1", nil
		},
		tzFetchAccounts: failTZ(t),
	}

	res := p.TestConnection(context.Background(), "alpaca", "paper", "myAlpaca", "typed-key", "typed-secret", "")

	if calls != 1 {
		t.Fatalf("alpacaVerify called %d times, want 1", calls)
	}
	want := creds.Pair{KeyID: "typed-key", SecretKey: "typed-secret"}
	if gotCr != want {
		t.Fatalf("alpacaVerify received %+v, want %+v (typed creds must bypass the file)", gotCr, want)
	}
	if !res.OK || res.Env != "paper" {
		t.Fatalf("res = %+v, want OK:true Env:paper", res)
	}
}

func TestTestConnection_EmptyTypedFieldsFallBackToSavedFile(t *testing.T) {
	dir := t.TempDir()
	credsPath := filepath.Join(dir, "credentials.json")
	if err := os.WriteFile(credsPath, []byte(`{"myAlpaca":{"keyId":"K1","secretKey":"S1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var gotCr creds.Pair
	p := &Prober{
		credsPath: credsPath,
		clk:       fakeClock(),
		alpacaVerify: func(_ context.Context, env string, cr creds.Pair, _ clock.Clock) (string, error) {
			gotCr = cr
			return "ACC1", nil
		},
		tzFetchAccounts: failTZ(t),
	}

	res := p.TestConnection(context.Background(), "alpaca", "live", "myAlpaca", "", "", "")

	want := creds.Pair{KeyID: "K1", SecretKey: "S1"}
	if gotCr != want {
		t.Fatalf("alpacaVerify received %+v, want %+v (loaded from file)", gotCr, want)
	}
	if !res.OK || res.Env != "live" {
		t.Fatalf("res = %+v, want OK:true Env:live", res)
	}
}

func TestTestConnection_MissingCredentialName_NoCredentialsToTest(t *testing.T) {
	dir := t.TempDir()
	credsPath := filepath.Join(dir, "credentials.json")
	if err := os.WriteFile(credsPath, []byte(`{"someOtherName":{"keyId":"K1","secretKey":"S1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	p := &Prober{
		credsPath:       credsPath,
		clk:             fakeClock(),
		alpacaVerify:    failAlpaca(t),
		tzFetchAccounts: failTZ(t),
	}

	res := p.TestConnection(context.Background(), "alpaca", "paper", "doesNotExist", "", "", "")

	want := Result{OK: false, Message: "no credentials to test"}
	if !reflect.DeepEqual(res, want) {
		t.Fatalf("res = %+v, want %+v", res, want)
	}
}

func TestTestConnection_MissingCredentialsFile_NoCredentialsToTest(t *testing.T) {
	p := &Prober{
		credsPath:       filepath.Join(t.TempDir(), "nonexistent.json"),
		clk:             fakeClock(),
		alpacaVerify:    failAlpaca(t),
		tzFetchAccounts: failTZ(t),
	}

	res := p.TestConnection(context.Background(), "tradezero", "", "anything", "", "", "")

	want := Result{OK: false, Message: "no credentials to test"}
	if !reflect.DeepEqual(res, want) {
		t.Fatalf("res = %+v, want %+v", res, want)
	}
}

// ---- alpaca ----

func TestTestConnection_Alpaca_FallsBackToOtherEnvOnFailure(t *testing.T) {
	var calls []string
	p := &Prober{
		clk: fakeClock(),
		alpacaVerify: func(_ context.Context, env string, _ creds.Pair, _ clock.Clock) (string, error) {
			calls = append(calls, env)
			if env == "live" {
				return "LIVE-ACC", nil
			}
			return "", errors.New("wrong env")
		},
		tzFetchAccounts: failTZ(t),
	}

	res := p.TestConnection(context.Background(), "alpaca", "paper", "", "k", "s", "")

	if !res.OK || res.Env != "live" {
		t.Fatalf("res = %+v, want OK:true Env:live", res)
	}
	if res.Message != "account LIVE-ACC" {
		t.Fatalf("res.Message = %q, want %q", res.Message, "account LIVE-ACC")
	}
	if len(calls) != 2 || calls[0] != "paper" || calls[1] != "live" {
		t.Fatalf("alpacaVerify calls = %v, want [paper live]", calls)
	}
}

func TestTestConnection_Alpaca_BothEnvsFail(t *testing.T) {
	var calls []string
	p := &Prober{
		clk: fakeClock(),
		alpacaVerify: func(_ context.Context, env string, _ creds.Pair, _ clock.Clock) (string, error) {
			calls = append(calls, env)
			return "", errors.New("401 for " + env)
		},
		tzFetchAccounts: failTZ(t),
	}

	res := p.TestConnection(context.Background(), "alpaca", "paper", "", "k", "s", "")

	if res.OK {
		t.Fatalf("res.OK = true, want false: %+v", res)
	}
	if res.Message != "401 for live" {
		t.Fatalf("res.Message = %q, want the fallback env's error %q", res.Message, "401 for live")
	}
	if len(calls) != 2 || calls[0] != "paper" || calls[1] != "live" {
		t.Fatalf("alpacaVerify calls = %v, want exactly [paper live]", calls)
	}
}

func TestTestConnection_Alpaca_LiveEnvFallsBackToPaper(t *testing.T) {
	var calls []string
	p := &Prober{
		clk: fakeClock(),
		alpacaVerify: func(_ context.Context, env string, _ creds.Pair, _ clock.Clock) (string, error) {
			calls = append(calls, env)
			return "", errors.New("boom")
		},
		tzFetchAccounts: failTZ(t),
	}

	p.TestConnection(context.Background(), "alpaca", "live", "", "k", "s", "")

	if len(calls) != 2 || calls[0] != "live" || calls[1] != "paper" {
		t.Fatalf("alpacaVerify calls = %v, want [live paper]", calls)
	}
}

// ---- tradezero ----

func TestTestConnection_TradeZero_SingleAccount(t *testing.T) {
	p := &Prober{
		clk:          fakeClock(),
		alpacaVerify: failAlpaca(t),
		tzFetchAccounts: func(context.Context, creds.Pair, clock.Clock) ([]tradezero.AccountInfo, error) {
			return []tradezero.AccountInfo{{AccountID: "12345", AccountType: "Live"}}, nil
		},
	}

	res := p.TestConnection(context.Background(), "tradezero", "", "", "k", "s", "")

	want := Result{OK: true, Env: "live", AccountID: "12345", AccountType: "Live"}
	if !reflect.DeepEqual(res, want) {
		t.Fatalf("res = %+v, want %+v", res, want)
	}
	if len(res.Accounts) != 0 {
		t.Fatalf("res.Accounts = %v, want empty", res.Accounts)
	}
}

func TestTestConnection_TradeZero_MultipleAccounts(t *testing.T) {
	p := &Prober{
		clk:          fakeClock(),
		alpacaVerify: failAlpaca(t),
		tzFetchAccounts: func(context.Context, creds.Pair, clock.Clock) ([]tradezero.AccountInfo, error) {
			return []tradezero.AccountInfo{
				{AccountID: "A1", AccountType: "Live"},
				{AccountID: "A2", AccountType: "Paper"},
			}, nil
		},
	}

	res := p.TestConnection(context.Background(), "tradezero", "", "", "k", "s", "")

	if !res.OK {
		t.Fatalf("res.OK = false, want true: %+v", res)
	}
	if res.Env != "" || res.AccountID != "" || res.AccountType != "" {
		t.Fatalf("res = %+v, want top-level Env/AccountID/AccountType left empty", res)
	}
	wantAccounts := []Account{
		{AccountID: "A1", AccountType: "Live", Env: "live"},
		{AccountID: "A2", AccountType: "Paper", Env: "paper"},
	}
	if len(res.Accounts) != len(wantAccounts) {
		t.Fatalf("res.Accounts = %+v, want %+v", res.Accounts, wantAccounts)
	}
	for i, want := range wantAccounts {
		if res.Accounts[i] != want {
			t.Fatalf("res.Accounts[%d] = %+v, want %+v", i, res.Accounts[i], want)
		}
	}
}

func TestTestConnection_TradeZero_EmptyAccounts(t *testing.T) {
	p := &Prober{
		clk:          fakeClock(),
		alpacaVerify: failAlpaca(t),
		tzFetchAccounts: func(context.Context, creds.Pair, clock.Clock) ([]tradezero.AccountInfo, error) {
			return nil, nil
		},
	}

	res := p.TestConnection(context.Background(), "tradezero", "", "", "k", "s", "")

	want := Result{OK: false, Message: "no accounts (check credentials)"}
	if !reflect.DeepEqual(res, want) {
		t.Fatalf("res = %+v, want %+v", res, want)
	}
}

func TestTestConnection_TradeZero_FetchError(t *testing.T) {
	p := &Prober{
		clk:          fakeClock(),
		alpacaVerify: failAlpaca(t),
		tzFetchAccounts: func(context.Context, creds.Pair, clock.Clock) ([]tradezero.AccountInfo, error) {
			return nil, errors.New("network unreachable")
		},
	}

	res := p.TestConnection(context.Background(), "tradezero", "", "", "k", "s", "")

	want := Result{OK: false, Message: "network unreachable"}
	if !reflect.DeepEqual(res, want) {
		t.Fatalf("res = %+v, want %+v", res, want)
	}
}

// ---- moomoo ----

func TestTestConnection_Moomoo_Success(t *testing.T) {
	var gotAddr, gotEnv string
	var gotAccID uint64
	p := &Prober{
		clk:             fakeClock(),
		openDAddr:       "127.0.0.1:11111",
		alpacaVerify:    failAlpaca(t),
		tzFetchAccounts: failTZ(t),
		moomooVerify: func(_ context.Context, addr string, accID uint64, env string, _ clock.Clock) (*trdcommon.TrdAcc, error) {
			gotAddr, gotAccID, gotEnv = addr, accID, env
			return &trdcommon.TrdAcc{}, nil
		},
	}

	res := p.TestConnection(context.Background(), "moomoo", "paper", "", "", "", "123456")

	want := Result{OK: true, Env: "paper", AccountID: "123456"}
	if !reflect.DeepEqual(res, want) {
		t.Fatalf("res = %+v, want %+v", res, want)
	}
	if gotAddr != "127.0.0.1:11111" || gotAccID != 123456 || gotEnv != "paper" {
		t.Fatalf("moomooVerify received addr=%q accID=%d env=%q, want addr=127.0.0.1:11111 accID=123456 env=paper", gotAddr, gotAccID, gotEnv)
	}
}

func TestTestConnection_Moomoo_EmptyAccountID(t *testing.T) {
	p := &Prober{
		clk:             fakeClock(),
		alpacaVerify:    failAlpaca(t),
		tzFetchAccounts: failTZ(t),
		moomooVerify:    failMoomoo(t),
	}

	res := p.TestConnection(context.Background(), "moomoo", "paper", "", "", "", "")

	want := Result{OK: false, Message: "account_id is required for moomoo"}
	if !reflect.DeepEqual(res, want) {
		t.Fatalf("res = %+v, want %+v", res, want)
	}
}

func TestTestConnection_Moomoo_NonNumericAccountID(t *testing.T) {
	p := &Prober{
		clk:             fakeClock(),
		alpacaVerify:    failAlpaca(t),
		tzFetchAccounts: failTZ(t),
		moomooVerify:    failMoomoo(t),
	}

	res := p.TestConnection(context.Background(), "moomoo", "paper", "", "", "", "not-a-number")

	want := Result{OK: false, Message: "account_id must be numeric"}
	if !reflect.DeepEqual(res, want) {
		t.Fatalf("res = %+v, want %+v", res, want)
	}
}

func TestTestConnection_Moomoo_VerifyError(t *testing.T) {
	p := &Prober{
		clk:             fakeClock(),
		alpacaVerify:    failAlpaca(t),
		tzFetchAccounts: failTZ(t),
		moomooVerify: func(context.Context, string, uint64, string, clock.Clock) (*trdcommon.TrdAcc, error) {
			return nil, errors.New("moomoo: accID 123456 is disabled (accStatus=Disabled)")
		},
	}

	res := p.TestConnection(context.Background(), "moomoo", "paper", "", "", "", "123456")

	want := Result{OK: false, Message: "moomoo: accID 123456 is disabled (accStatus=Disabled)"}
	if !reflect.DeepEqual(res, want) {
		t.Fatalf("res = %+v, want %+v", res, want)
	}
}

// ---- unrecognized broker ----

func TestTestConnection_UnrecognizedBroker_NotSupported(t *testing.T) {
	p := &Prober{
		clk:             fakeClock(),
		alpacaVerify:    failAlpaca(t),
		tzFetchAccounts: failTZ(t),
	}

	res := p.TestConnection(context.Background(), "sim", "", "", "", "", "")

	want := Result{OK: false, Message: "connection testing is not supported for sim"}
	if !reflect.DeepEqual(res, want) {
		t.Fatalf("res = %+v, want %+v", res, want)
	}
}

// ---- New ----

func TestNew_WiresRealHelpers(t *testing.T) {
	p := New("/tmp/does-not-matter.json", "127.0.0.1:11111", clock.System{})
	if p.alpacaVerify == nil || p.tzFetchAccounts == nil || p.moomooVerify == nil {
		t.Fatal("New must wire all three broker helpers")
	}
	if p.credsPath != "/tmp/does-not-matter.json" {
		t.Fatalf("credsPath = %q, want /tmp/does-not-matter.json", p.credsPath)
	}
	if p.openDAddr != "127.0.0.1:11111" {
		t.Fatalf("openDAddr = %q, want 127.0.0.1:11111", p.openDAddr)
	}
}
