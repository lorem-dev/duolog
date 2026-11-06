package session

import (
	"os"
	"testing"
)

// A peer is watched while a live process says it waits on it, and only that
// peer; a lead's own wait marker does not count for it.
func TestWatchedPeer(t *testing.T) {
	r, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SetWaiting("peer-abc123", "lead-token", os.Getpid()); err != nil {
		t.Fatal(err)
	}
	if watched, err := r.Watched("peer-abc123"); err != nil || watched {
		t.Fatalf("a wait marker under the peer's bare id counted: %v %v", watched, err)
	}
	release, err := r.Watch("peer-abc123")
	if err != nil {
		t.Fatal(err)
	}
	if watched, err := r.Watched("peer-abc123"); err != nil || !watched {
		t.Fatalf("watched %v %v during the wait", watched, err)
	}
	if watched, _ := r.Watched("peer-other1"); watched {
		t.Fatal("another peer counts as watched")
	}
	release()
	if watched, err := r.Watched("peer-abc123"); err != nil || watched {
		t.Fatalf("watched %v %v after the wait", watched, err)
	}
	var s state
	if err := r.access(false, func(st *state) error { s = *st; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Waiting[watchKey("peer-abc123")]; ok {
		t.Fatalf("an ended wait leaves its key: %v", s.Waiting)
	}
}

// The status rule: a peer's task is unfinished while the peer is open and the
// task is still recorded.
func TestEntryUnfinished(t *testing.T) {
	for _, tc := range []struct {
		e    Entry
		want bool
	}{
		{Entry{Task: "t.md"}, true},
		{Entry{}, false},
		{Entry{Task: "t.md", Closed: true}, false},
	} {
		if got := tc.e.Unfinished(); got != tc.want {
			t.Errorf("%+v: unfinished %v", tc.e, got)
		}
	}
}
