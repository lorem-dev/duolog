package telemetry

import (
	"encoding/json"
	"time"
)

// Status holds the facts `duolog telemetry status` shows: the switches, the
// root and this project's id, what is on disk, the last completed prune, the
// caps, this process's lost events and the protection checks. It names no
// record's content.
//
// Segments counts the days of each partition that hold a segment: a day with
// both a plain and a gzip copy counts once, as Read takes only the gzip one.
// OldestDay is the earliest of those days. Partitions counts the partition
// directories, the account's included. Bytes is every file under the root, as
// the cap counts it. LastPrune is zero when no prune has completed.
type Status struct {
	Enabled         bool       `json:"enabled"`
	ProjectEnabled  bool       `json:"project_enabled"`
	Root            string     `json:"root"`
	ProjectID       string     `json:"project_id"`
	OldestDay       string     `json:"oldest_day"`
	Bytes           int64      `json:"bytes"`
	Segments        int        `json:"segments"`
	Partitions      int        `json:"partitions"`
	LastPrune       time.Time  `json:"last_prune,omitzero"`
	RetentionDays   int        `json:"retention_days"`
	PruneEveryHours int        `json:"prune_every_hours"`
	MaxMegabytes    int        `json:"max_megabytes"`
	Health          Health     `json:"health"`
	Protection      Protection `json:"protection"`
}

// healthJSON and protectionJSON give Health and Protection snake_case keys.
type healthJSON struct {
	Dropped  uint64    `json:"dropped"`
	Failures uint64    `json:"failures"`
	Since    time.Time `json:"since"`
}

type protectionJSON struct {
	ModeFixed      bool   `json:"mode_fixed"`
	BackupVerified bool   `json:"backup_verified"`
	IndexVerified  bool   `json:"index_verified"`
	Refusal        string `json:"refusal"`
}

func (st Status) MarshalJSON() ([]byte, error) {
	type fields Status // without this method
	return json.Marshal(struct {
		fields
		Health     healthJSON     `json:"health"`
		Protection protectionJSON `json:"protection"`
	}{fields(st), healthJSON(st.Health), protectionJSON(st.Protection)})
}

// Status checks the root and reports on it. With collection on it runs the
// checks of Open: a too open root is tightened and reported, and the backup
// exclusion is written and read back. With collection off it changes
// nothing, and the backup exclusion reads as unverified. A refused root
// returns its refusal with a closed error and nothing counted. Status takes
// no lock, prunes nothing and creates no file; a store with no root on disk
// reports its settings only.
func (s *Store) Status() (Status, error) {
	st := Status{
		Enabled:         s.o.Enabled,
		ProjectEnabled:  s.o.ProjectEnabled,
		Root:            s.path,
		RetentionDays:   s.o.RetentionDays,
		PruneEveryHours: s.o.PruneEveryHours,
		MaxMegabytes:    s.o.MaxMegabytes,
		Health:          s.Health(),
		Protection:      Protection{Refusal: refusalNone},
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root == nil {
		return st, nil
	}
	var err error
	if s.o.Enabled {
		st.Protection, err = checkRoot(s.path)
	} else {
		st.Protection, _, err = inspectRoot(s.path)
	}
	if err != nil {
		return st, readCategory(err)
	}
	if err := sameDir(s.root, s.path); err != nil {
		st.Protection.Refusal = refusalOther
		return st, readCategory(err)
	}
	if s.o.Enabled {
		st.Protection.BackupVerified, _ = excludeBackup(s.path)
	}
	st.Protection.IndexVerified = indexExcluded(s.path)

	if id, ok, err := s.storedProject(); err == nil && ok {
		st.ProjectID = id
	}
	if info, err := s.root.Lstat(stampName); err == nil && regular(info) && info.ModTime().After(time.Unix(0, 0)) {
		st.LastPrune = info.ModTime().UTC()
	}
	if st.Bytes, err = rootBytes(s.root); err != nil {
		return st, readCategory(err)
	}
	projects, err := projectPartitions(s.root)
	if err != nil {
		return st, readCategory(err)
	}
	for _, p := range append([]string{accountPartition}, projects...) {
		// A partition that is missing or fails a check holds nothing.
		entries, err := listDir(s.root, "v1/"+p)
		if err != nil {
			continue
		}
		st.Partitions++
		days := map[string]bool{}
		for _, e := range entries {
			g := segmentName.FindStringSubmatch(e.Name())
			if g != nil && e.Type().IsRegular() && validDay(g[1]) {
				days[g[1]] = true
			}
		}
		st.Segments += len(days)
		for d := range days {
			if st.OldestDay == "" || d < st.OldestDay {
				st.OldestDay = d
			}
		}
	}
	return st, nil
}
