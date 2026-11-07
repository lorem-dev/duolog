package delegate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/caps"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/testbin"
)

func scanPath(t *testing.T, bins ...string) {
	t.Helper()
	dirs := []string{t.TempDir()}
	for _, b := range bins {
		dirs = append(dirs, filepath.Dir(b))
	}
	t.Setenv("PATH", strings.Join(dirs, string(os.PathListSeparator)))
}

// The lead's scan and its cache list the servers, and marking them with the
// worker agent's real Refusal names why one cannot be bridged, with no value of
// their config in the inventory or in the cache.
func TestScanMarksBridgeableWithoutSecrets(t *testing.T) {
	const secret = "s3cret-alpha-value"
	d, home, root := bridgeDeps(t, nil)
	scanPath(t, testbin.Install(t, "claude", testbin.Scenario{}))
	put(t, filepath.Join(root, ".duolog", ".gitignore"), "*\n")
	bridgeClaudeState(t, home, root, `{"jira":{"type":"http","url":"https://j.example.com","headers":{"X-Token":"`+secret+`"}},"notion":{"type":"http","url":"https://n.example.com"}}`, "")
	inv, err := d.leadInventory(context.Background(), "claude", "codex", root)
	if err != nil || inv.Unknown {
		t.Fatal(inv, err)
	}
	var jira, notion caps.Item
	for _, it := range inv.Items {
		switch it.Name {
		case "jira":
			jira = it
		case "notion":
			notion = it
		}
	}
	// notion defines no credentials, so only Codex's own Refusal can say it
	// cannot be bridged.
	want := d.Agents["codex"].Caps.Refusal(root, d.servers("claude", root, os.Environ())["notion"], os.Environ())
	if want == "" || !jira.Bridgeable || notion.Bridgeable || notion.NoBridge != want {
		t.Fatalf("want notion marked %q: %+v", want, inv.Items)
	}
	cached := read(t, filepath.Join(root, ".duolog", "cache", "inventory-claude.json"))
	var cachedInv caps.Inventory
	if err := json.Unmarshal([]byte(cached), &cachedInv); err != nil || len(cachedInv.Items) != 2 {
		t.Fatalf("cache holds %+v; want both servers", cachedInv.Items)
	}
	for _, text := range []string{cached, notion.NoBridge, jira.NoBridge} {
		if strings.Contains(text, secret) || strings.Contains(text, "X-Token") {
			t.Fatal(text)
		}
	}
}

// A Codex worker's own scan lists the servers of a trusted project's
// .codex/config.toml, so a task that needs one runs on the worker.
func TestWorkerProjectServerIsPresent(t *testing.T) {
	h := newHarness(t)
	h.lead("claude", mode.Edits, "high")
	h.cfg.EnvPassthrough = []string{"HOME", "USERPROFILE"}
	scanPath(t, testbin.Install(t, "codex", testbin.Scenario{Responses: map[string]testbin.Scenario{"plugin list --json": {Stdout: `{"installed":[]}`}}}))
	put(t, filepath.Join(h.root, ".codex", "config.toml"), "[mcp_servers.docs]\ncommand = \"docs\"\n")
	// newHarness trusts the project in the worker's CODEX_HOME.
	o, e := h.runNeeds("implement", "mcp:docs")
	if e != nil || len(o.Needs) != 0 || strings.Contains(o.Line, "missing") {
		t.Fatal(o, e)
	}
	h.requireCalls(1)
	if h.calls[0].Route.Agent != "codex" {
		t.Fatal(h.calls[0].Route.Agent)
	}
}
