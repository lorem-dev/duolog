package delegate

import (
	"cmp"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
	"github.com/lorem-dev/duolog/internal/project"
)

// LocatedRecord is a delegation record and the file it was read from, in the
// delegations folder of the work project that ran it.
type LocatedRecord struct {
	Record
	Path string
}

// ProjectRecordDir is the delegations folder of the work project whose main
// checkout is projectRoot.
func ProjectRecordDir(projectRoot string) string {
	return RecordDir(filepath.Join(projectRoot, ".duolog"))
}

// LeadRecords reads the delegation records of the work projects the registry
// located runs in, and the server project's own records, which include runs
// from before locations were kept. A located project is read only while it
// shares the registry's lead home. lead filters by the record's lead, unless
// it is empty. The records are sorted newest first, by start and then id. A
// missing folder holds none; a folder that cannot be read is an error. Record
// paths come from where they were read, never from a record.
func LeadRecords(reg *session.Registry, root, lead string) ([]LocatedRecord, error) {
	locations, err := reg.Delegations("")
	if err != nil {
		return nil, err
	}
	var out []LocatedRecord
	for _, work := range RecordRoots(reg, root, locations) {
		dir := ProjectRecordDir(work)
		records, err := ListRecords(dir, session.ProcessAlive)
		if err != nil {
			return nil, err
		}
		out = appendLocated(out, dir, records, lead)
	}
	slices.SortFunc(out, func(a, b LocatedRecord) int {
		return cmp.Or(b.Started.Compare(a.Started), strings.Compare(b.ID, a.ID))
	})
	return out, nil
}

// RecordRoots checks each work project once, regardless of its run count.
func RecordRoots(reg *session.Registry, root string, locations []session.DelegationLocation) []string {
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
		if SharesLeadHome(reg, loc.WorkRoot) {
			roots = append(roots, loc.WorkRoot)
		}
	}
	return roots
}

func appendLocated(out []LocatedRecord, dir string, records []Record, lead string) []LocatedRecord {
	for _, r := range records {
		if lead == "" || r.Lead == lead {
			out = append(out, LocatedRecord{Record: r, Path: filepath.Join(dir, r.ID+".json")})
		}
	}
	return out
}

// SharesLeadHome reports whether the project at root keeps its lead state in
// the registry's folder.
func SharesLeadHome(reg *session.Registry, root string) bool {
	return checkoutKey(project.LeadHome(root)) == checkoutKey(reg.Dir)
}

func checkoutKey(dir string) string {
	return pathutil.Clean(pathutil.Resolved(dir))
}
