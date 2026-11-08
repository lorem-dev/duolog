package doctor

import (
	"context"
	"strings"
	"testing"
)

const routeSecret = "bearer-secret-value"

// A bearer token variable counts as a paid Claude route even when empty, so
// doctor names each one set and what it does, and never prints a value.
func TestDoctorNamesBearerTokenRoutes(t *testing.T) {
	t.Run("named without its value", func(t *testing.T) {
		cfg, opts, _ := setup(t)
		t.Setenv("AWS_BEARER_TOKEN_BEDROCK", routeSecret)
		t.Setenv("AWS_BEARER_TOKEN_EMPTY", "")
		text := joined(Run(context.Background(), testDeps, cfg, opts))
		for _, name := range []string{"AWS_BEARER_TOKEN_BEDROCK", "AWS_BEARER_TOKEN_EMPTY"} {
			var lines []string
			for _, line := range strings.Split(text, "\n") {
				if strings.HasPrefix(line, "warning: "+name+" is set") {
					lines = append(lines, line)
				}
			}
			if len(lines) != 1 {
				t.Fatalf("%s is named on %d lines, want 1:\n%s", name, len(lines), text)
			}
			for _, want := range []string{"warning: ", "paid Claude route", "every Claude worker"} {
				if !strings.Contains(lines[0], want) {
					t.Errorf("line for %s lacks %q: %s", name, want, lines[0])
				}
			}
		}
		if strings.Contains(text, routeSecret) {
			t.Fatalf("the report shows a value:\n%s", text)
		}
	})

	t.Run("nothing set, nothing said", func(t *testing.T) {
		cfg, opts, _ := setup(t)
		if text := joined(Run(context.Background(), testDeps, cfg, opts)); strings.Contains(text, "AWS_BEARER_TOKEN_") {
			t.Fatalf("a report names a variable that is not set:\n%s", text)
		}
	})

	t.Run("other names are not counted", func(t *testing.T) {
		got := testDeps.paidRouteFindings([]string{"AWS_BEARER_TOKEN=x", "AWS_REGION=eu", "CLAUDE_CODE_OAUTH_TOKEN=x", "PATH=/bin"})
		if len(got) != 0 {
			t.Fatalf("findings for names that are no bearer route: %v", got)
		}
	})
}
