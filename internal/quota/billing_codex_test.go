package quota

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/testbin"
)

// codexBillingFixture holds the replies of testdata/codex-billing.json as
// plain maps, so that each case can change one property.
type codexBillingFixture struct {
	Account    map[string]any `json:"account"`
	Config     map[string]any `json:"config"`
	Auth       map[string]any `json:"auth"`
	RateLimits map[string]any `json:"rateLimits"`
}

func loadCodexBilling(t *testing.T) codexBillingFixture {
	t.Helper()
	data, err := os.ReadFile("testdata/codex-billing.json")
	if err != nil {
		t.Fatal(err)
	}
	var f codexBillingFixture
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f codexBillingFixture) account() map[string]any {
	return f.Account["account"].(map[string]any)
}

func (f codexBillingFixture) limit() map[string]any {
	return f.RateLimits["rateLimits"].(map[string]any)
}

func (f codexBillingFixture) byID(id string) map[string]any {
	return f.RateLimits["rateLimitsByLimitId"].(map[string]any)[id].(map[string]any)
}

func (f codexBillingFixture) config() map[string]any {
	return f.Config["config"].(map[string]any)
}

// provider returns the model_providers entry of id, made empty when the
// config has none.
func (f codexBillingFixture) provider(id string) map[string]any {
	providers := f.config()["model_providers"].(map[string]any)
	if providers[id] == nil {
		providers[id] = map[string]any{}
	}
	return providers[id].(map[string]any)
}

func codexJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// codexMatrixFacts joins the four reads as readCodex does; unread names a
// read that failed.
func codexMatrixFacts(t *testing.T, f codexBillingFixture, unread string) BillingFacts {
	t.Helper()
	account := unreadFacts("account:unread", codexAccountLayer)
	config := unreadFacts("provider:unread", codexConfigLayer)
	auth := unreadFacts("auth:unread", codexAuthLayer)
	if unread != "account" {
		account, _ = parseCodexAccount(codexJSON(t, f.Account))
	}
	if unread != "config" {
		config, _ = parseCodexConfig(codexJSON(t, f.Config))
	}
	if unread != "auth" {
		auth, _ = parseCodexAuth(codexJSON(t, f.Auth))
	}
	s, err := parseCodex(codexJSON(t, f.RateLimits))
	if err != nil {
		t.Fatal(err)
	}
	return codexBilling(s.Billing, account, config, auth)
}

func signalNames(b BillingFacts) []string {
	var names []string
	for _, s := range b.RouteSignals {
		names = append(names, s.Name)
	}
	return names
}

func flag(v bool) *bool { return &v }

