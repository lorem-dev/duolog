package workerenv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
)

const (
	antKey   = "sk-ant-test-secret"
	antToken = "tok-test-secret"
	oaiKey   = "sk-oai-test-secret"
)

// envValue finds name in env, the last entry winning.
func envValue(env []string, name string) (string, bool) {
	value, found := "", false
	for _, pair := range env {
		if n, v, _ := strings.Cut(pair, "="); n == name {
			value, found = v, true
		}
	}
	return value, found
}

func envCount(env []string, name string) int {
	n := 0
	for _, pair := range env {
		if strings.HasPrefix(pair, name+"=") {
			n++
		}
	}
	return n
}

func paidConfig() *config.Config {
	cfg := &config.Config{EnvPassthrough: []string{
		"PATH", "HOME", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL",
		"OPENAI_API_KEY", "CODEX_API_KEY", "CORP_LLM_KEY", "DUOLOG_LEAD", "REX_SESSION",
	}}
	cfg.PaidRuns.UsableShare = 0.5
	cfg.PaidRuns.Claude.Allow = true
	cfg.PaidRuns.Claude.DailyCapUSD = 5
	cfg.PaidRuns.Claude.Env = []string{"ANTHROPIC_API_KEY", "CLAUDE_PAID_EXTRA"}
	cfg.PaidRuns.Claude.SubscriptionEndpoints = map[string]string{"corp-gateway": "https://llm.example.com/claude"}
	cfg.PaidRuns.Codex.Env = []string{"OPENAI_API_KEY", "CODEX_PAID_EXTRA"}
	return cfg
}

func paidEnviron() []string {
	return []string{
		"PATH=/usr/bin", "HOME=/home/test",
		"ANTHROPIC_API_KEY=" + antKey, "ANTHROPIC_AUTH_TOKEN=" + antToken,
		"ANTHROPIC_BASE_URL=https://LLM.example.com:443/claude/",
		"OPENAI_API_KEY=" + oaiKey, "CODEX_API_KEY=" + oaiKey, "CORP_LLM_KEY=corp-test-value",
		"CLAUDE_PAID_EXTRA=c", "CODEX_PAID_EXTRA=x",
		"DUOLOG_LEAD=old-lead", "REX_SESSION=rex", "DISABLE_AUTOUPDATER=0",
	}
}

// buildInput is a launch with every kind of name set. CORP_LLM_KEY is the
// env_key of Codex's default provider.
func buildInput(agent string, shape Shape, variant Variant) ProcessInput {
	in := ProcessInput{
		Agent: agent, Shape: shape, Variant: variant, Config: paidConfig(),
		Environ: paidEnviron(), LeadID: "lead-1", Role: "implement", PeerID: "peer-1",
		StateDir: "/p",
	}
	if agent == "codex" {
		in.ProviderEnvKey = "CORP_LLM_KEY"
	}
	return in
}

