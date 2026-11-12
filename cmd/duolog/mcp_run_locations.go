package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/adapter/mcp"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/project"
)

// mcpFindResult finds the run a delegate result call names: by id through
// its location first, else by id or task path among the lead home's records.
// A relative task path that more than one run has is an error that asks for
// the id or a worktree.
func mcpFindResult(ctx context.Context, reg *session.Registry, root string, in mcp.DelegateInput) (delegate.LocatedRecord, error) {
	if found, ok, err := mcpResultByID(reg, root, in.TaskPath); err != nil || ok {
		return found, err
	}
	records, err := mcpResultRecords(ctx, reg, root)
	if err != nil {
		return delegate.LocatedRecord{}, err
	}
	matches, err := mcpResultMatches(records, workRoot(root), in)
	if err != nil {
		return delegate.LocatedRecord{}, err
	}
	switch len(matches) {
	case 0:
		return delegate.LocatedRecord{}, fmt.Errorf("no delegation with task_path %s; status lists them", in.TaskPath)
	case 1:
		return matches[0], nil
	}
	var ids []string
	for _, m := range matches {
		ids = append(ids, m.ID+" in "+m.WorkRoot)
	}
	return delegate.LocatedRecord{}, fmt.Errorf("task_path %s names more than one delegation (%s); pass its id, or the worktree", in.TaskPath, strings.Join(ids, ", "))
}

// mcpResultMatches keeps the records whose id or task file is in.TaskPath,
// in in.Worktree when the call names one.
func mcpResultMatches(records []delegate.LocatedRecord, root string, in mcp.DelegateInput) ([]delegate.LocatedRecord, error) {
	key := in.TaskPath
	full := key
	if !filepath.IsAbs(full) {
		full = filepath.Join(root, filepath.FromSlash(full))
	}
	var work string
	if in.Worktree != "" {
		resolved, err := mcpPeerWorktree(in.Worktree)
		if err != nil {
			return nil, err
		}
		work = checkoutKey(resolved)
	}
	var matches []delegate.LocatedRecord
	for _, r := range records {
		if work != "" && checkoutKey(r.WorkRoot) != work {
			continue
		}
		if r.ID == key || r.TaskPath == key || checkoutKey(filepath.Join(r.WorkRoot, filepath.FromSlash(r.TaskPath))) == checkoutKey(full) {
			matches = append(matches, r)
		}
	}
	return matches, nil
}

// mcpResultRecords includes configured Git submodules even when an older run
// has no registry location. Git establishes membership and the shared lead
// home; unrelated repositories nested under the server are never searched.
func mcpResultRecords(ctx context.Context, reg *session.Registry, root string) ([]delegate.LocatedRecord, error) {
	records, err := delegate.LeadRecords(reg, root, "")
	if err != nil {
		return nil, err
	}
	modules, err := project.DuologSubmodules(ctx, root)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, r := range records {
		seen[checkoutKey(filepath.Dir(r.Path))] = true
	}
	for _, work := range modules {
		dir := delegate.ProjectRecordDir(project.StateRoot(work))
		key := checkoutKey(dir)
		if seen[key] {
			continue
		}
		seen[key] = true
		found, err := delegate.ListRecords(dir, session.ProcessAlive)
		if err != nil {
			return nil, err
		}
		for _, r := range found {
			records = append(records, delegate.LocatedRecord{Record: r, Path: filepath.Join(dir, r.ID+".json")})
		}
	}
	return records, nil
}

// mcpResultByID reads the record of run id at its recorded location, or in
// the server project when none is recorded. ok is false when id is no run id
// of either; a record there that cannot be read is an error.
func mcpResultByID(reg *session.Registry, root, id string) (found delegate.LocatedRecord, ok bool, err error) {
	if id == "" || strings.ContainsAny(id, `/\.`) {
		return found, false, nil
	}
	locations, err := reg.Delegations("")
	if err != nil {
		return found, false, err
	}
	path := filepath.Join(delegate.ProjectRecordDir(root), id+".json")
	if i := slices.IndexFunc(locations, func(loc session.DelegationLocation) bool { return loc.ID == id }); i >= 0 {
		if !delegate.SharesLeadHome(reg, locations[i].WorkRoot) {
			return found, false, fmt.Errorf("delegation %s ran in %s, which no longer shares this lead", id, locations[i].WorkRoot)
		}
		path = filepath.Join(delegate.ProjectRecordDir(locations[i].WorkRoot), id+".json")
	} else if _, statErr := os.Stat(path); errors.Is(statErr, fs.ErrNotExist) {
		path = filepath.Join(delegate.ProjectRecordDir(filepath.Dir(reg.Dir)), id+".json")
		if _, statErr := os.Stat(path); errors.Is(statErr, fs.ErrNotExist) {
			return found, false, nil
		}
	}
	r, err := delegate.ReadRecord(path, session.ProcessAlive)
	if err != nil {
		return found, false, fmt.Errorf("delegation %s: %w", id, err)
	}
	return delegate.LocatedRecord{Record: r, Path: path}, true, nil
}
