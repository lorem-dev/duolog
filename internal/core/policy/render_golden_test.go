package policy

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/golden")

// checkGolden compares got with testdata/golden/<name>, or rewrites the file
// under -update. An empty golden means the renderer wrote nothing.
func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from its golden\n got: %q\nwant: %q", name, got, want)
	}
}

func goldenDecisions() []Decision {
	return []Decision{
		{Kind: Allow, Reason: "allowed by rule"},
		{Kind: Deny, Reason: `duolog: "rm -rf" is denied by <rule> & policy`},
		{Kind: Ask, Reason: "duolog: needs the person's answer"},
	}
}

func TestRenderGolden(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		for _, event := range []string{"PreToolUse", "PermissionRequest"} {
			for _, d := range goldenDecisions() {
				t.Run(agent+"-"+event+"-"+d.Kind, func(t *testing.T) {
					checkGolden(t, agent+"-"+event+"-"+d.Kind, Render(agent, event, d))
				})
			}
		}
	}
}

func TestRenderLeadGolden(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		for _, d := range goldenDecisions() {
			t.Run(agent+"-lead-"+d.Kind, func(t *testing.T) {
				checkGolden(t, agent+"-lead-"+d.Kind, RenderLead(agent, d))
			})
		}
	}
}
