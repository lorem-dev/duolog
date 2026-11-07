package peer

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

var runIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// A peer's pane gets a duolog run id in DUOLOG_RUN, the entry keeps it, and
// the peer's open and close operations carry it, so its gate events and its
// peer events share one id.
func TestPeerLaunchGetsDuologRunID(t *testing.T) {
	r := newRig(t, baseScript())
	var ops []Operation
	opts := Options{Deps: r.deps, Root: r.root, Config: r.cfg, Lead: "lead-1", Observe: func(o Operation) { ops = append(ops, o) }}
	r.ctx = WithOptions(context.Background(), opts)
	opened := r.ready()

	var runs []string
	for _, pair := range r.expandLaunch(r.spawns()[0]) {
		if value, ok := strings.CutPrefix(pair, "DUOLOG_RUN="); ok {
			runs = append(runs, value)
		}
	}
	if len(runs) != 1 || !runIDPattern.MatchString(runs[0]) {
		t.Fatalf("DUOLOG_RUN in the pane: %v", runs)
	}
	if got := r.entry(opened.ID).Run; got != runs[0] {
		t.Fatalf("entry run %q, pane run %q", got, runs[0])
	}
	if err := Close(r.ctx, opened.ID); err != nil {
		t.Fatal(err)
	}
	if len(ops) != 2 || ops[0].Op != "open" || ops[1].Op != "close" {
		t.Fatalf("operations: %+v", ops)
	}
	for _, o := range ops {
		if o.Run != runs[0] {
			t.Fatalf("%s carries run %q, want %q", o.Op, o.Run, runs[0])
		}
	}
}

// Each launch draws its own id, and it is never the peer's id or a vendor's.
func TestPeerRunIDsDifferPerLaunch(t *testing.T) {
	r := newRig(t, baseScript())
	first := r.ready()
	a := r.entry(first.ID).Run
	if err := Close(r.ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	r.panes(baseScript())
	second := r.ready()
	b := r.entry(second.ID).Run
	if !runIDPattern.MatchString(a) || !runIDPattern.MatchString(b) || a == b || strings.Contains(b, strings.TrimPrefix(second.ID, "peer-")) {
		t.Fatalf("runs %q and %q", a, b)
	}
}
