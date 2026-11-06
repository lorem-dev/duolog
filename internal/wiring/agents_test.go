package wiring

import (
	"reflect"
	"testing"
)

func TestRegistry(t *testing.T) {
	names := Names()
	if want := []string{"claude", "codex"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("Names() = %v, want %v", names, want)
	}
	agents := Agents()
	if len(agents) != len(names) {
		t.Fatalf("Agents() has %d entries, Names() has %d", len(agents), len(names))
	}
	for i, a := range agents {
		if a.Facts.Name != names[i] {
			t.Errorf("Agents()[%d].Facts.Name = %q, want %q", i, a.Facts.Name, names[i])
		}
	}
}
