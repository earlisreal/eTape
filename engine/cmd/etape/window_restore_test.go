package main

import (
	"testing"

	"github.com/earlisreal/eTape/engine/internal/uihub"
)

type windowRestoreConfigSpy struct{ values map[string]string }

func (s *windowRestoreConfigSpy) GetConfig(key string) (string, bool, error) {
	v, ok := s.values[key]
	return v, ok, nil
}
func (s *windowRestoreConfigSpy) SetConfig(key, value string) { s.values[key] = value }

func TestRestoredWindowSpecsFiltersCatalogAndKeepsMainImplicit(t *testing.T) {
	state := `{"version":1,"entries":[` +
		`{"workspaceId":"main","x":0,"y":0,"width":1200,"height":900},` +
		`{"workspaceId":"monitoring","x":1920,"y":0,"width":1200,"height":900},` +
		`{"workspaceId":"custom","x":0,"y":0,"width":1200,"height":900}` +
		`]}`
	spy := &windowRestoreConfigSpy{values: map[string]string{
		"windows.v1":               `{"version":1,"entries":[{"id":"custom"}]}`,
		uihub.WindowStateConfigKey: state,
	}}
	specs := restoredWindowSpecs(spy, "127.0.0.1:8686", true)
	if len(specs) != 2 {
		t.Fatalf("restored specs = %+v, want monitoring and catalog-backed custom", specs)
	}
	if specs[0].URL != "http://127.0.0.1:8686?debug=1&workspace=custom" || specs[1].URL != "http://127.0.0.1:8686?debug=1&workspace=monitoring" {
		t.Fatalf("restored URLs = %q, %q", specs[0].URL, specs[1].URL)
	}
}

func TestRestoredWindowSpecsResetsMalformedState(t *testing.T) {
	spy := &windowRestoreConfigSpy{values: map[string]string{uihub.WindowStateConfigKey: "not-json"}}
	if got := restoredWindowSpecs(spy, "127.0.0.1:8686", false); got != nil {
		t.Fatalf("malformed state restored = %+v, want none", got)
	}
	if spy.values[uihub.WindowStateConfigKey] != `{"version":1,"entries":[]}` {
		t.Fatalf("reset state = %q", spy.values[uihub.WindowStateConfigKey])
	}
}
