package lead

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
	"github.com/lorem-dev/duolog/internal/project"
)

// SubmoduleLine names each submodule work project once, without reading its
// instruction files. Linked checkouts share their main checkout's rules.
func SubmoduleLine(ctx context.Context, root string) (string, error) {
	modules, err := project.DuologSubmodules(ctx, root)
	if err != nil {
		return "", err
	}
	seen := map[string]bool{}
	var paths []string
	for _, module := range modules {
		main := project.StateRoot(module)
		key := pathutil.Clean(pathutil.Resolved(main))
		if seen[key] {
			continue
		}
		seen[key] = true
		rel, err := filepath.Rel(pathutil.Resolved(project.StateRoot(root)), pathutil.Resolved(main))
		if err != nil {
			return "", err
		}
		paths = append(paths, filepath.ToSlash(rel))
	}
	slices.Sort(paths)
	return submoduleLine(paths), nil
}

func submoduleLine(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	return "Submodules with their own duolog rules: " + strings.Join(paths, ", ") + ". Before planning work there, read their AGENTS.md and CLAUDE.md files; roles and models come from their .duolog/config.yaml."
}

// ClaimSubmoduleReminder shares one notice across APIs and linked checkouts,
// scoped to this lead's session. Closing the lead clears its notice claims.
func ClaimSubmoduleReminder(reg *session.Registry, leadID, workRoot string) (string, error) {
	main := project.StateRoot(workRoot)
	key := pathutil.Clean(pathutil.Resolved(main))
	home := filepath.Dir(reg.Dir)
	if key == pathutil.Clean(pathutil.Resolved(home)) {
		return "", nil
	}
	rel, err := filepath.Rel(pathutil.Resolved(home), pathutil.Resolved(main))
	if err != nil {
		return "", err
	}
	claimed, err := reg.ClaimNotice(leadID, "submodule-rules:"+key)
	if err != nil || !claimed {
		return "", err
	}
	return submoduleLine([]string{filepath.ToSlash(rel)}), nil
}

// SubmoduleLookupError explains the shared-state boundary on lead lookup
// failures, without ever opening the ignored legacy work-local registry.
func SubmoduleLookupError(err error, workRoot string) error {
	if err == nil || (!strings.Contains(err.Error(), "no live lead") && !strings.Contains(err.Error(), "not a live lead")) {
		return err
	}
	work := filepath.Join(project.StateRoot(workRoot), ".duolog")
	home := project.LeadHome(workRoot)
	if pathutil.Clean(pathutil.Resolved(work)) == pathutil.Clean(pathutil.Resolved(home)) {
		return err
	}
	return fmt.Errorf("%w; checked work state %s and lead home %s; old work-local lead files are ignored", err, work, home)
}
