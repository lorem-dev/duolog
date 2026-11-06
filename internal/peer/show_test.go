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

// Show and View read the registry and Rex only: a missing bus stays
// missing, and a bus the caller may not write does not stop them.
func TestShowAndViewLeaveTheBusAlone(t *testing.T) {
	r := newRig(t, baseScript())
	e := r.ready()
	dir := filepath.Join(r.root, ".duolog")
	busFile := filepath.Join(dir, "bus.ndjson")
	if err := os.Remove(busFile); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dir, "cursors")); err != nil {
		t.Fatal(err)
	}
	r.rex(viewScript(clientsOn(t, "sess")))
	if _, err := Show(r.ctx, e.ID, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := View(r.ctx, e.ID, ""); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bus.ndjson", "cursors", "bus.lock"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s was created: %v", name, err)
		}
	}

	if err := os.WriteFile(busFile, []byte(`{"k":"note"}`+"\n"), 0400); err != nil {
		t.Fatal(err)
	}
	if f, err := os.OpenFile(busFile, os.O_RDWR, 0); err == nil {
		f.Close()
		t.Skip("this user can write a read-only file")
	}
	if _, err := Show(r.ctx, e.ID, 5); err != nil {
		t.Fatalf("show with a read-only bus: %v", err)
	}
	if _, err := View(r.ctx, e.ID, ""); err != nil {
		t.Fatalf("view with a read-only bus: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "cursors")); !os.IsNotExist(err) {
		t.Fatalf("cursors was created: %v", err)
	}
}

// The limits count what the person sees: a cut line with its marker is at
// most 300 runes, and the header and every newline fit in 64 KiB.
func TestShowLimitsIncludeMarkerAndHeader(t *testing.T) {
	r := newRig(t, baseScript())
	e := r.ready()
	script := baseScript()
	out := ""
	for i := 0; i < 54; i++ {
		out += strings.Repeat("😀", 300) + "\n"
	}
	out += strings.Repeat("€", 225) + "\n"
	script["capture"] = testbin.Scenario{Stdout: out}
	r.rex(script)
	got, err := Show(r.ctx, e.ID, 200)
	if err != nil {
		t.Fatal(err)
	}
	total := len(got.Header()) + 1
	for _, l := range got.Lines {
		total += len(l) + 1
	}
	if total > maxShowBytes || !got.Cut {
		t.Fatalf("%d bytes printed, cut=%v", total, got.Cut)
	}

	script["capture"] = testbin.Scenario{Stdout: strings.Repeat("x", 301) + "\n" + strings.Repeat("y", 300) + "\n"}
	r.rex(script)
	if got, err = Show(r.ctx, e.ID, 5); err != nil {
		t.Fatal(err)
	}
	if len(got.Lines) != 2 {
		t.Fatalf("%q", got.Lines)
	}
	if n := utf8.RuneCountInString(got.Lines[0]); n != maxShowRunes || !strings.HasSuffix(got.Lines[0], cutMark) {
		t.Fatalf("cut line has %d runes: %q", n, got.Lines[0])
	}
	if got.Lines[1] != strings.Repeat("y", 300) {
		t.Fatalf("a line of exactly 300 runes was changed: %q", got.Lines[1])
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
	total := len(got.Header()) + 1
	for _, l := range got.Lines {
		if n := utf8.RuneCountInString(l); n > maxShowRunes {
			t.Fatalf("line of %d runes", n)
		}
		if len(l) == maxShowRunes && !strings.HasSuffix(l, " [cut]") {
			t.Fatalf("long line not marked: %d", len(l))
		}
		total += len(l) + 1
	}
	if total > maxShowBytes {
		t.Fatalf("%d bytes", total)
	}
	// 200 ASCII lines of 300 bytes fit in 64 KiB, so nothing is dropped.
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
	total := len(got.Header()) + 1
	for _, l := range got.Lines {
		total += len(l) + 1
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
