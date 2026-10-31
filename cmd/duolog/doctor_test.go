package main

import (
	"strings"
	"testing"
)

func TestDoctorExplainsHookTrustLimit(t *testing.T) {
	isolatedRouteConfig(t)
	t.Chdir(t.TempDir())
	out, err := executeBusCommand(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"hooks approved=false", "changed after review cannot be detected", "Codex itself will skip", "/hooks"} {
		if !strings.Contains(out, want) {
			t.Fatalf("doctor output %q missing %q", out, want)
		}
	}
}
