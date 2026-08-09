package uihub

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/locates"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

type locateProviderSpy struct {
	eligibility locates.Eligibility
	found       bool
	quotes      locates.QuoteResult
	page        locates.Page
	record      locates.Record
	err         error
	request     locates.Request
	filter      locates.ListFilter
	locateID    string
}

func (s *locateProviderSpy) LocateEligibility(string) (locates.Eligibility, bool) {
	return s.eligibility, s.found
}
func (s *locateProviderSpy) QuoteLocates(context.Context, []string) (locates.QuoteResult, error) {
	return s.quotes, s.err
}
func (s *locateProviderSpy) CreateLocate(_ context.Context, request locates.Request) (locates.Record, error) {
	s.request = request
	return s.record, s.err
}
func (s *locateProviderSpy) ListLocates(_ context.Context, filter locates.ListFilter) (locates.Page, error) {
	s.filter = filter
	return s.page, s.err
}
func (s *locateProviderSpy) GetLocate(_ context.Context, id string) (locates.Record, error) {
	s.locateID = id
	return s.record, s.err
}

func locateTestRegistry(provider locates.Provider) *locates.Registry {
	r := locates.NewRegistry()
	r.Register(exec.VenueID("alpaca-paper"), provider)
	return r
}

func TestLocateQueriesMapEligibilityQuotesAndErrors(t *testing.T) {
	borrow := "hard_to_borrow"
	shortable := true
	provider := &locateProviderSpy{
		eligibility: locates.Eligibility{BorrowStatus: &borrow, Shortable: &shortable}, found: true,
		quotes: locates.QuoteResult{
			Quotes: []locates.Quote{{Symbol: "US.AAPL", AvailableQty: 1200, Price: "0.012300", QuotedAt: time.Date(2026, 7, 6, 13, 30, 0, 123456000, time.UTC)}},
			Errors: []locates.QuoteError{{Symbol: "US.TSLA", Code: "not_quotable", Message: "no locate"}},
		},
	}
	q := newQueries(&spyFills{}, clock.NewFake(time.UnixMilli(0)))
	q.locates = locateTestRegistry(provider)

	eligibility, ok := q.handle("QueryLocateEligibility", json.RawMessage(`{"venue":"alpaca-paper","symbol":"US.AAPL"}`)).(wsmsg.LocateEligibility)
	if !ok || !eligibility.Supported || !eligibility.Found || eligibility.BorrowStatus == nil || *eligibility.BorrowStatus != borrow {
		t.Fatalf("eligibility = %#v", eligibility)
	}
	quotes, ok := q.handle("QueryLocateQuotes", json.RawMessage(`{"venue":"alpaca-paper","symbols":["US.AAPL","US.TSLA"]}`)).(wsmsg.LocateQuoteResult)
	if !ok || len(quotes.Quotes) != 1 || quotes.Quotes[0].Price != "0.012300" || len(quotes.Errors) != 1 || quotes.Errors[0].Code != "not_quotable" {
		t.Fatalf("quote result = %#v", quotes)
	}
}

func TestLocateQueriesRouteExactVenueAndPagination(t *testing.T) {
	created := time.Date(2026, 7, 6, 13, 30, 0, 0, time.UTC)
	provider := &locateProviderSpy{page: locates.Page{
		Locates:       []locates.Record{{ID: "loc-1", Symbol: "US.AAPL", RequestedQty: 500, LimitPrice: "0.0123", CreatedAt: created}},
		NextPageToken: "next",
	}, record: locates.Record{ID: "loc-1", Symbol: "US.AAPL"}}
	q := newQueries(&spyFills{}, clock.NewFake(time.UnixMilli(0)))
	q.locates = locateTestRegistry(provider)

	page, ok := q.handle("QueryLocates", json.RawMessage(`{"venue":"alpaca-paper","status":"active","symbol":"US.AAPL","start":"2026-07-01","end":"2026-07-06","limit":25,"pageToken":"page-1"}`)).(wsmsg.LocateListResult)
	if !ok || len(page.Locates) != 1 || page.NextPageToken != "next" || page.Locates[0].CreatedAt != "2026-07-06T13:30:00Z" {
		t.Fatalf("locate page = %#v", page)
	}
	if provider.filter.Status != "active" || provider.filter.Symbol != "US.AAPL" || provider.filter.PageToken != "page-1" {
		t.Fatalf("provider filter = %+v", provider.filter)
	}
	got, ok := q.handle("QueryLocate", json.RawMessage(`{"venue":"alpaca-paper","locateId":"loc-1"}`)).(wsmsg.LocateRecord)
	if !ok || got.ID != "loc-1" || provider.locateID != "loc-1" {
		t.Fatalf("locate = %#v id=%q", got, provider.locateID)
	}
	unsupported, ok := q.handle("QueryLocateQuotes", json.RawMessage(`{"venue":"sim","symbols":["US.AAPL"]}`)).(wsmsg.LocateQuoteResult)
	if !ok || unsupported.Error == "" {
		t.Fatalf("unsupported venue result = %#v", unsupported)
	}
}

