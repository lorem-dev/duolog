package peer

import (
	"context"
	"os"
	"path/filepath"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/gate"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
	"github.com/lorem-dev/duolog/internal/core/policy"
	corescreen "github.com/lorem-dev/duolog/internal/core/screen"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/store"
)

// trustCaptureLines is how many lines a folder-trust question is read from
// when the default capture cuts its heading off.
const trustCaptureLines = 200

// leadRoot is the lead's recorded checkout, whose trust and MCP decisions
// Claude holds. Legacy leads fall back to the checkout owning shared state.
func (e *env) leadRoot() string {
	if lead, ok, err := e.reg.Lead(e.leadID); err == nil && ok && lead.Root != "" {
		return lead.Root
	}
	return filepath.Dir(e.dir)
}

// agentQuestion handles the questions the agent's pane adapter reads itself,
// before any text rule: a plugin offer is declined, and the folder-trust
// question is answered for the lead's own project only. Either way handled is
// true, so the question never reaches the text rules; the trust question that
// is not answered stays for the lead.
func (e *env) agentQuestion(ctx context.Context, entry session.Entry, screen Screen, agent PaneAgent) (answered, handled bool, err error) {
	if agent == nil {
		return false, false, nil
	}
	if offer, ok := agent.Offer(screen); ok {
		answered, err = e.declinePlugin(ctx, entry, screen, offer)
		return answered, true, err
	}
	// A command prompt is the gate's when it is the question itself. One left
	// in the scrollback above a trust screen is not: PromptCommand finds a
	// command only in the block right above the options.
	if ask := e.pane(entry.Agent).Facts.CommandQuestion; ask != "" && corescreen.PromptCommand(screen.Text, ask) != "" {
		return false, false, nil
	}
	label, folders, ok := agent.FolderTrust(screen)
	// A trust question can be taller than the capture: a long warning block
	// pushes its heading off the top. It is read again from more lines, so
	// that its heading and folders are seen.
	if !ok && corescreen.OffersFolderTrust(screen) && e.cfg.Rex.CaptureLines < trustCaptureLines {
		raw, cerr := e.panes.CaptureStyled(ctx, e.paneOf(entry), trustCaptureLines)
		if cerr != nil {
			return false, true, cerr
		}
		screen = corescreen.Parse(raw, e.pane(entry.Agent).Facts)
		// The screen moved on or closed: nothing is answered from the old
		// capture, and the next poll reads the pane again.
		if !corescreen.OffersFolderTrust(screen) {
			return false, true, nil
		}
		label, folders, ok = agent.FolderTrust(screen)
	}
	if ok {
		answered, err = e.answerFolderTrust(ctx, entry, screen, agent, label, folders)
		return answered, true, err
	}
	// A trust screen the reading above does not know is the lead's to answer,
	// and no text rule may.
	return false, corescreen.OffersFolderTrust(screen), nil
}

// answerFolderTrust answers the agent's startup question whether to trust the
// pane's folder with the label that trusts it, only where the screen names the
// pane's folder and trustAllowed says that folder belongs to the lead's own
// project, whose root the agent already trusts. Otherwise the question stays
// for the lead. It grants no hook trust, approval or plugin. The answer is
// logged like the other automatic ones.
func (e *env) answerFolderTrust(ctx context.Context, entry session.Entry, screen Screen, agent PaneAgent, label string, folders []string) (bool, error) {
	folder := entry.WorkRoot
	if folder == "" {
		folder = e.root
	}
	if !namedFoldersOK(folders, e.leadRoot(), folder) || !trustAllowed(agent, e.leadRoot(), folder, os.Environ()) {
		return false, nil
	}
	if err := e.selectOption(ctx, entry, screen, label); err != nil {
		return false, err
	}
	action := policy.Action{Kind: "answer", Role: entry.Role, Work: folder, Target: screen.Question + " => " + label}
	decision := policy.Decision{Kind: policy.Allow, Reason: "the pane's folder is the lead's own project, whose root the agent trusts", Rule: "peer.folder_trust"}
	if err := gate.Log(&store.Store{Dir: e.dir}, action, decision); err != nil {
		return false, err
	}
	_, err := e.update(entry.ID, func(x *session.Entry) { x.Asked = "" })
	return true, err
}

// namedFoldersOK reports whether the question is about the folder duolog
// checked and nothing else: one of the folders a screen names resolves to
// folder, and every one of them belongs to the lead's project.
func namedFoldersOK(named []string, leadRoot, folder string) bool {
	want := pathutil.Clean(pathutil.Resolved(folder))
	found := false
	for _, n := range named {
		if !project.SameProject(leadRoot, n) {
			return false
		}
		found = found || pathutil.Clean(pathutil.Resolved(n)) == want
	}
	return found
}

// trustAllowed reports whether a pane in folder may have its trust question
// answered: folder is the lead's root, below it, or a linked worktree that the
// lead's repository lists (project.SameProject, which runs git without the
// caller's GIT_ variables), and the agent already trusts the lead's root in
// the config environ selects.
func trustAllowed(agent PaneAgent, leadRoot, folder string, environ []string) bool {
	if leadRoot == "" || folder == "" || !project.SameProject(leadRoot, folder) {
		return false
	}
	return agent.TrustsFolder(leadRoot, environ)
}
