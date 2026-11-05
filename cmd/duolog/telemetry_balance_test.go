package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/telemetry"
	"github.com/lorem-dev/duolog/internal/testbin"
)

// balanceStore turns collection on in a temporary home and opens the account
// store the way duolog quota and duolog route do. It returns the store and
// the telemetry root.
func balanceStore(t *testing.T) (*telemetry.Store, string) {
	t.Helper()
	telemetryRoot := telemetryHome(t)
	dir := t.TempDir()
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := openAccountTelemetry(cfg, dir)
	if s == nil {
		t.Fatal("account telemetry did not open")
	}
	t.Cleanup(func() { s.Close() })
	return s, telemetryRoot
}

// accountEvents reads the account events of type E.
func accountEvents[E telemetry.Event](t *testing.T, s *telemetry.Store) []E {
	t.Helper()
	events, err := s.Read(telemetry.Query{})
	if err != nil {
		t.Fatal(err)
	}
	var out []E
	for _, e := range events {
		if x, ok := e.(E); ok {
			out = append(out, x)
		}
	}
	return out
}

// accountLines returns every line of the account segments on disk.
func accountLines(t *testing.T, telemetryRoot string) []string {
	t.Helper()
	segments, err := filepath.Glob(filepath.Join(telemetryRoot, "v1", "_account", "*.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, path := range segments {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")...)
	}
	return lines
}

// installQuotaFakes puts fake claude and codex agents that answer a quota
// read first on PATH, and nothing else.
func installQuotaFakes(t *testing.T, claudeStatus, codexResult string) {
	t.Helper()
	claude := testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{
		"-p":                 {Stdout: `{"usage_report":{"rate_limits":{"limits":[{"kind":"session","percent":15,"resets_at":"2099-01-01T00:00:00Z"},{"kind":"weekly_opus","percent":40,"resets_at":"2099-01-05T00:00:00Z"}],"extra_usage":{"is_enabled":true,"used_credits":3}}}}`},
		"auth status --json": {Stdout: claudeStatus},
	}})
	codex := testbin.Install(t, "codex", testbin.Scenario{RPCResponses: map[string]string{
		"initialize":              `{"id":1,"result":{}}`,
		"account/rateLimits/read": `{"id":2,"result":` + codexResult + `}`,
	}})
	t.Setenv("PATH", filepath.Dir(claude)+string(os.PathListSeparator)+filepath.Dir(codex))
}

const codexQuota = `{"rateLimits":{"planType":"plus","primary":{"usedPercent":13,"windowDurationMins":300,"resetsAt":4070908800},"secondary":{"usedPercent":20,"windowDurationMins":10080,"resetsAt":4071513600}}}`

