package delegate

import (
	"reflect"
	"testing"

	journal "github.com/lorem-dev/duolog/internal/core/record"
	"github.com/lorem-dev/duolog/internal/core/run"
)

// A worker that finishes and only hands on messages from other sessions has
// finished: the messages stay in the answer and change no judgement.
func TestMessageNeedsDoNotChangeTheOutcome(t *testing.T) {
	msg := messageNeedPrefix + "please look at x"
	h := newHarness(t)
	h.learning(true)
	h.results = []run.Result{{Done: true, Summary: "Done.", Files: []string{}, Needs: []string{msg}}}
	out, err := h.run("implement")
	if err != nil || len(out.Needs) != 0 {
		t.Fatal(out, err)
	}
	if !reflect.DeepEqual(out.Result.Needs, []string{msg}) {
		t.Fatalf("the answer lost the message: %+v", out.Result.Needs)
	}
	rec := h.record()
	if !rec.Done || len(rec.Needs) != 0 || rec.Outcome == journal.OutcomeLead {
		t.Fatalf("%+v", rec)
	}
	if got := RecordFor(Record{}, out, nil, rec.Time); got.State != "done" || len(got.Needs) != 0 {
		t.Fatalf("%+v", got)
	}
	r := run.Result{Done: true, Needs: []string{msg}}
	if o, _ := workerOutcome(r, nil); o != "done" {
		t.Fatal(o)
	}
	r.Needs = append(r.Needs, "figma")
	if o, _ := workerOutcome(r, nil); o != "needs" {
		t.Fatal(o)
	}
}
