package workerenv

import (
	"strings"
	"testing"
)

// Route names are matched by shape, so a new provider switch or key is caught
// without a list of providers. Only the Claude OAuth token stays a
// subscription candidate. The predicate is case sensitive: callers fold names
// on Windows only.
func TestRouteNameShapes(t *testing.T) {
	for _, c := range []struct {
		agent, name, envKey string
		want                bool
	}{
		{"claude", "CLAUDE_CODE_USE_BEDROCK", "", true},
		{"claude", "CLAUDE_CODE_USE_VERTEX", "", true},
		{"claude", "CLAUDE_CODE_USE_FOUNDRY", "", true},
		{"claude", "CLAUDE_CODE_USE_NEW", "", true},
		{"claude", "ANTHROPIC_API_KEY", "", true},
		{"claude", "ANTHROPIC_AUTH_TOKEN", "", true},
		{"claude", "ANTHROPIC_BASE_URL", "", true},
		{"claude", "ANTHROPIC_FOUNDRY_API_KEY", "", true},
		{"claude", "ANTHROPIC_FOUNDRY_AUTH_TOKEN", "", true},
		{"claude", "ANTHROPIC_AWS_API_KEY", "", true},
		{"claude", "ANTHROPIC_BEDROCK_BASE_URL", "", true},
		{"claude", "ANTHROPIC_NEW_TOKEN", "", true},
		{"claude", "ANTHROPIC_PROFILE", "", true},
		{"claude", "ANTHROPIC_FEDERATION_RULE_ID", "", true},
		{"claude", "AWS_BEARER_TOKEN_BEDROCK", "", true},
		{"claude", "AWS_BEARER_TOKEN_NEW", "", true},
		{"claude", "CORP_LLM_KEY", "CORP_LLM_KEY", true},
		{"claude", "CLAUDE_CODE_OAUTH_TOKEN", "", false},
		{"claude", "ANTHROPIC_MODEL", "", false},
		{"claude", "ANTHROPIC_ORGANIZATION_ID", "", false},
		{"claude", "CLAUDE_CODE_MAX_OUTPUT_TOKENS", "", false},
		{"claude", "AWS_REGION", "", false},
		{"claude", "OPENAI_API_KEY", "", false},
		{"claude", "anthropic_api_key", "", false},
		{"claude", "claude_code_use_bedrock", "", false},
		{"codex", "CODEX_API_KEY", "", true},
		{"codex", "OPENAI_API_KEY", "", true},
		{"codex", "OPENAI_ADMIN_TOKEN", "", true},
		{"codex", "OPENAI_BASE_URL", "", true},
		{"codex", "OPENAI_NEW_KEY", "", true},
		{"codex", "OPENAI_FEDERATION_RULE_ID", "", true},
		{"codex", "CORP_GATEWAY_SECRET", "CORP_GATEWAY_SECRET", true},
		{"codex", "CORP_GATEWAY_SECRET", "", false},
		{"codex", "CORP_GATEWAY_SECRET", "OTHER_SECRET", false},
		{"codex", "OPENAI_ORGANIZATION", "", false},
		{"codex", "ANTHROPIC_API_KEY", "", false},
		{"codex", "openai_api_key", "", false},
		{"codex", "corp_gateway_secret", "CORP_GATEWAY_SECRET", false},
		{"other", "ANTHROPIC_API_KEY", "", false},
		{"claude", "", "", false},
	} {
		if got := RouteName(c.agent, c.name, c.envKey); got != c.want {
			t.Errorf("RouteName(%q, %q, %q) = %v, want %v", c.agent, c.name, c.envKey, got, c.want)
		}
	}
}

