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

// TestVerifyAccount_Success drives VerifyAccount's REAL dial -> wait-for-
// ConnUp -> getAccList -> teardown path against the mock OpenD trade server
// (mockTrdOpenD, shared with trd_test.go/moomoo_test.go), rather than the
// venueprobe package's injected fake (moomooVerify in venueprobe_test.go).
// That fake never actually calls VerifyAccount itself -- this is the direct
// coverage of the function's own dial/handshake/request/teardown logic.
func TestVerifyAccount_Success(t *testing.T) {
	m := newMockTrdOpenD(t)
	acc := validTrdAcc(testAccID, trdcommon.TrdEnv_TrdEnv_Simulate)
	m.setRespond(opend.ProtoTrdGetAccList, func(opend.Frame) proto.Message { return accListResp(acc) })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	got, err := VerifyAccount(ctx, m.addr(), testAccID, "paper", clock.System{})
	if err != nil {
		t.Fatalf("VerifyAccount: %v", err)
	}
	if got.GetAccID() != testAccID {
		t.Fatalf("accID = %d, want %d", got.GetAccID(), testAccID)
	}
}

// TestVerifyAccount_RejectsUnknownAccount exercises VerifyAccount's own
// error path (not just trdClient.getAccList's, which trd_test.go already
// covers in isolation) -- an account list that never contains accountID
// must surface as an error from VerifyAccount itself.
func TestVerifyAccount_RejectsUnknownAccount(t *testing.T) {
	m := newMockTrdOpenD(t)
	other := validTrdAcc(testAccID+1, trdcommon.TrdEnv_TrdEnv_Simulate)
	m.setRespond(opend.ProtoTrdGetAccList, func(opend.Frame) proto.Message { return accListResp(other) })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := VerifyAccount(ctx, m.addr(), testAccID, "paper", clock.System{}); err == nil {
		t.Fatal("expected error for an account id absent from the acc list")
	}
}

// TestVerifyAccount_NoConnectionLeak proves the "never leaving a lingering
// goroutine or open TCP connection after it returns" property VerifyAccount's
// own doc comment calls critical. There is no direct hook into the client's
// goroutine count, so this uses the mock server's connCount() (grows by
// exactly one per accepted TCP connection) as the most direct available
// proxy: N successful back-to-back VerifyAccount calls against the SAME
// mock server, with no intervening closeConns(), must grow connCount by
// EXACTLY N. A leak that kept the transport's Run() goroutine alive past
// VerifyAccount's return (so its backoff/redial loop kept dialing in the
// background) would inflate this count above N; this test would also have
// caught a leak that instead caused a later call to hang, since it runs the
// calls sequentially with a per-call timeout.
func TestVerifyAccount_NoConnectionLeak(t *testing.T) {
	m := newMockTrdOpenD(t)
	acc := validTrdAcc(testAccID, trdcommon.TrdEnv_TrdEnv_Simulate)
	m.setRespond(opend.ProtoTrdGetAccList, func(opend.Frame) proto.Message { return accListResp(acc) })

	const n = 5
	for i := 0; i < n; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		got, err := VerifyAccount(ctx, m.addr(), testAccID, "paper", clock.System{})
		cancel()
		if err != nil {
			t.Fatalf("call %d: VerifyAccount: %v", i, err)
		}
		if got.GetAccID() != testAccID {
			t.Fatalf("call %d: accID = %d, want %d", i, got.GetAccID(), testAccID)
		}
	}

	if got := m.connCount(); got != n {
		t.Fatalf("connCount = %d, want exactly %d (one dial per call, no leaked reconnects)", got, n)
	}
}
