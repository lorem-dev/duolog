package delegate

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/testutil/submodule"
)

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