// An endpoint matches a named entry after normalisation of scheme and host
// case, default ports and dot segments; any other scheme, host, port or path
// does not. Userinfo, a query, a fragment or an encoded separator make the
// value ambiguous, so it never matches. Only the entry name comes back, the
// first in sorted order, and a trusted endpoint clears only a base URL.
func TestEndpointMatchNormalizesWithoutLeaking(t *testing.T) {
	entries := map[string]string{
		"corp-gateway": "https://llm.example.com/claude",
		"local":        "http://127.0.0.1:8080/",
	}
	for _, c := range []struct {
		raw  string
		want string
	}{
		{"https://llm.example.com/claude", "corp-gateway"},
		{"HTTPS://LLM.Example.COM/claude", "corp-gateway"},
		{"https://llm.example.com:443/claude", "corp-gateway"},
		{"https://llm.example.com/claude/", "corp-gateway"},
		{"https://llm.example.com/./claude", "corp-gateway"},
		{"https://llm.example.com/other/../claude", "corp-gateway"},
		{"http://127.0.0.1:8080", "local"},
		{"http://127.0.0.1:8080/", "local"},
		{"http://llm.example.com/claude", ""},
		{"https://llm.example.org/claude", ""},
		{"https://llm.example.com:8443/claude", ""},
		{"https://llm.example.com/Claude", ""},
		{"https://llm.example.com/claude/v2", ""},
		{"https://llm.example.com/", ""},
		{"http://127.0.0.1/", ""},
		{"https://user:secret@llm.example.com/claude", ""},
		{"https://user@llm.example.com/claude", ""},
		{"https://llm.example.com/claude?key=1", ""},
		{"https://llm.example.com/claude?", ""},
		{"https://llm.example.com/claude#top", ""},
		{"https://llm.example.com/x%2F..%2Fclaude", ""},
		{"https://llm.example.com/x%2f..%2fclaude", ""},
		{"https://llm.example.com/x%5C..%5Cclaude", ""},
		{"https://llm.example.com/%2E/claude", ""},
		{`https://llm.example.com/x\..\claude`, ""},
		{"llm.example.com/claude", ""},
		{"ftp://llm.example.com/claude", ""},
		{"", ""},
		{"https:///claude", ""},
	} {
		got, ok := MatchEndpoint(c.raw, entries)
		if got != c.want || ok != (c.want != "") {
			t.Errorf("MatchEndpoint(%q) = %q, %v, want %q", c.raw, got, ok, c.want)
		}
	}

	// Two entries for the same endpoint: the first name in sorted order wins.
	same := map[string]string{
		"zeta":  "https://llm.example.com/claude",
		"alpha": "https://LLM.example.com:443/claude/",
		"mid":   "https://llm.example.com/claude",
	}
	for range 20 {
		if got, _ := MatchEndpoint("https://llm.example.com/claude", same); got != "alpha" {
			t.Fatalf("MatchEndpoint chose %q, want alpha", got)
		}
	}

	// An entry that is itself ambiguous never matches, even the same text.
	bad := map[string]string{"bad": "https://user:pw@llm.example.com/claude"}
	if got, ok := MatchEndpoint("https://user:pw@llm.example.com/claude", bad); ok || got != "" {
		t.Errorf("ambiguous entry matched as %q", got)
	}

	// The result never carries the URL, matched or not.
	secret := "https://llm.example.com/claude"
	for _, raw := range []string{secret, secret + "?token=sk-test-value"} {
		got, _ := MatchEndpoint(raw, entries)
		if strings.Contains(got, "example") || strings.Contains(got, "sk-test") {
			t.Errorf("MatchEndpoint leaked the value: %q", got)
		}
	}

	// Trust never clears a key: a key whose value equals a trusted endpoint
	// stays a signal, and a matched base URL clears only itself.
	settings := `{"env": {
		"ANTHROPIC_BASE_URL": "https://llm.example.com/claude",
		"ANTHROPIC_AUTH_TOKEN": "https://llm.example.com/claude",
		"ANTHROPIC_API_KEY": "sk-test-value"}}`
	facts := ReadLayer("claude", Layer{Name: "user", Path: "settings.json"}, entries,
		fakeRead(map[string]string{"settings.json": settings}), fakeStat)
	if got, want := signalNames(facts.Signals), []string{"env:ANTHROPIC_API_KEY", "env:ANTHROPIC_AUTH_TOKEN"}; !equal(got, want) {
		t.Errorf("signals = %v, want %v", got, want)
	}
	if !equal(facts.EndpointEntries, []string{"corp-gateway"}) {
		t.Errorf("endpoint entries = %v, want [corp-gateway]", facts.EndpointEntries)
	}
	for _, s := range facts.Signals {
		if strings.Contains(s.Name+s.Layer, "example") || strings.Contains(s.Name+s.Layer, "sk-test") {
			t.Errorf("signal leaked a value: %+v", s)
		}
	}

	// Without a matching entry the base URL is a signal; Codex has no
	// subscription endpoints.
	facts = ReadLayer("claude", Layer{Name: "user", Path: "settings.json"}, nil,
		fakeRead(map[string]string{"settings.json": `{"env": {"ANTHROPIC_BASE_URL": "https://llm.example.com/claude"}}`}), fakeStat)
	if got := signalNames(facts.Signals); !equal(got, []string{"env:ANTHROPIC_BASE_URL"}) || len(facts.EndpointEntries) != 0 {
		t.Errorf("unmatched base URL: signals %v, entries %v", got, facts.EndpointEntries)
	}
}
