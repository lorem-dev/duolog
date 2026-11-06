package peer

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/paidruns"
	"github.com/lorem-dev/duolog/internal/paidruns/paidrunstest"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/router"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// writeFile writes data at path, making its folder.
func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

// A pane is checked against the settings layers it will read: the user
// file in the pane's home, the project's files and managed policy of the
// platform. A paid route in any of them refuses the free pane, with the
// signal and its layer in the line, and no pane opens.
func TestPeerReadsPaneSettingsLayers(t *testing.T) {
	for _, c := range []struct {
		name, file, data, want string
	}{
		{"user base URL", "home", `{"env":{"ANTHROPIC_BASE_URL":"https://gateway.example"}}`, "env:ANTHROPIC_BASE_URL in user"},
		{"user API key", "home", `{"env":{"ANTHROPIC_API_KEY":"sk-in-settings"}}`, "env:ANTHROPIC_API_KEY in user"},
		{"project key helper", "project", `{"apiKeyHelper":"/bin/echo"}`, "apiKeyHelper in project"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t, withScreens(claudeIdle("")))
			r.gate.Layers = paidruns.ReadLayers
			path := filepath.Join(os.Getenv("HOME"), ".claude", "settings.json")
			if c.file == "project" {
				path = filepath.Join(r.root, ".claude", "settings.json")
			}
			writeFile(t, path, c.data)
			_, err := Open(r.ctx, "plan")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("a free pane over a paid settings layer: %v", err)
			}
			if len(r.spawns()) != 0 {
				t.Fatalf("a refused pane opened: %v", r.spawns())
			}
			if st := r.accounts(); len(st.Reservations) != 0 {
				t.Fatalf("a refused pane holds: %+v", st.Reservations)
			}
		})
	}
}

// The pane's own environment says where its user layer is: a config folder
// set in duolog's shell but not passed to the pane moves nothing, so the
// pane's home layer is read.
func TestPeerLayersFollowPaneEnvironment(t *testing.T) {
	r := newRig(t, withScreens(claudeIdle("")))
	r.gate.Layers = paidruns.ReadLayers
	elsewhere := t.TempDir()
	writeFile(t, filepath.Join(elsewhere, "settings.json"), `{}`)
	t.Setenv("CLAUDE_CONFIG_DIR", elsewhere)
	var names []string
	for _, n := range r.cfg.EnvPassthrough {
		if n != "CLAUDE_CONFIG_DIR" {
			names = append(names, n)
		}
	}
	r.cfg.EnvPassthrough = names
	writeFile(t, filepath.Join(os.Getenv("HOME"), ".claude", "settings.json"), `{"apiKeyHelper":"/bin/echo"}`)
	if _, err := Open(r.ctx, "plan"); err == nil || !strings.Contains(err.Error(), "apiKeyHelper in user") {
		t.Fatalf("the pane's own user layer was not read: %v", err)
	}
}

// A send is admitted on the route and billing class its pane was admitted
// with. A paid pane stays paid when a later read looks like a subscription,
// and a send whose rebuilt route differs from the pane's, such as from a
// shell without the pane's API key, is refused with the reopen command:
// the sender's environment never turns a paid pane into free accounting.
func TestPeerSendKeepsPaneRoute(t *testing.T) {
	for _, c := range []struct {
		name string
		// key puts an API key in the pane's environment, and the sender
		// lacks it.
		key, refuse bool
	}{{"same route, free-looking read", false, false}, {"sender without the key", true, true}} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t, baseScript())
			t.Setenv("OPENAI_API_KEY", "")
			if err := os.Unsetenv("OPENAI_API_KEY"); err != nil {
				t.Fatal(err)
			}
			if c.key {
				t.Setenv("OPENAI_API_KEY", "sk-pane-key")
			}
			r.cfg.PaidRuns.Codex.Allow, r.cfg.PaidRuns.Codex.DailyCapUSD = true, 100
			r.cfg.PaidRuns.Codex.Env = []string{"OPENAI_API_KEY"}
			paid := true
			r.gate.Cache.Read = func(ctx context.Context, p workerenv.Process) (quota.Snapshot, error) {
				s, err := paidrunstest.Subscription(ctx, p)
				if paid {
					s.Billing.RouteSignals = []workerenv.Signal{{Name: "auth:apiKey", Layer: "account/read"}}
				}
				return s, err
			}
			e := r.ready()
			entry := r.entry(e.ID)
			if open := r.accounts().Reservations[entry.AccountSessionID]; open.Variant != workerenv.Paid || !open.Paid {
				t.Fatalf("pane: %+v", open)
			}
			path := r.task()
			r.send(e.ID, path)
			r.finish(e.ID)

			// A new command reads afresh: nothing cached says paid.
			paid = false
			r.gate.Cache = &paidruns.Cache{Read: r.gate.Cache.Read}
			if c.key {
				if err := os.Unsetenv("OPENAI_API_KEY"); err != nil {
					t.Fatal(err)
				}
			}
			script := baseScript()
			script["capture"] = screens(sendOK(path)...)
			r.rex(script)
			err := Send(r.ctx, e.ID, path)
			if c.refuse {
				if err == nil || !strings.Contains(err.Error(), "duolog peer close "+e.ID) {
					t.Fatalf("a send on another route: %v", err)
				}
				if gen := r.entry(e.ID).TaskGeneration; gen != "" {
					t.Fatalf("a refused send holds %s", gen)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if task := r.accounts().Reservations[r.entry(e.ID).TaskGeneration]; !task.Paid || task.Estimate.USD <= 0 {
				t.Fatalf("a paid pane's send counted as free: %+v", task)
			}
		})
	}
}

// The layers are found where the pane looks: its home, the project root,
// this platform and the environment the pane gets.
func TestPeerInputLocations(t *testing.T) {
	r := newRig(t, baseScript())
	e, err := setup(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	route := router.Route{Target: config.Target{Agent: "claude", Model: "opus"}}
	in, err := e.paneInput(r.root, route, mode.Ask, workerenv.Free, "lead-1", "plan", "peer-x", "")
	if err != nil {
		t.Fatal(err)
	}
	loc := in.Locations
	if loc.Home != os.Getenv("HOME") || loc.Root != r.root || loc.GOOS != runtime.GOOS || len(loc.Environ) == 0 {
		t.Fatalf("pane locations: %+v", loc)
	}
}
