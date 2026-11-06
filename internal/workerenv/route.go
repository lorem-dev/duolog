package workerenv

import (
	"net"
	"net/url"
	"path"
	"slices"
	"strings"
)

// RouteName reports whether a variable name can move agent's runs onto a
// billed route. The rule goes by the shape of the name, so a new provider
// switch or key is caught without a list of providers. providerEnvKey is the
// env_key of the default model provider, which may name any variable; empty
// when there is none. Only presence counts, so an empty value is a route too.
// The comparison is case sensitive: callers fold names on Windows only.
func RouteName(agent, name, providerEnvKey string) bool {
	if name == "" {
		return false
	}
	if name == providerEnvKey {
		return true
	}
	switch agent {
	case "claude":
		// CLAUDE_CODE_OAUTH_TOKEN is the one credential that stays a
		// subscription candidate; it matches none of these shapes.
		return strings.HasPrefix(name, "CLAUDE_CODE_USE_") ||
			strings.HasPrefix(name, "ANTHROPIC_") && credentialSuffix(name) ||
			name == "ANTHROPIC_PROFILE" || name == "ANTHROPIC_FEDERATION_RULE_ID" ||
			strings.HasPrefix(name, "AWS_BEARER_TOKEN_")
	case "codex":
		return name == "CODEX_API_KEY" ||
			strings.HasPrefix(name, "OPENAI_") && credentialSuffix(name) ||
			name == "OPENAI_FEDERATION_RULE_ID"
	}
	return false
}

func credentialSuffix(name string) bool {
	return strings.HasSuffix(name, "_KEY") || strings.HasSuffix(name, "_TOKEN") || strings.HasSuffix(name, "_BASE_URL")
}

// MatchEndpoint returns the name of the first entry, in sorted order, whose
// URL is the same endpoint as raw: scheme, host, port and path compared after
// normalisation of case, default ports and dot segments. A value with
// userinfo, a query, a fragment or an encoded separator is ambiguous and
// matches nothing, and so does such an entry. Only the name comes back, never
// a URL, so that callers cannot print or log the value.
func MatchEndpoint(raw string, entries map[string]string) (string, bool) {
	want, ok := normalEndpoint(raw)
	if !ok {
		return "", false
	}
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if got, ok := normalEndpoint(entries[name]); ok && got == want {
			return name, true
		}
	}
	return "", false
}

// endpoint is a normalised base URL.
type endpoint struct{ scheme, host, port, path string }

func normalEndpoint(raw string) (endpoint, bool) {
	if strings.ContainsAny(raw, `\#`) {
		return endpoint{}, false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return endpoint{}, false
	}
	lower := strings.ToLower(raw)
	for _, separator := range []string{"%2f", "%5c", "%2e"} {
		if strings.Contains(lower, separator) {
			return endpoint{}, false
		}
	}
	scheme := strings.ToLower(u.Scheme)
	port := u.Port()
	switch {
	case scheme == "http" && (port == "" || port == "80"):
		port = "80"
	case scheme == "https" && (port == "" || port == "443"):
		port = "443"
	case scheme != "http" && scheme != "https":
		return endpoint{}, false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || strings.HasSuffix(u.Host, ":") && u.Port() == "" {
		return endpoint{}, false
	}
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	}
	return endpoint{scheme, host, port, path.Clean("/" + u.Path)}, true
}
