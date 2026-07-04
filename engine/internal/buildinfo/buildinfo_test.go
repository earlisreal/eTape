package buildinfo

import "testing"

func TestNameIsSet(t *testing.T) {
	if Name != "etape-engine" {
		t.Fatalf("Name = %q, want %q", Name, "etape-engine")
	}
}
