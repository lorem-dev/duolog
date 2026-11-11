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

// TaskFolder is the run's task folder, "" when the record names none.
func (l LocatedRecord) TaskFolder() string {
	if l.TaskDir == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(filepath.Dir(l.Path)), filepath.FromSlash(l.TaskDir))
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
	out, unread, err := LeadRecordsReadable(reg, root, lead)
	if err == nil && len(unread) > 0 {
		return nil, unread[0].Err
	}
	return out, err
}

// UnreadFolder is the delegations folder of a work project that could not be
// read, and why.
type UnreadFolder struct {
	WorkRoot string
	Err      error
}

// LeadRecordsReadable is LeadRecords that skips the delegations folder of
// another work project than root when it cannot be read, and returns it in
// unread, so that one project's folder does not hide the others. root's own
// folder that cannot be read is still an error.
func LeadRecordsReadable(reg *session.Registry, root, lead string) (records []LocatedRecord, unread []UnreadFolder, err error) {
	locations, err := reg.Delegations("")
	if err != nil {
		return nil, nil, err
	}
	own := checkoutKey(root)
	for _, work := range RecordRoots(reg, root, locations) {
		dir := ProjectRecordDir(work)
		found, err := ListRecords(dir, session.ProcessAlive)
		if err != nil {
			if checkoutKey(work) == own {
				return nil, nil, err
			}
			unread = append(unread, UnreadFolder{WorkRoot: work, Err: err})
			continue
		}
		records = appendLocated(records, dir, found, lead)
	}
	slices.SortFunc(records, func(a, b LocatedRecord) int {
		return cmp.Or(b.Started.Compare(a.Started), strings.Compare(b.ID, a.ID))
	})
	return records, unread, nil
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
