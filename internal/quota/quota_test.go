package quota

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/testbin"
)

const claudeReply = `{"usage_report":{"rate_limits":{"limits":[{"kind":"session","percent":15,"resets_at":"2099-01-01T00:00:00Z","severity":"normal","is_active":false},{"kind":"weekly_all","percent":50,"resets_at":"2099-01-08T00:00:00Z"}],"extra_usage":{"is_enabled":false,"used_credits":0,"monthly_limit":null}}}}`
const codexAccountReply = `{"jsonrpc":"2.0","id":3,"result":{"account":{"type":"chatgpt","email":"PRIVATE","planType":"plus"},"requiresOpenaiAuth":true}}`
const codexConfigReply = `{"jsonrpc":"2.0","id":4,"result":{"config":{"model_provider":null,"model_providers":{},"profile":null}}}`
const codexAuthReply = `{"jsonrpc":"2.0","id":5,"result":{"authMethod":"chatgpt","authToken":null,"requiresOpenaiAuth":true}}`

// codexRoute adds the config and auth status replies of a chatgpt login on
// the built-in provider to a fake app-server's replies.
func codexRoute(rpc map[string]string) map[string]string {
	rpc["config/read"], rpc["getAuthStatus"] = codexConfigReply, codexAuthReply
	return rpc
}

const codexReadMethods = "initialize,initialized,account/read,account/rateLimits/read,config/read,getAuthStatus"
const codexReply = `{"jsonrpc":"2.0","id":2,"result":{"rateLimits":{"planType":"plus","primary":{"usedPercent":13,"windowDurationMins":300,"resetsAt":4070908800},"secondary":{"usedPercent":20,"windowDurationMins":10080,"resetsAt":4071513600},"credits":{"hasCredits":true,"unlimited":false,"balance":"10"},"ordinaryUsageAllowed":true,"accountId":"PRIVATE"},"rateLimitsByLimitId":{"sonnet":{"primary":{"usedPercent":30,"windowDurationMins":60,"resetsAt":4070908800}}}}}`

func isolate(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "CODEX_HOME", "CLAUDE_CONFIG_DIR"} {
		t.Setenv(key, home)
	}
}