func TestCodexBillingRPCSequence(t *testing.T) {
	f := loadCodexBilling(t)
	reply := func(id int, result any) string {
		return string(codexJSON(t, map[string]any{"jsonrpc": "2.0", "id": id, "result": result}))
	}
	blocked := loadCodexBilling(t)
	blocked.limit()["ordinaryUsageAllowed"] = false
	// Notifications and server requests arrive between the replies. A
	// sparse update says usage is allowed again; only the read's own reply
	// counts, so it cannot clear the block.
	update := `{"jsonrpc":"2.0","method":"account/rateLimits/updated","params":{"rateLimits":{"ordinaryUsageAllowed":true,"rateLimitReachedType":null}}}`
	request := func(id int) string {
		return `{"jsonrpc":"2.0","id":` + string(codexJSON(t, id)) + `,"method":"item/tool/requestUserInput","params":{}}`
	}
	responses := func() map[string]string {
		return map[string]string{
			"initialize":              `{"id":1,"result":{}}`,
			"account/read":            update + "\n" + request(3) + "\n" + reply(3, f.Account),
			"account/rateLimits/read": `{"id":99,"result":{}}` + "\n" + update + "\n" + request(2) + "\n" + reply(2, blocked.RateLimits),
			"config/read":             `{"jsonrpc":"2.0","method":"account/updated","params":{"authMode":"apiKey"}}` + "\n" + reply(4, f.Config),
			"getAuthStatus":           request(5) + "\n" + reply(5, f.Auth),
		}
	}
	read := func(t *testing.T, rpc map[string]string) (Snapshot, error, string) {
		t.Helper()
		isolate(t)
		codex := testbin.Install(t, "codex", testbin.Scenario{RPCResponses: rpc})
		s, err := (Reader{Version: "test", Timeout: 3 * time.Second}).Read(context.Background(), "codex")
		input, _ := os.ReadFile(codex + ".stdin")
		return s, err, string(input)
	}
	checkRequests := func(t *testing.T, input string) {
		t.Helper()
		var methods []string
		ids := map[int]bool{}
		for _, line := range strings.Split(strings.TrimSpace(input), "\n") {
			var msg struct {
				ID     *int           `json:"id"`
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
			}
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				t.Fatal(err)
			}
			methods = append(methods, msg.Method)
			// The auth status read never asks for the token.
			if msg.Method == "getAuthStatus" && (msg.Params["includeToken"] != false || msg.Params["refreshToken"] != false) {
				t.Fatal("auth status asks for a token:", line)
			}
			if msg.Method == "initialized" {
				if msg.ID != nil {
					t.Fatal("notification with id:", line)
				}
				continue
			}
			if msg.ID == nil || ids[*msg.ID] {
				t.Fatal("request id missing or reused:", line)
			}
			ids[*msg.ID] = true
		}
		want := []string{"initialize", "initialized", "account/read", "account/rateLimits/read", "config/read", "getAuthStatus"}
		if !slices.Equal(methods, want) {
			t.Fatalf("methods %v, want %v", methods, want)
		}
		lower := strings.ToLower(input)
		for _, banned := range []string{"reset", "turn/", "thread/", "admin", "luna", "reserve"} {
			if strings.Contains(lower, banned) {
				t.Fatalf("request mentions %q: %s", banned, input)
			}
		}
	}

	t.Run("full", func(t *testing.T) {
		s, err, input := read(t, responses())
		if err != nil {
			t.Fatal(err)
		}
		checkRequests(t, input)
		b := s.Billing
		if !b.RouteKnown || len(b.RouteSignals) != 0 || b.AccountType != "chatgpt" || b.AuthMode != "" || b.AuthMethod != "chatgpt" {
			t.Fatalf("route: %+v", b)
		}
		if len(s.Windows) != 2 || !s.Rejected || b.OrdinaryUsageAllowed == nil || *b.OrdinaryUsageAllowed {
			t.Fatalf("block lost: %+v", s)
		}
		if b.ResetCredits == nil || *b.ResetCredits != 2 {
			t.Fatalf("reset credits: %+v", b)
		}
		if data := codexJSON(t, s); strings.Contains(string(data), "PRIVATE") {
			t.Fatal(string(data))
		}
	})
	for method, signal := range map[string]string{"config/read": "provider:unread", "getAuthStatus": "auth:unread"} {
		t.Run(method+" unavailable", func(t *testing.T) {
			rpc := responses()
			id := map[string]int{"config/read": codexConfigID, "getAuthStatus": codexAuthID}[method]
			rpc[method] = `{"id":` + string(codexJSON(t, id)) + `,"error":{"code":-32601,"message":"PRIVATE"}}`
			s, err, input := read(t, rpc)
			if err != nil {
				t.Fatal(err)
			}
			checkRequests(t, input)
			if s.Billing.RouteKnown || !slices.Equal(signalNames(s.Billing), []string{signal}) || len(s.Windows) != 2 {
				t.Fatalf("%+v", s)
			}
		})
	}
	t.Run("rate limits rejected", func(t *testing.T) {
		rpc := responses()
		rpc["account/rateLimits/read"] = `{"id":2,"error":{"message":"PRIVATE"}}`
		s, err, input := read(t, rpc)
		if err == nil || strings.Contains(err.Error(), "PRIVATE") {
			t.Fatal(err)
		}
		checkRequests(t, input)
		if !s.Billing.RouteKnown || s.Billing.AccountType != "chatgpt" || len(s.Windows) != 0 {
			t.Fatalf("route evidence lost: %+v", s)
		}
	})
}

