package opend

import (
	"testing"
)

func TestIsPushProtoID(t *testing.T) {
	tests := []struct {
		name string
		id   uint32
		want bool
	}{
		// Market-data push protocols (should return true)
		{"QotUpdateBasicQot", ProtoQotUpdateBasicQot, true},
		{"QotUpdateKL", ProtoQotUpdateKL, true},
		{"QotUpdateRT", ProtoQotUpdateRT, true},
		{"QotUpdateTicker", ProtoQotUpdateTicker, true},
		{"QotUpdateOrderBook", ProtoQotUpdateOrderBook, true},

		// Trade push protocols (should return true)
		{"TrdUpdateOrder", ProtoTrdUpdateOrder, true},
		{"TrdUpdateOrderFill", ProtoTrdUpdateOrderFill, true},

		// Non-push protocols (should return false)
		{"InitConnect", ProtoInitConnect, false},
		{"KeepAlive", ProtoKeepAlive, false},
		{"QotSub", ProtoQotSub, false},
		{"QotGetBasicQot", ProtoQotGetBasicQot, false},
		{"QotGetKL", ProtoQotGetKL, false},
		{"QotGetTicker", ProtoQotGetTicker, false},
		{"QotGetOrderBook", ProtoQotGetOrderBook, false},

		// Arbitrary non-push ID
		{"UnknownID", 9999, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsPushProtoID(tt.id)
			if got != tt.want {
				t.Errorf("IsPushProtoID(%d) = %v, want %v", tt.id, got, tt.want)
			}
		})
	}
}
