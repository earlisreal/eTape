package opend

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/common"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/initconnect"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/keepalive"
)

// initConnect performs the 1001 handshake and records connID + keepalive interval.
func (c *Client) initConnect(ctx context.Context) error {
	req := &initconnect.Request{C2S: &initconnect.C2S{
		ClientVer:           proto.Int32(c.opt.ClientVer),
		ClientID:            proto.String(c.opt.ClientID),
		RecvNotify:          proto.Bool(true),
		ProgrammingLanguage: proto.String("Go"),
	}}
	f, err := c.Request(ctx, ProtoInitConnect, req)
	if err != nil {
		return fmt.Errorf("initconnect: %w", err)
	}
	var resp initconnect.Response
	if err := proto.Unmarshal(f.Body, &resp); err != nil {
		return fmt.Errorf("initconnect decode: %w", err)
	}
	if resp.GetRetType() != int32(common.RetType_RetType_Succeed) {
		return fmt.Errorf("initconnect failed: retType=%d msg=%q", resp.GetRetType(), resp.GetRetMsg())
	}
	s2c := resp.GetS2C()
	c.setConnInfo(s2c.GetConnID(), time.Duration(s2c.GetKeepAliveInterval())*time.Second)
	return nil
}

// keepAliveLoop sends a KeepAlive every interval; any failure ends the session.
func (c *Client) keepAliveLoop(ctx context.Context, errc chan<- error) {
	interval := c.keepAliveInterval()
	if interval <= 0 {
		interval = 10 * time.Second
	}
	ticker := c.clk.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
			req := &keepalive.Request{C2S: &keepalive.C2S{Time: proto.Int64(c.clk.Now().Unix())}}
			rctx, cancel := context.WithTimeout(ctx, c.opt.RequestTimeout)
			_, err := c.Request(rctx, ProtoKeepAlive, req)
			cancel()
			if err != nil {
				select {
				case errc <- fmt.Errorf("keepalive: %w", err):
				default:
				}
				return
			}
		}
	}
}
