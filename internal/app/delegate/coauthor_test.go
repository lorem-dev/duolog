package delegate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/run"
)

func TestWorkerCoauthors(t *testing.T) {
	for _, tc := range []struct {
		name                                string
		reported, changed, disabled, failed bool
	}{
		{name: "reported", reported: true},
		{name: "snapshot", changed: true},
		{name: "failed", changed: true, failed: true},
		{name: "unchanged"},
		{name: "disabled", reported: true, disabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.gitRepo()
			h.cfg.Coauthors.Enabled = !tc.disabled
			h.results = []run.Result{{Done: !tc.failed}}
			if tc.reported {
				h.results[0].Files = []string{"a.go"}
			}
			if tc.changed {
				h.change = "a.go"
			}
			if tc.failed {
				h.errs = []error{errors.New("worker failed")}
			}
			_, _ = h.run("implement")
			data, err := os.ReadFile(filepath.Join(h.s.Dir, "coauthors.ndjson"))
			if (tc.reported || tc.changed) && !tc.disabled {
				if err != nil || !strings.Contains(string(data), `"model":"gpt-6.1-sol"`) || !strings.Contains(string(data), `"lead":"lead-123456"`) {
					t.Fatalf("%s %v", data, err)
				}
			} else if !os.IsNotExist(err) {
				t.Fatalf("unexpected record: %s %v", data, err)
			}
		})
	}
}
