package openbrowser

import "testing"

// TestCommandPerOS verifies command dispatches to the right OS-specific
// launcher without ever starting a process (Open itself is not exercised
// here — that would actually pop open a browser window on the machine
// running the test).
func TestCommandPerOS(t *testing.T) {
	cases := []struct {
		goos string
		want string
	}{
		{"windows", "rundll32"},
		{"darwin", "open"},
		{"linux", "xdg-open"},
		{"freebsd", "xdg-open"}, // default fallback for anything unlisted
	}
	for _, c := range cases {
		cmd := command(c.goos, "http://127.0.0.1:8686")
		if got := cmd.Args[0]; got != c.want {
			t.Fatalf("goos=%s: command = %q, want %q", c.goos, got, c.want)
		}
		if got := cmd.Args[len(cmd.Args)-1]; got != "http://127.0.0.1:8686" {
			t.Fatalf("goos=%s: url arg = %q, want it passed through unchanged", c.goos, got)
		}
	}
}
