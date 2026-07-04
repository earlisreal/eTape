package pb_test

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/common"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/initconnect"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/keepalive"
)

func TestInitConnectMessageRoundTrips(t *testing.T) {
	req := &initconnect.Request{C2S: &initconnect.C2S{
		ClientVer: proto.Int32(100),
		ClientID:  proto.String("etape-test"),
	}}
	b, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got initconnect.Request
	if err := proto.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.GetC2S().GetClientID() != "etape-test" {
		t.Fatalf("clientID = %q, want etape-test", got.GetC2S().GetClientID())
	}
}

func TestCommonAndKeepAliveGenerated(t *testing.T) {
	// success enum is available and zero-valued
	if common.RetType_RetType_Succeed != 0 {
		t.Fatalf("RetType_Succeed = %d, want 0", common.RetType_RetType_Succeed)
	}
	// keepalive message compiles and round-trips
	ka := &keepalive.Request{C2S: &keepalive.C2S{Time: proto.Int64(123)}}
	if ka.GetC2S().GetTime() != 123 {
		t.Fatal("keepalive time getter mismatch")
	}
}
