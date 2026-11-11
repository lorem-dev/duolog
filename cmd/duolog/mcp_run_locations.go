package main

import (
	"cmp"
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

// locatedRecord is a delegation record and the file it was read from, in the
// delegations folder of the work project that ran it.
type locatedRecord struct {
	delegate.Record
	Path string
}

// delegationDir is the delegations folder of the work project whose main
// checkout is projectRoot.
func delegationDir(projectRoot string) string {
	return delegate.RecordDir(filepath.Join(projectRoot, ".duolog"))
}

// leadDelegationRecords reads the delegation records of the work projects the
// registry located runs in, and the server project's own records, which
// include runs from before locations were kept. A located project is read
// only while it shares the registry's lead home. lead filters by the record's
// lead, unless it is empty. The records are sorted newest first, by start and
// then id. A missing folder holds none; a folder that cannot be read is an
// error. Record paths come from where they were read, never from a record.
func leadDelegationRecords(reg *session.Registry, root, lead string) ([]locatedRecord, error) {
	locations, err := reg.Delegations("")
	if err != nil {
		return nil, err
	}
	var out []locatedRecord
	for _, work := range delegationRoots(reg, root, locations) {
		dir := delegationDir(work)
		records, err := delegate.ListRecords(dir, session.ProcessAlive)
		if err != nil {
			return nil, err
		}
		out = appendLocated(out, dir, records, lead)
	}
	slices.SortFunc(out, func(a, b locatedRecord) int {
		return cmp.Or(b.Started.Compare(a.Started), strings.Compare(b.ID, a.ID))
	})
	return out, nil
}

// delegationRoots checks each work project once, regardless of its run count.
func delegationRoots(reg *session.Registry, root string, locations []session.DelegationLocation) []string {
	var roots []string
	seen := map[string]bool{}
	for _, work := range []string{root, filepath.Dir(reg.Dir)} {
		key := checkoutKey(work)
		if !seen[key] {
			seen[key] = true
			roots = append(roots, work)
		}
	}
	for _, loc := range locations {
		key := checkoutKey(loc.WorkRoot)
		if seen[key] {
			continue
		}
		seen[key] = true
		if sharesLeadHome(reg, loc.WorkRoot) {
			roots = append(roots, loc.WorkRoot)
		}
	}
	return roots
}

func appendLocated(out []locatedRecord, dir string, records []delegate.Record, lead string) []locatedRecord {
	for _, r := range records {
		if lead == "" || r.Lead == lead {
			out = append(out, locatedRecord{Record: r, Path: filepath.Join(dir, r.ID+".json")})
		}
	}
	return out
}

// sharesLeadHome reports whether the project at root keeps its lead state in
// the registry's folder.
func sharesLeadHome(reg *session.Registry, root string) bool {
	return checkoutKey(project.LeadHome(root)) == checkoutKey(reg.Dir)
}

// mcpFindResult finds the run a delegate result call names: by id through
// its location first, else by id or task path among the lead home's records.
// A relative task path that more than one run has is an error that asks for
// the id or a worktree.
func mcpFindResult(reg *session.Registry, root string, in mcp.DelegateInput) (locatedRecord, error) {
	if found, ok, err := mcpResultByID(reg, root, in.TaskPath); err != nil || ok {
		return found, err
	}
	records, err := leadDelegationRecords(reg, root, "")
	if err != nil {
		return locatedRecord{}, err
	}
	matches, err := mcpResultMatches(records, in)
	if err != nil {
		return locatedRecord{}, err
	}
	switch len(matches) {
	case 0:
		return locatedRecord{}, fmt.Errorf("no delegation with task_path %s; status lists them", in.TaskPath)
	case 1:
		return matches[0], nil
	}
	var ids []string
	for _, m := range matches {
		ids = append(ids, m.ID+" in "+m.WorkRoot)
	}
	return locatedRecord{}, fmt.Errorf("task_path %s names more than one delegation (%s); pass its id, or the worktree", in.TaskPath, strings.Join(ids, ", "))
}

// mcpResultMatches keeps the records whose id or task file is in.TaskPath,
// in in.Worktree when the call names one.
func mcpResultMatches(records []locatedRecord, in mcp.DelegateInput) ([]locatedRecord, error) {
	key := in.TaskPath
	var work string
	if in.Worktree != "" {
		resolved, err := mcpPeerWorktree(in.Worktree)
		if err != nil {
			return nil, err
		}
		work = checkoutKey(resolved)
	}
	var matches []locatedRecord
	for _, r := range records {
		if work != "" && checkoutKey(r.WorkRoot) != work {
			continue
		}
		if r.ID == key || r.TaskPath == key || filepath.IsAbs(key) && checkoutKey(filepath.Join(r.WorkRoot, filepath.FromSlash(r.TaskPath))) == checkoutKey(key) {
			matches = append(matches, r)
		}
	}
	return matches, nil
}

// mcpResultByID reads the record of run id at its recorded location, or in
// the server project when none is recorded. ok is false when id is no run id
// of either; a record there that cannot be read is an error.
func mcpResultByID(reg *session.Registry, root, id string) (found locatedRecord, ok bool, err error) {
	if id == "" || strings.ContainsAny(id, `/\.`) {
		return found, false, nil
	}
	locations, err := reg.Delegations("")
	if err != nil {
		return found, false, err
	}
	path := filepath.Join(delegationDir(root), id+".json")
	if i := slices.IndexFunc(locations, func(loc session.DelegationLocation) bool { return loc.ID == id }); i >= 0 {
		if !sharesLeadHome(reg, locations[i].WorkRoot) {
			return found, false, fmt.Errorf("delegation %s ran in %s, which no longer shares this lead", id, locations[i].WorkRoot)
		}
		path = filepath.Join(delegationDir(locations[i].WorkRoot), id+".json")
	} else if _, statErr := os.Stat(path); errors.Is(statErr, fs.ErrNotExist) {
		path = filepath.Join(delegationDir(filepath.Dir(reg.Dir)), id+".json")
		if _, statErr := os.Stat(path); errors.Is(statErr, fs.ErrNotExist) {
			return found, false, nil
		}
	}
	r, err := delegate.ReadRecord(path, session.ProcessAlive)
	if err != nil {
		return found, false, fmt.Errorf("delegation %s: %w", id, err)
	}
	return locatedRecord{Record: r, Path: path}, true, nil
}
