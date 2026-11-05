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
		"-p":                 {Stdout: "{\"type\":\"system\"}\n" + claudeReply + "\n"},
		"auth status --json": {Stdout: `{"loggedIn":true,"subscriptionType":"pro","email":"PRIVATE"}`},
	}})
	codex := testbin.Install(t, "codex", testbin.Scenario{SleepMS: 10000, RPCResponses: map[string]string{
		"initialize": `{"id":1,"result":{}}`, "account/rateLimits/read": codexReply,
	}})
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
	if !strings.Contains(string(args), `["-p","/usage","--output-format","stream-json","--verbose","--no-session-persistence"]`) {
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
	if strings.Join(methods, ",") != "initialize,initialized,account/rateLimits/read" {
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
				s = testbin.Scenario{RPCResponses: map[string]string{"initialize": `{"id":1,"result":{}}`, "account/rateLimits/read": tc.reply}}
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