func mustBuild(t *testing.T, in ProcessInput) Process {
	t.Helper()
	p, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func settingsOf(t *testing.T, p Process) map[string]json.RawMessage {
	t.Helper()
	var s map[string]json.RawMessage
	if err := json.Unmarshal([]byte(p.Settings), &s); err != nil {
		t.Fatal(p.Settings, err)
	}
	return s
}

// processSignals lists a process's signals as name@layer.
func processSignals(p Process) []string {
	var names []string
	for _, s := range p.Signals {
		names = append(names, s.Name+"@"+s.Layer)
	}
	return names
}

// The free variant drops every route name, also one the person listed in
// env_passthrough and also when paid runs are allowed; only a Claude base URL
// that matches a named subscription endpoint passes. The paid variant adds
// its own agent's paid env and lets listed route names through. duolog's own
// variables and the updater and memory switches survive either way.
func TestBuildFreeAndPaid(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Dir(exe) + string(os.PathListSeparator) + "/usr/bin"

	free := mustBuild(t, buildInput("claude", Worker, Free))
	for _, name := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_PAID_EXTRA", "CODEX_PAID_EXTRA", "REX_SESSION", "DUOLOG_PEER"} {
		if _, ok := envValue(free.Env, name); ok {
			t.Errorf("free claude worker got %s", name)
		}
	}
	for name, want := range map[string]string{
		"HOME": "/home/test", "ANTHROPIC_BASE_URL": "https://LLM.example.com:443/claude/",
		"PATH": wantPath, "DUOLOG_DEPTH": "1", "DUOLOG_LEAD": "lead-1", "DUOLOG_ROLE": "implement",
		"DISABLE_AUTOUPDATER": "1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY": "1",
	} {
		if got, ok := envValue(free.Env, name); !ok || got != want || envCount(free.Env, name) != 1 {
			t.Errorf("free %s = %q (%d), want %q once", name, got, envCount(free.Env, name), want)
		}
	}
	if !reflect.DeepEqual(free.Dropped, []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"}) {
		t.Errorf("free Dropped = %v", free.Dropped)
	}
	if !reflect.DeepEqual(free.EndpointEntries, []string{"corp-gateway"}) || len(free.Signals) != 0 || !free.FastPinned {
		t.Errorf("free = %+v", free)
	}
	s := settingsOf(t, free)
	if string(s["autoMemoryEnabled"]) != "false" || string(s["fastMode"]) != "false" {
		t.Error(free.Settings)
	}
	if got := free.Argv("-p", "--model", "opus"); !reflect.DeepEqual(got, []string{"claude", "--settings", free.Settings, "-p", "--model", "opus"}) {
		t.Error(got)
	}

	peer := mustBuild(t, buildInput("claude", Peer, Free))
	if got, _ := envValue(peer.Env, "DUOLOG_PEER"); got != "peer-1" {
		t.Errorf("peer DUOLOG_PEER = %q", got)
	}

	paid := mustBuild(t, buildInput("claude", Worker, Paid))
	for name, want := range map[string]string{
		"ANTHROPIC_API_KEY": antKey, "ANTHROPIC_AUTH_TOKEN": antToken, "CLAUDE_PAID_EXTRA": "c",
		"ANTHROPIC_BASE_URL": "https://LLM.example.com:443/claude/", "PATH": wantPath,
		"DUOLOG_LEAD": "lead-1", "DISABLE_AUTOUPDATER": "1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY": "1",
	} {
		if got, ok := envValue(paid.Env, name); !ok || got != want || envCount(paid.Env, name) != 1 {
			t.Errorf("paid %s = %q (%d), want %q once", name, got, envCount(paid.Env, name), want)
		}
	}
	// Only its own agent's paid env: Codex's list stays with Codex.
	if _, ok := envValue(paid.Env, "CODEX_PAID_EXTRA"); ok {
		t.Error("paid claude worker got the codex paid env")
	}
	if want := []string{"env:ANTHROPIC_API_KEY@env", "env:ANTHROPIC_AUTH_TOKEN@env"}; !reflect.DeepEqual(processSignals(paid), want) {
		t.Errorf("paid signals = %v, want %v", processSignals(paid), want)
	}
	if len(paid.Dropped) != 0 || !reflect.DeepEqual(paid.EndpointEntries, []string{"corp-gateway"}) {
		t.Errorf("paid = %+v", paid)
	}

	// Paid without allow is an error, never a quiet free run.
	if _, err := Build(buildInput("codex", Worker, Paid)); err == nil || !strings.Contains(err.Error(), "paid_runs.codex.allow") {
		t.Fatalf("codex paid without allow: %v", err)
	}

	// Codex: its key shapes and the provider env_key are routes; the Claude
	// variables are not Codex routes.
	codex := mustBuild(t, buildInput("codex", Worker, Free))
	for _, name := range []string{"OPENAI_API_KEY", "CODEX_API_KEY", "CORP_LLM_KEY", "CODEX_PAID_EXTRA", "DISABLE_AUTOUPDATER"} {
		if _, ok := envValue(codex.Env, name); ok {
			t.Errorf("free codex worker got %s", name)
		}
	}
	if !reflect.DeepEqual(codex.Dropped, []string{"CODEX_API_KEY", "CORP_LLM_KEY", "OPENAI_API_KEY"}) || codex.Settings != "" {
		t.Errorf("codex = %+v", codex)
	}
	if got := codex.Argv("exec", "--json"); !reflect.DeepEqual(got, []string{"codex", "-c", "check_for_update_on_startup=false", "exec", "--json"}) {
		t.Error(got)
	}

	// An untrusted base URL is a route name like any other.
	in := buildInput("claude", Worker, Free)
	in.Config.PaidRuns.Claude.SubscriptionEndpoints = map[string]string{"other": "https://llm.example.com/other"}
	untrusted := mustBuild(t, in)
	if _, ok := envValue(untrusted.Env, "ANTHROPIC_BASE_URL"); ok || len(untrusted.EndpointEntries) != 0 ||
		!slices.Contains(untrusted.Dropped, "ANTHROPIC_BASE_URL") {
		t.Errorf("untrusted = %+v", untrusted)
	}

	// Explicit passthrough cannot smuggle a route name into free even when
	// allow is true, and never through the paid env either.
	for _, p := range []Process{free, peer, codex, untrusted} {
		for _, pair := range p.Env {
			if strings.Contains(pair, antKey) || strings.Contains(pair, antToken) || strings.Contains(pair, oaiKey) {
				name, _, _ := strings.Cut(pair, "=")
				t.Errorf("%s %s free variant got a key in %s", p.Agent, p.Shape, name)
			}
		}
	}
}

