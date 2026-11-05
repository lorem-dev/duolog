package telemetry

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"slices"
	"testing"
	"time"
)

func setFaultHook(t *testing.T, hook func(step string) error) {
	t.Helper()
	faultHook = hook
	t.Cleanup(func() { faultHook = nil })
}

func gunzipFile(t *testing.T, path string) []byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestCompressCrashSteps(t *testing.T) {
	const yesterday = "2026-11-04"
	injected := errors.New("injected crash")
	steps := []struct {
		step               string
		tmp, gz, plainLeft bool // the files a crash after this step leaves
	}{
		{"create", true, false, true},
		{"write", true, false, true},
		{"sync", true, false, true},
		{"close", true, false, true},
		{"rename", false, true, true},
		{"remove", false, true, false},
	}
	for _, c := range steps {
		t.Run(c.step, func(t *testing.T) {
			s, root := readFixture(t)
			plain := accountSegment(root, yesterday)
			data := append(quotaLine(t, 1, testNow.Add(-day)), quotaLine(t, 2, testNow.Add(-day+time.Hour))...)
			plant(t, plain, data)
			today := accountSegment(root, testDay)
			plant(t, today, quotaLine(t, 3, testNow))

			setFaultHook(t, func(step string) error {
				if step == c.step {
					return injected
				}
				return nil
			})
			m := mustPrune(t, s, true)
			if m.Compressed != 0 || m.Skipped != 1 {
				t.Errorf("crashed pass: %+v, want 1 skipped", m)
			}
			if exists(t, plain+".gz.tmp") != c.tmp || exists(t, plain+".gz") != c.gz || exists(t, plain) != c.plainLeft {
				t.Errorf("after the crash: tmp %v, gz %v, plain %v; want %v, %v, %v",
					exists(t, plain+".gz.tmp"), exists(t, plain+".gz"), exists(t, plain), c.tmp, c.gz, c.plainLeft)
			}
			// Readers see one complete copy of the day.
			if got, want := epochs(t, mustRead(t, s, Query{})), []int64{1, 2, 3}; !slices.Equal(got, want) {
				t.Errorf("read after the crash %v, want %v", got, want)
			}

			faultHook = nil
			m = mustPrune(t, s, true)
			if m.Skipped != 0 {
				t.Errorf("next pass: %+v", m)
			}
			if exists(t, plain+".gz.tmp") || exists(t, plain) || !exists(t, plain+".gz") {
				t.Errorf("after the next prune: tmp %v, plain %v, gz %v",
					exists(t, plain+".gz.tmp"), exists(t, plain), exists(t, plain+".gz"))
			}
			if got := gunzipFile(t, plain+".gz"); !bytes.Equal(got, data) {
				t.Errorf("compressed copy %q, want %q", got, data)
			}
			assertTight(t, plain+".gz", false)
			if got, want := epochs(t, mustRead(t, s, Query{})), []int64{1, 2, 3}; !slices.Equal(got, want) {
				t.Errorf("read after the next prune %v, want %v", got, want)
			}
			// Today's segment stays plain.
			if !exists(t, today) || exists(t, today+".gz") {
				t.Error("today's segment was compressed")
			}
		})
	}

	t.Run("failure keeps the original", func(t *testing.T) {
		s, root := readFixture(t)
		plain := accountSegment(root, yesterday)
		data := quotaLine(t, 1, testNow.Add(-day))
		plant(t, plain, data)
		setCloseFile(t, func(f *os.File) error {
			f.Close()
			return errors.New("injected close failure")
		})
		if m := mustPrune(t, s, true); m.Compressed != 0 || m.Skipped != 1 {
			t.Errorf("failed pass: %+v", m)
		}
		if got, err := os.ReadFile(plain); err != nil || !bytes.Equal(got, data) {
			t.Errorf("original %q, %v", got, err)
		}
		if exists(t, plain+".gz") {
			t.Error("a failed compression published its copy")
		}
	})

	t.Run("today is refused", func(t *testing.T) {
		s, root := readFixture(t)
		today := accountSegment(root, testDay)
		plant(t, today, quotaLine(t, 3, testNow))
		s.mu.Lock()
		err := s.compress(accountPartition, testDay)
		s.mu.Unlock()
		if !errors.Is(err, errRefused) || exists(t, today+".gz") {
			t.Errorf("compress today: %v", err)
		}
	})

	t.Run("a day written again is joined", func(t *testing.T) {
		// A clock set back wrote a closed day again beside its .gz.
		s, root := readFixture(t)
		plain := accountSegment(root, yesterday)
		whole := quotaLine(t, 1, testNow.Add(-day))
		cut := quotaLine(t, 4, testNow.Add(-day))
		cut = cut[:len(cut)/2] // a crash cut the last line
		plant(t, plain+".gz", gzipped(t, append(slices.Clone(whole), cut...)))
		again := quotaLine(t, 2, testNow.Add(-day+time.Hour))
		plant(t, plain, again)
		if m := mustPrune(t, s, true); m.Compressed != 1 || m.Skipped != 0 {
			t.Errorf("join: %+v", m)
		}
		want := slices.Concat(whole, cut, []byte("\n"), again)
		if got := gunzipFile(t, plain+".gz"); !bytes.Equal(got, want) {
			t.Errorf("joined copy %q, want %q", got, want)
		}
		if got, want := epochs(t, mustRead(t, s, Query{})), []int64{1, 2}; !slices.Equal(got, want) {
			t.Errorf("read after the join %v, want %v", got, want)
		}
		if exists(t, plain) {
			t.Error("plain file kept after the join")
		}
	})
}
