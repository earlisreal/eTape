package main

import (
	"reflect"
	"testing"
)

func TestNewsSymbolPlan_SeparatesPriorityGroups(t *testing.T) {
	got := newsSymbolPlan(
		[]string{"US.AAPL", "US.MSFT"},     // pool members
		[]string{"US.MSFT", "US.NVDA", ""}, // live UI demands (+ empty)
	)
	if !reflect.DeepEqual(got.Active, []string{"US.MSFT", "US.NVDA"}) || !reflect.DeepEqual(got.Scanner, []string{"US.AAPL"}) {
		t.Fatalf("got %+v", got)
	}
}

func TestNewsSymbolPlan_Empty(t *testing.T) {
	if got := newsSymbolPlan(nil, nil); len(got.All()) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}
