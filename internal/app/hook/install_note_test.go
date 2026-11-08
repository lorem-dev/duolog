package hook

import (
	"context"
	"strings"
	"testing"
)

const staleNote = "duolog: this project's duolog install is out of date; run duolog install --scope project"

func TestSessionStartNamesStaleInstall(t *testing.T) {
	e, _, _ := setup(t, "claude")
	var asked []string
	e.InstallNote = func(root string) string {
		asked = append(asked, root)
		return staleNote
	}
	c := content(t, SessionStart(context.Background(), e), "SessionStart")
	if !strings.Contains(c, "\n"+staleNote+"\n") || len(asked) != 1 || asked[0] != e.Root {
		t.Fatalf("context %q, asked %v", c, asked)
	}
	// The note comes with the session start only.
	if o := Prompt(context.Background(), e); strings.Contains(string(o.Stdout), staleNote) {
		t.Fatalf("prompt %q", o.Stdout)
	}
}

func TestSessionStartCurrentInstallHasNoNote(t *testing.T) {
	e, _, _ := setup(t, "codex")
	e.InstallNote = func(string) string { return "" }
	if c := content(t, SessionStart(context.Background(), e), "SessionStart"); strings.Contains(c, "install") {
		t.Fatalf("context %q", c)
	}
}

func TestSessionStartRefreshesSkillsBeforeTheNote(t *testing.T) {
	e, _, _ := setup(t, "claude")
	var order []string
	e.RefreshSkills = func(root string) string {
		order = append(order, "refresh "+root)
		return "duolog: updated the duolog skills to 9.9.9"
	}
	e.InstallNote = func(string) string {
		order = append(order, "note")
		return ""
	}
	c := content(t, SessionStart(context.Background(), e), "SessionStart")
	if !strings.Contains(c, "duolog: updated the duolog skills to 9.9.9") || strings.Join(order, ",") != "refresh "+e.Root+",note" {
		t.Fatalf("context %q, order %v", c, order)
	}
}

func TestWorkerSessionStartSkipsSkillRefresh(t *testing.T) {
	e, _, _ := setup(t, "codex")
	e.Environ = append(e.Environ, "DUOLOG_DEPTH=1")
	e.RefreshSkills = func(string) string {
		t.Fatal("a worker refreshed the skills")
		return ""
	}
	silent(t, SessionStart(context.Background(), e))
}

func TestWorkerSessionStartSkipsInstallNote(t *testing.T) {
	e, _, _ := setup(t, "claude")
	e.Environ = append(e.Environ, "DUOLOG_DEPTH=1")
	e.InstallNote = func(string) string {
		t.Fatal("a worker checked the install")
		return staleNote
	}
	silent(t, SessionStart(context.Background(), e))
}