// Free pins fast mode off over the caller's extras; paid leaves fast mode to
// the person's settings and sets no key for it. A managed fastMode stays a
// paid signal either way. The lead is reported only: its environment and
// prefix come back as observed. Process's JSON carries names, never values.
func TestBuildFastModeVariants(t *testing.T) {
	extra := map[string]any{"fastMode": true, "preferredNotifChannel": "notifications_disabled"}
	on := true
	managed := LayerFacts{Signals: []Signal{{Name: "fastMode", Layer: "managed"}}, FastMode: &on}

	in := buildInput("claude", Peer, Free)
	in.ExtraSettings = extra
	free := mustBuild(t, in)
	s := settingsOf(t, free)
	if string(s["fastMode"]) != "false" || string(s["preferredNotifChannel"]) != `"notifications_disabled"` || !free.FastPinned {
		t.Error(free.Settings)
	}

	in.Variant = Paid
	paid := mustBuild(t, in)
	s = settingsOf(t, paid)
	if _, ok := s["fastMode"]; ok || paid.FastPinned || string(s["preferredNotifChannel"]) != `"notifications_disabled"` {
		t.Error(paid.Settings)
	}
	if string(s["autoMemoryEnabled"]) != "false" {
		t.Error(paid.Settings)
	}
	if extra["fastMode"] != true || len(extra) != 2 {
		t.Fatal("caller options were changed", extra)
	}

	for _, variant := range []Variant{Free, Paid} {
		in.Variant, in.Layers = variant, []LayerFacts{managed}
		p := mustBuild(t, in)
		if !slices.Contains(processSignals(p), "fastMode@managed") {
			t.Errorf("%s: managed fast mode lost: %v", variant, p.Signals)
		}
	}

	// The lead keeps its environment and prefix, and gets no worker
	// variables, settings or pin. Its route names are reported.
	lead := ProcessInput{
		Agent: "claude", Shape: Lead, Variant: Free, Config: paidConfig(),
		Environ: paidEnviron(), Prefix: []string{"claude", "--model", "opus"}, LeadID: "lead-1",
		ExtraSettings: extra, Layers: []LayerFacts{managed},
	}
	lp := mustBuild(t, lead)
	if !reflect.DeepEqual(lp.Env, paidEnviron()) || !reflect.DeepEqual(lp.Prefix, lead.Prefix) {
		t.Errorf("lead env or prefix changed: %v %v", lp.Env, lp.Prefix)
	}
	if lp.Settings != "" || lp.FastPinned || len(lp.Dropped) != 0 {
		t.Errorf("lead = %+v", lp)
	}
	if got := lp.Argv("-p"); !reflect.DeepEqual(got, []string{"claude", "--model", "opus", "-p"}) {
		t.Error(got)
	}
	if want := []string{"env:ANTHROPIC_API_KEY@env", "env:ANTHROPIC_AUTH_TOKEN@env", "fastMode@managed"}; !reflect.DeepEqual(processSignals(lp), want) {
		t.Errorf("lead signals = %v, want %v", processSignals(lp), want)
	}
	lead.Environ[0] = "PATH=/changed"
	if lp.Env[0] != "PATH=/usr/bin" {
		t.Error("lead env shares the caller's slice")
	}

	for _, p := range []Process{free, paid, lp} {
		data, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"Env", "Prefix", "Settings"} {
			if _, ok := fields[key]; ok {
				t.Errorf("JSON has %s: %s", key, data)
			}
		}
		for _, value := range []string{antKey, antToken, oaiKey, "sk-", "llm.example.com", "notifications_disabled"} {
			if strings.Contains(string(data), value) {
				t.Errorf("JSON has %q: %s", value, data)
			}
		}
		if _, ok := fields["CacheKey"]; !ok {
			t.Errorf("JSON lacks the cache key: %s", data)
		}
	}
}

