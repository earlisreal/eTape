//go:build wails

package main

import (
	"context"

	"github.com/earlisreal/eTape/engine/internal/wailsruntime"
	"github.com/wailsapp/wails/v3/pkg/application"
)

const (
	runtimeStreamName = "etape.runtime"
	runtimeHintEvent  = "etape:runtime-hint"
)

type RuntimeCapabilities struct {
	BindingCaller    string `json:"bindingCaller"`
	BindingHasWindow bool   `json:"bindingHasWindow"`
	StreamCaller     string `json:"streamCaller"`
	ServerMode       bool   `json:"serverMode"`
	EventsScope      string `json:"eventsScope"`
	EnginePhase      string `json:"enginePhase"`
	EngineError      string `json:"engineError,omitempty"`
}

type RuntimeEvent struct {
	WorkspaceID string `json:"workspaceId"`
	Revision    uint64 `json:"revision"`
	Kind        string `json:"kind"`
	Phase       string `json:"phase,omitempty"`
	Error       string `json:"error,omitempty"`
}

type RuntimeService struct {
	runtime   *wailsruntime.Runtime
	lifecycle *engineRuntime
}

func init() {
	application.RegisterEvent[RuntimeEvent](runtimeHintEvent)
}

func (s *RuntimeService) ServiceName() string { return "RuntimeService" }

func (s *RuntimeService) ServiceStartup(context.Context, application.ServiceOptions) error {
	if s.lifecycle == nil {
		return nil
	}
	return s.lifecycle.Start()
}

func (s *RuntimeService) Capabilities(ctx context.Context) (RuntimeCapabilities, error) {
	_, release, err := s.runtime.EnterContext(ctx)
	if err != nil {
		return RuntimeCapabilities{}, err
	}
	defer release()
	state := engineBootState{}
	if s.lifecycle != nil {
		state = s.lifecycle.State()
	}

	return RuntimeCapabilities{
		BindingCaller:    "application.WindowKey",
		BindingHasWindow: s.runtime.CallerWindowID(ctx) != 0,
		StreamCaller:     "StreamConn.Window",
		ServerMode:       wailsruntime.ServerMode,
		EventsScope:      "application-wide hint only",
		EnginePhase:      state.Phase,
		EngineError:      state.Error,
	}, nil
}

func (s *RuntimeService) emitHint(event RuntimeEvent) bool {
	return s.runtime.EnqueueHint(wailsruntime.Hint{
		Class:    wailsruntime.EventApplicationHint,
		Key:      event.WorkspaceID + "\x00" + event.Kind,
		Revision: event.Revision,
		Data:     event,
	})
}

func (s *RuntimeService) OpenStreamSession(ctx context.Context, workspaceID string) (string, error) {
	return s.runtime.OpenSession(ctx, workspaceID)
}

func (s *RuntimeService) RestartApplication(ctx context.Context) (string, error) {
	if s.lifecycle == nil {
		return "", errEngineNotStart
	}
	_, release, err := s.runtime.EnterContext(ctx)
	if err != nil {
		return "", err
	}
	if err := s.lifecycle.RequestRestart(); err != nil {
		release()
		return "", err
	}
	release()
	return "accepted", nil
}

func (s *RuntimeService) ServiceShutdown() error {
	if s.lifecycle != nil {
		return s.lifecycle.Stop(context.Background())
	}
	return s.runtime.Stop(context.Background())
}
