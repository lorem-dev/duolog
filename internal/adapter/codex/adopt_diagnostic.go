package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/install"
	"github.com/pelletier/go-toml/v2"
)

// WorktreeTrustProblem explains a failed adoption without changing trust. It
// proves the same worktree relationship as AdoptWorktree before naming sources.
func (Trust) WorktreeTrustProblem(lead, dir string, environ []string) string {
	if !plainPath(lead) || !plainPath(dir) || !filepath.IsAbs(lead) || !filepath.IsAbs(dir) || samePath(lead, dir) {
		return ""
	}
	codexHome, home, ok := codexHomes(environ)
	if !ok {
		return ""
	}
	main, ok := linkedWorktree(lead, dir)
	if !ok {
		return ""
	}
	a := adopter{path: filepath.Join(codexHome, "config.toml"), dir: dir, roots: []string{lead}, userHooks: filepath.Join(codexHome, "hooks.json"), stale: install.StaleApprovalsIn(home, environ)}
	if main != "" && !samePath(main, lead) {
		a.roots = append(a.roots, main)
	}
	data, err := os.ReadFile(a.path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Sprintf("could not read source trust: %v", err)
	}
	return a.adoptionProblem(data)
}

// adoptionProblem uses the adoption checks only to explain a refusal. The
// planner and writer remain the sole authority for copying trust and approvals.
func (a adopter) adoptionProblem(data []byte) string {
	if p, ok := planAdoption(data, a.dir, a.roots, a.userHooks, a.stale); ok {
		if !sameDevLaunchers(p.from, a.dir) {
			return fmt.Sprintf("trust was not carried over from %s: the dev launcher scripts/dev/bin/duolog is not the same in %s", p.from, a.dir)
		}
		if p.trustKey != "" {
			if name, same := sameCodexTree(p.from, a.dir); !same {
				return fmt.Sprintf("trust was not carried over from %s: %s is not the same in %s", p.from, name, a.dir)
			}
		}
		return ""
	}
	var cfg adoptConfig
	var settings codexSettings
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return fmt.Sprintf("could not read source trust: %v", err)
	}
	if err := toml.Unmarshal(data, &settings); err != nil {
		return fmt.Sprintf("could not read source trust: %v", err)
	}
	if _, ok := worktreeTrust(cfg, a.dir); !ok {
		return fmt.Sprintf("trust was not carried over from %s: worktree has an explicit trust level in %s", strings.Join(a.roots, ", "), a.dir)
	}
	reasons := make([]string, 0, len(a.roots))
	for _, root := range a.roots {
		reasons = append(reasons, fmt.Sprintf("trust was not carried over from %s: %s", root, a.rootProblem(cfg, settings, root)))
	}
	return strings.Join(reasons, "; ")
}

// rootProblem names why the trust of the source checkout root was not
// carried over.
func (a adopter) rootProblem(cfg adoptConfig, settings codexSettings, root string) string {
	if trustedKey(cfg, root) == "" {
		return "source checkout lacks folder trust"
	}
	hooksPath := filepath.Join(root, ".codex", "hooks.json")
	reason := "cause is unknown"
	if state, err := hooksAt(hooksPath, settings, a.stale); err == nil {
		switch {
		case !state.Trusted:
			reason = "source checkout lacks gate approval"
		case state.Unproven != "":
			reason = "source checkout has a stale gate approval; renew it in /hooks in the source checkout"
		}
	}
	source, haveSource := readHooksDoc(hooksPath)
	if !haveSource {
		return reason
	}
	c := copyCheck{cfg: cfg, hooksPath: hooksPath, source: source, gateApproved: reason == "cause is unknown"}
	c.target, _ = readHooksDoc(filepath.Join(a.dir, ".codex", "hooks.json"))
	keys := make([]string, 0, len(cfg.Hooks.State))
	for key := range cfg.Hooks.State {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		if problem := a.keyProblem(c, key); problem != "" {
			return problem
		}
	}
	return reason
}

// copyCheck is what keyProblem compares for one source checkout: the Codex
// config, the source hooks file and its contents, the worktree's hooks file
// if it has one, and whether the source gate approval exists.
type copyCheck struct {
	cfg          adoptConfig
	hooksPath    string
	source       hooksDoc
	target       hooksDoc
	gateApproved bool
}

// keyProblem names how the approval key of the source hooks file blocks the
// copy, or returns "" when it does not.
func (a adopter) keyProblem(c copyCheck, key string) string {
	path, spelled, position, ok := splitApproval(key)
	event := codexEvents()[spelled]
	if !ok || event == "" || !samePath(path, c.hooksPath) {
		return ""
	}
	_, hash, approved := approvalEntry(c.cfg.Hooks.State[key])
	if !approved || a.stale[key] == hash {
		return ""
	}
	g, h := positionOf(position)
	if !hasHandler(c.source, event, g, h) {
		return ""
	}
	if !ownHandler(event, c.source.groups[event][g], h) {
		return "hooks to copy are not duolog's own in .codex/hooks.json"
	}
	// With the gate approval missing, that is the blocker, and a difference
	// in another hook would only hide it.
	if c.gateApproved && !hasOwnEvent(c.target, event) {
		return "worktree has no duolog hook for " + event + " in .codex/hooks.json in " + a.dir
	}
	if event != "PreToolUse" || !c.source.groups[event][g].IsGate(h) {
		return ""
	}
	return a.targetGateProblem(c, key, event)
}

func (a adopter) targetGateProblem(c copyCheck, key, event string) string {
	path, spelled, _, _ := splitApproval(key)
	for g, group := range c.target.groups[event] {
		for h := range group.Hooks {
			if !ownHandler(event, group, h) || !group.IsGate(h) {
				continue
			}
			position := fmt.Sprintf("%d:%d", g, h)
			_, exists := existing(c.cfg.Hooks.State, copyKey(path, spelled, position, a.dir))
			_, ready := copyApproval(c.cfg, key, a.dir, event, g, h, c.source, c.target)
			if exists && !ready {
				return "target checkout already has a different gate approval in " + a.dir
			}
		}
	}
	return ""
}

// hasHandler reports whether doc has handler h in group g of event.
func hasHandler(doc hooksDoc, event string, g, h int) bool {
	return g < len(doc.groups[event]) && h < len(doc.groups[event][g].Hooks)
}

func hasOwnEvent(doc hooksDoc, event string) bool {
	for _, group := range doc.groups[event] {
		for h := range group.Hooks {
			if ownHandler(event, group, h) {
				return true
			}
		}
	}
	return false
}
