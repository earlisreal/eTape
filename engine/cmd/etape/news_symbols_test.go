package main

import (
	"reflect"
	"testing"
)

func TestNewsSymbols_UnionDedupSorted(t *testing.T) {
	got := newsSymbols(
		[]string{"US.AAPL", "US.MSFT"},      // pool members
		[]string{"US.MSFT", "US.NVDA", ""},  // live UI demands (+ empty)
	)
	want := []string{"US.AAPL", "US.MSFT", "US.NVDA"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestNewsSymbols_Empty(t *testing.T) {
	if got := newsSymbols(nil, nil); len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}
