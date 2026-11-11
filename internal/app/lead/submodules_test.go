package lead_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/lead"
	"github.com/lorem-dev/duolog/internal/testutil/submodule"
)

func reminderProjects(t *testing.T) submodule.Fixture {
	t.Helper()
	f := submodule.New(t)
	for _, root := range []string{f.Superproject, f.Submodule, f.Nested} {
		if err := os.MkdirAll(filepath.Join(root, ".duolog"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".duolog/config.yaml"), []byte("{}\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func TestSubmoduleLine(t *testing.T) {
	f := reminderProjects(t)
	line, err := lead.SubmoduleLine(context.Background(), f.Superproject)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, "rules: module, module/nested.") || strings.Contains(line, f.Linked) {
		t.Fatal(line)
	}
	if len(strings.Fields(line)) > 50 {
		t.Fatal("session line grew", line)
	}
	empty, err := lead.SubmoduleLine(context.Background(), f.Unrelated)
	if err != nil || empty != "" {
		t.Fatal(empty, err)
	}
}

func TestSubmoduleReminderSharedClaim(t *testing.T) {
	f := reminderProjects(t)
	reg, err := session.Open(filepath.Join(f.Superproject, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	if err := reg.RegisterLead(session.Lead{ID: "first", PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	lines := make(chan string, 16)
	for i := 0; i < 16; i++ {
		wg.Go(func() {
			line, err := lead.ClaimSubmoduleReminder(reg, "first", f.Linked)
			if err != nil {
				t.Error(err)
			}
			lines <- line
		})
	}
	wg.Wait()
	close(lines)
	count := 0
	for line := range lines {
		if line != "" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("claims", count)
	}
	if line, err := lead.ClaimSubmoduleReminder(reg, "first", f.Submodule); err != nil || line != "" {
		t.Fatal(line, err)
	}
	if line, err := lead.ClaimSubmoduleReminder(reg, "other", f.Linked); err != nil || line == "" {
		t.Fatal(line, err)
	}
	if line, err := lead.ClaimSubmoduleReminder(reg, "first", f.Superproject); err != nil || line != "" {
		t.Fatal(line, err)
	}
	if err := reg.Close("first"); err != nil {
		t.Fatal(err)
	}
	if line, err := lead.ClaimSubmoduleReminder(reg, "first", f.Submodule); err != nil || line == "" {
		t.Fatal(line, err)
	}
}
