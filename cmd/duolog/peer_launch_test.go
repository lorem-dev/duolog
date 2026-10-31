package main

import (
	"slices"
	"testing"
)

func TestLaunchEnvKeepsOnlyTerminalVariables(t *testing.T) {
	got := launchEnv([]string{"TERM=xterm", "LC_ALL=C", "AWS_SECRET_ACCESS_KEY=x", "REX_SESSION=s", "REX_OTHER=o"}, []string{"HOME=/h", "DUOLOG_DEPTH=1"})
	for _, want := range []string{"TERM=xterm", "LC_ALL=C", "HOME=/h", "DUOLOG_DEPTH=1"} {
		if !slices.Contains(got, want) {
			t.Fatalf("%q is missing from %v", want, got)
		}
	}
	for _, name := range []string{"AWS_SECRET_ACCESS_KEY=x", "REX_SESSION=s", "REX_OTHER=o"} {
		if slices.Contains(got, name) {
			t.Fatalf("%q reached the peer: %v", name, got)
		}
	}
}
