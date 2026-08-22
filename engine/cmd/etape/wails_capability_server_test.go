//go:build wails && server

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/earlisreal/eTape/engine/internal/wailsruntime"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestWailsServerBindingAndStreamCapabilities(t *testing.T) {
	port := freePort(t)
	frontend := httptest.NewServer(application.AlphaAssets.Handler)
	defer frontend.Close()
	t.Setenv("FRONTEND_DEVSERVER_URL", frontend.URL)
	t.Setenv("WAILS_SERVER_HOST", "127.0.0.1")
	t.Setenv("WAILS_SERVER_PORT", strconv.Itoa(port))

	app, err := newWailsApp()
	if err != nil {
		t.Fatalf("new Wails app: %v", err)
	}
	services := app.Config().Services
	var service *RuntimeService
	for _, candidate := range services {
		if runtimeService, ok := candidate.Instance().(*RuntimeService); ok {
			service = runtimeService
			break
		}
	}
	if service == nil {
		t.Fatal("RuntimeService was not registered")
	}

	runDone := make(chan error, 1)
	go func() { runDone <- app.Run() }()
	defer func() {
		app.Quit()
		select {
		case err := <-runDone:
			if err != nil {
				t.Errorf("Wails server: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Wails server did not stop")
		}
	}()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitForHealth(t, baseURL)

	capabilitiesBody := callBinding(t, baseURL, "Capabilities", "capabilities")
	var capabilities RuntimeCapabilities
	if err := json.Unmarshal(capabilitiesBody, &capabilities); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	if capabilities.ServerMode != wailsruntime.ServerMode || !capabilities.ServerMode {
		t.Fatalf("server mode = %v, want true", capabilities.ServerMode)
	}
	if capabilities.BindingCaller != "application.WindowKey" || capabilities.StreamCaller != "StreamConn.Window" {
		t.Fatalf("caller capabilities = %#v", capabilities)
	}

	token := string(callBinding(t, baseURL, "OpenStreamSession", "session", "alpha"))
	if token == "" {
		t.Fatal("OpenStreamSession returned an empty capability")
	}

	testRuntimeStream(t, baseURL, token)
	waitForGateIdle(t, service.runtime.Gate())
	if err := service.runtime.ValidateSession(wailsruntime.StreamHello{
		Protocol:    wailsruntime.StreamProtocol,
		WorkspaceID: "alpha",
		Session:     token,
	}, 0); err == nil {
		t.Fatal("closed stream capability remained valid")
	}
	mismatchToken := string(callBinding(t, baseURL, "OpenStreamSession", "mismatch-session", "alpha"))
	testRejectedRuntimeStream(t, baseURL, mismatchToken, "beta")
	waitForGateIdle(t, service.runtime.Gate())
	testApplicationEventBroadcast(t, app, baseURL)
}

func testRuntimeStream(t *testing.T, baseURL, token string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn := dialRuntimeStream(t, ctx, baseURL)
	defer conn.CloseNow()

	writeJSON(t, ctx, conn, wailsruntime.StreamHello{
		Protocol:    wailsruntime.StreamProtocol,
		WorkspaceID: "alpha",
		Session:     token,
	})
	var accepted wailsruntime.StreamReply
	readJSON(t, ctx, conn, &accepted)
	if accepted.Type != "accepted" {
		t.Fatalf("stream handshake = %#v, want accepted", accepted)
	}

	for _, want := range []string{"one", "two", "three"} {
		if err := conn.Write(ctx, websocket.MessageBinary, []byte(want)); err != nil {
			t.Fatalf("write %q: %v", want, err)
		}
		_, got, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read %q: %v", want, err)
		}
		if string(got) != want {
			t.Fatalf("echo = %q, want %q", got, want)
		}
	}
}

func testRejectedRuntimeStream(t *testing.T, baseURL, token, workspaceID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn := dialRuntimeStream(t, ctx, baseURL)
	defer conn.CloseNow()

	writeJSON(t, ctx, conn, wailsruntime.StreamHello{
		Protocol:    wailsruntime.StreamProtocol,
		WorkspaceID: workspaceID,
		Session:     token,
	})
	var rejected wailsruntime.StreamReply
	readJSON(t, ctx, conn, &rejected)
	if rejected.Type != "rejected" {
		t.Fatalf("mismatched workspace handshake = %#v, want rejected", rejected)
	}
}

func testApplicationEventBroadcast(t *testing.T, app *application.App, baseURL string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	first := dialWebSocket(t, ctx, baseURL+"/wails/events?clientId=first")
	defer first.CloseNow()
	second := dialWebSocket(t, ctx, baseURL+"/wails/events?clientId=second")
	defer second.CloseNow()

	if app.Event.Emit(runtimeHintEvent, RuntimeEvent{WorkspaceID: "alpha", Revision: 7, Kind: "invalidate"}) {
		t.Fatal("application hint event was canceled")
	}

	for name, conn := range map[string]*websocket.Conn{"first": first, "second": second} {
		var event application.CustomEvent
		if err := wsjson.Read(ctx, conn, &event); err != nil {
			t.Fatalf("read %s event: %v", name, err)
		}
		if event.Name != runtimeHintEvent {
			t.Fatalf("%s event name = %q, want %q", name, event.Name, runtimeHintEvent)
		}
	}
}

func callBinding(t *testing.T, baseURL, method, callID string, args ...any) []byte {
	t.Helper()
	payload := map[string]any{
		"object": 0,
		"method": 0,
		"args": map[string]any{
			"call-id":    callID,
			"methodName": reflect.TypeOf(RuntimeService{}).PkgPath() + ".RuntimeService." + method,
			"args":       args,
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal binding: %v", err)
	}
	request, err := http.NewRequest(http.MethodPost, baseURL+"/wails/runtime", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("binding request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("binding %s: %v", method, err)
	}
	defer response.Body.Close()
	result, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read binding %s: %v", method, err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("binding %s status = %d: %s", method, response.StatusCode, result)
	}
	return result
}

func dialRuntimeStream(t *testing.T, ctx context.Context, baseURL string) *websocket.Conn {
	t.Helper()
	endpoint := "ws://" + baseURL[len("http://"):] + "/wails/stream/ws?name=" + url.QueryEscape(runtimeStreamName)
	return dialWebSocket(t, ctx, endpoint)
}

func dialWebSocket(t *testing.T, ctx context.Context, endpoint string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.Dial(ctx, endpoint, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", endpoint, err)
	}
	return conn
}

func writeJSON(t *testing.T, ctx context.Context, conn *websocket.Conn, value any) {
	t.Helper()
	if err := wsjson.Write(ctx, conn, value); err != nil {
		t.Fatalf("write JSON: %v", err)
	}
}

func readJSON(t *testing.T, ctx context.Context, conn *websocket.Conn, value any) {
	t.Helper()
	if _, data, err := conn.Read(ctx); err != nil {
		t.Fatalf("read JSON: %v", err)
	} else if err := json.Unmarshal(data, value); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
}

func waitForHealth(t *testing.T, baseURL string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/health", nil)
		if err != nil {
			t.Fatalf("health request: %v", err)
		}
		response, err := http.DefaultClient.Do(request)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("Wails server did not become healthy: %v", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func waitForGateIdle(t *testing.T, gate *wailsruntime.Gate) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if gate.InFlight() == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("runtime gate still has %d in-flight handlers", gate.InFlight())
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate server port: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}
