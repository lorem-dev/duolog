package peer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/coauthor"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/delegate"
	"github.com/lorem-dev/duolog/internal/session"
)

func TestPeerCoauthorRecordedOnce(t *testing.T) {
	r := newRig(t, baseScript()) // All pane operations use the fake executable.
	r.git()
	r.lead("claude", mode.Edits)
	p := r.open("implement")
	base, err := delegate.Snapshot(r.root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.reg.Update(p.ID, func(x *session.Entry) { x.Task, x.Base = "task.md", base })
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.root, "changed.go"), []byte("package changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// Changing config after Open must not change the recorded version.
	role := r.cfg.Roles["implement"]
	role.Use.Model = "gpt-future"
	r.cfg.Roles["implement"] = role
	for range 2 {
		if _, err := Status(r.ctx, p.ID); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(r.reg.Dir, "coauthors.ndjson"))
	if err != nil || strings.Count(string(data), "\n") != 1 {
		t.Fatalf("%s %v", data, err)
	}
	var record coauthor.Record
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if record.Peer != p.ID || record.Model != "gpt-6.1-sol" || record.Lead != "lead-1" || record.Files != 1 || record.Time.IsZero() {
		t.Fatalf("%+v", record)
	}
}

func TestPeerCoauthorDisabledOrUnchanged(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "unchanged", true: "disabled"}[disabled], func(t *testing.T) {
			r := newRig(t, baseScript())
			r.git()
			r.lead("claude", mode.Edits)
			r.cfg.Coauthors.Enabled = !disabled
			p := r.open("implement")
			base, _ := delegate.Snapshot(r.root)
			if _, err := r.reg.Update(p.ID, func(x *session.Entry) { x.Task, x.Base = "task.md", base }); err != nil {
				t.Fatal(err)
			}
			if disabled {
				if err := os.WriteFile(filepath.Join(r.root, "changed.go"), []byte("package changed\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Status(r.ctx, p.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(r.reg.Dir, "coauthors.ndjson")); !os.IsNotExist(err) {
				t.Fatal("unexpected record", err)
			}
		})
	}
}