func TestRequestLocateCommandUsesSelectedProviderAndReturnsRecord(t *testing.T) {
	provider := &locateProviderSpy{record: locates.Record{
		ID: "loc-42", Symbol: "US.AAPL", RequestedQty: 500, LimitPrice: "0.012300", AllOrNone: true, Status: locates.StatusActive,
	}}
	cd := newCommands(&spyExec{}, &spyCfg{}, &spyInd{}, &spyDemandCtl{}, &spyVenueAdmin{}, func() Feed { return nil }, &spyVenueTester{}, locateTestRegistry(provider))
	replies := make(chan wsmsg.AckMsg, 1)
	ack, deferred := cd.handle(context.Background(), "RequestLocate", mustJSON(t, wsmsg.RequestLocateArgs{
		Venue: "alpaca-paper", Symbol: "US.AAPL", Qty: 500, LimitPrice: "0.012300", AllOrNone: true, IdempotencyKey: "retry-1",
	}), 0, func(ack wsmsg.AckMsg) { replies <- ack })
	if !deferred || ack.Status != "" {
		t.Fatalf("ack = %+v deferred=%v", ack, deferred)
	}
	var resolved wsmsg.AckMsg
	select {
	case resolved = <-replies:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for deferred locate ack")
	}
	value, ok := resolved.Value.(wsmsg.LocateRecord)
	if !ok || value.ID != "loc-42" || provider.request.Qty != 500 || provider.request.LimitPrice != "0.012300" || provider.request.IdempotencyKey != "retry-1" {
		t.Fatalf("ack value=%#v request=%+v", resolved.Value, provider.request)
	}
	blockedAck, _ := cd.handle(context.Background(), "RequestLocate", json.RawMessage(`{"venue":"sim","symbol":"US.AAPL","qty":500,"limitPrice":"0.0123","allOrNone":true,"idempotencyKey":"retry-1"}`), 0, func(wsmsg.AckMsg) {})
	if blockedAck.Status != wsmsg.AckBlocked || blockedAck.Reason != "locate unsupported for selected venue" {
		t.Fatalf("unsupported command ack = %+v", blockedAck)
	}
}

func TestRequestLocateCommandMarksAmbiguousProviderFailures(t *testing.T) {
	provider := &locateProviderSpy{err: locates.MarkAmbiguous(errors.New("timeout"))}
	cd := newCommands(&spyExec{}, &spyCfg{}, &spyInd{}, &spyDemandCtl{}, &spyVenueAdmin{}, func() Feed { return nil }, &spyVenueTester{}, locateTestRegistry(provider))
	replies := make(chan wsmsg.AckMsg, 1)
	_, deferred := cd.handle(context.Background(), "RequestLocate", mustJSON(t, wsmsg.RequestLocateArgs{
		Venue: "alpaca-paper", Symbol: "US.AAPL", Qty: 500, LimitPrice: "0.012300", AllOrNone: true, IdempotencyKey: "retry-1",
	}), 0, func(ack wsmsg.AckMsg) { replies <- ack })
	if !deferred {
		t.Fatal("RequestLocate must defer provider work")
	}
	select {
	case ack := <-replies:
		if ack.Status != wsmsg.AckBlocked || !ack.Ambiguous || ack.Reason != "timeout" {
			t.Fatalf("ambiguous ack = %+v", ack)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ambiguous locate ack")
	}
}
