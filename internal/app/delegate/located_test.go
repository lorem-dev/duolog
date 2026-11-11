package delegate

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/testutil/submodule"
)

// lockFolder makes dir unreadable until the test ends. It skips where a mode
// does not stop a read: on Windows, and for root.
func lockFolder(t *testing.T, dir string) {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("folder modes do not stop a read here")
	}
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
}

// A delegations folder of another work project that cannot be read is
// skipped and named; the own project's folder is still an error.
func TestLeadRecordsReadableSkipsAnotherProjectsUnreadableFolder(t *testing.T) {
	f := submodule.New(t)
	put(t, filepath.Join(f.Superproject, ".duolog", "config.yaml"), "learning:\n  enabled: false\n")
	put(t, filepath.Join(f.Submodule, ".duolog", "config.yaml"), "learning:\n  enabled: false\n")
	reg, err := session.Open(project.LeadHome(f.Superproject))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for root, id := range map[string]string{f.Superproject: "d-super1", f.Submodule: "d-sub0001"} {
		r := Record{ID: id, Lead: "lead-a", State: "done", Started: now, Ended: now, WorkRoot: root}
		if err := WriteRecord(filepath.Join(ProjectRecordDir(root), id+".json"), r); err != nil {
			t.Fatal(err)
		}
	}
	if err := reg.RecordDelegation(session.DelegationLocation{ID: "d-sub0001", Lead: "lead-a", WorkRoot: f.Submodule}); err != nil {
		t.Fatal(err)
	}
	lockFolder(t, ProjectRecordDir(f.Submodule))
	records, unread, err := LeadRecordsReadable(reg, f.Superproject, "")
	if err != nil || len(records) != 1 || records[0].ID != "d-super1" {
		t.Fatalf("records %+v, %v", records, err)
	}
	if len(unread) != 1 || unread[0].WorkRoot != f.Submodule || unread[0].Err == nil {
		t.Fatalf("unread = %+v", unread)
	}
	if _, _, err := LeadRecordsReadable(reg, f.Submodule, ""); err == nil {
		t.Fatal("the own project's unreadable folder read as no error")
	}
	if _, err := LeadRecords(reg, f.Superproject, ""); err == nil {
		t.Fatal("LeadRecords skipped an unreadable folder")
	}
}

func TestLeadRecordsReadsSubmoduleAndSuperproject(t *testing.T) {
	f := submodule.New(t)
	put(t, filepath.Join(f.Superproject, ".duolog", "config.yaml"), "learning:\n  enabled: false\n")
	put(t, filepath.Join(f.Submodule, ".duolog", "config.yaml"), "learning:\n  enabled: false\n")
	reg, err := session.Open(project.LeadHome(f.Superproject))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	write := func(root, id string, started time.Time) {
		t.Helper()
		r := Record{ID: id, Lead: "lead-a", State: "done", Started: started, Ended: started.Add(time.Minute), WorkRoot: root}
		if err := WriteRecord(filepath.Join(ProjectRecordDir(root), id+".json"), r); err != nil {
			t.Fatal(err)
		}
	}
	write(f.Superproject, "d-super1", now.Add(-time.Hour))
	write(f.Submodule, "d-sub0001", now)
	if err := reg.RecordDelegation(session.DelegationLocation{ID: "d-sub0001", Lead: "lead-a", WorkRoot: f.Submodule}); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{f.Superproject, f.Submodule} {
		records, err := LeadRecords(reg, root, "")
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, r := range records {
			ids = append(ids, r.ID)
		}
		if !slices.Equal(ids, []string{"d-sub0001", "d-super1"}) {
			t.Fatalf("ids from %s: %v", root, ids)
		}
		want := filepath.Join(f.Submodule, ".duolog", "delegations")
		if got := filepath.Dir(records[0].Path); got != want {
			t.Fatalf("path dir %s, want %s", got, want)
		}
	}
}