func TestReadBoth(t *testing.T) {
	isolate(t)
	claude := testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{
		freeClaude("-p"):                 {Stdout: "{\"type\":\"system\"}\n" + claudeReply + "\n"},
		freeClaude("auth status --json"): {Stdout: `{"loggedIn":true,"subscriptionType":"pro","email":"PRIVATE"}`},
	}})
	codex := testbin.Install(t, "codex", testbin.Scenario{SleepMS: 10000, RPCResponses: codexRoute(map[string]string{
		"initialize": `{"id":1,"result":{}}`, "account/rateLimits/read": codexReply,
		"account/read": codexAccountReply,
	})})
	r := Reader{Version: "test", Timeout: 3 * time.Second}
	for _, agent := range []string{"claude", "codex"} {
		s, err := r.Read(context.Background(), agent)
		if err != nil {
			t.Fatal(agent, err)
		}
		if len(s.Windows) != 2 || !s.Healthy(time.Now()) {
			t.Fatalf("%s: %+v", agent, s)
		}
		if agent == "claude" && (s.Plan != "pro" || s.ExtraUsage == nil || s.ExtraUsage.Enabled) {
			t.Fatalf("%+v", s)
		}
		if agent == "codex" && (s.Plan != "plus" || len(s.ModelLimits) != 1 || s.Credits == nil) {
			t.Fatalf("%+v", s)
		}
		data, _ := json.Marshal(s)
		if strings.Contains(string(data), "PRIVATE") {
			t.Fatal(string(data))
		}
	}
	args, _ := os.ReadFile(claude + ".calls.ndjson")
	if !strings.Contains(string(args), `,"-p","/usage","--output-format","stream-json","--verbose","--no-session-persistence"]`) {
		t.Fatal(string(args))
	}
	cwd, _ := os.ReadFile(claude + ".cwd")
	if string(cwd) == "" || string(cwd) == filepath.Dir(claude) {
		t.Fatal("missing isolated cwd")
	}
	input, _ := os.ReadFile(codex + ".stdin")
	var methods []string
	for _, line := range strings.Split(strings.TrimSpace(string(input)), "\n") {
		var msg struct {
			Method string `json:"method"`
			Params struct {
				ClientInfo struct{ Name, Version string } `json:"clientInfo"`
			} `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatal(err)
		}
		methods = append(methods, msg.Method)
		if msg.Method == "initialize" && (msg.Params.ClientInfo.Name != "duolog" || msg.Params.ClientInfo.Version != "test") {
			t.Fatal(line)
		}
	}
	if strings.Join(methods, ",") != codexReadMethods {
		t.Fatal(string(input))
	}
}

func TestReadMissing(t *testing.T) {
	isolate(t)
	t.Setenv("PATH", t.TempDir())
	_, err := (Reader{}).Read(context.Background(), "claude")
	if err == nil || err.Error() != "not installed" {
		t.Fatal(err)
	}
}

func TestReadTimeout(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		t.Run(agent, func(t *testing.T) {
			isolate(t)
			scenario := testbin.Scenario{SleepMS: 60000}
			if agent == "codex" {
				scenario = testbin.Scenario{RPCResponses: map[string]string{"initialize": `{"id":1,"result":{}}`, "account/rateLimits/read": `{"id":3,"result":{}}`}}
			}
			testbin.Install(t, agent, scenario)
			start := time.Now()
			_, err := (Reader{Timeout: 100 * time.Millisecond}).Read(context.Background(), agent)
			if err == nil || !strings.Contains(err.Error(), "timed out") {
				t.Fatal(err)
			}
			// The fake hangs for a minute; 15 s past a 100 ms limit leaves a
			// slow runner room and still proves the read was bounded.
			if time.Since(start) > 15*time.Second {
				t.Fatal("unbounded read")
			}
		})
	}
}

func TestUntrustedReplies(t *testing.T) {
	for _, tc := range []struct{ name, agent, reply string }{
		{"no report", "claude", `{"result":"PRIVATE"}`},
		{"rpc error", "codex", `{"id":2,"error":{"message":"PRIVATE"}}`},
		{"no limits", "codex", `{"id":2,"result":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolate(t)
			s := testbin.Scenario{Stdout: tc.reply}
			if tc.agent == "codex" {
				s = testbin.Scenario{RPCResponses: codexRoute(map[string]string{"initialize": `{"id":1,"result":{}}`, "account/rateLimits/read": tc.reply,
					"account/read": codexAccountReply})}
			}
			testbin.Install(t, tc.agent, s)
			_, err := (Reader{Timeout: time.Second}).Read(context.Background(), tc.agent)
			if err == nil || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal(err)
			}
		})
	}
}

