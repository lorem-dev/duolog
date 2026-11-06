package hook

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/golden")

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

func TestRenderGolden(t *testing.T) {
	events := []string{"SessionStart", "UserPromptSubmit", "PostToolUse"}
	cases := map[string][]byte{
		"context": []byte("duolog: 1 message\n\"quoted\" <tag> & more\n"),
		"empty":   nil,
	}
	for _, agent := range []string{"claude", "codex"} {
		for _, event := range events {
			for name, data := range cases {
				t.Run(agent+"-"+event+"-"+name, func(t *testing.T) {
					out := render(agent, event, data)
					if len(out.Stderr) != 0 || out.Code != 0 {
						t.Errorf("stderr %q, code %d; want neither", out.Stderr, out.Code)
					}
					checkGolden(t, agent+"-"+event+"-"+name, out.Stdout)
				})
			}
		}
	}
}
