package paidruns

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/testbin"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

const (
	accountMarker  = "RAW-ACCOUNT-MARKER"
	orgMarker      = "RAW-ORG-MARKER"
	authFileMarker = "RAW-AUTH-FILE-MARKER"
)

// identityHomes points every home a CLI or os.UserConfigDir may use at one
// temporary folder, and plants auth files there that no read may open.
func identityHomes(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "APPDATA", "LOCALAPPDATA", "CODEX_HOME", "CLAUDE_CONFIG_DIR"} {
		t.Setenv(key, home)
	}
	for _, name := range []string{".credentials.json", "auth.json"} {
		path := filepath.Join(home, name)
		if err := os.WriteFile(path, []byte(`{"email":"`+authFileMarker+`"}`), 0600); err != nil {
			t.Fatal(err)
		}
		// Where the OS supports it, opening the file fails outright.
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

// installIdentityAgents installs fake claude and codex CLIs whose free
// replies carry account and org markers.
func installIdentityAgents(t *testing.T, account, org string) {
	t.Helper()
	free := "--settings " + workerenv.ClaudeSettings("", false, nil) + " "
	usage := `{"usage_report":{"rate_limits_available":true,"rate_limits":{"five_hour":{"utilization":10,"resets_at":"2099-01-01T00:00:00Z"},"seven_day":{"utilization":20,"resets_at":"2099-01-08T00:00:00Z"}}}}`
	testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{
		free + "-p": {Stdout: "{\"type\":\"system\"}\n" + usage + "\n"},
		free + "auth status --json": {Stdout: `{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty","email":"` +
			account + `","orgId":"` + org + `"}`},
	}})
	testbin.Install(t, "codex", testbin.Scenario{RPCResponses: map[string]string{
		"initialize": `{"id":1,"result":{}}`,
		"account/read": `{"id":3,"result":{"account":{"type":"chatgpt","planType":"plus","email":"` + account +
			`","workspaceName":"` + org + `"},"requiresOpenaiAuth":true}}`,
		"account/rateLimits/read": `{"id":2,"result":{"rateLimits":{"primary":{"usedPercent":13,"windowDurationMins":300,"resetsAt":4070908800},` +
			`"secondary":{"usedPercent":20,"windowDurationMins":10080,"resetsAt":4071513600},"ordinaryUsageAllowed":true}}}`,
		"config/read":   `{"id":4,"result":{"config":{"model_provider":null,"model_providers":{},"profile":null}}}`,
		"getAuthStatus": `{"id":5,"result":{"authMethod":"chatgpt","authToken":null,"requiresOpenaiAuth":true}}`,
	}})
}

// freeWorker builds the free worker process of agent from duolog's own
// environment, as the compatibility quota read does.
func freeWorker(agent string) func() (workerenv.Process, error) {
	return func() (workerenv.Process, error) {
		cfg := &config.Config{EnvPassthrough: config.DefaultEnvPassthrough(runtime.GOOS)}
		return workerenv.Build(workerenv.ProcessInput{Agent: agent, Shape: workerenv.Worker, Variant: workerenv.Free,
			Config: cfg, Environ: os.Environ()})
	}
}

type sinkCall struct{ agent, kind, raw string }

// recordingSink hands each identity to st, as the launch paths do, and
// keeps what it saw.
func recordingSink(st *Store, calls *[]sinkCall, mu *sync.Mutex) func(agent, kind string, raw []byte) error {
	return func(agent, kind string, raw []byte) error {
		mu.Lock()
		*calls = append(*calls, sinkCall{agent, kind, string(raw)})
		mu.Unlock()
		_, err := st.Identity(agent, kind, raw)
		return err
	}
}

// noRawMarker fails when any text holds a raw identity marker.
func noRawMarker(t *testing.T, what string, texts ...string) {
	t.Helper()
	for _, text := range texts {
		if strings.Contains(text, "RAW-") {
			t.Fatalf("%s holds a raw marker: %s", what, text)
		}
	}
}

