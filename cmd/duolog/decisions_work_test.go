package main

import (
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/gate"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/policy"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/store"
)

func TestDecisionsIdentifyOtherWorkRoots(t *testing.T) {
	_, f, _, _ := submoduleDelegateProject(t)
	s := &store.Store{Dir: project.LeadHome(f.Superproject)}
	for _, log := range []func(*store.Store, policy.Action, policy.Decision) error{gate.Log, gate.LogRequest} {
		for _, root := range []string{f.Superproject, f.Linked, ""} {
			if err := log(s, policy.Action{Kind: "write", Target: "a.go", Work: root}, policy.Decision{Kind: policy.Allow, Mode: mode.Edits}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, root := range []string{f.Superproject, f.Linked} {
		t.Chdir(root)
		out, err := gateCommand(t, "", "decisions")
		if err != nil {
			t.Fatal(err)
		}
		other := f.Linked
		if root == f.Linked {
			other = f.Superproject
		}
		if strings.Count(out, other) != 2 || strings.Contains(out, root) {
			t.Fatalf("work roots from %s: %q", root, out)
		}
	}
}
