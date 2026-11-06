package calibration

import (
	"reflect"
	"testing"
)

func TestRatesRoundTrip(t *testing.T) {
	dir := stateDir(t)
	rates, err := ReadRates(dir)
	if err != nil || len(rates) != 0 {
		t.Fatalf("ReadRates without a file = %v, %v; want empty", rates, err)
	}
	want := map[string]Rate{"claude": {WeeklyPerMTok: 0.5, WindowPerMTok: 3}}
	if err := WriteRates(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRates(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadRates = %v, want %v", got, want)
	}
}
