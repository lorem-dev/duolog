package peer

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/peer/peertest"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/testutil/submodule"
)

func TestPeerSubmoduleWorktreesShareCap(t *testing.T) {
	r, f := queueSubmoduleRig(t)
	peerSubmoduleFile(t, f.Superproject, ".duolog/config.yaml", capConfig(2))
	peerSubmoduleFile(t, f.Submodule, ".duolog/config.yaml", capConfig(2))
	other := filepath.Join(t.TempDir(), "other")
	submodule.Git(t, f.Submodule, "worktree", "add", "-q", "--detach", other)
	script := baseScript()
	script["open-tab"] = peertest.Reply{Block: "block:second"}
	r.panes(script)
	for i, root := range []string{f.Submodule, f.Linked, other} {
		t.Chdir(root)
		_, err := Open(r.submoduleCtx(f), "implement")
		if i < 2 && err != nil {
			t.Fatal(err)
		}
		if i == 2 && (!errors.Is(err, ErrRefused) || len(r.spawns()) != 2) {
			t.Fatal("submodule worktree bypassed cap", err, r.spawns())
		}
	}
}

func capConfig(limit int) string {
	return fmt.Sprintf("limits:\n  max_live_sessions: %d\nroles:\n  implement:\n    use: {agent: codex, model: luna}\n    balance: false\n", limit)
}

// The lead home's max_live_sessions caps the lead's peers in the superproject
// and its submodule together; the submodule's own value plays no part.
func TestPeerSharedLeadCap(t *testing.T) {
	for _, tc := range []struct{ lead, sub int }{{1, 8}, {8, 1}} {
		t.Run(fmt.Sprintf("lead %d submodule %d", tc.lead, tc.sub), func(t *testing.T) {
			r := newRig(t, baseScript())
			f := submodule.New(t)
			peerSubmoduleFile(t, f.Superproject, ".duolog/config.yaml", capConfig(tc.lead))
			peerSubmoduleFile(t, f.Submodule, ".duolog/config.yaml", capConfig(tc.sub))
			r.root = f.Superproject
			var err error
			if r.cfg, err = configfile.Load(r.root); err != nil {
				t.Fatal(err)
			}
			if r.reg, err = session.Open(project.LeadHome(f.Linked)); err != nil {
				t.Fatal(err)
			}
			r.lead("codex", mode.Ask)
			var limits []int
			r.useDeps(func(d *Deps) {
				d.RecordPeer = func(reg *session.Registry, e session.Entry, limit int) error {
					limits = append(limits, limit)
					return reg.RecordLimited(e, limit)
				}
			})
			script := baseScript()
			script["open-tab"] = peertest.Reply{Block: "block:second"}
			r.panes(script)
			t.Chdir(f.Superproject)
			first := r.open("implement")

			t.Chdir(f.Linked)
			second, err := Open(r.submoduleCtx(f), "implement")
			switch {
			case tc.lead == 1:
				if !errors.Is(err, ErrRefused) || len(r.spawns()) != 1 {
					t.Fatal("submodule peer passed the lead's cap", err, r.spawns())
				}
			case err != nil || second.ID == first.ID || len(r.spawns()) != 2:
				t.Fatal("reused across checkouts or refused under the lead's cap", second, err, r.spawns())
			}
			want := []int{tc.lead, tc.lead}[:len(r.spawns())]
			if !slices.Equal(limits, want) {
				t.Fatalf("limits %v, want the lead home's %v", limits, want)
			}
		})
	}
}
