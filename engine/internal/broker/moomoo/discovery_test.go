package moomoo

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/trdcommon"
)

// acc builds a TrdAcc with every field EligibleLiveUS/getAccList inspect,
// starting from validTrdAcc's baseline (Real env, US-authorized, Active,
// Normal role) and applying opts -- lets each table case above spell out only
// what it deviates from "eligible".
func acc(accID uint64, opts ...func(*trdcommon.TrdAcc)) *trdcommon.TrdAcc {
	a := validTrdAcc(accID, trdcommon.TrdEnv_TrdEnv_Real)
	for _, opt := range opts {
		opt(a)
	}
	return a
}

func withRole(role trdcommon.TrdAccRole) func(*trdcommon.TrdAcc) {
	return func(a *trdcommon.TrdAcc) { a.AccRole = proto.Int32(int32(role)) }
}

func withStatus(status trdcommon.TrdAccStatus) func(*trdcommon.TrdAcc) {
	return func(a *trdcommon.TrdAcc) { a.AccStatus = proto.Int32(int32(status)) }
}

func withEnv(env trdcommon.TrdEnv) func(*trdcommon.TrdAcc) {
	return func(a *trdcommon.TrdAcc) { a.TrdEnv = proto.Int32(int32(env)) }
}

func withMarkets(markets ...trdcommon.TrdMarket) func(*trdcommon.TrdAcc) {
	return func(a *trdcommon.TrdAcc) {
		ms := make([]int32, len(markets))
		for i, m := range markets {
			ms[i] = int32(m)
		}
		a.TrdMarketAuthList = ms
	}
}

func TestEligibleLiveUS(t *testing.T) {
	cases := []struct {
		name string
		acc  *trdcommon.TrdAcc
		want bool
	}{
		{"nil account", nil, false},
		{"eligible real US account", acc(1), true},
		{"master account excluded", acc(1, withRole(trdcommon.TrdAccRole_TrdAccRole_Master)), false},
		{"disabled account excluded", acc(1, withStatus(trdcommon.TrdAccStatus_TrdAccStatus_Disabled)), false},
		{"non-US market excluded", acc(1, withMarkets(trdcommon.TrdMarket_TrdMarket_HK)), false},
		{"no authorized markets excluded", acc(1, withMarkets()), false},
		{"simulate env excluded", acc(1, withEnv(trdcommon.TrdEnv_TrdEnv_Simulate)), false},
		{"real env with US among several markets", acc(1, withMarkets(trdcommon.TrdMarket_TrdMarket_HK, trdcommon.TrdMarket_TrdMarket_US)), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EligibleLiveUS(tc.acc); got != tc.want {
				t.Errorf("EligibleLiveUS(%+v) = %v, want %v", tc.acc, got, tc.want)
			}
		})
	}
}

// TestListAccounts_Success drives ListAccounts' real dial -> wait-for-ConnUp
// -> Trd_GetAccList -> teardown path against the shared mockTrdOpenD fixture,
// asserting it returns the FULL raw list (unlike VerifyAccount/getAccList,
// which filter down to one accID) -- accListResp is already variadic, so the
// existing fixture infra expresses a multi-account response with no changes.
func TestListAccounts_Success(t *testing.T) {
	m := newMockTrdOpenD(t)
	eligible := acc(testAccID)
	master := acc(testAccID+1, withRole(trdcommon.TrdAccRole_TrdAccRole_Master))
	m.setRespond(opend.ProtoTrdGetAccList, func(opend.Frame) proto.Message { return accListResp(eligible, master) })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	got, err := ListAccounts(ctx, m.addr(), "etape-seed", clock.System{})
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(accounts) = %d, want 2", len(got))
	}
	ids := map[uint64]bool{got[0].GetAccID(): true, got[1].GetAccID(): true}
	if !ids[testAccID] || !ids[testAccID+1] {
		t.Fatalf("accounts = %v, want ids %d and %d present", ids, testAccID, testAccID+1)
	}
}

// TestListAccounts_ConnectionFailure covers ListAccounts' own dial-failure
// path: a ctx that is already past its deadline by the time ListAccounts
// waits on client.State() must surface as an error rather than hang.
func TestListAccounts_ConnectionFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()
	<-ctx.Done() // guarantee the deadline has already passed

	if _, err := ListAccounts(ctx, "127.0.0.1:1", "etape-seed", clock.System{}); err == nil {
		t.Fatal("expected an error for a connection that can never come up")
	}
}

// TestListAccounts_NoConnectionLeak mirrors TestVerifyAccount_NoConnectionLeak:
// N back-to-back calls against the same mock server (no intervening
// closeConns()) must grow connCount by exactly N -- proof that ListAccounts
// never leaves a lingering goroutine or open TCP connection behind.
func TestListAccounts_NoConnectionLeak(t *testing.T) {
	m := newMockTrdOpenD(t)
	one := acc(testAccID)
	m.setRespond(opend.ProtoTrdGetAccList, func(opend.Frame) proto.Message { return accListResp(one) })

	const n = 5
	for i := 0; i < n; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		got, err := ListAccounts(ctx, m.addr(), "etape-seed", clock.System{})
		cancel()
		if err != nil {
			t.Fatalf("call %d: ListAccounts: %v", i, err)
		}
		if len(got) != 1 || got[0].GetAccID() != testAccID {
			t.Fatalf("call %d: accounts = %v", i, got)
		}
	}

	if got := m.connCount(); got != n {
		t.Fatalf("connCount = %d, want exactly %d (one dial per call, no leaked reconnects)", got, n)
	}
}
