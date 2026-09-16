package opend

import "testing"

func TestSymbolSpecificFailureRequiresAUsefulSymbolToken(t *testing.T) {
	if !SymbolSpecificFailure("US.BAD has no permission", []string{"US.A", "US.BAD"}) {
		t.Fatal("named symbol failure was not recognized")
	}
	if SymbolSpecificFailure("a connection is unavailable", []string{"US.A"}) {
		t.Fatal("ordinary prose was treated as a one-letter symbol failure")
	}
	if SymbolSpecificFailure("permission denied for the account", []string{"US.BAD"}) {
		t.Fatal("account-wide failure was treated as symbol-specific")
	}
	if SymbolSpecificFailure("BAD has no permission", []string{"US.BAD"}) {
		t.Fatal("a bare ticker in provider prose was treated as symbol-specific")
	}
}