// Ruling 2: the paid variant leaves fast mode to the person's settings and the
// lead's settings are never changed, so an effective fastMode true there is a
// paid signal, named after the layer that set it. Layers come lowest first,
// so a later false clears an earlier true. The free variant pins fast mode
// off over every ordinary layer and gets no signal for it.
func TestBuildFastModeLayerIsPaidSignal(t *testing.T) {
	read := fakeRead(map[string]string{"user.json": `{"fastMode": true}`, "local.json": `{"fastMode": false}`, "argv.json": `{"fastMode": true}`})
	user := ReadLayer("claude", Layer{Name: "user", Path: "user.json"}, nil, read, fakeStat)
	local := ReadLayer("claude", Layer{Name: "local", Path: "local.json"}, nil, read, fakeStat)
	argv := ReadLayer("claude", Layer{Name: "argv", Path: "argv.json"}, nil, read, fakeStat)
	if user.Layer != "user" || user.FastMode == nil || !*user.FastMode || len(user.Signals) != 0 {
		t.Fatalf("user layer = %+v", user)
	}
	for _, c := range []struct {
		name    string
		shape   Shape
		variant Variant
		layers  []LayerFacts
		want    []string
	}{
		{"paid worker", Worker, Paid, []LayerFacts{user}, []string{"fastMode@user"}},
		{"paid peer, later argv", Peer, Paid, []LayerFacts{local, argv}, []string{"fastMode@argv"}},
		{"paid, later false", Worker, Paid, []LayerFacts{user, local}, nil},
		{"free worker", Worker, Free, []LayerFacts{user}, nil},
		{"free peer", Peer, Free, []LayerFacts{local, argv}, nil},
		{"lead", Lead, Free, []LayerFacts{user}, []string{"fastMode@user"}},
		{"lead, later false", Lead, Free, []LayerFacts{user, local}, nil},
	} {
		in := ProcessInput{Agent: "claude", Shape: c.shape, Variant: c.variant, Config: paidConfig(),
			Environ: []string{"PATH=/usr/bin"}, Layers: c.layers}
		p := mustBuild(t, in)
		if got := processSignals(p); !equal(got, c.want) {
			t.Errorf("%s: signals = %v, want %v", c.name, got, c.want)
		}
		if c.shape != Lead && p.FastPinned != (c.variant == Free) {
			t.Errorf("%s: pinned = %v", c.name, p.FastPinned)
		}
	}
}

