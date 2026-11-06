package peer

import (
	"encoding/json"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	coreroute "github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/workerenv"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// peerSandbox reads the sandbox table of a Claude peer's --settings.
func peerSandbox(t *testing.T, argv []string) map[string]any {
	t.Helper()
	i := slices.Index(argv, "--settings")
	if i < 0 || i+1 == len(argv) {
		t.Fatal(argv)
	}
	var settings map[string]any
	if err := json.Unmarshal([]byte(argv[i+1]), &settings); err != nil {
		t.Fatal(err)
	}
	if settings["preferredNotifChannel"] != "notifications_disabled" {
		t.Fatal(argv[i+1])
	}
	sandbox, _ := settings["sandbox"].(map[string]any)
	if _, on := sandbox["enabled"]; on {
		t.Fatal("duolog must never turn the sandbox on", argv[i+1])
	}
	return sandbox
}

func peerDenied(sandbox map[string]any) []any {
	fs, _ := sandbox["filesystem"].(map[string]any)
	denied, _ := fs["denyRead"].([]any)
	return denied
}

// TestProtectedRootsForwarded: the roots in peer.Options reach the Claude
// peer's launch, collection on or off, beside the .duolog denies.
func TestProtectedRootsForwarded(t *testing.T) {
	for _, collection := range []string{"", "off"} {
		r := newRig(t, withScreens(claudeIdle("")))
		t.Setenv("DUOLOG_TELEMETRY", collection)
		home := os.Getenv("HOME")
		roots := []string{filepath.Join(home, "state", "duolog", "telemetry"), filepath.Join(home, "state", "duolog", "balance")}
		r.ctx = WithOptions(r.ctx, Options{Deps: r.deps, Root: r.root, Config: r.cfg, Lead: "lead-1", ProtectedRoots: roots})
		r.open("plan")
		argv := r.started()
		denied := peerDenied(peerSandbox(t, argv))
		for _, root := range roots {
			if !slices.Contains(denied, any(filepath.ToSlash(root))) {
				t.Fatal(collection, denied)
			}
		}
		if !slices.ContainsFunc(denied, func(v any) bool { s, _ := v.(string); return strings.HasSuffix(s, "/.duolog/decisions.ndjson") }) {
			t.Fatal(denied)
		}
	}
}

// TestTelemetryDenyReadSettings covers the minimal and normal peer paths:
// both roots join the .duolog denies once each, and a Codex peer gets none.
func TestTelemetryDenyReadSettings(t *testing.T) {
	state := t.TempDir()
	roots := []string{"/t/telemetry", "/t/balance", "/t/telemetry"}
	for _, context := range []string{"minimal", "project"} {
		route := coreroute.Route{Target: config.Target{Agent: "claude", Model: "opus"}, Context: context}
		denied := peerDenied(peerSandbox(t, agentArgv(route, mode.Ask, peerProcess(t, &config.Config{}, route, workerenv.Free, state, roots...))))
		if len(denied) < 3 || denied[len(denied)-2] != "/t/telemetry" || denied[len(denied)-1] != "/t/balance" {
			t.Fatal(context, denied)
		}
		if !slices.Contains(denied, any(filepath.ToSlash(state)+"/.duolog/journal")) {
			t.Fatal(context, denied)
		}
	}
	codexRoute := coreroute.Route{Target: config.Target{Agent: "codex", Model: "sol"}}
	codex := agentArgv(codexRoute, mode.Ask, peerProcess(t, &config.Config{}, codexRoute, workerenv.Free, state, roots...))
	if strings.Contains(strings.Join(codex, " "), "/t/") {
		t.Fatal(codex)
	}
}

func TestAgentEnvPinsExecutableDirectory(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, parent := range []string{"", t.TempDir()} {
		t.Setenv("PATH", parent)
		for _, agent := range []string{"claude", "codex"} {
			route := coreroute.Route{Target: config.Target{Agent: agent, Model: "sol"}}
			p, err := agentEnv(peerInput(&config.Config{EnvPassthrough: []string{"PATH", "Path"}}, route, mode.Ask, workerenv.Free, "lead", "docs", "peer-test",
				"0123456789abcdef0123456789abcdef", "", nil, os.Environ()))
			if err != nil {
				t.Fatal(err)
			}
			pairs := p.Env
			if !slices.Contains(pairs, "DUOLOG_RUN=0123456789abcdef0123456789abcdef") {
				t.Fatal("the pane's run id is missing", pairs)
			}
			count := 0
			want := "PATH=" + filepath.Dir(exe)
			if parent != "" {
				want += string(os.PathListSeparator) + parent
			}
			for _, pair := range pairs {
				if strings.EqualFold(strings.SplitN(pair, "=", 2)[0], "PATH") {
					count++
					if pair != want {
						t.Fatal(pair, want)
					}
				}
			}
			if count != 1 {
				t.Fatal("expected one pinned PATH", pairs)
			}
		}
	}
}