func TestHealthy(t *testing.T) {
	now := time.Now()
	good := Window{Kind: "session", UsedPercent: 10, ResetsAt: now.Add(time.Hour)}
	for _, tc := range []struct {
		name     string
		snapshot Snapshot
		want     bool
	}{
		{"healthy", Snapshot{Windows: []Window{good}}, true},
		{"empty", Snapshot{}, false},
		{"full", Snapshot{Windows: []Window{{UsedPercent: 100, ResetsAt: good.ResetsAt}}}, false},
		{"expired", Snapshot{Windows: []Window{{UsedPercent: 10, ResetsAt: now.Add(-time.Hour)}}}, false},
		{"rejected", Snapshot{Windows: []Window{good}, Rejected: true}, false},
		{"model full", Snapshot{Windows: []Window{good}, ModelLimits: map[string][]Window{"sonnet": {{UsedPercent: 100, ResetsAt: good.ResetsAt}}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.snapshot.Healthy(now); got != tc.want {
				t.Fatal(got)
			}
		})
	}
}

func TestCodexWindowAndRejection(t *testing.T) {
	for _, flags := range []string{`"ordinaryUsageAllowed":false`, `"spendControlReached":true`, `"rateLimitReachedType":"weekly"`} {
		data := `{"rateLimits":{"primary":{"usedPercent":10,"windowDurationMins":60,"resetsAt":4070908800},` + flags + `}}`
		s, err := parseCodex([]byte(data))
		if err != nil {
			t.Fatal(err)
		}
		if s.Windows[0].Kind != "60m" || s.Windows[0].DurationMinutes != 60 || s.Healthy(time.Now()) || !s.Rejected {
			t.Fatalf("%+v", s)
		}
	}
}

func TestCodexDuplicateLimits(t *testing.T) {
	data, err := os.ReadFile("testdata/codex-duplicate-limits.json")
	if err != nil {
		t.Fatal(err)
	}
	s, err := parseCodex(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.ModelLimits) != 1 || len(s.ModelLimits["sol"]) != 1 {
		t.Fatalf("duplicate limits retained: %+v", s.ModelLimits)
	}
	// A duplicate may still carry a rejection that prevents mark clearing.
	data = []byte(strings.Replace(string(data), `"codex": {`, `"codex": {"ordinaryUsageAllowed": false,`, 1))
	s, err = parseCodex(data)
	if err != nil || !s.Rejected {
		t.Fatalf("rejection lost: %+v %v", s, err)
	}
}

func TestCodexLimitIDsAreSorted(t *testing.T) {
	window := func(percent string) string {
		return `{"primary":{"usedPercent":` + percent + `,"windowDurationMins":60,"resetsAt":4070908800}}`
	}
	data := []byte(`{"rateLimits":{"primary":{"usedPercent":1,"windowDurationMins":300,"resetsAt":4070908800}},"rateLimitsByLimitId":{` +
		`"h":` + window("8") + `,"c":` + window("3") + `,"f":` + window("6") + `,"a":` + window("1.5") + `,` +
		`"d":` + window("4") + `,"b":` + window("2") + `,"g":` + window("7") + `,"e":` + window("5") + `}}`)
	want := []float64{1.5, 2, 3, 4, 5, 6, 7, 8}
	for i := 0; i < 20; i++ {
		s, err := parseCodex(data)
		if err != nil {
			t.Fatal(err)
		}
		var got []float64
		for _, w := range s.ModelLimits["other"] {
			got = append(got, w.UsedPercent)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("unstable order: %v", got)
		}
	}
}

// codexIndividualReply returns the result of the anonymised Codex 0.166
// rate limit read of an enterprise seat: no primary or secondary window, a
// per-person individualLimit and ordinaryUsageAllowed null.
func codexIndividualReply(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile("testdata/codex-0.166-enterprise-individual/rate-limits-read.json")
	if err != nil {
		t.Fatal(err)
	}
	var msg struct {
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatal(err)
	}
	return msg.Result
}

// setIndividual replaces the individualLimit of the account limit and of its
// duplicate under "codex"; nil removes it.
func setIndividual(reply map[string]any, key string, value map[string]any) {
	for _, limit := range []map[string]any{reply["rateLimits"].(map[string]any), reply["rateLimitsByLimitId"].(map[string]any)["codex"].(map[string]any)} {
		delete(limit, "individualLimit")
		if value != nil {
			limit[key] = value
		}
	}
}

// setSeatPlan sets the plan name of the account limit and of its duplicate.
func setSeatPlan(reply map[string]any, plan string) {
	for _, limit := range []map[string]any{reply["rateLimits"].(map[string]any), reply["rateLimitsByLimitId"].(map[string]any)["codex"].(map[string]any)} {
		limit["planType"] = plan
	}
}

// Only one shape turns an unknown permission into a grant. Everything else
// stays unknown, which counts as paid.
func TestCodexSeatGrantNeedsTheFullShape(t *testing.T) {
	const reset = 1794441600
	amounts := func(limit map[string]any) func(map[string]any) {
		return func(r map[string]any) { setIndividual(r, "individualLimit", limit) }
	}
	plan := func(name string) func(map[string]any) {
		return func(r map[string]any) { setSeatPlan(r, name) }
	}
	for name, change := range map[string]func(map[string]any){
		"permission key absent":   func(r map[string]any) { delete(r, "ordinaryUsageAllowed") },
		"permission wrong type":   func(r map[string]any) { r["ordinaryUsageAllowed"] = "yes" },
		"no amount":               amounts(map[string]any{"remainingPercent": 100, "resetsAt": reset}),
		"limit zero":              amounts(map[string]any{"limit": "0", "used": "5", "remainingPercent": 50, "resetsAt": reset}),
		"limit unlimited":         amounts(map[string]any{"limit": "unlimited", "used": "5", "remainingPercent": 50, "resetsAt": reset}),
		"limit negative share":    amounts(map[string]any{"limit": "-0", "used": "5", "remainingPercent": 50, "resetsAt": reset}),
		"used not numeric":        amounts(map[string]any{"limit": "400", "used": "n/a", "remainingPercent": 89, "resetsAt": reset}),
		"no used":                 amounts(map[string]any{"limit": "400", "remainingPercent": 89, "resetsAt": reset}),
		"plus":                    plan("plus"),
		"plus in capitals":        plan("PLUS"),
		"pro":                     plan("pro"),
		"free":                    plan("free"),
		"max":                     plan("max"),
		"ultra":                   plan("ultra"),
		"go":                      plan("go"),
		"usage based name":        plan("enterprise_cbp_usage_based"),
		"usage-based name":        plan("usage-based"),
		"pay as you go name":      plan("pay_as_you_go"),
		"pay-as-you-go in a name": plan("team-pay-as-you-go"),
		"empty plan":              plan(""),
		"blank plan":              plan("  "),
		"no plan": func(r map[string]any) {
			delete(r["rateLimits"].(map[string]any), "planType")
			delete(r["rateLimitsByLimitId"].(map[string]any)["codex"].(map[string]any), "planType")
		},
	} {
		t.Run(name, func(t *testing.T) {
			reply := codexIndividualReply(t)
			change(reply)
			s, err := parseCodex(codexJSON(t, reply))
			if err != nil {
				t.Fatal(err)
			}
			if got := s.Billing.OrdinaryUsageAllowed; got != nil {
				t.Fatalf("granted ordinary usage: %v", *got)
			}
		})
	}
	for _, name := range []string{"ent26", "enterprise", "business", "team", "edu", "newseat27"} {
		t.Run("seat plan "+name, func(t *testing.T) {
			reply := codexIndividualReply(t)
			setSeatPlan(reply, name)
			s, err := parseCodex(codexJSON(t, reply))
			if err != nil {
				t.Fatal(err)
			}
			if got := s.Billing.OrdinaryUsageAllowed; got == nil || !*got {
				t.Fatalf("no grant for plan %q: %v", name, got)
			}
		})
	}
}

// A limit of one model names no plan in Codex's replies. It says nothing about
// the seat, so it takes no grant away; one that names a person's plan does.
func TestCodexScopedLimitWithoutAPlanKeepsTheGrant(t *testing.T) {
	scoped := func(fields map[string]any) map[string]any {
		reply := codexIndividualReply(t)
		reply["rateLimitsByLimitId"].(map[string]any)["sol"] = fields
		return reply
	}
	s, err := parseCodex(codexJSON(t, scoped(map[string]any{"limitId": "sol"})))
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Billing.OrdinaryUsageAllowed; got == nil || !*got {
		t.Fatalf("no grant: %v", got)
	}
	s, err = parseCodex(codexJSON(t, scoped(map[string]any{"limitId": "sol", "planType": "plus"})))
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Billing.OrdinaryUsageAllowed; got != nil {
		t.Fatalf("granted beside a personal plan: %v", *got)
	}
}

// An open individual limit is workspace evidence, so an unknown plan name
// cannot read as a personal account. A known personal plan is not a seat.
func TestCodexIndividualLimitIsWorkspaceEvidence(t *testing.T) {
	reply := codexIndividualReply(t)
	s, err := parseCodex(codexJSON(t, reply))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(s.Billing.WorkspaceReasons, "workspace_plan") {
		t.Fatalf("no workspace evidence: %v", s.Billing.WorkspaceReasons)
	}
	reply = codexIndividualReply(t)
	setSeatPlan(reply, "plus")
	if s, err = parseCodex(codexJSON(t, reply)); err != nil || slices.Contains(s.Billing.WorkspaceReasons, "workspace_plan") {
		t.Fatalf("a personal plan counted as a workspace: %v %v", s.Billing.WorkspaceReasons, err)
	}
	reply = codexIndividualReply(t)
	setIndividual(reply, "individualLimit", map[string]any{"limit": "400", "used": "400", "remainingPercent": 0, "resetsAt": 1794441600})
	if s, err = parseCodex(codexJSON(t, reply)); err != nil || slices.Contains(s.Billing.WorkspaceReasons, "workspace_plan") {
		t.Fatalf("a used up limit counted as evidence: %v %v", s.Billing.WorkspaceReasons, err)
	}
}

func TestCodexSeatFalseDuplicateWinsWithOtherNumbers(t *testing.T) {
	reply := codexIndividualReply(t)
	duplicate := reply["rateLimitsByLimitId"].(map[string]any)["codex"].(map[string]any)
	duplicate["ordinaryUsageAllowed"] = false
	duplicate["individualLimit"] = map[string]any{"limit": "400", "used": "90", "remainingPercent": 75, "resetsAt": 1794441600}
	s, err := parseCodex(codexJSON(t, reply))
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Billing.OrdinaryUsageAllowed; got == nil || *got || !s.Rejected {
		t.Fatalf("ordinary usage %v, rejected %t", got, s.Rejected)
	}
}

// A bad individual limit is left out beside another window, and fails the
// read only when there is no other window.
func TestCodexBadIndividualLimitBesideAWindow(t *testing.T) {
	primary := map[string]any{"usedPercent": 10, "windowDurationMins": 300, "resetsAt": 1794441600}
	bad := map[string]any{"limit": "-1", "used": "5"}
	t.Run("beside a primary window", func(t *testing.T) {
		reply := codexIndividualReply(t)
		setIndividual(reply, "individualLimit", bad)
		reply["rateLimits"].(map[string]any)["primary"] = primary
		reply["rateLimitsByLimitId"].(map[string]any)["codex"].(map[string]any)["primary"] = primary
		s, err := parseCodex(codexJSON(t, reply))
		if err != nil {
			t.Fatal(err)
		}
		if len(s.Windows) != 1 || s.Windows[0].Kind != "session" || s.Billing.OrdinaryUsageAllowed != nil {
			t.Fatalf("%+v", s)
		}
	})
	t.Run("only in a duplicate", func(t *testing.T) {
		reply := codexIndividualReply(t)
		limit := reply["rateLimits"].(map[string]any)
		delete(limit, "individualLimit")
		limit["primary"] = primary
		reply["rateLimitsByLimitId"].(map[string]any)["codex"].(map[string]any)["individualLimit"] = bad
		s, err := parseCodex(codexJSON(t, reply))
		if err != nil {
			t.Fatal(err)
		}
		if len(s.Windows) != 1 || s.Windows[0].Kind != "session" {
			t.Fatalf("%+v", s)
		}
	})
	t.Run("alone", func(t *testing.T) {
		reply := codexIndividualReply(t)
		setIndividual(reply, "individualLimit", bad)
		if s, err := parseCodex(codexJSON(t, reply)); err == nil {
			t.Fatalf("accepted %+v", s.Windows)
		}
	})
}

func TestCodexIndividualLimit(t *testing.T) {
	reset := time.Unix(1794441600, 0).UTC()
	for _, tc := range []struct {
		name  string
		key   string
		limit map[string]any
		used  float64
		open  bool
	}{
		{"live reply", "individualLimit", nil, 11.48, true},
		{"snake case and numbers", "individual_limit", map[string]any{"limit": 400, "used": 100, "remaining_percent": 75, "resets_at": 1794441600}, 25, true},
		{"remaining only", "individualLimit", map[string]any{"remainingPercent": 89, "resetsAt": 1794441600}, 11, false},
		{"zero limit uses remaining", "individualLimit", map[string]any{"limit": "0", "used": "0", "remainingPercent": 40, "resetsAt": 1794441600}, 60, false},
		{"unparsable amounts use remaining", "individualLimit", map[string]any{"limit": "n/a", "used": "45", "remainingPercent": 89, "resetsAt": 1794441600}, 11, false},
		{"at the limit", "individualLimit", map[string]any{"limit": "400.00", "used": "400.00", "remainingPercent": 0, "resetsAt": 1794441600}, 100, false},
		{"past the limit", "individualLimit", map[string]any{"limit": "400.00", "used": "412.5", "remainingPercent": 0, "resetsAt": 1794441600}, 100, false},
		{"no remaining share", "individualLimit", map[string]any{"limit": "400.00", "used": "399", "remainingPercent": 0, "resetsAt": 1794441600}, 100, false},
		{"the higher used share wins", "individualLimit", map[string]any{"limit": 100, "used": 1, "remainingPercent": 1, "resetsAt": 1794441600}, 99, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reply := codexIndividualReply(t)
			if tc.limit != nil {
				setIndividual(reply, tc.key, tc.limit)
			}
			s, err := parseCodex(codexJSON(t, reply))
			if err != nil {
				t.Fatal(err)
			}
			if len(s.Windows) != 1 || len(s.ModelLimits) != 0 {
				t.Fatalf("windows: %+v %+v", s.Windows, s.ModelLimits)
			}
			w := s.Windows[0]
			if w.Kind != "individual" || w.DurationMinutes != 0 || w.UsedPercent != tc.used || !w.ResetsAt.Equal(reset) || w.Severity != "" {
				t.Fatalf("window: %+v", w)
			}
			got := s.Billing.OrdinaryUsageAllowed
			if tc.open && (got == nil || !*got) || !tc.open && got != nil {
				t.Fatalf("ordinary usage = %v, want allowed %t", got, tc.open)
			}
			if s.Rejected || s.SpendControlReached {
				t.Fatalf("refused: %+v", s)
			}
			if healthy := s.Healthy(reset.Add(-time.Hour)); healthy != (tc.used < 100) {
				t.Fatalf("healthy = %t at %v%%", healthy, tc.used)
			}
		})
	}
	t.Run("no reset time", func(t *testing.T) {
		reply := codexIndividualReply(t)
		setIndividual(reply, "individualLimit", map[string]any{"limit": "400", "used": "40", "resetsAt": nil})
		s, err := parseCodex(codexJSON(t, reply))
		if err != nil {
			t.Fatal(err)
		}
		if len(s.Windows) != 1 || !s.Windows[0].ResetsAt.IsZero() || s.Billing.OrdinaryUsageAllowed != nil {
			t.Fatalf("a limit without a reset granted usage: %+v", s)
		}
	})
	t.Run("invalid", func(t *testing.T) {
		for name, limit := range map[string]map[string]any{
			"negative used":      {"limit": "400", "used": "-1", "remainingPercent": 89, "resetsAt": 1794441600},
			"negative limit":     {"limit": "-400", "used": "1", "remainingPercent": 89, "resetsAt": 1794441600},
			"remaining over 100": {"remainingPercent": 120, "resetsAt": 1794441600},
			"remaining below 0":  {"limit": "400", "used": "40", "remainingPercent": -1, "resetsAt": 1794441600},
			"no usable value":    {"limit": "lots", "resetsAt": 1794441600},
			"empty":              {},
		} {
			reply := codexIndividualReply(t)
			setIndividual(reply, "individualLimit", limit)
			if s, err := parseCodex(codexJSON(t, reply)); err == nil {
				t.Errorf("%s: accepted %+v", name, s.Windows)
			}
		}
	})
	t.Run("primary and secondary unchanged", func(t *testing.T) {
		var reply map[string]any
		if err := json.Unmarshal([]byte(codexReply), &reply); err != nil {
			t.Fatal(err)
		}
		before, err := parseCodex(codexJSON(t, reply["result"]))
		if err != nil {
			t.Fatal(err)
		}
		limit := reply["result"].(map[string]any)["rateLimits"].(map[string]any)
		limit["individualLimit"] = map[string]any{"limit": "400.00", "used": "100", "remainingPercent": 75, "resetsAt": 1794441600}
		after, err := parseCodex(codexJSON(t, reply["result"]))
		if err != nil {
			t.Fatal(err)
		}
		if len(after.Windows) != 3 || !slices.Equal(after.Windows[:2], before.Windows) || after.Windows[2].Kind != "individual" || after.Windows[2].UsedPercent != 25 {
			t.Fatalf("windows: %+v, before %+v", after.Windows, before.Windows)
		}
	})
	t.Run("null individual limit", func(t *testing.T) {
		reply := codexIndividualReply(t)
		for _, limit := range []map[string]any{reply["rateLimits"].(map[string]any), reply["rateLimitsByLimitId"].(map[string]any)["codex"].(map[string]any)} {
			limit["individualLimit"] = nil
		}
		s, err := parseCodex(codexJSON(t, reply))
		if err == nil || err.Error() != "no quota windows" || s.Billing.OrdinaryUsageAllowed != nil {
			t.Fatalf("%+v %v", s, err)
		}
	})
}