// stateFiles returns every file of the account state folder.
func stateFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		files[filepath.Base(path)] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func readState(t *testing.T, st *Store) State {
	t.Helper()
	var got State
	if err := st.Update(context.Background(), func(s *State) error { got = *s; return nil }); err != nil {
		t.Fatal(err)
	}
	return got
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestIdentitySinkSaltedAndTransient(t *testing.T) {
	identityHomes(t)
	installIdentityAgents(t, accountMarker, orgMarker)
	ctx := context.Background()
	st, err := OpenUserState(filepath.Join(t.TempDir(), "accounts"))
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var calls []sinkCall
	reader := quota.Reader{Version: "test", Timeout: 5 * time.Second, IdentitySink: recordingSink(st, &calls, &mu)}

	// The markers go straight from the reply decoders to the sink; the
	// snapshot, the assessment, the cache and the state never hold them.
	cache := &Cache{Read: reader.ReadProcess}
	for _, agent := range []string{"claude", "codex"} {
		_, s, a, err := cache.Get(ctx, freeWorker(agent), &config.Config{}, quota.RunEvidence{}, true)
		if err != nil {
			t.Fatal(agent, err)
		}
		snapshot, _ := json.Marshal(s)
		assessment, _ := json.Marshal(a)
		noRawMarker(t, agent+" read", string(snapshot), string(assessment))
	}
	want := []sinkCall{
		{"claude", quota.IdentityAccount, accountMarker}, {"claude", quota.IdentityOrg, orgMarker},
		{"codex", quota.IdentityAccount, accountMarker}, {"codex", quota.IdentityOrg, orgMarker},
	}
	if !slices.Equal(calls, want) {
		t.Fatalf("sink calls %+v, want %+v", calls, want)
	}
	cached, err := json.Marshal(cache)
	if err != nil {
		t.Fatal(err)
	}
	noRawMarker(t, "cache", string(cached))
	for name, data := range stateFiles(t, st.Dir) {
		noRawMarker(t, name, data)
		if strings.Contains(data, authFileMarker) {
			t.Fatalf("%s holds an auth file's content", name)
		}
	}

	// Same salt and input give the same digest, from any client of the
	// folder; another installation, agent or kind gives another.
	key := func(st *Store, agent, kind, raw string) string {
		t.Helper()
		var data []byte
		if raw != "" {
			data = []byte(raw)
		}
		k, err := st.Identity(agent, kind, data)
		if err != nil {
			t.Fatal(err)
		}
		noRawMarker(t, "digest", k)
		return k
	}
	claudeAccount := key(st, "claude", quota.IdentityAccount, accountMarker)
	if got := readState(t, st).AccountKeys["claude/"+quota.IdentityAccount]; got != claudeAccount {
		t.Fatalf("state keeps %q, Identity returned %q", got, claudeAccount)
	}
	again, err := OpenUserState(st.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if key(again, "claude", quota.IdentityAccount, accountMarker) != claudeAccount {
		t.Fatal("same salt and input gave another digest")
	}
	other, err := OpenUserState(filepath.Join(t.TempDir(), "accounts"))
	if err != nil {
		t.Fatal(err)
	}
	digests := map[string]string{
		"same":         claudeAccount,
		"installation": key(other, "claude", quota.IdentityAccount, accountMarker),
		"agent":        key(st, "codex", quota.IdentityAccount, accountMarker),
		"kind":         key(st, "claude", quota.IdentityOrg, accountMarker),
		"unknown":      key(st, "claude", quota.IdentityAccount, ""),
	}
	seen := map[string]string{}
	for name, d := range digests {
		if prev, ok := seen[d]; ok {
			t.Fatalf("%s and %s share a digest", prev, name)
		}
		seen[d] = name
	}

	// A missing identity is a stable bucket of the installation and agent,
	// never a random identity.
	unknown := key(st, "claude", quota.IdentityAccount, "")
	if unknown != digests["unknown"] || key(again, "claude", quota.IdentityAccount, "") != unknown {
		t.Fatal("unknown identity bucket is not stable")
	}
	if empty, err := st.Identity("claude", quota.IdentityAccount, []byte{}); err != nil || empty != unknown {
		t.Fatalf("empty identity %q, %v; want the unknown bucket", empty, err)
	}
	if key(other, "claude", quota.IdentityAccount, "") == unknown || key(st, "codex", quota.IdentityAccount, "") == unknown {
		t.Fatal("unknown bucket shared across installations or agents")
	}

	// A new identity drops the agent's rates and tier, but keeps its spend
	// and reservations: rotating an account never brings a fresh budget.
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	key(st, "claude", quota.IdentityAccount, accountMarker)
	err = st.Update(ctx, func(s *State) error {
		s.Settled["spent"] = Settlement{ID: "spent", Agent: "claude", AccountKey: claudeAccount, ChargedUSD: 3, Ended: now}
		s.Reservations["running"] = Reservation{ID: "running", Agent: "claude", AccountKey: claudeAccount, Paid: true, Started: true, Created: now}
		s.Samples = []RateSample{{Agent: "claude", Family: "sonnet", Intervals: 3}, {Agent: "codex", Family: "codex", Intervals: 3}}
		s.Tiers = map[string]TierResult{"claude": {Tier: "large"}, "codex": {Tier: "medium"}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	key(st, "claude", quota.IdentityAccount, accountMarker)
	if s := readState(t, st); len(s.Samples) != 2 || len(s.Tiers) != 2 {
		t.Fatalf("an unchanged identity dropped rates: %+v", s)
	}
	rotated := key(st, "claude", quota.IdentityAccount, "RAW-ROTATED-MARKER")
	s := readState(t, st)
	if s.AccountKeys["claude/"+quota.IdentityAccount] != rotated {
		t.Fatalf("new identity not recorded: %+v", s.AccountKeys)
	}
	if s.Settled["spent"].ChargedUSD != 3 || !s.Reservations["running"].Started {
		t.Fatalf("identity change reset spend or freed a reservation: %+v %+v", s.Settled, s.Reservations)
	}
	if !slices.Equal(s.Samples, []RateSample{{Agent: "codex", Family: "codex", Intervals: 3}}) || len(s.Tiers) != 1 || s.Tiers["codex"].Tier != "medium" {
		t.Fatalf("identity change kept the agent's rates or dropped another's: %+v %+v", s.Samples, s.Tiers)
	}

	// A sink that fails fails the identity refresh, and so the read: the
	// verdict is paid and nothing fresh is written.
	failing := quota.Reader{Version: "test", Timeout: 5 * time.Second, IdentitySink: func(string, string, []byte) error {
		return errors.New("sink failed: " + accountMarker)
	}}
	_, failed, a, err := (&Cache{Read: failing.ReadProcess}).Get(ctx, freeWorker("claude"), &config.Config{}, quota.RunEvidence{}, true)
	if err == nil || a.Verdict != Paid || len(failed.Windows) != 0 {
		t.Fatalf("failed identity refresh still admitted: %v %+v %+v", err, a, failed)
	}
	noRawMarker(t, "error", errText(err))

	// State that cannot be read fails Identity and the read with it; the
	// state file is never replaced by a fresh one.
	path := filepath.Join(st.Dir, "state.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"settled":`), 0600); err != nil {
		t.Fatal(err)
	}
	if k, err := st.Identity("claude", quota.IdentityAccount, []byte(accountMarker)); err == nil || k != "" {
		t.Fatalf("Identity on corrupt state: %q, %v", k, err)
	} else {
		noRawMarker(t, "error", err.Error())
	}
	_, failed, a, err = cache.Get(ctx, freeWorker("claude"), &config.Config{}, quota.RunEvidence{}, true)
	if err == nil || a.Verdict != Paid || len(failed.Windows) != 0 {
		t.Fatalf("corrupt state still admitted: %v %+v", err, a)
	}
	noRawMarker(t, "error", errText(err))
	if data, _ := os.ReadFile(path); string(data) != `{"version":1,"settled":` {
		t.Fatalf("corrupt state replaced: %s", data)
	}
}

func TestAccountStateUserWideAndAtomic(t *testing.T) {
	ctx := context.Background()

	// With no folder given, every project uses the user config folder; no
	// project path is the source of the ledger.
	identityHomes(t)
	userDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	projects := []string{t.TempDir(), t.TempDir()}
	for i, project := range projects {
		t.Chdir(project)
		st, err := OpenUserState("")
		if err != nil {
			t.Fatal(err)
		}
		if st.Dir != filepath.Join(userDir, "duolog", "accounts") {
			t.Fatalf("default folder %s", st.Dir)
		}
		err = st.Update(ctx, func(s *State) error {
			if s.NextTicket != uint64(i) {
				return fmt.Errorf("project %d sees ticket %d", i, s.NextTicket)
			}
			s.NextTicket++
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, project := range projects {
		if entries, _ := os.ReadDir(project); len(entries) != 0 {
			t.Fatalf("account state written into a project: %v", entries)
		}
	}
	if _, err := OpenUserState("accounts"); err == nil {
		t.Fatal("relative account state folder accepted")
	}

	// Two project clients share one injected folder under its lock.
	parent := t.TempDir()
	dir := filepath.Join(parent, "accounts")
	a, err := OpenUserState(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := OpenUserState(dir)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	digests := make([]string, 20)
	errs := make([]error, 20)
	for i := range 20 {
		client := a
		if i%2 == 1 {
			client = b
		}
		wg.Go(func() {
			// The first Identity of either client makes the salt; all agree.
			digests[i], errs[i] = client.Identity("claude", quota.IdentityAccount, []byte(accountMarker))
			if errs[i] != nil {
				return
			}
			errs[i] = client.Update(ctx, func(s *State) error {
				s.NextTicket++
				s.Tickets = append(s.Tickets, Ticket{ID: fmt.Sprintf("t%d", i), Agent: "claude", Sequence: s.NextTicket})
				return nil
			})
		})
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatal(i, err)
		}
		if digests[i] != digests[0] {
			t.Fatal("clients made different salts")
		}
	}
	got := readState(t, b)
	if got.NextTicket != 20 || len(got.Tickets) != 20 {
		t.Fatalf("lost updates: next %d, tickets %d", got.NextTicket, len(got.Tickets))
	}
	salt := stateFiles(t, dir)["salt"]
	if salt == "" {
		t.Fatal("no salt file")
	}
	if _, err := a.Identity("codex", quota.IdentityOrg, nil); err != nil || stateFiles(t, dir)["salt"] != salt {
		t.Fatalf("salt changed after it was made: %v", err)
	}

	// The folder is private to the user, and so is every file in it.
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0700 {
			t.Fatalf("folder mode %v, %v", info.Mode().Perm(), err)
		}
		for _, name := range []string{"state.json", "salt", "accounts.lock"} {
			if info, err := os.Stat(filepath.Join(dir, name)); err != nil || info.Mode().Perm() != 0600 {
				t.Fatalf("%s mode %v, %v", name, info.Mode().Perm(), err)
			}
		}
		loose := filepath.Join(parent, "loose")
		if err := os.Mkdir(loose, 0755); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenUserState(loose); err != nil {
			t.Fatal(err)
		}
		if info, _ := os.Stat(loose); info.Mode().Perm() != 0700 {
			t.Fatalf("existing folder kept mode %v", info.Mode().Perm())
		}
	}

	// The state is versioned JSON.
	path := filepath.Join(dir, "state.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(before, &raw); err != nil || raw["version"] != float64(1) {
		t.Fatalf("state is not versioned JSON: %v %s", err, before)
	}

	// An interrupted write, a failed update and a temporary file left by a
	// crash all keep the previous state.
	saved := publish
	publish = func(string, string) error { return errors.New("interrupted") }
	err = a.Update(ctx, func(s *State) error { s.NextTicket = 99; return nil })
	publish = saved
	if err == nil {
		t.Fatal("interrupted write reported success")
	}
	if err := a.Update(ctx, func(s *State) error { s.NextTicket = 98; return errors.New("refused") }); err == nil {
		t.Fatal("failed update reported success")
	}
	if err := os.WriteFile(filepath.Join(dir, ".state.json-crash"), []byte(`{"version":1,"next_ticket":`), 0600); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(after, before) {
		t.Fatalf("previous state lost:\n%s", after)
	}
	if got := readState(t, a); got.NextTicket != 20 {
		t.Fatalf("next ticket %d after interrupted writes", got.NextTicket)
	}
	for name := range stateFiles(t, dir) {
		if strings.HasPrefix(name, ".state.json-") && name != ".state.json-crash" {
			t.Fatalf("temporary file left: %s", name)
		}
	}

	// Corrupt or unknown state refuses admission and is never overwritten.
	for _, bad := range []string{"", "{not json", `{"version":2}`, `{"next_ticket":3}`} {
		if err := os.WriteFile(path, []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		called := false
		err := b.Update(ctx, func(*State) error { called = true; return nil })
		if err == nil || called {
			t.Fatalf("state %q admitted: %v", bad, err)
		}
		if data, _ := os.ReadFile(path); string(data) != bad {
			t.Fatalf("state %q overwritten with %s", bad, data)
		}
	}
}
