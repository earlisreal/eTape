package md

import "testing"

func TestBarPrependIsUpdate(t *testing.T) {
	var u Update = BarPrepend{Symbol: "US.AAPL", TF: "1m", Bars: []Bar{{BucketMs: 1}}}
	if _, ok := u.(BarPrepend); !ok {
		t.Fatalf("BarPrepend does not satisfy Update")
	}
}
