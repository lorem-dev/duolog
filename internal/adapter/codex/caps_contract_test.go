package codex_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/codex"
	"github.com/lorem-dev/duolog/internal/app/delegate/delegatetest"
	"github.com/lorem-dev/duolog/internal/core/caps"
)

func TestCapabilitiesContract(t *testing.T) {
	delegatetest.CapabilitiesContract(t, "codex", func(*testing.T) delegatetest.CapsPort { return codex.Caps{} })
	// The refusals only a Codex worker gives, word for word.
	t.Run("codex refusal reasons word for word", func(t *testing.T) {
		home, root := t.TempDir(), t.TempDir()
		worker := []string{"HOME=" + home, "USERPROFILE=" + home}
		if err := os.MkdirAll(filepath.Join(home, ".codex"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte("[mcp_servers.taken]\ncommand = \"old\"\nenabled = false\n"), 0600); err != nil {
			t.Fatal(err)
		}
		for want, s := range map[string]caps.Server{
			"its name has characters that a Codex -c key cannot hold":    {Name: "a.b", Agent: "claude", Transport: "stdio", Command: "x"},
			"the worker's Codex config has a server of that name":        {Name: "taken", Agent: "claude", Transport: "stdio", Command: "x"},
			"an env name is not a plain variable name":                   {Name: "svc", Agent: "claude", Transport: "stdio", Command: "x", Env: []caps.Pair{{Key: "A-B", Value: "c"}}},
			"its url reads a variable, which Codex would get in argv":    {Name: "svc", Agent: "claude", Transport: "http", URL: "https://x.example.com/${KEY}/mcp"},
			"its command or args read a variable, which Codex cannot do": {Name: "svc", Agent: "claude", Transport: "stdio", Command: "x", Args: []string{"${T}"}},
		} {
			if got := (codex.Caps{}).Refusal(root, s, worker); got != want {
				t.Errorf("Refusal(%s) = %q, want %q", s.Name, got, want)
			}
		}
		s := caps.Server{Name: "svc", Agent: "claude", Transport: "stdio", Command: "x"}
		if got, want := (codex.Caps{}).Refusal(root, s, []string{"PATH=" + t.TempDir()}), "the worker's Codex config cannot be read: "; len(got) <= len(want) || got[:len(want)] != want {
			t.Errorf("Refusal without the worker's home = %q, want %q...", got, want)
		}
	})
}
