package runsnap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/load"
	"github.com/lorem-dev/duolog/internal/app/peer"
	"github.com/lorem-dev/duolog/internal/core/config"
)

var snapNow = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// absolutePath matches a string that starts as an absolute path does, on
// POSIX or Windows.
var absolutePath = regexp.MustCompile(`^(/|\\|[A-Za-z]:)`)

// eachString calls f with every string value in the JSON data.
func eachString(t *testing.T, data []byte, f func(string)) {
	t.Helper()
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			f(x)
		case []any:
			for _, e := range x {
				walk(e)
			}
		case map[string]any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(v)
}

func TestSnapshotPathsAreRelative(t *testing.T) {
	root := t.TempDir()
	g := Gathered{
		Root: root,
		Lead: &session.Lead{ID: "lead-1", Target: config.Target{Agent: "claude"}},
		Rex:  true,
		Peers: []peer.ListedPeer{{ID: "peer-1", State: peer.Ready, Agent: "claude", Role: "plan",
			WorkRoot: filepath.Join(root, "sub", ".worktrees", "a"), Task: ".duolog/t.md"}},
		Records: []delegate.LocatedRecord{{
			Record: delegate.Record{ID: "d-1", State: "running", WorkRoot: filepath.Join(root, "sub"), TaskDir: "tasks/task-x", Log: ".duolog/tasks/task-x/stdout.log"},
			Path:   filepath.Join(root, "sub", ".duolog", "delegations", "d-1.json"),
		}},
	}
	if got := g.Records[0].TaskFolder(); got != filepath.Join(root, "sub", ".duolog", "tasks", "task-x") {
		t.Fatalf("task folder %q", got)
	}
	s := g.Snapshot(snapNow)
	if len(s.Peers) != 1 || len(s.Runs) != 1 {
		t.Fatalf("peers %+v, runs %+v", s.Peers, s.Runs)
	}
	if p := s.Peers[0]; p.Worktree != "sub/.worktrees/a" || p.Task != "sub/.worktrees/a/.duolog/t.md" {
		t.Fatalf("peer %+v", p)
	}
	if r := s.Runs[0]; r.Worktree != "sub" || r.TaskDir != "sub/.duolog/tasks/task-x" {
		t.Fatalf("run %+v", r)
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	eachString(t, data, func(v string) {
		if absolutePath.MatchString(v) {
			t.Errorf("absolute path %q in %s", v, data)
		}
	})
}

func TestSnapshotNoLeadNoRex(t *testing.T) {
	s := Gathered{Root: t.TempDir()}.Snapshot(snapNow)
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Version     int             `json:"version"`
		GeneratedAt string          `json:"generated_at"`
		Lead        json.RawMessage `json:"lead"`
		Rex         struct {
			Available bool `json:"available"`
		} `json:"rex"`
		Peers json.RawMessage `json:"peers"`
		Runs  json.RawMessage `json:"runs"`
		Error *string         `json:"error"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 || got.GeneratedAt != "2026-01-02T03:04:05Z" || string(got.Lead) != "null" || got.Rex.Available ||
		string(got.Peers) != "[]" || string(got.Runs) != "[]" || got.Error != nil {
		t.Fatalf("snapshot %s", data)
	}
}

func TestSnapshotErrorNamesTheFailedPart(t *testing.T) {
	g := Gathered{Root: t.TempDir(), PeersErr: errors.New("rex down"), RecordsErr: errors.New("bad record")}
	if got := g.Snapshot(snapNow).Error; got != "peers: rex down; runs: bad record" {
		t.Fatalf("error %q", got)
	}
	// An error that names a folder of the project names it relative to the
	// root, in either spelling of the root.
	root := t.TempDir()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, spelling := range []string{root, resolved} {
		g = Gathered{Root: root,
			PeersErr:   fmt.Errorf("read %s: gone", spelling),
			RecordsErr: fmt.Errorf("open %s/sub/.duolog/delegations: permission denied", spelling)}
		s := g.Snapshot(snapNow)
		if s.Error != "peers: read .: gone; runs: open sub/.duolog/delegations: permission denied" {
			t.Fatalf("error %q", s.Error)
		}
		data, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), root) || strings.Contains(string(data), resolved) {
			t.Fatalf("root in %s", data)
		}
	}
}

// With no lead, the snapshot shows no peer and no run: it never reads the
// ports, so another lead's workers stay out.
func TestBuildWithoutALeadShowsNothing(t *testing.T) {
	f := newFake(t, false)
	s, err := Build(context.Background(), f.deps, Options{Now: snapNow})
	if err != nil {
		t.Fatal(err)
	}
	if s.Lead != nil || len(s.Peers) != 0 || len(s.Runs) != 0 || s.LineText != "" || len(f.records) != 0 || len(f.peersLead) != 0 || f.loads != 0 {
		t.Fatalf("snapshot %+v, records %v, peers %v, loads %d", s, f.records, f.peersLead, f.loads)
	}
}

func TestBuildWithALead(t *testing.T) {
	f := newFake(t, true)
	s, err := Build(context.Background(), f.deps, Options{LeadID: "lead-1", Now: snapNow})
	if err != nil {
		t.Fatal(err)
	}
	if s.Lead == nil || s.Lead.ID != "lead-1" || s.Lead.Agent != "codex" || len(s.Peers) != 1 || len(s.Runs) != 2 || s.Load.Queued != 1 {
		t.Fatalf("snapshot %+v", s)
	}
}

func TestSnapshotHasLog(t *testing.T) {
	root := t.TempDir()
	log := filepath.Join(root, ".duolog", "tasks", "task-a", "stdout.log")
	if err := os.MkdirAll(filepath.Dir(log), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(log, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	g := Gathered{Root: root, Records: []delegate.LocatedRecord{
		{Record: delegate.Record{ID: "d-a", State: "done", WorkRoot: root, Log: ".duolog/tasks/task-a/stdout.log"}},
		{Record: delegate.Record{ID: "d-b", State: "done", WorkRoot: root, Log: ".duolog/tasks/task-b/stdout.log"}},
		{Record: delegate.Record{ID: "d-c", State: "done", WorkRoot: root}},
	}}
	runs := g.Snapshot(snapNow).Runs
	if len(runs) != 3 || !runs[0].HasLog || runs[1].HasLog || runs[2].HasLog {
		t.Fatalf("runs %+v", runs)
	}
}

func TestSnapshotLoad(t *testing.T) {
	g := Gathered{Root: t.TempDir(), Load: load.Summary{
		Agents: []load.Agent{{Name: "claude", Running: 2, Idle: 1, Limit: 10, Live: 4}},
		Queued: 1, Window: 44, WindowKnown: true,
	}}
	l := g.Snapshot(snapNow).Load
	if len(l.Agents) != 1 || l.Queued != 1 || l.Window5h == nil || *l.Window5h != 44 || l.Weekly != nil {
		t.Fatalf("load %+v", l)
	}
	if a := l.Agents[0]; a.Agent != "claude" || a.Running != 2 || a.Idle != 1 || a.Limit != 10 || a.Live != 4 {
		t.Fatalf("agent %+v", a)
	}
}

func TestSnapshotCarriesLine(t *testing.T) {
	g := Gathered{Root: t.TempDir(), Rex: true,
		Peers:   []peer.ListedPeer{{ID: "peer-1", State: peer.Ready}},
		Records: []delegate.LocatedRecord{{Record: delegate.Record{ID: "d-1", State: "running"}}},
		Asks:    []Ask{{N: 1}},
	}
	s := g.Snapshot(snapNow)
	if s.LineText == "" || s.LineText != s.Line() {
		t.Fatalf("line %q, Line() %q", s.LineText, s.Line())
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Line string `json:"line"`
	}
	if err := json.Unmarshal(data, &got); err != nil || got.Line != "duolog 1 peer 1 run 1 ask" {
		t.Fatalf("line %q %v", got.Line, err)
	}
}
