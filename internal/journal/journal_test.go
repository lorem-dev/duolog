package journal

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func open(t *testing.T) *Journal {
	t.Helper()
	j, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func TestAppendAndReadAll(t *testing.T) {
	j := open(t)
	for _, id := range []string{"d1", "d2"} {
		if err := j.Append("delegations.ndjson", Delegation{ID: id, Role: "implement", Outcome: "infra"}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ReadAll[Delegation](j, "delegations.ndjson")
	if err != nil || len(got) != 2 || got[0].ID != "d1" || got[1].ID != "d2" || got[0].Outcome != "infra" {
		t.Fatal(got, err)
	}
	if none, err := ReadAll[Task](j, "tasks.ndjson"); err != nil || len(none) != 0 {
		t.Fatal(none, err)
	}
}

func TestReadAllSkipsTruncatedLastLine(t *testing.T) {
	j := open(t)
	os.WriteFile(filepath.Join(j.Dir, "delegations.ndjson"), []byte("{\"id\":\"a\"}\n{\"id\":"), 0600)
	got, err := ReadAll[Delegation](j, "delegations.ndjson")
	if err != nil || len(got) != 1 {
		t.Fatal(got, err)
	}
}

func TestReadAllNamesBadMiddleLine(t *testing.T) {
	j := open(t)
	os.WriteFile(filepath.Join(j.Dir, "delegations.ndjson"), []byte("{\"id\":\"a\"}\nnot json\n{\"id\":\"b\"}\n"), 0600)
	_, err := ReadAll[Delegation](j, "delegations.ndjson")
	if err == nil || !strings.Contains(err.Error(), "delegations.ndjson line 2") {
		t.Fatal(err)
	}
}

func TestStartTaskRecordsBaseAndLead(t *testing.T) {
	j := open(t)
	started, err := j.StartTask("lead-a", "fix login", "abc", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	cur, ok, err := j.CurrentTask("lead-a")
	if err != nil || !ok || cur.Base != "abc" || cur.Title != "fix login" || cur.ID != started.ID || cur.Lead != "lead-a" {
		t.Fatal(cur, ok, err)
	}
}

func TestSecondStartRefused(t *testing.T) {
	j := open(t)
	if _, err := j.StartTask("lead-a", "x", "abc", time.Now()); err != nil {
		t.Fatal(err)
	}
	_, err := j.StartTask("lead-a", "y", "abc", time.Now())
	if err == nil || !strings.Contains(err.Error(), "duolog task finish") {
		t.Fatal(err)
	}
}

func TestOpenTasksArePerLead(t *testing.T) {
	j := open(t)
	j.StartTask("lead-a", "x", "abc", time.Now())
	j.StartTask("lead-b", "y", "abc", time.Now())
	if _, err := j.CloseTask("lead-a"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := j.CurrentTask("lead-a"); ok {
		t.Fatal("lead-a still open")
	}
	if _, ok, _ := j.CurrentTask("lead-b"); !ok {
		t.Fatal("lead-b closed")
	}
}

func TestNewIDShape(t *testing.T) {
	if id := NewID("t", time.Now()); !regexp.MustCompile(`^t-\d{8}-\d{6}-[0-9a-f]{4}$`).MatchString(id) {
		t.Fatal(id)
	}
}
