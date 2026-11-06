package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// A bypass lead owns ask 1 in its own project, and another project holds
// another lead's ask 1. A command that can move to the other project before
// it allows is asked: the ask it answers is not the one the hook read.
func TestSelfDecidingLeadIsAskedWhenTheCommandCanLeaveItsProject(t *testing.T) {
	isolatedRouteConfig(t)
	_, rootB := leadTrustProject(t, "claude", "bypassPermissions")
	rootA := t.TempDir()
	if n := otherLeadAsk(t, filepath.Join(rootA, ".duolog"), "default"); n != 1 {
		t.Fatal("other project's ask is", n)
	}
	t.Chdir(rootB)
	run := func(command string) string {
		t.Helper()
		out, err := gateCommand(t, string(modeInput("lead-claude", rootB, "bypassPermissions", command)), "gate", "--agent", "claude", "--event", "PreToolUse")
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	for _, command := range []string{
		"cd ../" + filepath.Base(rootA) + " && duolog allow 1 --lead lead-a",
		"cd " + rootA + " && duolog allow 1 --lead lead-a",
		"pushd " + rootA + " && duolog allow 1",
		"(cd " + rootA + "; duolog allow 1)",
		"env DUOLOG_X=1 duolog allow 1",
		`sh -c "cd ` + rootA + ` && duolog allow 1"`,
		"duolog allow 1 --project " + rootA,
		"git -C " + rootA + " status && duolog allow 1",
		"duolog peer show peer-x; cd " + rootA + " && duolog allow 1 --lead lead-a",
	} {
		if out := run(command); !strings.Contains(out, `"permissionDecision":"ask"`) {
			t.Errorf("%s: not asked: %s", command, out)
		}
	}
	// A plain allow of its own ask, in its own project, still goes ahead.
	if out := run("duolog allow 1"); out != "" {
		t.Errorf("own plain allow asked: %s", out)
	}
}
