package opend

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetsecuritysnapshot"
)

// securityExists probes a single symbol via Qot_GetSecuritySnapshot (3203) —
// subscription-free and quota-free. It returns nil if the symbol exists,
// feed.ErrUnknownSymbol if OpenD reports it unknown, or feed.ErrFeedUnavailable
// (wrapping the cause) on any other failure. Single-symbol only: a batch
// containing one bad code fails the whole request (verified live 2026-07-08).
func (b *backfill) securityExists(ctx context.Context, symbol string) error {
	sec, err := parseSymbol(symbol)
	if err != nil {
		return feed.ErrUnknownSymbol
	}
	req := &qotgetsecuritysnapshot.Request{C2S: &qotgetsecuritysnapshot.C2S{
		SecurityList: []*qotcommon.Security{sec},
	}}
	f, err := b.rpc.Request(ctx, ProtoQotGetSecuritySnapshot, req)
	if err != nil {
		return fmt.Errorf("%w: snapshot rpc: %v", feed.ErrFeedUnavailable, err)
	}
	var resp qotgetsecuritysnapshot.Response
	if err := proto.Unmarshal(f.Body, &resp); err != nil {
		return fmt.Errorf("%w: snapshot decode: %v", feed.ErrFeedUnavailable, err)
	}
	if resp.GetRetType() != 0 {
		msg := resp.GetRetMsg()
		if strings.Contains(strings.ToLower(msg), "unknown stock") {
			return fmt.Errorf("%w: %s", feed.ErrUnknownSymbol, msg)
		}
		return fmt.Errorf("%w: %s", feed.ErrFeedUnavailable, msg)
	}
	if len(resp.GetS2C().GetSnapshotList()) == 0 {
		return feed.ErrUnknownSymbol
	}
	return nil
}
