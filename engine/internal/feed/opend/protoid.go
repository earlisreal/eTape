package opend

// v1 protocol surface (protoIDs from the SDK's common/constant.py ProtoId class).
// Only InitConnect + KeepAlive are used in this plan; the rest are declared for
// the market-data plane (Plan 2) and are documented here as the single source of
// truth. The feed connection NEVER sends Trd_* protos (trade-incapability rule).
const (
	ProtoInitConnect    uint32 = 1001
	ProtoKeepAlive      uint32 = 1004
	ProtoGetGlobalState uint32 = 1002

	ProtoQotSub             uint32 = 3001
	ProtoQotRegQotPush      uint32 = 3002
	ProtoQotGetSubInfo      uint32 = 3003
	ProtoQotGetBasicQot     uint32 = 3004
	ProtoQotUpdateBasicQot  uint32 = 3005 // push
	ProtoQotGetKL           uint32 = 3006
	ProtoQotUpdateKL        uint32 = 3007 // push
	ProtoQotGetRT           uint32 = 3008
	ProtoQotUpdateRT        uint32 = 3009 // push
	ProtoQotGetTicker       uint32 = 3010
	ProtoQotUpdateTicker    uint32 = 3011 // push
	ProtoQotGetOrderBook    uint32 = 3012
	ProtoQotUpdateOrderBook uint32 = 3013 // push

	ProtoQotRequestHistoryKL      uint32 = 3103
	ProtoQotRequestHistoryKLQuota uint32 = 3104
	ProtoQotGetStaticInfo         uint32 = 3202
	ProtoQotGetSecuritySnapshot   uint32 = 3203
	ProtoQotGetOwnerPlate         uint32 = 3207
	ProtoQotStockFilter           uint32 = 3215
	ProtoQotGetSearchNews         uint32 = 3263
	ProtoQotGetUSPreMarketRank    uint32 = 3410
	ProtoQotGetUSAfterHoursRank   uint32 = 3411
	ProtoQotGetUSOvernightRank    uint32 = 3412
	ProtoQotGetTopMoversRank      uint32 = 3413

	// Trade push protocols (2xxx range). The feed connection never sends Trd_* protos,
	// but the shared pushProtoIDs map is consulted by any opend.Client reader loop
	// (including the future trade-only connection). These need to be registered so
	// the trade client's reader reliably routes them to Pushes() instead of the
	// resolve-miss fallback.
	ProtoTrdUpdateOrder     uint32 = 2208 // push
	ProtoTrdUpdateOrderFill uint32 = 2218 // push
)

// pushProtoIDs are server-initiated update protocols. A frame with one of
// these IDs is never a response, no matter what its serialNo says — real
// OpenD pushes carry an independent server-side serial that can collide with
// an in-flight request serial (observed live 2026-07-05).
var pushProtoIDs = map[uint32]struct{}{
	ProtoQotUpdateBasicQot:  {},
	ProtoQotUpdateKL:        {},
	ProtoQotUpdateRT:        {},
	ProtoQotUpdateTicker:    {},
	ProtoQotUpdateOrderBook: {},

	// Trade pushes (2xxx range): included here so the trade client's reader loop
	// (which uses the same opend.Client and pushProtoIDs map) reliably routes them.
	ProtoTrdUpdateOrder:     {},
	ProtoTrdUpdateOrderFill: {},
}

// IsPushProtoID reports whether protoID is a known push protocol.
func IsPushProtoID(id uint32) bool {
	_, ok := pushProtoIDs[id]
	return ok
}
