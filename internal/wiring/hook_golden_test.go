package wiring

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/hook"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/render")

func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "render", name)
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

// TestHookRenderGolden renders each hook event through the Events of every
// registered agent. An agent that is not registered renders nothing.
func TestHookRenderGolden(t *testing.T) {
	events := []string{"SessionStart", "UserPromptSubmit", "PostToolUse"}
	cases := map[string][]byte{
		"context": []byte("duolog: 1 message\n\"quoted\" <tag> & more\n"),
		"empty":   nil,
	}
	for _, a := range Agents() {
		for _, event := range events {
			for name, data := range cases {
				t.Run(a.Facts.Name+"-"+event+"-"+name, func(t *testing.T) {
					checkGolden(t, a.Facts.Name+"-"+event+"-"+name, a.Events.Render(event, data))
				})
			}
		}
	}
	t.Run("other", func(t *testing.T) {
		env := HookEnv(hook.Env{Agent: "other", Root: t.TempDir(), Input: []byte(`{"session_id":"s-1"}`), Environ: []string{}})
		if env.Events != nil || env.Lead != nil {
			t.Fatalf("an unregistered agent got Events %v and Lead %v", env.Events, env.Lead)
		}
		for name, run := range map[string]func(context.Context, hook.Env) hook.Output{"SessionStart": hook.SessionStart, "UserPromptSubmit": hook.Prompt, "PostToolUse": hook.PostTool} {
			if out := run(context.Background(), env); len(out.Stdout) != 0 || len(out.Stderr) != 0 || out.Code != 0 {
				t.Errorf("%s of an unregistered agent = %+v; want nothing", name, out)
			}
		}
	})
}
