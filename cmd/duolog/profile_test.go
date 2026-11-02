package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/profile"
)

func TestProfileRejectsBadEntry(t *testing.T) {
	learningRepo(t, true)
	_, err := executeBusCommand(t, "profile", "d1", "--entry", "sol/high")
	if err == nil || !strings.Contains(err.Error(), "agent/model/effort") {
		t.Fatal(err)
	}
}

func TestProfileSummaryPrintsPatchesAndChecks(t *testing.T) {
	var out bytes.Buffer
	r := profile.Replay{Original: profile.Side{Entry: "codex/terra/medium", Summary: "original summary", USD: 0.2, Checks: []journal.Check{{Command: "no-such-program-x", Exit: -1}}}, Profile: profile.Side{Entry: "codex/sol/high", Summary: "profile summary", Patch: []byte("diff --git a/y.txt b/y.txt\nnew file mode 100644\n--- /dev/null\n+++ b/y.txt\n@@ -0,0 +1 @@\n+replayed\n"), Checks: []journal.Check{{Command: "git --version", Exit: 0}}}}
	if err := writeReplaySummary(&out, r); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Original: codex/terra/medium ($0.2000)", "original summary", "Profile: codex/sol/high", "profile summary", "y.txt", "1 file changed", "0 files changed", "check: no-such-program-x; exit=-1", "check: git --version; exit=0"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in %s", want, &out)
		}
	}
}

func TestProfileRefusesWorker(t *testing.T) {
	root := learningRepo(t, true)
	t.Setenv("DUOLOG_DEPTH", "1")
	_, err := executeBusCommand(t, "profile", "d1", "--entry", "codex/sol/high")
	if err == nil || !strings.Contains(err.Error(), "workers cannot") {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".duolog", "journal")); !os.IsNotExist(err) {
		t.Fatal("worker created journal", err)
	}
}