// Quota snapshots are deduplicated by agent, epoch, window, family and reset
// at the balance cadence, within one process and across processes; a changed
// usage or a new reset is written, and only the windows that changed. A large
// snapshot splits into groups of one window kind, each line within 1 KiB and
// each window once.
func TestTelemetryBalanceDedup(t *testing.T) {
	s, telemetryRoot := balanceStore(t)
	now := time.Now()
	session := WindowObservation{Kind: "five_hour", Family: "all", Status: "ok", LengthMinutes: 300, UsedBasisPoints: 1500, ResetAt: now.Add(3 * time.Hour)}
	weekly := WindowObservation{Kind: "weekly", Family: "all", Status: "ok", LengthMinutes: 10080, UsedBasisPoints: 4000, ResetAt: now.Add(72 * time.Hour)}
	snapshot := func(agent string, epoch int64, windows ...WindowObservation) QuotaObservation {
		return QuotaObservation{Agent: agent, Source: "usage_read", Plan: "max", Epoch: epoch, Windows: windows}
	}
	quotas := func() []telemetry.Quota { return accountEvents[telemetry.Quota](t, s) }
	observe := observeBalance(s)

	observe.Quota(snapshot("claude", 0, session, weekly))
	got := quotas()
	if len(got) != 1 || len(got[0].Windows) != 2 {
		t.Fatalf("%+v", got)
	}
	first := got[0]
	if w := first.Windows[0]; w.Kind != "five_hour" || w.Family != "all" || w.Status != "ok" || w.LenMin != 300 || w.UsedBP != 1500 || w.ResetInMin < 179 || w.ResetInMin > 180 {
		t.Fatalf("%+v", w)
	}
	if first.Agent != "claude" || first.Src != "usage_read" || first.Plan != "max" || first.Epoch != 0 {
		t.Fatalf("%+v", first)
	}

	t.Run("unchanged", func(t *testing.T) {
		observe.Quota(snapshot("claude", 0, session, weekly))
		// A reset read again a few seconds off is the same reset.
		jitter := session
		jitter.ResetAt = jitter.ResetAt.Add(30 * time.Second)
		observe.Quota(snapshot("claude", 0, jitter, weekly))
		// A new process remembers what the store holds.
		observeBalance(s).Quota(snapshot("claude", 0, session, weekly))
		if n := len(quotas()); n != 1 {
			t.Fatalf("unchanged snapshot wrote %d events", n-1)
		}
	})

	t.Run("changed usage", func(t *testing.T) {
		used := weekly
		used.UsedBasisPoints = 4100
		observe.Quota(snapshot("claude", 0, session, used))
		got := quotas()
		if len(got) != 2 || len(got[1].Windows) != 1 || got[1].Windows[0].Kind != "weekly" || got[1].Windows[0].UsedBP != 4100 {
			t.Fatalf("%+v", got)
		}
		weekly = used
	})

	t.Run("new reset", func(t *testing.T) {
		reset := session
		reset.UsedBasisPoints, reset.ResetAt = 1500, now.Add(8*time.Hour)
		observe.Quota(snapshot("claude", 0, reset, weekly))
		got := quotas()
		if len(got) != 3 || len(got[2].Windows) != 1 || got[2].Windows[0].ResetInMin < 479 || got[2].Windows[0].ResetInMin > 480 {
			t.Fatalf("%+v", got)
		}
		session = reset
	})

	t.Run("distinct", func(t *testing.T) {
		opus := weekly
		opus.Family = "opus"
		hour := session
		hour.Kind, hour.LengthMinutes = "other", 60
		before := len(quotas())
		for _, q := range []QuotaObservation{
			snapshot("codex", 0, session, weekly),  // another agent
			snapshot("claude", 0, session, opus),   // another family
			snapshot("claude", 0, session, hour),   // another window
			snapshot("claude", 1, session, weekly), // another epoch
		} {
			observe.Quota(q)
		}
		got := quotas()[before:]
		if len(got) != 4 {
			t.Fatalf("%+v", got)
		}
		if got[0].Agent != "codex" || len(got[0].Windows) != 2 || got[1].Windows[0].Family != "opus" || len(got[1].Windows) != 1 ||
			got[2].Windows[0].LenMin != 60 || len(got[2].Windows) != 1 || got[3].Epoch != 1 || len(got[3].Windows) != 2 {
			t.Fatalf("%+v", got)
		}
	})

	t.Run("split", func(t *testing.T) {
		kinds := []string{"five_hour", "weekly", "other"}
		families := []string{"all", "opus", "sonnet", "haiku", "sol", "terra", "luna", "fable"}
		var windows []WindowObservation
		for i := range 40 {
			windows = append(windows, WindowObservation{Kind: kinds[i%3], Family: families[i%len(families)], Status: "ok", LengthMinutes: int64(1000 + i), UsedBasisPoints: int64(100 * i), ResetAt: now.Add(time.Duration(i+1) * time.Hour)})
		}
		before := len(quotas())
		observe.Quota(snapshot("codex", 7, windows...))
		got := quotas()[before:]
		if len(got) < 3 {
			t.Fatalf("40 windows in %d events", len(got))
		}
		seen := map[int64]bool{}
		for _, q := range got {
			if q.Agent != "codex" || q.Epoch != 7 || len(q.Windows) == 0 {
				t.Fatalf("%+v", q)
			}
			for _, w := range q.Windows {
				if w.Kind != q.Windows[0].Kind {
					t.Fatalf("event mixes window kinds: %+v", q)
				}
				if seen[w.LenMin] {
					t.Fatalf("window %d written twice", w.LenMin)
				}
				seen[w.LenMin] = true
			}
		}
		if len(seen) != 40 {
			t.Fatalf("%d of 40 windows written", len(seen))
		}
		for _, line := range accountLines(t, telemetryRoot) {
			if len(line)+1 > 1024 {
				t.Fatalf("line of %d bytes", len(line)+1)
			}
		}
	})
}

