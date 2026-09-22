//go:build windows

package openbrowser

import (
	"slices"
	"testing"

	"golang.org/x/sys/windows"
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

func TestSetRestoredWindowBoundsPreservesNegativeMonitorPosition(t *testing.T) {
	original := setWindowPosCall
	defer func() { setWindowPosCall = original }()
	var got []uintptr
	setWindowPosCall = func(args ...uintptr) (uintptr, uintptr, error) {
		got = slices.Clone(args)
		return 1, 0, nil
	}
	spec := WindowSpec{X: -1920, Y: 0, Width: 1920, Height: 1032}

	if err := setRestoredWindowBounds(windows.HWND(42), spec); err != nil {
		t.Fatal(err)
	}
	if len(got) != 7 || int(got[2]) != spec.X || int(got[3]) != spec.Y ||
		int(got[4]) != spec.Width || int(got[5]) != spec.Height {
		t.Fatalf("SetWindowPos args = %v, want saved bounds %+v", got, spec)
	}
}
