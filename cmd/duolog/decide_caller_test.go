package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/gate"
	"github.com/lorem-dev/duolog/internal/session"
)

// callerProject is a project whose registry holds lead-b, whose process is
// this test process, so every command the test starts runs below lead B, as
// a Bash call of lead B does. It returns the project root.
func callerProject(t *testing.T) string {
	t.Helper()
	t.Setenv("DUOLOG_DEPTH", "")
	t.Setenv("DUOLOG_PEER", "")
	root := t.TempDir()
	reg, err := session.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterLead(session.Lead{ID: "lead-b", Target: config.Target{Agent: "claude"}, PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	return root
}

// shellBelowLead runs script with sh in dir, with the built duolog first on
// PATH and the mark Claude Code puts in its tool shells, and returns its
// output and error.
func shellBelowLead(t *testing.T, dir, script string) (string, error) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	bin := duologBinary(t)
	cmd := exec.Command("sh", "-c", script)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"), "CLAUDECODE=1")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// answered reports whether the bus of the project at root holds an answer to
// ask n.
func answered(t *testing.T, root string, n int64) bool {
	t.Helper()
	kind, err := alreadyAnswered(filepath.Join(root, ".duolog"), n)
	if err != nil {
		t.Fatal(err)
	}
	return kind != ""
}

// Re-review R1: duolog allow takes its caller from the parent processes, not
// from --lead. Lead B cannot allow lead A's ask from a script, behind a name
// that the text guard cannot read, or from another project.
func TestAllowRefusesAnAskTheCallingLeadDoesNotOwn(t *testing.T) {
	isolatedRouteConfig(t)
	rootB := callerProject(t)
	rootA := t.TempDir()
	for _, tc := range []struct {
		name, root string
		script     func(ask string) string
	}{
		{"script", rootB, func(ask string) string {
			if err := os.WriteFile(filepath.Join(rootB, "a.sh"), []byte("duolog allow "+ask+" --lead lead-a\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return "sh a.sh"
		}},
		{"hidden name", rootB, func(ask string) string { return "D=duo; ${D}log allow " + ask + " --lead lead-a" }},
		{"other project", rootA, func(ask string) string {
			return "cd ../" + filepath.Base(rootA) + " && duolog allow " + ask + " --lead lead-a"
		}},
	} {
		n := otherLeadAsk(t, filepath.Join(tc.root, ".duolog"), "default")
		out, err := shellBelowLead(t, rootB, tc.script(strconv.FormatInt(n, 10)))
		if err == nil || !strings.Contains(out, "lead") {
			t.Errorf("%s: not refused: %s %v", tc.name, out, err)
		}
		if answered(t, tc.root, n) {
			t.Errorf("%s: the ask was answered", tc.name)
		}
	}
	// Lead B's own ask still goes ahead from a script.
	n := ownAsk(t, rootB)
	if out, err := shellBelowLead(t, rootB, "duolog allow "+strconv.FormatInt(n, 10)); err != nil || !answered(t, rootB, n) {
		t.Fatalf("own ask: %s %v", out, err)
	}
}

// ownAsk puts a person-only ask of lead-b on the bus of the project at root.
func ownAsk(t *testing.T, root string) int64 {
	t.Helper()
	b, err := bus.Open(filepath.Join(root, ".duolog"), config.Wire{MaxLineBytes: 512, MaxSummaryBytes: 200})
	if err != nil {
		t.Fatal(err)
	}
	lead := session.Lead{ID: "lead-b", Target: config.Target{Agent: "claude"}}
	n, err := b.Send(bus.Message{From: "duolog/ask.own", To: session.Address(lead), K: gate.Ask, S: "read ../notes.txt", P: true, W: 120})
	if err != nil {
		t.Fatal(err)
	}
	return n
}
