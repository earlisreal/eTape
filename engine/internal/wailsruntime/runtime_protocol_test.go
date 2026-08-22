//go:build wails

package wailsruntime

import "testing"

func TestStreamStopControlPreservesShutdownMeaning(t *testing.T) {
	tests := []struct {
		reason string
		want   StreamReply
	}{
		{reason: "engine stopped", want: StreamReply{Type: "stopping", Reason: "engine stopped"}},
		{reason: "restarting", want: StreamReply{Type: "restarting", Reason: "restarting"}},
		{reason: "unexpected", want: StreamReply{Type: "stopping", Reason: "engine stopped"}},
	}
	for _, tt := range tests {
		t.Run(tt.reason, func(t *testing.T) {
			if got := streamStopControl(tt.reason); got != tt.want {
				t.Fatalf("streamStopControl(%q) = %#v, want %#v", tt.reason, got, tt.want)
			}
		})
	}
}
