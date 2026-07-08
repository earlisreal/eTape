package main

import (
	"reflect"
	"testing"
)

func TestNewsSymbols_UnionDedupSorted(t *testing.T) {
	got := newsSymbols(
		[]string{"US.AAPL", "US.MSFT"}, // config watchlist
		[]string{"US.MSFT", "US.NVDA"}, // --watch
		[]string{"US.F"},               // --focus
		func() []string { return []string{"US.NVDA", "US.TSLA", ""} }, // live demands (+ empty)
	)
	want := []string{"US.AAPL", "US.F", "US.MSFT", "US.NVDA", "US.TSLA"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestNewsSymbols_NilDemand(t *testing.T) {
	got := newsSymbols([]string{"US.AAPL"}, nil, nil, nil)
	if !reflect.DeepEqual(got, []string{"US.AAPL"}) {
		t.Fatalf("got %v", got)
	}
}
