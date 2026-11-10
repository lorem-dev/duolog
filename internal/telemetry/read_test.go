package telemetry

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

const day = 24 * time.Hour

// plant writes data as a file under root, making its directories, the way a
// hand or another program could; nothing checks it.
func plant(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func gzipped(t *testing.T, data []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func quotaEpoch(epoch int64) Quota {
	q := sampleQuota()
	q.Epoch = epoch
	return q
}

func quotaLine(t *testing.T, epoch int64, at time.Time) []byte {
	t.Helper()
	line, err := Encode(quotaEpoch(epoch), testNames, at)
	if err != nil {
		t.Fatal(err)
	}
	return line
}

func runAttempt(attempt int) Run {
	r := sampleRun()
	r.Attempt = attempt
	return r
}

// rawLine marshals v as it is, without the checks of Encode, the way a
// hand-planted line could look.
func rawLine(t *testing.T, v any) []byte {
	t.Helper()
	line, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return append(line, '\n')
}

func mustRead(t *testing.T, s *Store, q Query) []Event {
	t.Helper()
	events, err := s.Read(q)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return events
}

// epochs lists the quota epochs and run attempts in the order Read gave
// them; runs count as negative attempts so both fit one list.
func epochs(t *testing.T, events []Event) []int64 {
	t.Helper()
	out := []int64{}
	for _, e := range events {
		switch e := e.(type) {
		case Quota:
			out = append(out, e.Epoch)
		case Run:
			out = append(out, -int64(e.Attempt))
		default:
			t.Fatalf("unexpected %T", e)
		}
	}
	return out
}

// readFixture opens a store with collection on, so the root and salt
// exist, and returns it with the root.
func readFixture(t *testing.T) (*Store, string) {
	t.Helper()
	noOverride(t)
	base := tempBase(t)
	s := mustOpenStore(t, storeOptions(base))
	return s, storeRoot(t, base)
}

func setReadLimits(t *testing.T, segment, total int64) {
	t.Helper()
	oldSegment, oldTotal := segmentLimit, readLimit
	segmentLimit, readLimit = segment, total
	t.Cleanup(func() { segmentLimit, readLimit = oldSegment, oldTotal })
}

func TestReadTimeBounds(t *testing.T) {
	type planted struct {
		epoch int64
		at    time.Time
		day   string // segment day; empty is the UTC day of at
	}
	now := testNow
	cases := []struct {
		name      string
		retention int
		since     time.Time
		events    []planted
		want      []int64
	}{
		{"exactly the retention cutoff", 30, time.Time{}, []planted{
			{1, now.Add(-30 * day), ""},
			{2, now.Add(-30*day - time.Second), ""},
			{3, now, ""},
		}, []int64{1, 3}},
		{"user retention one day", 1, time.Time{}, []planted{
			{1, now.Add(-day), ""},
			{2, now.Add(-day - time.Second), ""},
			{3, now.Add(-2 * day), ""},
			{4, now, ""},
		}, []int64{1, 4}},
		{"retention over 30 days stays 30", 90, time.Time{}, []planted{
			{1, now.Add(-30 * day), ""},
			{2, now.Add(-30*day - time.Second), ""},
		}, []int64{1}},
		{"exactly one day ahead", 30, time.Time{}, []planted{
			{1, now.Add(day), ""},
			{2, now.Add(day + time.Second), ""},
			{3, now.Add(40 * day), ""},
		}, []int64{1}},
		{"backward clock", 30, time.Time{}, []planted{
			// Written before the clock was set back: a later t in a
			// segment named for a later day than now.
			{1, now.Add(20 * time.Hour), "2026-11-06"},
			{2, now.Add(3 * day), "2026-11-08"},
			// Written after it, into the same later segment.
			{3, now.Add(-2 * time.Hour), "2026-11-06"},
			{4, now.Add(-time.Hour), ""},
		}, []int64{3, 4, 1}},
		{"timestamp different from segment day", 30, time.Time{}, []planted{
			{1, now.Add(-time.Hour), "2026-09-01"},
			{2, now.Add(-40 * day), testDay},
			{3, now, "2026-12-25"},
		}, []int64{1, 3}},
		{"since narrows", 30, now.Add(-time.Hour), []planted{
			{1, now.Add(-time.Hour), ""},
			{2, now.Add(-time.Hour - time.Second), ""},
			{3, now, ""},
		}, []int64{1, 3}},
		{"since cannot widen retention", 30, now.Add(-90 * day), []planted{
			{1, now.Add(-30 * day), ""},
			{2, now.Add(-30*day - time.Second), ""},
			{3, now.Add(-89 * day), ""},
		}, []int64{1}},
		{"since cannot widen user retention", 1, now.Add(-10 * day), []planted{
			{1, now.Add(-day), ""},
			{2, now.Add(-2 * day), ""},
		}, []int64{1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			noOverride(t)
			base := tempBase(t)
			o := storeOptions(base)
			o.RetentionDays = c.retention
			s := mustOpenStore(t, o)
			root := storeRoot(t, base)
			segments := map[string][]byte{}
			for _, p := range c.events {
				d := p.day
				if d == "" {
					d = p.at.UTC().Format(time.DateOnly)
				}
				segments[d] = append(segments[d], quotaLine(t, p.epoch, p.at)...)
			}
			for d, data := range segments {
				plant(t, accountSegment(root, d), data)
			}
			if got := epochs(t, mustRead(t, s, Query{Since: c.since})); !slices.Equal(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}

	t.Run("appended across a clock set back", func(t *testing.T) {
		noOverride(t)
		base := tempBase(t)
		clock := testNow.Add(20 * time.Hour)
		o := storeOptions(base)
		o.Now = func() time.Time { return clock }
		writer := mustOpenStore(t, o)
		mustAppend(t, writer, quotaEpoch(1))
		clock = testNow.Add(-time.Hour)
		mustAppend(t, writer, quotaEpoch(2))
		clock = testNow.Add(3 * day)
		mustAppend(t, writer, quotaEpoch(3))

		reader := mustOpenStore(t, storeOptions(base))
		if got := epochs(t, mustRead(t, reader, Query{})); !slices.Equal(got, []int64{2, 1}) {
			t.Errorf("got %v, want [2 1]", got)
		}
	})
}

func TestReadSegments(t *testing.T) {
	at := func(d string) time.Time {
		parsed, err := time.Parse(time.DateOnly, d)
		if err != nil {
			t.Fatal(err)
		}
		return parsed.Add(12 * time.Hour)
	}

	// Mutation killed: blindly preferring gzip hides an independent plain segment.
	t.Run("paired segments reconcile", func(t *testing.T) {
		s, root := readFixture(t)
		both := append(quotaLine(t, 1, at("2026-11-01")), quotaLine(t, 2, at("2026-11-01"))...)
		plant(t, accountSegment(root, "2026-11-01"), both)
		plant(t, accountSegment(root, "2026-11-01")+".gz", gzipped(t, both))
		plant(t, accountSegment(root, "2026-11-02"), quotaLine(t, 10, at("2026-11-02")))
		plant(t, accountSegment(root, "2026-11-02")+".gz", gzipped(t, quotaLine(t, 11, at("2026-11-02"))))
		plant(t, accountSegment(root, "2026-11-03")+".gz", gzipped(t, quotaLine(t, 20, at("2026-11-03"))))
		plant(t, accountSegment(root, "2026-11-04"), quotaLine(t, 30, at("2026-11-04")))
		plant(t, accountSegment(root, "2026-11-04")+".gz.tmp", gzipped(t, quotaLine(t, 40, at("2026-11-04"))))
		if got, want := epochs(t, mustRead(t, s, Query{})), []int64{1, 2, 11, 10, 20, 30}; !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("broken last line", func(t *testing.T) {
		s, root := readFixture(t)
		whole := quotaLine(t, 2, at("2026-11-02"))
		// The last line is valid JSON but has no newline: an interrupted
		// write, never read.
		plant(t, accountSegment(root, "2026-11-02"),
			append(quotaLine(t, 1, at("2026-11-02")), whole[:len(whole)-1]...))
		half := quotaLine(t, 4, at("2026-11-03"))
		plant(t, accountSegment(root, "2026-11-03")+".gz",
			gzipped(t, append(quotaLine(t, 3, at("2026-11-03")), half[:len(half)/2]...)))
		plant(t, accountSegment(root, "2026-11-04"), half[:len(half)/2])
		plant(t, accountSegment(root, "2026-11-05"), nil)
		if got, want := epochs(t, mustRead(t, s, Query{})), []int64{1, 3}; !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("unknown version and type", func(t *testing.T) {
		s, root := readFixture(t)
		line := quotaLine(t, 1, testNow)
		replace := func(old, new string) []byte {
			if !bytes.Contains(line, []byte(old)) {
				t.Fatalf("%q not in %q", old, line)
			}
			return bytes.Replace(line, []byte(old), []byte(new), 1)
		}
		var data []byte
		for _, bad := range [][]byte{
			replace(`"v":1,`, `"v":2,`),
			replace(`"v":1,`, `"v":0,`),
			replace(`"v":1,`, `"v":"1",`),
			replace(`"v":1,`, `"v":1.0,`),
			replace(`"v":1,`, ``),
			replace(`"type":"quota"`, `"type":"bogus"`),
			replace(`"type":"quota"`, `"type":"Quota"`),
			replace(`"type":"quota"`, `"type":""`),
			replace(`"type":"quota",`, ``),
		} {
			data = append(data, bad...)
		}
		data = append(data, quotaLine(t, 9, testNow)...)
		plant(t, accountSegment(root, testDay), data)
		if got, want := epochs(t, mustRead(t, s, Query{})), []int64{9}; !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("malformed complete lines", func(t *testing.T) {
		s, root := readFixture(t)
		line := quotaLine(t, 1, testNow)
		body := line[:len(line)-1]
		long := append(slices.Clone(body[:len(body)-1]), []byte(strings.Repeat(" ", maxLine))...)
		long = append(long, "}\n"...)
		var data []byte
		for _, bad := range []string{
			"not json\n",
			"{}\n",
			"[]\n",
			"null\n",
			"\x00\n",
			"\n",
			string(body[:len(body)/2]) + "\n",
			string(body) + ` {"v":1}` + "\n",
			string(body) + "x\n",
			string(body[:len(body)-1]) + `,"note":"secret"}` + "\n",
			string(body[:len(body)-1]) + `,"windows":"none"}` + "\n",
			string(long),
		} {
			data = append(data, bad...)
		}
		data = append(data, quotaLine(t, 9, testNow)...)
		plant(t, accountSegment(root, testDay), data)
		if got, want := epochs(t, mustRead(t, s, Query{})), []int64{9}; !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("hand-planted values revalidated", func(t *testing.T) {
		s, root := readFixture(t)
		stamp := func(typ string) Common { return Common{V: 1, T: testNow.Unix(), Type: typ} }
		id, err := NewID()
		if err != nil {
			t.Fatal(err)
		}

		quota := sampleQuota()
		quota.Common = stamp("quota")
		quota.Agent = "codex\x1b[31m"
		quota.Plan = "secret-token-0123"
		quota.Windows[0].Family = "family\nx"
		quota.Windows[1].Kind = `{"v":1}`
		wantQuota := sampleQuota()
		wantQuota.Common = stamp("quota")
		wantQuota.Agent, wantQuota.Plan = other, other
		wantQuota.Windows[0].Family, wantQuota.Windows[1].Kind = other, other

		badUsed := sampleQuota()
		badUsed.Common = stamp("quota")
		badUsed.Windows[1].UsedBP = maxBasisPoints + 1
		badEpoch := sampleQuota()
		badEpoch.Common = stamp("quota")
		badEpoch.Epoch = maxSafe + 1
		badScore := sampleRoute()
		badScore.Common = stamp("route")
		badScore.ScoreMin = 1e300

		route := sampleRoute()
		route.Common = stamp("route")
		route.Role = "admin"
		route.Chosen.Effort = "turbo"
		wantRoute := sampleRoute()
		wantRoute.Common = stamp("route")
		wantRoute.Role, wantRoute.Chosen.Effort = other, other

		var account []byte
		for _, v := range []any{quota, badUsed, badEpoch, badScore, route} {
			account = append(account, rawLine(t, v)...)
		}
		// t is a whole number of seconds.
		account = append(account, bytes.Replace(rawLine(t, wantQuota),
			[]byte(`"t":`+itoa(testNow.Unix())), []byte(`"t":`+itoa(testNow.Unix())+".5"), 1)...)
		plant(t, accountSegment(root, testDay), account)

		run := sampleRun()
		run.Common = stamp("run")
		run.Run = id
		run.Role = "admin"
		run.Family = "family-x"
		wantRun := sampleRun()
		wantRun.Common = stamp("run")
		wantRun.Run = id
		wantRun.Role, wantRun.Family = other, other

		var project []byte
		for _, mutate := range []func(r *Run){
			func(r *Run) { r.In = -1 },
			func(r *Run) { r.Cached = r.In + 1 },
			func(r *Run) { r.Attempt = 0 },
			func(r *Run) { r.Secs = maxInt31 + 1 },
			func(r *Run) { r.USDmicro = maxSafe + 1 },
		} {
			bad := sampleRun()
			bad.Common = stamp("run")
			mutate(&bad)
			project = append(project, rawLine(t, bad)...)
		}
		project = append(project, rawLine(t, run)...)
		peer := samplePeer()
		peer.Common = stamp("peer")
		project = append(project, rawLine(t, peer)...)
		// An id that is not 32 lowercase hex characters.
		gate := map[string]any{"v": 1, "t": testNow.Unix(), "type": "gate", "run": "NOT-AN-ID",
			"kind": "read", "rule_class": "outside", "answer": "deny", "by": "person", "wait_secs": 7}
		project = append(project, rawLine(t, gate)...)
		plant(t, projectSegment(t, root, testDay), project)

		got := mustRead(t, s, Query{})
		want := []Event{wantQuota, wantRoute, wantRun, peer}
		if !sameEvents(got, want) {
			t.Errorf("got\n%#v\nwant\n%#v", got, want)
		}
	})

	t.Run("events kept to their partition", func(t *testing.T) {
		s, root := readFixture(t)
		plant(t, accountSegment(root, testDay), append(
			mustEncode(t, runAttempt(7), testNames), quotaLine(t, 1, testNow)...))
		plant(t, projectSegment(t, root, testDay), append(
			quotaLine(t, 8, testNow), mustEncode(t, runAttempt(2), testNames)...))
		if got, want := epochs(t, mustRead(t, s, Query{})), []int64{1, -2}; !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("symlinked segment", func(t *testing.T) {
		s, root := readFixture(t)
		outside := filepath.Join(filepath.Dir(root), "outside.ndjson")
		plant(t, outside, quotaLine(t, 50, testNow))
		plant(t, accountSegment(root, "2026-11-04"), quotaLine(t, 1, testNow))
		if err := os.MkdirAll(filepath.Join(root, "v1", "_account"), 0o700); err != nil {
			t.Fatal(err)
		}
		symlinkOrSkip(t, outside, accountSegment(root, testDay))
		// A symlinked .gz does not hide the plain segment beside it.
		plant(t, accountSegment(root, "2026-11-03"), quotaLine(t, 60, testNow))
		symlinkOrSkip(t, outside, accountSegment(root, "2026-11-03")+".gz")
		if got, want := epochs(t, mustRead(t, s, Query{})), []int64{60, 1}; !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("hard-linked segment", func(t *testing.T) {
		s, root := readFixture(t)
		outside := filepath.Join(filepath.Dir(root), "outside.ndjson")
		plant(t, outside, quotaLine(t, 50, testNow))
		plant(t, accountSegment(root, "2026-11-04"), quotaLine(t, 1, testNow))
		if err := os.Link(outside, accountSegment(root, testDay)); err != nil {
			t.Fatal(err)
		}
		if got, want := epochs(t, mustRead(t, s, Query{})), []int64{1}; !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("symlinked partition and odd entries", func(t *testing.T) {
		s, root := readFixture(t)
		outside := filepath.Join(filepath.Dir(root), "outside")
		plant(t, filepath.Join(outside, testDay+".ndjson"), mustEncode(t, runAttempt(50), testNames))
		if err := os.MkdirAll(filepath.Join(root, "v1"), 0o700); err != nil {
			t.Fatal(err)
		}
		symlinkOrSkip(t, outside, filepath.Join(root, "v1", otherID))
		for _, name := range []string{"ABCDEF0123456789", "_other", "0123", "." + testID} {
			plant(t, filepath.Join(root, "v1", name, testDay+".ndjson"), mustEncode(t, runAttempt(51), testNames))
		}
		if err := os.MkdirAll(accountSegment(root, "2026-11-04"), 0o700); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"2026-13-01.ndjson", "2026-11-05.json", "notes.ndjson"} {
			plant(t, filepath.Join(root, "v1", "_account", name), quotaLine(t, 52, testNow))
		}
		plant(t, accountSegment(root, testDay), quotaLine(t, 1, testNow))
		if got, want := epochs(t, mustRead(t, s, Query{AllProjects: true})), []int64{1}; !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("bounded work", func(t *testing.T) {
		s, root := readFixture(t)
		const limit = 4096
		setReadLimits(t, limit, 1<<30)
		// complete counts the lines that end within the limit.
		complete := func(lines [][]byte) int {
			n, size := 0, 0
			for _, l := range lines {
				if size += len(l); size > limit {
					break
				}
				n++
			}
			return n
		}
		var plain, packed [][]byte
		var want []int64
		for i := range 100 {
			plain = append(plain, quotaLine(t, int64(1+i), at("2026-11-01")))
			packed = append(packed, quotaLine(t, int64(101+i), at("2026-11-02")))
		}
		for i := range complete(plain) {
			want = append(want, int64(1+i))
		}
		for i := range complete(packed) {
			want = append(want, int64(101+i))
		}
		if len(want) == 0 || len(want) >= 100 {
			t.Fatalf("bad fixture: %d lines within the limit", len(want))
		}
		plant(t, accountSegment(root, "2026-11-01"), bytes.Join(plain, nil))
		plant(t, accountSegment(root, "2026-11-02")+".gz", gzipped(t, bytes.Join(packed, nil)))
		// A gzip bomb inflates no further than the limit; the next
		// segment is still read.
		plant(t, accountSegment(root, "2026-11-03")+".gz", gzipped(t, make([]byte, 64<<20)))
		// A long line is skipped without holding it; the scanner carries on.
		long := append(bytes.Repeat([]byte("x"), 3*maxLine), '\n')
		plant(t, accountSegment(root, "2026-11-04"), append(long, quotaLine(t, 300, at("2026-11-04"))...))
		want = append(want, 300)
		if got := epochs(t, mustRead(t, s, Query{Since: at("2026-11-01").Add(-day)})); !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("bounded total", func(t *testing.T) {
		s, root := readFixture(t)
		var lines [][]byte
		for i := range 30 {
			lines = append(lines, quotaLine(t, int64(1+i), testNow))
		}
		plant(t, accountSegment(root, "2026-11-01"), bytes.Join(lines[:10], nil))
		plant(t, accountSegment(root, "2026-11-02"), bytes.Join(lines[10:20], nil))
		plant(t, accountSegment(root, "2026-11-03"), bytes.Join(lines[20:], nil))
		// Fifteen lines' worth: the first segment, half the second, none
		// of the third.
		setReadLimits(t, 1<<20, int64(len(bytes.Join(lines[:15], nil))))
		var want []int64
		for i := range 15 {
			want = append(want, int64(1+i))
		}
		if got := epochs(t, mustRead(t, s, Query{})); !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("project and all projects", func(t *testing.T) {
		noOverride(t)
		base := tempBase(t)
		here := mustOpenStore(t, storeOptions(base))
		mustAppend(t, here, runAttempt(1))
		mustAppend(t, here, quotaEpoch(1))
		o := storeOptions(base)
		o.CanonicalStateRoot = "/work/other"
		there := mustOpenStore(t, o)
		mustAppend(t, there, runAttempt(2))

		for _, c := range []struct {
			name string
			s    *Store
			q    Query
			want []int64
		}{
			{"here", here, Query{}, []int64{-1, 1}},
			{"there", there, Query{}, []int64{1, -2}},
			{"all", here, Query{AllProjects: true}, []int64{-1, 1, -2}},
		} {
			got := epochs(t, mustRead(t, c.s, c.q))
			slices.Sort(got)
			slices.Sort(c.want)
			if !slices.Equal(got, c.want) {
				t.Errorf("%s: got %v, want %v", c.name, got, c.want)
			}
		}
	})
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

// sameEvents compares events by value, ignoring nil against empty slices.
func sameEvents(got, want []Event) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		g, _ := json.Marshal(got[i])
		w, _ := json.Marshal(want[i])
		if reflect.TypeOf(got[i]) != reflect.TypeOf(want[i]) || !bytes.Equal(g, w) {
			return false
		}
	}
	return true
}

// backupExcluded reports a backup exclusion kept outside the files of the
// root; macOS sets it, elsewhere CACHEDIR.TAG shows in the tree.
var backupExcluded = func(*testing.T, string) bool { return false }

// treeState is what a write would change under a directory: names, types,
// modes, sizes and modification times.
func treeState(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		out[rel] = info.Mode().String() + " " + itoa(info.Size()) + " " + info.ModTime().UTC().Format(time.RFC3339Nano)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestOpenOffWritesNothing(t *testing.T) {
	for _, c := range []struct {
		name string
		env  string
	}{{"user off", ""}, {"env off", "off"}} {
		t.Run(c.name, func(t *testing.T) {
			noOverride(t)
			base := tempBase(t)
			root := storeRoot(t, base)
			// An existing root that no store has protected yet: too open,
			// no salt, no backup exclusion.
			if err := os.MkdirAll(root, 0o700); err != nil {
				t.Fatal(err)
			}
			plant(t, accountSegment(root, testDay), quotaLine(t, 1, testNow))
			loosen(t, root)
			before := treeState(t, base)

			o := storeOptions(base)
			if c.env != "" {
				t.Setenv(envSwitch, c.env)
			} else {
				o.Enabled = false
			}
			s, err := Open(o)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			if err := s.Append(sampleQuota()); err != nil {
				t.Errorf("Append: %v", err)
			}
			if got := epochs(t, mustRead(t, s, Query{AllProjects: true})); !slices.Equal(got, []int64{1}) {
				t.Errorf("Read: %v, want [1]", got)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			if after := treeState(t, base); !reflect.DeepEqual(after, before) {
				t.Errorf("off changed the store:\nbefore %v\nafter  %v", before, after)
			}
			if !isLoose(t, root) {
				t.Error("off tightened the root")
			}
			if backupExcluded(t, root) {
				t.Error("off set the backup exclusion")
			}
		})
	}

	t.Run("on protects the same root", func(t *testing.T) {
		noOverride(t)
		base := tempBase(t)
		root := storeRoot(t, base)
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
		loosen(t, root)
		mustOpenStore(t, storeOptions(base)).Close()
		if isLoose(t, root) {
			t.Error("on left the root open")
		}
	})
}
