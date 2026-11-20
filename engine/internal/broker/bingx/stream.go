package bingx

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/earlisreal/eTape/engine/internal/broker/netx"
	"github.com/earlisreal/eTape/engine/internal/broker/perps"
	"github.com/earlisreal/eTape/engine/internal/exec"
)

const (
	listenKeyPath   = "/openApi/user/auth/userDataStream"
	authTimeout     = 10 * time.Second
	readTimeout     = 15 * time.Second
	listenKeyRenew  = 30 * time.Minute
	streamReadLimit = 2 << 20
)

var _ perps.OrderStream = (*apiClient)(nil)

func (a *apiClient) StreamOrders(ctx context.Context, connection func(bool), fill func(perps.FillHint)) {
	backoff := netx.Backoff{Min: time.Second, Max: 30 * time.Second}
	for ctx.Err() == nil {
		started := time.Now()
		a.streamSession(ctx, connection, fill)
		if time.Since(started) >= listenKeyRenew {
			backoff.Reset()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff.Next()):
		}
	}
}

func (a *apiClient) listenKey(ctx context.Context, method, key string) (string, error) {
	params := make(url.Values)
	if key != "" {
		params.Set("listenKey", key)
	}
	req, err := a.newRequest(ctx, method, listenKeyPath, params, true)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-SOURCE-KEY", "BX-AI-SKILL")
	res, err := a.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("%s listen-key request failed", a.env.label)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s listen-key HTTP %d", a.env.label, res.StatusCode)
	}
	var reply struct {
		Code      json.Number
		ListenKey string
		Data      struct{ ListenKey string }
	}
	err = json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&reply)
	if method != http.MethodPost && errors.Is(err, io.EOF) {
		return "", nil // BingX returns HTTP 200 with an empty body on delete.
	}
	if err != nil || reply.Code != "" && reply.Code != "0" {
		return "", fmt.Errorf("%s listen-key request rejected", a.env.label)
	}
	if method != http.MethodPost {
		return "", nil
	}
	if reply.ListenKey != "" {
		return reply.ListenKey, nil
	}
	if reply.Data.ListenKey != "" {
		return reply.Data.ListenKey, nil
	}
	return "", fmt.Errorf("%s listen key missing", a.env.label)
}

func (a *apiClient) streamSession(ctx context.Context, connection func(bool), fill func(perps.FillHint)) {
	key, err := a.listenKey(ctx, http.MethodPost, "")
	if err != nil {
		return
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_, _ = a.listenKey(cleanup, http.MethodDelete, key)
	}()
	dial, cancel := context.WithTimeout(ctx, authTimeout)
	ws, _, err := websocket.Dial(dial, a.env.stream+"?"+url.Values{"listenKey": {key}}.Encode(),
		&websocket.DialOptions{HTTPClient: a.http})
	cancel()
	if err != nil {
		return // Dial errors can contain the listen key.
	}
	defer func() { _ = ws.CloseNow() }()
	ws.SetReadLimit(streamReadLimit)
	ready := false
	defer func() {
		if ready {
			connection(false)
		}
	}()
	authenticateBy := time.Now().Add(authTimeout)
	renewAt := time.Now().Add(listenKeyRenew)
	for ctx.Err() == nil {
		deadline := time.Now().Add(readTimeout)
		if !ready && authenticateBy.Before(deadline) {
			deadline = authenticateBy
		}
		read, cancel := context.WithDeadline(ctx, deadline)
		_, frame, err := ws.Read(read)
		cancel()
		if err != nil {
			return
		}
		if time.Now().After(renewAt) {
			if _, err := a.listenKey(ctx, http.MethodPut, key); err != nil {
				return
			}
			renewAt = time.Now().Add(listenKeyRenew)
		}
		frame, err = decodeAccountFrame(frame)
		if err != nil {
			return
		}
		if bytes.Equal(frame, []byte("Ping")) {
			write, cancel := context.WithTimeout(ctx, time.Second)
			err := ws.Write(write, websocket.MessageText, []byte("Pong"))
			cancel()
			if err != nil {
				return
			}
			continue
		}
		event, hint := parseFillHint(frame)
		switch event {
		case "listenKeyExpired":
			return
		case "SNAPSHOT", "ACCOUNT_CONFIG_UPDATE", "ACCOUNT_UPDATE", "ORDER_TRADE_UPDATE", "TRADE_UPDATE":
			if !ready {
				ready = true
				connection(true)
			}
			if hint.ClientOrderID != "" {
				fill(hint)
			}
		}
	}
}

func decodeAccountFrame(frame []byte) ([]byte, error) {
	if len(frame) < 2 || frame[0] != 0x1f || frame[1] != 0x8b {
		return frame, nil
	}
	reader, err := gzip.NewReader(bytes.NewReader(frame))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	decoded, err := io.ReadAll(io.LimitReader(reader, streamReadLimit+1))
	if len(decoded) > streamReadLimit {
		return nil, errors.New("BingX account frame exceeds size limit")
	}
	return decoded, err
}

func parseFillHint(frame []byte) (string, perps.FillHint) {
	var event map[string]json.RawMessage
	if json.Unmarshal(frame, &event) != nil {
		return "", perps.FillHint{}
	}
	var name string
	_ = json.Unmarshal(event["e"], &name)
	if name != "ORDER_TRADE_UPDATE" && name != "TRADE_UPDATE" {
		return name, perps.FillHint{}
	}
	// The protocol uses case-sensitive keys: x/X and t/T have distinct meanings.
	var order map[string]json.RawMessage
	if json.Unmarshal(event["o"], &order) != nil {
		return name, perps.FillHint{}
	}
	stringField := func(k string) string {
		var s string
		_ = json.Unmarshal(order[k], &s)
		return s
	}
	numberField := func(k string) json.Number {
		var n json.Number
		_ = json.Unmarshal(order[k], &n)
		return n
	}
	var status exec.OrderStatus
	switch stringField("X") {
	case "FILLED":
		status = exec.StatusFilled
	case "PARTIALLY_FILLED", "PARTIALLYFILLED":
		status = exec.StatusAccepted
	default:
		return name, perps.FillHint{}
	}
	if stringField("x") != "TRADE" {
		return name, perps.FillHint{}
	}
	when, _ := numberField("T").Int64()
	filled := numberField("z").String()
	if strings.Contains(filled, ".") && !strings.ContainsAny(filled, "eE") {
		filled = strings.TrimRight(strings.TrimRight(filled, "0"), ".")
	}
	return name, perps.FillHint{
		ClientOrderID: strings.ToUpper(stringField("c")), BrokerOrderID: numberField("i").String(),
		Symbol: stringField("s"), Side: stringField("S"), Type: stringField("o"),
		Quantity: numberField("q"), Filled: json.Number(filled),
		TimeMs: when, Status: status,
	}
}
