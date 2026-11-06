package peer

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/lorem-dev/duolog/internal/testbin"
)

func readState(t *testing.T, r *rig) (sessions, bus []byte) {
	t.Helper()
	dir := filepath.Join(r.root, ".duolog")
	sessions, err := os.ReadFile(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	bus, err = os.ReadFile(filepath.Join(dir, "bus.ndjson"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return sessions, bus
}

func TestShowIsPassive(t *testing.T) {
	r := newRig(t, baseScript())
	e := r.ready()
	sessions, bus := readState(t, r)
	r.rex(withScreens(queuedQuestion + working()))
	got, err := Show(r.ctx, e.ID, 40)
	if err != nil || got.State != Question {
		t.Fatalf("%+v %v", got, err)
	}
	for _, c := range r.calls() {
		if joined := strings.Join(c, " "); !strings.HasPrefix(joined, "block inspect") && !strings.HasPrefix(joined, "wait") && !strings.HasPrefix(joined, "capture") {
			t.Fatalf("show ran rex %q", joined)
		}
	}
	if len(r.keys()) != 0 {
		t.Fatalf("keys pressed: %v", r.keys())
	}
	sessions2, bus2 := readState(t, r)
	if !bytes.Equal(sessions, sessions2) || !bytes.Equal(bus, bus2) {
		t.Fatal("show changed the registry or the bus")
	}
}

func TestShowNeedsNoLead(t *testing.T) {
	r := newRig(t, baseScript())
	e := r.ready()
	// Registry.Close on a lead also closes its peers' records, which Show
	// refuses, so the lead is left out of the options instead: no lead is
	// named and none is looked up.
	ctx := WithOptions(context.Background(), Options{Root: r.root, Config: r.cfg})
	if _, err := Show(ctx, e.ID, 5); err != nil {
		t.Fatal(err)
	}
}

func TestShowStripsTerminalControls(t *testing.T) {
	r := newRig(t, baseScript())
	e := r.ready()
	script := baseScript()
	script["capture"] = testbin.Scenario{Stdout: "a\x1b]52;c;ZXZpbA==\x07b\n\x1b[2J\x1b[Hc\n" + codexIdle("")}
	r.rex(script)
	got, err := Show(r.ctx, e.ID, 40)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range got.Lines {
		if strings.ContainsAny(l, "\x1b\x07") {
			t.Fatalf("control left in %q", l)
		}
	}
	all := strings.Join(got.Lines, "\n")
	if !strings.Contains(all, "a") || !strings.Contains(all, "c") {
		t.Fatalf("text lost: %q", all)
	}
}

func TestShowBoundsLongLines(t *testing.T) {
	r := newRig(t, baseScript())
	e := r.ready()
	script := baseScript()
	out := strings.Repeat("x", 100_000) + "\n"
	for i := 0; i < 300; i++ {
		out += strings.Repeat("y", 1024) + "\n"
	}
	script["capture"] = testbin.Scenario{Stdout: out}
	r.rex(script)
	got, err := Show(r.ctx, e.ID, 200)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, l := range got.Lines {
		if n := utf8.RuneCountInString(l); n > maxShowRunes+len(" [cut]") {
			t.Fatalf("line of %d runes", n)
		}
		if len(l) > maxShowRunes && !strings.HasSuffix(l, " [cut]") {
			t.Fatalf("long line not marked: %d", len(l))
		}
		total += len(l)
	}
	if total > maxShowBytes {
		t.Fatalf("%d bytes", total)
	}
	// 200 ASCII lines of 306 bytes fit in 64 KiB, so nothing is dropped.
	if got.Cut || len(got.Lines) != 200 {
		t.Fatalf("cut=%v lines=%d", got.Cut, len(got.Lines))
	}
	// The 100 KB line is cut when it is among the lines kept.
	r.rex(func() map[string]testbin.Scenario {
		s := baseScript()
		s["capture"] = testbin.Scenario{Stdout: strings.Repeat("x", 100_000) + "\n" + codexIdle("")}
		return s
	}())
	got, err = Show(r.ctx, e.ID, 200)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range got.Lines {
		if strings.HasPrefix(l, "xxx") {
			found = true
			if !strings.HasSuffix(l, " [cut]") {
				t.Fatal("long line not marked [cut]")
			}
		}
	}
	if !found {
		t.Fatal("long line missing")
	}

	before := len(r.calls())
	for _, n := range []int{0, 201} {
		if _, err := Show(r.ctx, e.ID, n); err == nil || !strings.Contains(err.Error(), "--lines must be 1 to 200") {
			t.Fatalf("%d: %v", n, err)
		}
	}
	if len(r.calls()) != before {
		t.Fatal("rex was called for a bad --lines")
	}
}

func TestShowDropsOldestLinesOverBytes(t *testing.T) {
	r := newRig(t, baseScript())
	e := r.ready()
	script := baseScript()
	// Four-byte runes: 200 lines of 300 runes are 240 KB, over the limit.
	out := ""
	for i := 0; i < 200; i++ {
		out += strings.Repeat("😀", 300) + "\n"
	}
	script["capture"] = testbin.Scenario{Stdout: out}
	r.rex(script)
	got, err := Show(r.ctx, e.ID, 200)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, l := range got.Lines {
		total += len(l)
	}
	if !got.Cut || total > maxShowBytes || len(got.Lines) == 0 || len(got.Lines) >= 200 {
		t.Fatalf("cut=%v total=%d lines=%d", got.Cut, total, len(got.Lines))
	}
	if h := got.Header(); !strings.HasSuffix(h, ", cut to 64 KiB") || !strings.Contains(h, "last ") {
		t.Fatal(h)
	}
}

func TestShowGoneAndClosed(t *testing.T) {
	r := newRig(t, baseScript())
	e := r.ready()
	script := baseScript()
	script["block inspect"] = gone
	r.rex(script)
	if _, err := Show(r.ctx, e.ID, 5); err == nil || !strings.Contains(err.Error(), "pane is gone") {
		t.Fatal(err)
	}
	if len(r.named("capture")) != 0 {
		t.Fatal("captured a gone pane")
	}

	r.rex(baseScript())
	if err := r.reg.Close(e.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := Show(r.ctx, e.ID, 5); err == nil || !strings.Contains(err.Error(), "is closed") {
		t.Fatal(err)
	}
	if len(r.calls()) != 0 {
		t.Fatalf("rex called for a closed peer: %v", r.calls())
	}
}
