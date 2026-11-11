package lead_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/lead"
	"github.com/lorem-dev/duolog/internal/app/lead/leadtest"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/testutil/submodule"
)

func TestRegisterSubmoduleUsesLeadHome(t *testing.T) {
	f := submodule.New(t)
	t.Chdir(f.Submodule)
	for _, root := range []string{f.Superproject, f.Submodule} {
		if err := os.MkdirAll(filepath.Join(root, ".duolog"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".duolog", "config.yaml"), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	d := deps(t, &leadtest.Fake{ID: "lead-1", IsInteract: true})
	var registryDir string
	d.Registry = func(dir string) (*session.Registry, error) { registryDir = dir; return session.Open(dir) }
	_, err := lead.Register(context.Background(), d, lead.RegisterRequest{Root: f.Submodule})
	if err != nil {
		t.Fatal(err)
	}
	if registryDir != project.LeadHome(f.Submodule) {
		t.Fatal(registryDir)
	}
	reg, err := session.Open(registryDir)
	if err != nil {
		t.Fatal(err)
	}
	registered, live, err := reg.Lead("lead-1")
	if err != nil || !live || registered.Root != f.Submodule {
		t.Fatalf("%+v %v %v", registered, live, err)
	}
	if _, err := os.Stat(filepath.Join(f.Submodule, ".duolog", "sessions.json")); !os.IsNotExist(err) {
		t.Fatal("created work-local registry", err)
	}
}
