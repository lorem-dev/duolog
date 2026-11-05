package quota

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/testbin"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// claudeBilling returns one compact case of testdata/claude-billing.json.
func claudeBilling(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile("testdata/claude-billing.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases map[string]json.RawMessage
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	raw, ok := cases[name]
	if !ok {
		t.Fatalf("missing case %q", name)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		t.Fatal(err)
	}
	return compact.String()
}

// readClaudeBilling reads quota through a fake claude with these replies.
func readClaudeBilling(t *testing.T, usage string, auth testbin.Scenario) (Snapshot, error) {
	t.Helper()
	isolate(t)
	testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{
		freeClaude("-p"):                 {Stdout: "{\"type\":\"system\"}\n" + usage + "\n"},
		freeClaude("auth status --json"): auth,
	}})
	return (Reader{Timeout: 3 * time.Second}).Read(context.Background(), "claude")
}

func TestClaudeBillingBothUsageLayouts(t *testing.T) {
	limits, err := parseClaudeUsage([]byte(claudeBilling(t, "limits")))
	if err != nil {
		t.Fatal(err)
	}
	sdk, err := parseClaudeUsage([]byte(claudeBilling(t, "sdk")))
	if err != nil {
		t.Fatal(err)
	}
	if len(limits.Windows) != 6 || !reflect.DeepEqual(limits.Windows, sdk.Windows) {
		t.Fatalf("layouts differ:\nlimits %+v\nsdk    %+v", limits.Windows, sdk.Windows)
	}
	scoped := Window{Kind: "weekly_scoped", Group: "weekly", Model: "fable", DurationMinutes: 10080, UsedPercent: 0, ResetsAt: time.Date(2099, 1, 8, 0, 0, 0, 0, time.UTC), Severity: "unknown"}
	if !slices.Contains(sdk.Windows, scoped) {
		t.Fatalf("scoped window lost: %+v", sdk.Windows)
	}
	session := Window{Kind: "other", Group: "session", Model: "fable", DurationMinutes: 300, UsedPercent: 3, ResetsAt: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC), Severity: "unknown"}
	if !slices.Contains(sdk.Windows, session) {
		t.Fatalf("scoped session window lost: %+v", sdk.Windows)
	}
	for _, s := range []Snapshot{limits, sdk} {
		if s.ExtraUsage == nil || !s.ExtraUsage.Enabled || s.ExtraUsage.UsedCredits != 2.5 || s.ExtraUsage.MonthlyLimit == nil || *s.ExtraUsage.MonthlyLimit != 20 || !s.Billing.OverageEnabled {
			t.Fatalf("extra usage lost: %+v %+v", s.ExtraUsage, s.Billing)
		}
	}
	if limits.Billing.WindowsAvailable != nil {
		t.Fatalf("limits layout invented rate_limits_available: %v", *limits.Billing.WindowsAvailable)
	}
	if sdk.Billing.WindowsAvailable == nil || !*sdk.Billing.WindowsAvailable {
		t.Fatalf("rate_limits_available true lost: %+v", sdk.Billing)
	}

	if _, err := parseClaudeUsage([]byte(claudeBilling(t, "sdk_over_100"))); err == nil {
		t.Fatal("utilization above 100 accepted")
	}
	if _, err := parseClaudeUsage([]byte(`{"type":"system"}`)); err == nil {
		t.Fatal("event without a usage report accepted")
	}

	unavailable, err := parseClaudeUsage([]byte(claudeBilling(t, "unavailable")))
	if err != nil {
		t.Fatal(err)
	}
	if len(unavailable.Windows) != 0 || unavailable.Billing.WindowsAvailable == nil || *unavailable.Billing.WindowsAvailable {
		t.Fatalf("rate_limits_available false lost: %+v", unavailable)
	}
	for _, name := range []string{"empty", "unknown_shape"} {
		s, err := parseClaudeUsage([]byte(claudeBilling(t, name)))
		if err != nil {
			t.Fatal(name, err)
		}
		if len(s.Windows) != 0 || s.Billing.WindowsAvailable != nil || s.Billing.OverageEnabled || s.ExtraUsage != nil {
			t.Fatalf("%s: unknown shape became evidence: %+v", name, s)
		}
	}

	// The read keeps false rate_limits_available and the route evidence of
	// auth status, although it returns no quota.
	s, err := readClaudeBilling(t, claudeBilling(t, "unavailable"), testbin.Scenario{Stdout: `{"loggedIn":true,"authMethod":"api_key"}`})
	if err == nil {
		t.Fatalf("windowless read succeeded: %+v", s)
	}
	if s.Billing.WindowsAvailable == nil || *s.Billing.WindowsAvailable || s.Billing.AuthMethod != "api_key" || !s.Billing.RouteKnown {
		t.Fatalf("evidence lost on windowless read: %+v", s.Billing)
	}
	if !slices.Equal(s.Billing.RouteSignals, []workerenv.Signal{{Name: "auth:api_key", Layer: "auth status"}}) {
		t.Fatalf("signals: %+v", s.Billing.RouteSignals)
	}

	for _, tc := range []struct {
		name     string
		auth     testbin.Scenario
		method   string
		provider string
		known    bool
		signals  []workerenv.Signal
	}{
		{"subscription", testbin.Scenario{Stdout: `{"loggedIn":true,"authMethod":"claude.ai","subscriptionType":"max"}`}, "claude.ai", "", true, nil},
		{"oauth token", testbin.Scenario{Stdout: `{"loggedIn":true,"authMethod":"oauth_token"}`}, "oauth_token", "", true, nil},
		{"first party", testbin.Scenario{Stdout: `{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty"}`}, "claude.ai", "firstParty", true, nil},
		{"cloud provider", testbin.Scenario{Stdout: `{"loggedIn":true,"authMethod":"third_party","apiProvider":"bedrock"}`}, "third_party", "bedrock", true, []workerenv.Signal{{Name: "auth:third_party", Layer: "auth status"}, {Name: "auth:apiProvider", Layer: "auth status"}}},
		{"api key helper", testbin.Scenario{Stdout: `{"loggedIn":true,"authMethod":"api_key_helper"}`}, "api_key_helper", "", true, []workerenv.Signal{{Name: "auth:api_key_helper", Layer: "auth status"}}},
		{"no method", testbin.Scenario{Stdout: `{"loggedIn":true}`}, "", "", false, []workerenv.Signal{{Name: "auth:unknown", Layer: "auth status"}}},
		{"new method", testbin.Scenario{Stdout: `{"loggedIn":true,"authMethod":"passkey"}`}, "unknown", "", false, []workerenv.Signal{{Name: "auth:unknown", Layer: "auth status"}}},
		{"new provider", testbin.Scenario{Stdout: `{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"orbit"}`}, "claude.ai", "unknown", false, []workerenv.Signal{{Name: "auth:apiProvider", Layer: "auth status"}}},
		{"wrong type", testbin.Scenario{Stdout: `{"loggedIn":true,"authMethod":7}`}, "", "", false, []workerenv.Signal{{Name: "auth:unread", Layer: "auth status"}}},
		{"failed exit", testbin.Scenario{Stdout: `{"loggedIn":true,"authMethod":"claude.ai"}`, Exit: 2}, "", "", false, []workerenv.Signal{{Name: "auth:unread", Layer: "auth status"}}},
		{"garbage", testbin.Scenario{Stdout: "not json"}, "", "", false, []workerenv.Signal{{Name: "auth:unread", Layer: "auth status"}}},
		{"timeout", testbin.Scenario{SleepMS: 10000}, "", "", false, []workerenv.Signal{{Name: "auth:unread", Layer: "auth status"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := readClaudeBilling(t, claudeBilling(t, "sdk"), tc.auth)
			if err != nil {
				t.Fatal(err)
			}
			if len(s.Windows) != 6 {
				t.Fatalf("windows lost: %+v", s)
			}
			b := s.Billing
			if b.AuthMethod != tc.method || b.APIProvider != tc.provider || b.RouteKnown != tc.known || !slices.Equal(b.RouteSignals, tc.signals) {
				t.Fatalf("%+v", b)
			}
		})
	}
}

