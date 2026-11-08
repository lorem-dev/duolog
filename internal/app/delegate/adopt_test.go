package delegate_test

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/delegate/delegatetest"
	coreagent "github.com/lorem-dev/duolog/internal/core/agent"
)

// adoptingTrust is a Trust that records AdoptWorktree calls and returns
// scripted lines.
type adoptingTrust struct {
	delegatetest.Trust
	lines []string
	err   error
	calls [][]string
}

func (a *adoptingTrust) AdoptWorktree(lead, dir string, environ []string) ([]string, error) {
	a.calls = append(a.calls, append([]string{lead, dir}, environ...))
	return a.lines, a.err
}

func TestAdoptWorktreeTellsWhatItWrote(t *testing.T) {
	fake := &adoptingTrust{lines: []string{"Codex now trusts the linked worktree /p/.worktrees/w"}}
	d := delegate.Deps{Agents: map[string]delegate.Agent{"codex": {Trust: fake}}}
	var out bytes.Buffer
	if err := delegate.AdoptWorktree(d, "codex", "/p", "/p/.worktrees/w", []string{"HOME=/h"}, &out); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 1 || strings.Join(fake.calls[0], " ") != "/p /p/.worktrees/w HOME=/h" {
		t.Fatalf("calls = %q", fake.calls)
	}
	if out.String() != "duolog: Codex now trusts the linked worktree /p/.worktrees/w\n" {
		t.Fatalf("told %q", out.String())
	}
	// A failed adoption is told; the gate checks then read the config as it is.
	fake.lines, fake.err = nil, errors.New("disk full")
	out.Reset()
	if err := delegate.AdoptWorktree(d, "codex", "/p", "/p/.worktrees/w", nil, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "disk full") {
		t.Fatalf("told %q", out.String())
	}
	// A nil writer discards.
	if err := delegate.AdoptWorktree(d, "codex", "/p", "/p/.worktrees/w", nil, nil); err != nil {
		t.Fatal(err)
	}
}

// A worktree that changed since duolog trusted it is told and stops the run:
// the agent does not run there until the person trusts the folder.
func TestAdoptWorktreeStopsOnAChangedWorktree(t *testing.T) {
	changed := fmt.Errorf("%w: .codex/config.toml is not the same", coreagent.ErrWorktreeChanged)
	fake := &adoptingTrust{lines: []string{"Codex no longer trusts the linked worktree /p/.worktrees/w"}, err: changed}
	d := delegate.Deps{Agents: map[string]delegate.Agent{"codex": {Trust: fake}}}
	var out bytes.Buffer
	err := delegate.AdoptWorktree(d, "codex", "/p", "/p/.worktrees/w", nil, &out)
	if !errors.Is(err, coreagent.ErrWorktreeChanged) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(out.String(), "no longer trusts") || !strings.Contains(out.String(), ".codex/config.toml") {
		t.Fatalf("told %q", out.String())
	}
}

func TestAdoptWorktreeSkipsATrustThatCannotAdopt(t *testing.T) {
	d := delegate.Deps{Agents: map[string]delegate.Agent{"claude": {Trust: &delegatetest.Trust{}}, "codex": {}}}
	var out bytes.Buffer
	for _, agent := range []string{"claude", "codex", "missing"} {
		if err := delegate.AdoptWorktree(d, agent, "/p", "/p/.worktrees/w", nil, &out); err != nil {
			t.Fatal(err)
		}
	}
	if out.Len() != 0 {
		t.Fatalf("told %q", out.String())
	}
}
