package doctor

import (
	"runtime"
	"sort"
	"strings"
)

// paidRouteFindings names each bearer token variable in environ that
// PaidClaudeRoute counts as a paid Claude route. Presence is enough, so an
// empty value is named too. Names only: no value is read out.
func (d Deps) paidRouteFindings(environ []string) []Finding {
	if d.PaidClaudeRoute == nil {
		return nil
	}
	var names []string
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if runtime.GOOS == "windows" {
			name = strings.ToUpper(name)
		}
		// The prefix only picks the route name that counts when empty and is
		// often set globally; PaidClaudeRoute is the predicate.
		if strings.HasPrefix(name, "AWS_BEARER_TOKEN_") && d.PaidClaudeRoute(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	out := make([]Finding, 0, len(names))
	for _, name := range names {
		out = append(out, Finding{"warning", name + " is set in the environment (even an empty value counts); duolog treats it as a paid Claude route, so every Claude worker is paid, not free, until you unset it"})
	}
	return out
}
