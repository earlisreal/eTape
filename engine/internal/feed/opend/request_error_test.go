package opend

import "testing"

func TestIsSymbolSpecificRequestError(t *testing.T) {
	for _, tc := range []struct {
		message string
		want    bool
	}{
		{"invalid security: AAPL", true},
		{"Get Stock's Sector interface does not support ETFs type.", true},
		{"GET STOCK'S SECTOR INTERFACE DOES NOT SUPPORT ETFS TYPE.", true},
		{"subscription quota exceeded", false},
		{"no permission to access market data", false},
		{"requests too frequent", false},
		{"interface does not support this request", false},
		{"unexpected server error", false},
	} {
		t.Run(tc.message, func(t *testing.T) {
			if got := IsSymbolSpecificRequestError(tc.message); got != tc.want {
				t.Fatalf("symbol-specific = %v, want %v", got, tc.want)
			}
		})
	}
}
