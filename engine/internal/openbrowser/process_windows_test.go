//go:build windows

package openbrowser

import (
	"slices"
	"testing"
)

func TestOwnedProcessCommandTargetsOnlyTheProcessTree(t *testing.T) {
	for _, test := range []struct {
		name  string
		force bool
		want  []string
	}{
		{name: "graceful", want: []string{"taskkill", "/T", "/PID", "1234"}},
		{name: "force", force: true, want: []string{"taskkill", "/T", "/F", "/PID", "1234"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cmd := ownedProcessCommand(1234, test.force)
			if !slices.Equal(cmd.Args, test.want) {
				t.Fatalf("ownedProcessCommand() = %q, want %q", cmd.Args, test.want)
			}
		})
	}
}
