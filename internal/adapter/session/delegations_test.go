package session

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/core/config"
)

// A delegation's location is registry metadata that outlives the lead's
// session: a fresh run never makes a resumable entry, and its result must
// still be found after SessionEnd.
func TestDelegationLocationsRetainCompletedRuns(t *testing.T) {
	reg, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterLead(Lead{ID: "lead-a", Target: config.Target{Agent: "claude"}}); err != nil {
		t.Fatal(err)
	}
	locations := []DelegationLocation{
		{ID: "d-bbbbbb", Lead: "lead-a", WorkRoot: "/work/sub"},
		{ID: "d-aaaaaa", Lead: "lead-a", WorkRoot: "/work/super"},
		{ID: "d-cccccc", Lead: "lead-b", WorkRoot: "/work/super"},
	}
	for i := range locations {
		locations[i].WorkRoot = filepath.Join(t.TempDir(), "project")
		writeLocationRecord(t, locations[i])
	}
	for _, loc := range locations {
		if err := reg.RecordDelegation(loc); err != nil {
			t.Fatal(err)
		}
	}
	// Recording the same location again is idempotent.
	if err := reg.RecordDelegation(locations[0]); err != nil {
		t.Fatal(err)
	}
	for _, clash := range []DelegationLocation{
		{ID: "d-bbbbbb", Lead: "lead-b", WorkRoot: "/work/sub"},
		{ID: "d-bbbbbb", Lead: "lead-a", WorkRoot: "/work/super"},
	} {
		if err := reg.RecordDelegation(clash); err == nil {
			t.Fatalf("collision %+v accepted", clash)
		}
	}
	for _, bad := range []DelegationLocation{{Lead: "lead-a", WorkRoot: "/w"}, {ID: "d-x", WorkRoot: "/w"}, {ID: "d-x", Lead: "lead-a"}} {
		if err := reg.RecordDelegation(bad); err == nil {
			t.Fatalf("incomplete %+v accepted", bad)
		}
	}
	if _, err := reg.ObserveLead(LeadObservation{ID: "lead-a", Agent: "claude", Ended: true}, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err := reg.Delegations("lead-a")
	if err != nil {
		t.Fatal(err)
	}
	if want := []DelegationLocation{locations[1], locations[0]}; !slices.Equal(got, want) {
		t.Fatalf("lead-a locations = %+v, want %+v", got, want)
	}
	all, err := reg.Delegations("")
	if err != nil || len(all) != 3 {
		t.Fatalf("all locations = %+v (%v)", all, err)
	}
}

func writeLocationRecord(t *testing.T, loc DelegationLocation) string {
	t.Helper()
	dir := filepath.Join(loc.WorkRoot, ".duolog", "delegations")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, loc.ID+".json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDelegationLocationsPruneMissingRecords(t *testing.T) {
	reg, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	loc := DelegationLocation{ID: "d-expired", Lead: "lead", WorkRoot: t.TempDir()}
	path := writeLocationRecord(t, loc)
	if err := reg.RecordDelegation(loc); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	got, err := reg.Delegations("")
	if err != nil || len(got) != 0 {
		t.Fatalf("missing record retained: %+v (%v)", got, err)
	}
	if err := reg.access(false, func(s *state) error {
		if len(s.Delegations) != 0 {
			t.Fatal("pruning was not persisted")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