func TestCodexBillingPropertyMatrix(t *testing.T) {
	for _, tc := range []struct {
		name        string
		edit        func(f codexBillingFixture)
		unread      string
		known       bool
		signals     []string
		accountType string
		envKey      string
		// authLayer says the auth: signals come from getAuthStatus.
		authLayer bool
	}{
		{name: "subscription", edit: func(codexBillingFixture) {}, known: true, accountType: "chatgpt"},
		{name: "account unread", edit: func(codexBillingFixture) {}, unread: "account", signals: []string{"account:unread"}},
		{name: "null account", edit: func(f codexBillingFixture) { f.Account["account"] = nil }, known: true, signals: []string{"account:none"}, accountType: "none"},
		{name: "api key", edit: func(f codexBillingFixture) { f.Account["account"] = map[string]any{"type": "apiKey"} }, known: true, signals: []string{"account:apiKey"}, accountType: "apiKey"},
		{name: "bedrock", edit: func(f codexBillingFixture) {
			f.Account["account"] = map[string]any{"type": "amazonBedrock", "usesCodexManagedCredentials": true}
		}, known: true, signals: []string{"account:amazonBedrock"}, accountType: "amazonBedrock"},
		{name: "gcp", edit: func(f codexBillingFixture) { f.Account["account"] = map[string]any{"type": "gcp"} }, known: true, signals: []string{"account:gcp"}, accountType: "gcp"},
		{name: "headers", edit: func(f codexBillingFixture) { f.Account["authMode"] = "headers" }, known: true, signals: []string{"auth:headers"}, accountType: "chatgpt"},
		{name: "agent identity", edit: func(f codexBillingFixture) { f.account()["authMode"] = "agentIdentity" }, known: true, signals: []string{"auth:agentIdentity"}, accountType: "chatgpt"},
		{name: "forced api login", edit: func(f codexBillingFixture) {
			f.Account["account"] = map[string]any{"type": "apiKey"}
			f.Account["authMode"] = "apiKey"
		}, known: true, signals: []string{"account:apiKey", "auth:apiKey"}, accountType: "apiKey"},
		{name: "new account type", edit: func(f codexBillingFixture) { f.account()["type"] = "seat" }, signals: []string{"account:unknown"}, accountType: "unknown"},
		{name: "new auth mode", edit: func(f codexBillingFixture) { f.Account["authMode"] = "deviceToken" }, signals: []string{"auth:unknown"}, accountType: "chatgpt"},
		{name: "provider without openai auth", edit: func(f codexBillingFixture) {
			f.provider("openai")["requires_openai_auth"] = false
			f.Account["account"], f.Account["requiresOpenaiAuth"] = nil, false
		}, known: true, signals: []string{"account:none", "account:requires_openai_auth", "provider:requires_openai_auth"}, accountType: "none"},
		{name: "explicit openai", edit: func(f codexBillingFixture) { f.config()["model_provider"] = "openai" }, known: true, accountType: "chatgpt"},
		{name: "openai entry without route keys", edit: func(f codexBillingFixture) {
			f.provider("openai")["name"] = "OpenAI"
			f.provider("openai")["requires_openai_auth"] = true
		}, known: true, accountType: "chatgpt"},
		{name: "arbitrary env key", edit: func(f codexBillingFixture) { f.provider("openai")["env_key"] = "TEAM_GATEWAY_TOKEN" }, known: true, signals: []string{"provider:env_key"}, accountType: "chatgpt", envKey: "TEAM_GATEWAY_TOKEN"},
		{name: "env key not a name", edit: func(f codexBillingFixture) { f.provider("openai")["env_key"] = "sk-PRIVATE value" }, known: true, signals: []string{"provider:env_key"}, accountType: "chatgpt"},
		{name: "bearer token", edit: func(f codexBillingFixture) { f.provider("openai")["experimental_bearer_token"] = "PRIVATE" }, known: true, signals: []string{"provider:bearer_token"}, accountType: "chatgpt"},
		{name: "provider headers", edit: func(f codexBillingFixture) {
			f.provider("openai")["env_http_headers"] = map[string]any{"X-Key": "PRIVATE_VAR"}
		}, known: true, signals: []string{"provider:headers"}, accountType: "chatgpt"},
		{name: "auth command", edit: func(f codexBillingFixture) {
			f.provider("openai")["auth"] = map[string]any{"command": "PRIVATE"}
		}, known: true, signals: []string{"provider:auth"}, accountType: "chatgpt"},
		{name: "aws", edit: func(f codexBillingFixture) { f.provider("openai")["aws"] = map[string]any{} }, known: true, signals: []string{"provider:aws"}, accountType: "chatgpt"},
		{name: "base url", edit: func(f codexBillingFixture) { f.provider("openai")["base_url"] = "https://proxy.example.com/v1" }, known: true, signals: []string{"provider:base_url"}, accountType: "chatgpt"},
		{name: "custom provider with env key", edit: func(f codexBillingFixture) {
			f.config()["model_provider"] = "gateway"
			f.provider("gateway")["env_key"] = "GATEWAY_KEY"
			f.provider("gateway")["base_url"] = "https://gateway.example.com/v1"
		}, known: true, signals: []string{"provider:base_url", "provider:custom", "provider:env_key"}, accountType: "chatgpt", envKey: "GATEWAY_KEY"},
		{name: "configured local provider", edit: func(f codexBillingFixture) { f.config()["model_provider"] = "local" },
			known: true, signals: []string{"provider:base_url", "provider:custom", "provider:requires_openai_auth"}, accountType: "chatgpt"},
		{name: "built-in other provider", edit: func(f codexBillingFixture) { f.config()["model_provider"] = "oss" }, known: true, signals: []string{"provider:custom"}, accountType: "chatgpt"},
		{name: "profile selects provider", edit: func(f codexBillingFixture) {
			f.config()["profile"] = "work"
			f.config()["profiles"] = map[string]any{"work": map[string]any{"model_provider": "local"}}
		}, known: true, signals: []string{"provider:base_url", "provider:custom", "provider:requires_openai_auth"}, accountType: "chatgpt"},
		{name: "profile keeps provider", edit: func(f codexBillingFixture) {
			f.config()["profile"] = "fast"
			f.config()["profiles"] = map[string]any{"fast": map[string]any{"model": "luna"}}
		}, known: true, accountType: "chatgpt"},
		{name: "profile not listed", edit: func(f codexBillingFixture) { f.config()["profile"] = "elsewhere" }, signals: []string{"provider:unknown"}, accountType: "chatgpt"},
		{name: "provider key missing", edit: func(f codexBillingFixture) { delete(f.config(), "model_provider") }, signals: []string{"provider:unknown"}, accountType: "chatgpt"},
		{name: "providers key missing", edit: func(f codexBillingFixture) { delete(f.config(), "model_providers") }, signals: []string{"provider:unknown"}, accountType: "chatgpt"},
		{name: "profile key missing", edit: func(f codexBillingFixture) { delete(f.config(), "profile") }, signals: []string{"provider:unknown"}, accountType: "chatgpt"},
		{name: "provider of another type", edit: func(f codexBillingFixture) { f.config()["model_provider"] = 7 }, signals: []string{"provider:unknown"}, accountType: "chatgpt"},
		{name: "providers of another type", edit: func(f codexBillingFixture) { f.config()["model_providers"] = []any{} }, signals: []string{"provider:unknown"}, accountType: "chatgpt"},
		{name: "no config", edit: func(f codexBillingFixture) { f.Config["config"] = nil }, signals: []string{"provider:unknown"}, accountType: "chatgpt"},
		{name: "camel case", edit: func(f codexBillingFixture) {
			c := f.config()
			c["modelProvider"], c["modelProviders"] = nil, map[string]any{"openai": map[string]any{"baseUrl": "https://proxy.example.com/v1"}}
			delete(c, "model_provider")
			delete(c, "model_providers")
		}, known: true, signals: []string{"provider:base_url"}, accountType: "chatgpt"},
		{name: "config unread", edit: func(codexBillingFixture) {}, unread: "config", signals: []string{"provider:unread"}, accountType: "chatgpt"},
		{name: "auth unread", edit: func(codexBillingFixture) {}, unread: "auth", signals: []string{"auth:unread"}, accountType: "chatgpt", authLayer: true},
		{name: "api key login", edit: func(f codexBillingFixture) { f.Auth["authMethod"] = "apikey" }, known: true, signals: []string{"auth:apiKey"}, accountType: "chatgpt", authLayer: true},
		{name: "api key login camel", edit: func(f codexBillingFixture) { f.Auth["authMethod"] = "apiKey" }, known: true, signals: []string{"auth:apiKey"}, accountType: "chatgpt", authLayer: true},
		{name: "auth tokens login", edit: func(f codexBillingFixture) { f.Auth["authMethod"] = "chatgptAuthTokens" }, known: true, signals: []string{"auth:chatgptAuthTokens"}, accountType: "chatgpt", authLayer: true},
		{name: "no login", edit: func(f codexBillingFixture) { f.Auth["authMethod"] = nil }, known: true, signals: []string{"auth:none"}, accountType: "chatgpt", authLayer: true},
		{name: "new login method", edit: func(f codexBillingFixture) { f.Auth["authMethod"] = "passkey" }, signals: []string{"auth:unknown"}, accountType: "chatgpt", authLayer: true},
		{name: "login method missing", edit: func(f codexBillingFixture) { delete(f.Auth, "authMethod") }, signals: []string{"auth:unknown"}, accountType: "chatgpt", authLayer: true},
		{name: "snake case login", edit: func(f codexBillingFixture) {
			delete(f.Auth, "authMethod")
			f.Auth["auth_method"] = "apikey"
		}, known: true, signals: []string{"auth:apiKey"}, accountType: "chatgpt", authLayer: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := loadCodexBilling(t)
			tc.edit(f)
			b := codexMatrixFacts(t, f, tc.unread)
			if b.RouteKnown != tc.known || !slices.Equal(signalNames(b), tc.signals) || b.AccountType != tc.accountType || b.ProviderEnvKey != tc.envKey {
				t.Fatalf("got known=%v signals=%v account=%q env=%q", b.RouteKnown, signalNames(b), b.AccountType, b.ProviderEnvKey)
			}
			for _, s := range b.RouteSignals {
				want := codexAccountLayer
				switch {
				case strings.HasPrefix(s.Name, "provider:"):
					want = codexConfigLayer
				case strings.HasPrefix(s.Name, "auth:") && tc.authLayer:
					want = codexAuthLayer
				}
				if s.Layer != want {
					t.Fatalf("signal %+v, want layer %q", s, want)
				}
			}
			if data := codexJSON(t, b); strings.Contains(string(data), "PRIVATE") {
				t.Fatal(string(data))
			}
		})
	}
	t.Run("renames", func(t *testing.T) {
		base := codexMatrixFacts(t, loadCodexBilling(t), "")
		f := loadCodexBilling(t)
		f.provider("local")["name"] = "Renamed"
		f.account()["planType"] = "newplan"
		f.limit()["planType"] = "newplan"
		if got := codexMatrixFacts(t, f, ""); !reflect.DeepEqual(got, base) {
			t.Fatalf("renames changed facts: %+v, want %+v", got, base)
		}
		// A paid provider renamed to the first-party id stays paid.
		f = loadCodexBilling(t)
		providers := f.config()["model_providers"].(map[string]any)
		providers["openai"] = providers["local"]
		delete(providers, "local")
		got := codexMatrixFacts(t, f, "")
		if !got.RouteKnown || !slices.Equal(signalNames(got), []string{"provider:base_url", "provider:requires_openai_auth"}) {
			t.Fatalf("renamed provider: %+v", got)
		}
		// A provider id is never kept.
		f = loadCodexBilling(t)
		f.config()["model_provider"] = "PRIVATE-gateway"
		if data := codexJSON(t, codexMatrixFacts(t, f, "")); strings.Contains(string(data), "PRIVATE") {
			t.Fatal(string(data))
		}
	})
}