func TestClaudeBillingDiscardsIdentityAndValues(t *testing.T) {
	check := func(t *testing.T, s Snapshot, err error) {
		t.Helper()
		data, marshalErr := json.Marshal(s)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		for _, text := range []string{string(data), errorText(err)} {
			if strings.Contains(text, "RAW-") {
				t.Fatalf("raw marker kept: %s", text)
			}
		}
		for _, reason := range s.Billing.WorkspaceReasons {
			if !slices.Contains([]string{"org", "seat_tier", "group", "member"}, reason) {
				t.Fatalf("workspace reason is not a category: %q", reason)
			}
		}
	}

	s, err := readClaudeBilling(t, claudeBilling(t, "identity_usage"), testbin.Scenario{Stdout: claudeBilling(t, "identity_auth")})
	if err != nil {
		t.Fatal(err)
	}
	check(t, s, err)
	if !slices.Equal(s.Billing.WorkspaceReasons, []string{"member"}) || s.Billing.AuthMethod != "claude.ai" || s.Billing.APIProvider != "firstParty" || !s.Billing.RouteKnown || s.Plan != "" {
		t.Fatalf("%+v %+v", s.Plan, s.Billing)
	}

	identity := claudeBilling(t, "identity_auth")
	for _, tc := range []struct {
		name, usage, auth string
		fails             bool
	}{
		{"raw method", claudeBilling(t, "sdk"), strings.Replace(identity, `"claude.ai"`, `"RAW-METHOD-MARKER"`, 1), false},
		{"raw provider", claudeBilling(t, "sdk"), strings.Replace(identity, `"firstParty"`, `"RAW-PROVIDER-MARKER"`, 1), false},
		{"raw reason", strings.Replace(claudeBilling(t, "identity_usage"), `"member_zero_credit_limit"`, `"RAW-REASON-MARKER"`, 1), identity, false},
		{"raw percent", strings.Replace(claudeBilling(t, "identity_usage"), `"utilization":1`, `"utilization":"RAW-VALUE-MARKER"`, 1), identity, true},
		{"logged out", claudeBilling(t, "sdk"), strings.Replace(identity, `"loggedIn":true`, `"loggedIn":false`, 1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := readClaudeBilling(t, tc.usage, testbin.Scenario{Stdout: tc.auth})
			if (err != nil) != tc.fails {
				t.Fatalf("error %v, want failure %v", err, tc.fails)
			}
			check(t, s, err)
			if tc.name == "raw reason" && len(s.Billing.WorkspaceReasons) != 0 {
				t.Fatalf("unknown reason kept: %+v", s.Billing.WorkspaceReasons)
			}
		})
	}

	for _, name := range []string{"identity_usage", "limits", "sdk"} {
		s, err := parseClaudeUsage([]byte(claudeBilling(t, name)))
		check(t, s, err)
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