// Codex's speed and service tiers are unconfirmed (open question 4), so
// duolog adds no guessed flag to switch them off. A speed-shaped setting is
// the route:unknown-speed signal instead. Only a setting recorded as
// supported adds a pin, and then only to the free variant.
func TestBuildDoesNotInventCodexSpeedFlag(t *testing.T) {
	speedArgs := func(argv []string) []string {
		var out []string
		for _, arg := range argv {
			lower := strings.ToLower(arg)
			if strings.Contains(lower, "service_tier") || strings.Contains(lower, "speed") || strings.Contains(lower, "fast") {
				out = append(out, arg)
			}
		}
		return out
	}

	plain := mustBuild(t, buildInput("codex", Worker, Free))
	if got := speedArgs(plain.Argv("exec")); len(got) != 0 || plain.FastPinned || len(plain.Signals) != 0 {
		t.Errorf("plain codex invented a speed flag: %v %+v", got, plain)
	}

	in := buildInput("codex", Peer, Free)
	in.ExtraSettings = map[string]any{"service_tier": "fast", "tui.notifications": false, "notify": []string{}}
	extra := mustBuild(t, in)
	if want := []string{"service_tier='fast'"}; !reflect.DeepEqual(speedArgs(extra.Argv("exec")), want) {
		t.Errorf("speed args = %v, want only the caller's %v", speedArgs(extra.Argv("exec")), want)
	}
	if want := []string{"route:unknown-speed@duolog"}; !reflect.DeepEqual(processSignals(extra), want) || extra.FastPinned {
		t.Errorf("signals = %v, want %v", processSignals(extra), want)
	}
	if got := extra.Argv(); !slices.Contains(got, "tui.notifications=false") || !slices.Contains(got, "notify=[]") {
		t.Error(got)
	}

	lead := ProcessInput{Agent: "codex", Shape: Lead, Variant: Free, Config: paidConfig(),
		Prefix: []string{"codex", "-c", `features.speed="fast"`, "--config=model_reasoning_effort=high"}}
	lp := mustBuild(t, lead)
	if want := []string{"route:unknown-speed@argv"}; !reflect.DeepEqual(processSignals(lp), want) {
		t.Errorf("lead signals = %v, want %v", processSignals(lp), want)
	}

	// A recorded setting pins the free variant, last so that it wins, and
	// is then no unknown signal. The paid variant follows the settings.
	codexSpeedPins = map[string]string{"service_tier": `"recorded-off"`}
	t.Cleanup(func() { codexSpeedPins = map[string]string{} })
	pinned := mustBuild(t, in)
	argv := pinned.Argv("exec")
	if want := []string{`service_tier="recorded-off"`}; !reflect.DeepEqual(speedArgs(argv), want) || argv[len(argv)-2] != want[0] || !pinned.FastPinned {
		t.Errorf("pinned argv = %v", argv)
	}
	if len(pinned.Signals) != 0 {
		t.Errorf("pinned signals = %v", pinned.Signals)
	}
	in.Variant = Paid
	in.Config.PaidRuns.Codex.Allow, in.Config.PaidRuns.Codex.DailyCapUSD = true, 5
	unpinned := mustBuild(t, in)
	if want := []string{"service_tier='fast'"}; !reflect.DeepEqual(speedArgs(unpinned.Argv("exec")), want) || unpinned.FastPinned {
		t.Errorf("paid argv = %v", unpinned.Argv("exec"))
	}
}

// The cache key changes with route names, matched entries, layer facts and
// the config, and never with a value that is not compared.
func TestBuildCacheKeyFromNamesAndFacts(t *testing.T) {
	key := func(change func(*ProcessInput)) string {
		in := buildInput("claude", Worker, Paid)
		in.Layers = []LayerFacts{{Modified: time.Unix(100, 0)}}
		if change != nil {
			change(&in)
		}
		p := mustBuild(t, in)
		if len(p.CacheKey) != 64 || strings.Contains(p.CacheKey, antKey) {
			t.Fatalf("cache key %q", p.CacheKey)
		}
		return p.CacheKey
	}
	base := key(nil)
	if key(func(in *ProcessInput) { in.Environ = append(in.Environ, "ANTHROPIC_API_KEY=sk-other") }) != base {
		t.Error("a key value changed the cache key")
	}
	if key(func(in *ProcessInput) { in.LeadID = "lead-2" }) != base {
		t.Error("the lead id changed the cache key")
	}
	for name, change := range map[string]func(*ProcessInput){
		"variant":  func(in *ProcessInput) { in.Variant = Free },
		"env name": func(in *ProcessInput) { in.Environ = append(in.Environ, "CLAUDE_CODE_USE_BEDROCK=1") },
		"entry": func(in *ProcessInput) {
			in.Environ = append(in.Environ, "ANTHROPIC_BASE_URL=https://other.example.com")
		},
		"mtime":      func(in *ProcessInput) { in.Layers[0].Modified = time.Unix(200, 0) },
		"unreadable": func(in *ProcessInput) { in.Layers[0].Unreadable = true },
		"cap":        func(in *ProcessInput) { in.Config.PaidRuns.Claude.DailyCapUSD = 6 },
		"endpoint": func(in *ProcessInput) {
			in.Config.PaidRuns.Claude.SubscriptionEndpoints = map[string]string{"corp-gateway": "https://llm.example.com/v2"}
		},
		"passthrough": func(in *ProcessInput) { in.Config.EnvPassthrough = in.Config.EnvPassthrough[:2] },
	} {
		if key(change) == base {
			t.Errorf("%s did not change the cache key", name)
		}
	}
}