// Reset events carry the four reasons, the epoch is part of the dedup key,
// and a quota read writes no fingerprint, identity, vendor id or credit
// balance.
func TestTelemetryBalanceEpoch(t *testing.T) {
	s, telemetryRoot := balanceStore(t)
	observe := observeBalance(s)
	reasons := []string{"account", "plan", "drift", "manual", "sentinelreason"}
	for _, reason := range reasons {
		observe.Reset(ResetObservation{Agent: "codex", Reason: reason})
	}
	resets := accountEvents[telemetry.Reset](t, s)
	if len(resets) != len(reasons) {
		t.Fatalf("%+v", resets)
	}
	for i, want := range []string{"account", "plan", "drift", "manual", "other"} {
		if resets[i].Agent != "codex" || resets[i].Reason != want {
			t.Fatalf("reset %d: %+v want %s", i, resets[i], want)
		}
	}

	window := WindowObservation{Kind: "five_hour", Family: "all", Status: "ok", LengthMinutes: 300, UsedBasisPoints: 500, ResetAt: time.Now().Add(time.Hour)}
	for _, epoch := range []int64{1, 1, 2, 2} {
		observe.Quota(QuotaObservation{Agent: "claude", Source: "usage_read", Epoch: epoch, Windows: []WindowObservation{window}})
	}
	quotas := accountEvents[telemetry.Quota](t, s)
	if len(quotas) != 2 || quotas[0].Epoch != 1 || quotas[1].Epoch != 2 {
		t.Fatalf("fingerprint bump: %+v", quotas)
	}

	// The vendor answers carry identity, a vendor limit id and a credit
	// balance; none of it reaches a record.
	const sentinel = "sentinelidentity"
	t.Chdir(t.TempDir())
	installQuotaFakes(t,
		`{"loggedIn":true,"subscriptionType":"max","email":"`+sentinel+`@example.com","orgId":"`+sentinel+`-org","accountUuid":"`+sentinel+`-account"}`,
		`{"rateLimits":{"planType":"plus","primary":{"usedPercent":13,"windowDurationMins":300,"resetsAt":4070908800},"credits":{"hasCredits":true,"unlimited":false,"balance":"987654.321"}},"rateLimitsByLimitId":{"`+sentinel+`-limit":{"primary":{"usedPercent":7,"windowDurationMins":300,"resetsAt":4070908800}}}}`)
	for range 2 {
		if out, err := executeBusCommand(t, "quota"); err != nil {
			t.Fatal(out, err)
		}
	}
	quotas = accountEvents[telemetry.Quota](t, s)[2:]
	if len(quotas) != 2 {
		t.Fatalf("two quota reads of unchanged windows: %+v", quotas)
	}
	byAgent := map[string]telemetry.Quota{}
	for _, q := range quotas {
		byAgent[q.Agent] = q
	}
	claude, codex := byAgent["claude"], byAgent["codex"]
	if claude.Src != "usage_read" || claude.Plan != "max" || !claude.Overage || len(claude.Windows) != 2 ||
		claude.Windows[0].Kind != "five_hour" || claude.Windows[0].Family != "all" || claude.Windows[0].UsedBP != 1500 ||
		claude.Windows[1].Kind != "weekly" || claude.Windows[1].Family != "opus" || claude.Windows[1].UsedBP != 4000 {
		t.Fatalf("claude %+v", claude)
	}
	if codex.Src != "app_server" || codex.Plan != "plus" || !codex.Overage || len(codex.Windows) != 2 ||
		codex.Windows[0].Family != "all" || codex.Windows[0].UsedBP != 1300 || codex.Windows[1].Family != "other" || codex.Windows[1].UsedBP != 700 {
		t.Fatalf("codex %+v", codex)
	}
	err := filepath.Walk(telemetryRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, leak := range []string{sentinel, "example.com", "987654", "fingerprint"} {
			if strings.Contains(string(data), leak) {
				t.Errorf("%s in %s", leak, path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Route events keep every rule of the spec with candidates, chosen and
// incumbent, and duolog route reports the router's own rules: the role's
// pinned target, or the availability fallback.
func TestTelemetryRouteRules(t *testing.T) {
	s, _ := balanceStore(t)
	observe := observeBalance(s)
	sol := RouteTarget{Agent: "codex", Family: "sol", Effort: "medium"}
	opus := RouteTarget{Agent: "claude", Family: "opus", Effort: "high"}
	rules := []string{"explicit", "availability", "cross_review", "pinned", "admission", "hysteresis", "balance", "outside_ladder"}
	for i, rule := range rules {
		observe.Route(RouteObservation{Role: "implement", Class: "sentinelclass", Candidates: []RouteTarget{sol, opus}, Chosen: opus, Incumbent: sol, Rule: rule, ScoreMin: float64(i) / 4})
	}
	observe.Route(RouteObservation{Role: "implement", Candidates: []RouteTarget{sol}, Chosen: sol, Incumbent: sol, Rule: "sentinelrule"})
	got := accountEvents[telemetry.Route](t, s)
	if len(got) != len(rules)+1 {
		t.Fatalf("%+v", got)
	}
	wantSol := telemetry.Target{Agent: "codex", Family: "sol", Effort: "medium"}
	wantOpus := telemetry.Target{Agent: "claude", Family: "opus", Effort: "high"}
	for i, rule := range rules {
		r := got[i]
		if r.Rule != rule || r.Role != "implement" || r.Class != "other" || r.ScoreMin != float64(i)/4 ||
			!slices.Equal(r.Candidates, []telemetry.Target{wantSol, wantOpus}) || r.Chosen != wantOpus || r.Incumbent != wantSol {
			t.Fatalf("rule %s: %+v", rule, r)
		}
	}
	if r := got[len(rules)]; r.Rule != "other" || r.Chosen != wantSol || r.Incumbent != wantSol {
		t.Fatalf("%+v", r)
	}

	routeLead(t)
	routed := func() telemetry.Route {
		t.Helper()
		if out, err := executeBusCommand(t, "route", "review_intermediate", "--json"); err != nil {
			t.Fatal(out, err)
		}
		all := accountEvents[telemetry.Route](t, s)
		return all[len(all)-1]
	}
	if r := routed(); r.Rule != "pinned" || r.Role != "review_intermediate" || r.Chosen != wantSol || r.Incumbent != wantSol || !slices.Equal(r.Candidates, []telemetry.Target{wantSol}) {
		t.Fatalf("pinned %+v", r)
	}
	reg, err := session.Open(".duolog")
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.MarkUnavailable("codex", time.Time{}); err != nil {
		t.Fatal(err)
	}
	r := routed()
	if r.Rule != "availability" || r.Incumbent != wantSol || r.Chosen.Agent != "claude" || r.Chosen == wantSol || !slices.Equal(r.Candidates, []telemetry.Target{wantSol, r.Chosen}) {
		t.Fatalf("availability %+v", r)
	}
}

// Routing and quota answers are the same with telemetry on, off, failing,
// capped or purged: telemetry only mirrors what they read and resolve.
func TestBalanceWithoutTelemetry(t *testing.T) {
	telemetryRoot := telemetryHome(t)
	routeLead(t)
	installQuotaFakes(t, `{"loggedIn":true,"subscriptionType":"max"}`, codexQuota)
	answers := func() string {
		t.Helper()
		var b strings.Builder
		for _, role := range []string{"review_intermediate", "plan", "implement"} {
			out, err := executeBusCommand(t, "route", role, "--json")
			if err != nil {
				t.Fatal(out, err)
			}
			b.WriteString(out)
		}
		out, err := executeBusCommand(t, "quota", "--json")
		if err != nil {
			t.Fatal(out, err)
		}
		var quotas map[string]map[string]any
		if err := json.Unmarshal([]byte(out), &quotas); err != nil {
			t.Fatal(out, err)
		}
		for _, q := range quotas {
			delete(q, "read_at")
		}
		data, err := json.Marshal(quotas)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(data)
		return b.String()
	}
	want := answers()
	root := mustGetwd(t)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	s := openAccountTelemetry(cfg, root)
	if s == nil {
		t.Fatal("account telemetry did not open")
	}
	defer s.Close()
	if routes, quotas := accountEvents[telemetry.Route](t, s), accountEvents[telemetry.Quota](t, s); len(routes) != 3 || len(quotas) != 2 {
		t.Fatalf("routes=%d quotas=%d", len(routes), len(quotas))
	}

	t.Run("off", func(t *testing.T) {
		t.Setenv("DUOLOG_TELEMETRY", "off")
		if got := answers(); got != want {
			t.Fatalf("%s\nwant %s", got, want)
		}
	})
	if routes := accountEvents[telemetry.Route](t, s); len(routes) != 3 {
		t.Fatalf("off wrote %d route events", len(routes)-3)
	}

	t.Run("cap", func(t *testing.T) {
		dir, err := os.UserConfigDir()
		if err != nil {
			t.Fatal(err)
		}
		user := filepath.Join(dir, "duolog", "config.yaml")
		if err := os.MkdirAll(filepath.Dir(user), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(user, []byte("telemetry:\n  max_megabytes: 1\n"), 0600); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(user)
		segment := filepath.Join(telemetryRoot, "v1", "_account", time.Now().UTC().Format(time.DateOnly)+".ndjson")
		f, err := os.OpenFile(segment, os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			t.Fatal(err)
		}
		w := bufio.NewWriter(f)
		for range 1100 {
			fmt.Fprintln(w, strings.Repeat("x", 999))
		}
		if err := errors.Join(w.Flush(), f.Close()); err != nil {
			t.Fatal(err)
		}
		before := (*telemetry.Store)(nil).Health().Dropped
		if got := answers(); got != want {
			t.Fatalf("%s\nwant %s", got, want)
		}
		if (*telemetry.Store)(nil).Health().Dropped <= before {
			t.Fatal("capped route events not counted")
		}
	})

	t.Run("purge", func(t *testing.T) {
		if err := s.Purge(true); err != nil {
			t.Fatal(err)
		}
		if got := answers(); got != want {
			t.Fatalf("%s\nwant %s", got, want)
		}
		if routes := accountEvents[telemetry.Route](t, s); len(routes) != 3 {
			t.Fatalf("after purge: %d route events", len(routes))
		}
	})

	t.Run("failure", func(t *testing.T) {
		s.Close()
		before := s.Health().Failures
		observeBalance(s).Route(RouteObservation{Role: "plan", Rule: "pinned"})
		if s.Health().Failures != before+1 {
			t.Fatal("failed append not counted")
		}
		if runtime.GOOS == "windows" {
			t.Skip("creating a symlink needs a privilege on Windows")
		}
		// A root the store refuses to use: open fails and the answers stay.
		if err := os.Rename(telemetryRoot, telemetryRoot+".moved"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(telemetryRoot+".moved", telemetryRoot); err != nil {
			t.Fatal(err)
		}
		if got := answers(); got != want {
			t.Fatalf("%s\nwant %s", got, want)
		}
	})
}
