package quota

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	getsubinfo "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetsubinfo"
	histquota "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotrequesthistoryklquota"
)

// requester is the narrow OpenD request/response seam (satisfied by
// *opend.Client), mirroring stockinfo/news/scan.
type requester interface {
	Request(ctx context.Context, protoID uint32, req proto.Message) (opend.Frame, error)
}

// subInfo is the decoded, contention-relevant view of Qot_GetSubInfo.
type subInfo struct {
	totalUsed int // account-wide subscription slots used (server-authoritative)
	remain    int // account-wide subscription slots remaining
	own       int // slots used by this instance's OpenD connections
	foreign   int // slots used by other OpenD clients on the account
}

// readSubInfo issues Qot_GetSubInfo(all-conn) and computes own/foreign usage.
// Foreign is the MAX of two independent signals, so detection holds regardless
// of which one Task 1's empirical run proves authoritative:
//   - connection list: sum of UsedQuota over entries with IsOwnConnData=false —
//     catches identical-watchlist contention, where account-wide dedupe makes
//     the totals arithmetic read ~zero (total ≈ own).
//   - totals arithmetic: TotalUsedQuota − own — catches the case where a remote
//     OpenD's connections are invisible in the all-conn list but still count
//     against the server-side account totals.
func readSubInfo(ctx context.Context, r requester) (subInfo, error) {
	req := &getsubinfo.Request{C2S: &getsubinfo.C2S{IsReqAllConn: proto.Bool(true)}}
	f, err := r.Request(ctx, opend.ProtoQotGetSubInfo, req)
	if err != nil {
		return subInfo{}, err
	}
	var resp getsubinfo.Response
	if err := proto.Unmarshal(f.Body, &resp); err != nil {
		return subInfo{}, fmt.Errorf("get_sub_info decode: %w", err)
	}
	if resp.GetRetType() != 0 {
		return subInfo{}, fmt.Errorf("opend: proto %d: retType=%d msg=%q",
			opend.ProtoQotGetSubInfo, resp.GetRetType(), resp.GetRetMsg())
	}
	s2c := resp.GetS2C()
	var own, foreignList int
	for _, c := range s2c.GetConnSubInfoList() {
		if c.GetIsOwnConnData() {
			own += int(c.GetUsedQuota())
		} else {
			foreignList += int(c.GetUsedQuota())
		}
	}
	total := int(s2c.GetTotalUsedQuota())
	foreignTotals := total - own
	if foreignTotals < 0 {
		foreignTotals = 0
	}
	foreign := foreignList
	if foreignTotals > foreign {
		foreign = foreignTotals
	}
	return subInfo{totalUsed: total, remain: int(s2c.GetRemainQuota()), own: own, foreign: foreign}, nil
}

// readHistoryQuota issues Qot_RequestHistoryKLQuota (3104), mirroring
// opend backfill.historyQuota — re-issued here so the poller stays
// self-contained and unit-testable with a fake requester.
func readHistoryQuota(ctx context.Context, r requester) (used, remain int, err error) {
	req := &histquota.Request{C2S: &histquota.C2S{BGetDetail: proto.Bool(false)}}
	f, err := r.Request(ctx, opend.ProtoQotRequestHistoryKLQuota, req)
	if err != nil {
		return 0, 0, err
	}
	var resp histquota.Response
	if err := proto.Unmarshal(f.Body, &resp); err != nil {
		return 0, 0, fmt.Errorf("history_quota decode: %w", err)
	}
	if resp.GetRetType() != 0 {
		return 0, 0, fmt.Errorf("opend: proto %d: retType=%d msg=%q",
			opend.ProtoQotRequestHistoryKLQuota, resp.GetRetType(), resp.GetRetMsg())
	}
	return int(resp.GetS2C().GetUsedQuota()), int(resp.GetS2C().GetRemainQuota()), nil
}
