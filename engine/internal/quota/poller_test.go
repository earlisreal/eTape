package quota

import (
	"context"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	histquota "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotrequesthistoryklquota"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

type capPub struct {
	mu  sync.Mutex
	evs []wsmsg.SysEvent
}

func (c *capPub) Publish(_ wsmsg.Topic, _ string, payload any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := payload.(wsmsg.SysEvent); ok {
		c.evs = append(c.evs, e)
	}
}

func histBody(t *testing.T, used, remain int32) []byte {
	t.Helper()
	b, _ := proto.Marshal(&histquota.Response{RetType: proto.Int32(0),
		S2C: &histquota.S2C{UsedQuota: proto.Int32(used), RemainQuota: proto.Int32(remain)}})
	return b
}

func TestPollerEmitsForeignAfterDebounceAndPublishesLatest(t *testing.T) {
	f := &fakeReq{bodies: map[uint32][]byte{
		opend.ProtoQotGetSubInfo:            subInfoBody(t, 62, 38, conn(true, 47), conn(false, 15)),
		opend.ProtoQotRequestHistoryKLQuota: histBody(t, 41, 59),
	}}
	pub := &capPub{}
	clk := clock.NewFake(time.Now())
	p := New(Config{SubWarnHeadroom: 12, HistWarnRemain: 10}, f, pub, clk) // poll() calls clk.Now() when it emits
	ctx := context.Background()

	p.poll(ctx)
	if _, ok := p.Latest(); !ok {
		t.Fatal("Latest must be set after a successful poll")
	}
	if len(pub.evs) != 0 {
		t.Fatalf("no FOREIGN on first poll (debounce): %+v", pub.evs)
	}
	p.poll(ctx)
	q, _ := p.Latest()
	if q.State != "foreign" || q.SubForeign != 15 || q.SubOwn != 47 || q.SubUsed != 62 {
		t.Fatalf("snapshot wrong: %+v", q)
	}
	if len(pub.evs) != 1 || pub.evs[0].Level != "info" || pub.evs[0].Kind != "quota" {
		t.Fatalf("one FOREIGN(info) event expected: %+v", pub.evs)
	}
}

func TestPollFailureHoldsStateAndSkips(t *testing.T) {
	pub := &capPub{}
	p := New(Config{SubWarnHeadroom: 12, HistWarnRemain: 10}, &fakeReq{err: opend.ErrNotConnected}, pub, clock.NewFake(time.Now()))
	p.poll(context.Background())
	if _, ok := p.Latest(); ok {
		t.Fatal("failed poll must not set Latest")
	}
	if len(pub.evs) != 0 {
		t.Fatalf("failed poll must emit nothing: %+v", pub.evs)
	}
}