func TestCodexNullablePermissionAndSpend(t *testing.T) {
	parse := func(t *testing.T, f codexBillingFixture) Snapshot {
		t.Helper()
		s, err := parseCodex(codexJSON(t, f.RateLimits))
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	t.Run("ordinary usage", func(t *testing.T) {
		for _, key := range []string{"ordinaryUsageAllowed", "ordinary_usage_allowed"} {
			for _, tc := range []struct {
				name     string
				value    any
				set      bool
				want     *bool
				rejected bool
			}{
				{"true", true, true, flag(true), false},
				{"false", false, true, flag(false), true},
				{"null", nil, true, nil, false},
				{"missing", nil, false, nil, false},
				{"wrong type", "yes", true, nil, false},
			} {
				t.Run(key+"/"+tc.name, func(t *testing.T) {
					f := loadCodexBilling(t)
					delete(f.limit(), "ordinaryUsageAllowed")
					if tc.set {
						f.limit()[key] = tc.value
					}
					s := parse(t, f)
					got := s.Billing.OrdinaryUsageAllowed
					if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) || s.Rejected != tc.rejected {
						t.Fatalf("got %v rejected=%v", got, s.Rejected)
					}
				})
			}
		}
		// Spellings that disagree keep the stricter value.
		f := loadCodexBilling(t)
		f.limit()["ordinary_usage_allowed"] = nil
		if got := parse(t, f).Billing.OrdinaryUsageAllowed; got != nil {
			t.Fatalf("null spelling lost: %v", *got)
		}
		// Codex 0.165 sends the flag beside the limits; the stricter of
		// the reply's and the limit's value wins.
		for _, tc := range []struct {
			name       string
			top, limit any
			topSet     bool
			limitSet   bool
			want       *bool
			rejected   bool
		}{
			{"reply true only", true, nil, true, false, flag(true), false},
			{"reply false only", false, nil, true, false, flag(false), true},
			{"reply null only", nil, nil, true, false, nil, false},
			{"reply false, limit true", false, true, true, true, flag(false), true},
			{"reply true, limit false", true, false, true, true, flag(false), true},
			{"reply null, limit true", nil, true, true, true, nil, false},
			{"reply true, limit null", true, nil, true, true, nil, false},
			{"both true", true, true, true, true, flag(true), false},
		} {
			t.Run("reply/"+tc.name, func(t *testing.T) {
				f := loadCodexBilling(t)
				delete(f.limit(), "ordinaryUsageAllowed")
				if tc.topSet {
					f.RateLimits["ordinaryUsageAllowed"] = tc.top
				}
				if tc.limitSet {
					f.limit()["ordinaryUsageAllowed"] = tc.limit
				}
				s := parse(t, f)
				got := s.Billing.OrdinaryUsageAllowed
				if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) || s.Rejected != tc.rejected {
					t.Fatalf("got %v rejected=%v", got, s.Rejected)
				}
			})
		}
	})
	t.Run("spend control", func(t *testing.T) {
		for _, tc := range []struct {
			key              string
			value            any
			present, reached bool
		}{
			{"spendControlReached", false, true, false},
			{"spendControlReached", 0, true, false},
			{"spendControlReached", true, true, true},
			{"spendControlReached", nil, false, false},
			{"spend_control_reached", false, true, false},
			{"spend_control_reached", true, true, true},
			{"spendControl", map[string]any{"limit": 0}, true, false},
			{"spend_control", map[string]any{}, true, false},
			{"", nil, false, false},
		} {
			t.Run(tc.key+"/"+string(codexJSON(t, tc.value)), func(t *testing.T) {
				f := loadCodexBilling(t)
				delete(f.limit(), "spendControlReached")
				if tc.key != "" {
					f.limit()[tc.key] = tc.value
				}
				s := parse(t, f)
				b := s.Billing
				// Only a reached spend control is evidence: a personal
				// account sends spendControlReached false too.
				if b.SpendControlPresent != tc.present || b.OverageEnabled != tc.reached || slices.Contains(b.WorkspaceReasons, "spend_control") != tc.reached {
					t.Fatalf("present: %+v", b)
				}
				if s.SpendControlReached != tc.reached || s.Rejected != tc.reached {
					t.Fatalf("reached: %+v", s)
				}
			})
		}
	})
	t.Run("credits", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			credits any
			overage bool
		}{
			{"none", map[string]any{"hasCredits": false, "unlimited": false}, false},
			{"null", nil, false},
			{"has credits", map[string]any{"hasCredits": true, "unlimited": false}, true},
			{"unlimited", map[string]any{"hasCredits": false, "unlimited": true}, true},
			{"snake case", map[string]any{"has_credits": true}, true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				f := loadCodexBilling(t)
				f.limit()["credits"] = tc.credits
				if got := parse(t, f).Billing.OverageEnabled; got != tc.overage {
					t.Fatal(got)
				}
			})
		}
		// Credits on a per-model limit are the account's credits too.
		f := loadCodexBilling(t)
		f.byID("sol")["credits"] = map[string]any{"unlimited": true}
		if !parse(t, f).Billing.OverageEnabled {
			t.Fatal("scoped credits ignored")
		}
	})
	t.Run("workspace name", func(t *testing.T) {
		for _, tc := range []struct {
			key       string
			value     any
			workspace bool
		}{
			{"workspaceName", "Team PRIVATE", true},
			{"workspace_name", "PRIVATE", true},
			{"workspaceName", nil, false},
			{"workspaceName", "", false},
		} {
			f := loadCodexBilling(t)
			delete(f.account(), "workspaceName")
			f.account()[tc.key] = tc.value
			b, err := parseCodexAccount(codexJSON(t, f.Account))
			if err != nil {
				t.Fatal(err)
			}
			if slices.Equal(b.WorkspaceReasons, []string{"workspace_name"}) != tc.workspace || (!tc.workspace && len(b.WorkspaceReasons) != 0) {
				t.Fatalf("%s=%v: %+v", tc.key, tc.value, b)
			}
			if data := codexJSON(t, b); strings.Contains(string(data), "PRIVATE") {
				t.Fatal(string(data))
			}
			if got := codexBilling(BillingFacts{}, b, BillingFacts{}).WorkspaceReasons; !slices.Equal(got, b.WorkspaceReasons) {
				t.Fatalf("join lost workspace evidence: %v", got)
			}
		}
	})
	t.Run("workspace plan", func(t *testing.T) {
		for _, plan := range []string{"team", "business", "enterprise", "edu", "Enterprise"} {
			f := loadCodexBilling(t)
			f.account()["planType"] = plan
			if b := codexMatrixFacts(t, f, ""); !slices.Equal(b.WorkspaceReasons, []string{"workspace_plan"}) || !b.RouteKnown {
				t.Fatalf("account plan %q: %+v", plan, b)
			}
			f = loadCodexBilling(t)
			f.byID("sol")["planType"] = plan
			if b := parse(t, f).Billing; !slices.Equal(b.WorkspaceReasons, []string{"workspace_plan"}) {
				t.Fatalf("limit plan %q: %+v", plan, b)
			}
		}
		for _, plan := range []string{"free", "plus", "pro", "go", ""} {
			f := loadCodexBilling(t)
			f.account()["planType"], f.limit()["planType"] = plan, plan
			if b := codexMatrixFacts(t, f, ""); len(b.WorkspaceReasons) != 0 {
				t.Fatalf("personal plan %q: %+v", plan, b)
			}
		}
	})
	t.Run("forced workspace", func(t *testing.T) {
		for _, tc := range []struct {
			value     any
			workspace bool
		}{{"00000000-PRIVATE", true}, {nil, false}, {"", false}} {
			f := loadCodexBilling(t)
			f.config()["forced_chatgpt_workspace_id"] = tc.value
			b := codexMatrixFacts(t, f, "")
			if slices.Contains(b.WorkspaceReasons, "forced_workspace") != tc.workspace || !b.RouteKnown || len(b.RouteSignals) != 0 {
				t.Fatalf("%v: %+v", tc.value, b)
			}
			if data := codexJSON(t, b); strings.Contains(string(data), "PRIVATE") {
				t.Fatal(string(data))
			}
		}
	})
	t.Run("workspace routing", func(t *testing.T) {
		// A personal account has workspaceRouting too: it is no evidence.
		f := loadCodexBilling(t)
		f.Account["workspaceRouting"] = map[string]any{"chatgptAccountId": "PRIVATE", "accountRoutingOverride": "NO_CONSTRAINT"}
		if b := codexMatrixFacts(t, f, ""); len(b.WorkspaceReasons) != 0 || !b.RouteKnown || len(b.RouteSignals) != 0 {
			t.Fatalf("%+v", b)
		}
	})
	t.Run("reached types", func(t *testing.T) {
		camel := func(snake string, upper bool) string {
			var b strings.Builder
			for i, part := range strings.Split(snake, "_") {
				if i > 0 || upper {
					part = strings.ToUpper(part[:1]) + part[1:]
				}
				b.WriteString(part)
			}
			return b.String()
		}
		for _, category := range []string{"workspace_owner_credits_depleted", "workspace_member_credits_depleted", "workspace_owner_usage_limit_reached", "workspace_member_usage_limit_reached"} {
			for _, spelling := range []string{category, camel(category, false), camel(category, true)} {
				t.Run(spelling, func(t *testing.T) {
					f := loadCodexBilling(t)
					f.limit()["rateLimitReachedType"] = spelling
					s := parse(t, f)
					if !slices.Equal(s.Billing.WorkspaceReasons, []string{category}) || !s.Rejected {
						t.Fatalf("%+v", s)
					}
					f = loadCodexBilling(t)
					f.byID("sol")["rate_limit_reached_type"] = spelling
					if s := parse(t, f); !slices.Equal(s.Billing.WorkspaceReasons, []string{category}) {
						t.Fatalf("scoped: %+v", s.Billing)
					}
				})
			}
		}
		f := loadCodexBilling(t)
		f.limit()["rateLimitReachedType"] = "rate_limit_reached"
		if s := parse(t, f); len(s.Billing.WorkspaceReasons) != 0 || !s.Rejected {
			t.Fatalf("%+v", s)
		}
	})
	t.Run("sparse nulls", func(t *testing.T) {
		f := loadCodexBilling(t)
		f.limit()["ordinaryUsageAllowed"] = false
		f.limit()["rateLimitReachedType"] = "workspace_owner_credits_depleted"
		f.byID("codex")["ordinaryUsageAllowed"] = nil
		f.byID("codex")["rateLimitReachedType"] = nil
		s := parse(t, f)
		if got := s.Billing.OrdinaryUsageAllowed; got == nil || *got || !s.Rejected || !slices.Equal(s.Billing.WorkspaceReasons, []string{"workspace_owner_credits_depleted"}) {
			t.Fatalf("null cleared a block: %+v", s)
		}
		f = loadCodexBilling(t)
		f.limit()["ordinaryUsageAllowed"] = nil
		f.byID("codex")["ordinaryUsageAllowed"] = true
		if got := parse(t, f).Billing.OrdinaryUsageAllowed; got != nil {
			t.Fatalf("duplicate restored permission: %v", *got)
		}
		// Only a fresh, full read clears a block.
		s = parse(t, loadCodexBilling(t))
		if got := s.Billing.OrdinaryUsageAllowed; got == nil || !*got || s.Rejected {
			t.Fatalf("refetch: %+v", s)
		}
	})
	t.Run("reset credits", func(t *testing.T) {
		for _, tc := range []struct {
			key   string
			value any
			want  *int
		}{
			{"resetCredits", 2, func() *int { n := 2; return &n }()},
			{"reset_credits", 3, func() *int { n := 3; return &n }()},
			{"resetCredits", nil, nil},
			{"resetCredits", -1, nil},
			{"resetCredits", "many", nil},
			{"", nil, nil},
		} {
			f := loadCodexBilling(t)
			delete(f.limit(), "resetCredits")
			if tc.key != "" {
				f.limit()[tc.key] = tc.value
			}
			got := parse(t, f).Billing.ResetCredits
			if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
				t.Fatalf("%s=%v: %v", tc.key, tc.value, got)
			}
		}
	})
	t.Run("scoped rejection", func(t *testing.T) {
		f := loadCodexBilling(t)
		f.byID("sol")["ordinaryUsageAllowed"] = false
		s := parse(t, f)
		if s.Rejected || s.Billing.OrdinaryUsageAllowed == nil || !*s.Billing.OrdinaryUsageAllowed {
			t.Fatalf("scoped block became global: %+v", s)
		}
		sol := s.ModelLimits["sol"]
		if len(sol) != 1 || sol[0].Severity != "rejected" || s.Healthy(time.Now()) {
			t.Fatalf("scoped block lost: %+v", s.ModelLimits)
		}
		for _, w := range s.Windows {
			if w.Severity != "" {
				t.Fatalf("account window marked: %+v", w)
			}
		}
	})
}
