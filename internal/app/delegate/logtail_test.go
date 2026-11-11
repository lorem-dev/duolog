package delegate

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// tailLog is a running record whose log, task-1/stdout.log, is in a new work
// root; the log is not written yet.
func tailLog(t *testing.T) (rec Record, path string) {
	t.Helper()
	dir := t.TempDir()
	rec = Record{ID: "d-abcdef", State: "running", Log: "task-1/stdout.log", WorkRoot: dir}
	path = rec.LogPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	return rec, path
}

// assistantLine is one Claude assistant event with text, and its newline.
func assistantLine(text string) string {
	return `{"type":"assistant","message":{"content":[{"type":"text","text":"` + text + `"}]}}` + "\n"
}

func tailOf(t *testing.T, rec Record, from int64, cursor string) TailPage {
	t.Helper()
	page, err := Tail(rec, from, cursor)
	if err != nil {
		t.Fatal(err)
	}
	return page
}

func TestTailBoundsBytesPerCall(t *testing.T) {
	rec, path := tailLog(t)
	big := `{"type":"item.completed","item":{"id":"item_1","type":"command_execution","command":"cat big","aggregated_output":"` +
		strings.Repeat("x", 3<<20) + `"}}` + "\n"
	appendTo(t, path, big+assistantLine("after"))
	size := int64(len(big) + len(assistantLine("after")))
	var lines []string
	from, cursor := int64(0), ""
	for calls := 0; from < size; calls++ {
		if calls > 10 {
			t.Fatalf("no end after %d calls, offset %d of %d", calls, from, size)
		}
		page := tailOf(t, rec, from, cursor)
		if page.Offset <= from {
			t.Fatalf("offset did not move forward: %d after %d", page.Offset, from)
		}
		if page.Offset-from > TailMaxBytes {
			t.Fatalf("one call read %d bytes, more than %d", page.Offset-from, TailMaxBytes)
		}
		lines = append(lines, page.Lines...)
		from, cursor = page.Offset, page.Cursor
	}
	if want := []string{"> (output of 3 MiB not shown)", "after"}; !reflect.DeepEqual(lines, want) {
		t.Fatalf("lines = %q, want %q", lines, want)
	}
}

// A Codex item whose start printed and whose completion is too long to read
// is closed all the same: its id is in no later cursor.
func TestTailCutCompletionClosesItsItem(t *testing.T) {
	rec, path := tailLog(t)
	appendTo(t, path, `{"type":"item.started","item":{"id":"item_7","type":"command_execution","command":"cat big"}}`+"\n")
	page := tailOf(t, rec, 0, "")
	if cur, ok := decodeCursor(page.Cursor); !ok || !reflect.DeepEqual(cur.Open, []string{"item_7"}) {
		t.Fatalf("cursor after the start = %+v", cur)
	}
	appendTo(t, path, `{"type":"item.completed","item":{"id":"item_7","type":"command_execution","command":"cat big","aggregated_output":"`+
		strings.Repeat("x", 2<<20)+`"}}`+"\n"+assistantLine("after"))
	for calls := 0; ; calls++ {
		if calls > 10 {
			t.Fatal("no end")
		}
		page = tailOf(t, rec, page.Offset, page.Cursor)
		cur, ok := decodeCursor(page.Cursor)
		if !ok || len(cur.Open) != 0 {
			t.Fatalf("cursor while cutting = %+v", cur)
		}
		if slices.Contains(page.Lines, "after") {
			break
		}
	}
}

// watch prints a log line that is too long as one line that says its output
// was not shown, then goes on.
func TestWatchSaysAnOverlongLineWasNotShown(t *testing.T) {
	recordPath, logPath, rec := watchFixture(t)
	appendTo(t, logPath, `{"type":"user","message":{"content":[{"type":"tool_result","content":"`+strings.Repeat("y", 3<<20)+`"}]}}`+"\n"+claudeText+"\n")
	rec.State, rec.Ended = "done", time.Now().UTC()
	if err := WriteRecord(recordPath, rec); err != nil {
		t.Fatal(err)
	}
	out := &syncBuffer{}
	if err := Watch(context.Background(), out, recordPath, nil, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "> (output of 3 MiB not shown)\nLooking around.\n") || strings.Contains(got, "yyyy") {
		t.Fatalf("output %q", got)
	}
}

func TestTailFromMinusOneReadsTheLast64KiB(t *testing.T) {
	rec, path := tailLog(t)
	var b strings.Builder
	for b.Len() < 70000 {
		b.WriteString(`{"type":"x"}` + "\n")
	}
	b.WriteString(assistantLine("hello"))
	appendTo(t, path, b.String())
	page := tailOf(t, rec, -1, "")
	if !reflect.DeepEqual(page.Lines, []string{"hello"}) {
		t.Fatalf("lines = %q", page.Lines)
	}
	if page.Offset != int64(b.Len()) || page.Reset || page.Ended {
		t.Fatalf("page = %+v, size %d", page, b.Len())
	}
	if page.State != "running" || page.Cursor == "" {
		t.Fatalf("state %q, cursor %q", page.State, page.Cursor)
	}
}

func TestTailSecondCallReadsOnlyNewBytes(t *testing.T) {
	rec, path := tailLog(t)
	appendTo(t, path, assistantLine("one"))
	first := tailOf(t, rec, -1, "")
	if !reflect.DeepEqual(first.Lines, []string{"one"}) {
		t.Fatalf("first lines = %q", first.Lines)
	}
	appendTo(t, path, assistantLine("two"))
	second := tailOf(t, rec, first.Offset, first.Cursor)
	if !reflect.DeepEqual(second.Lines, []string{"two"}) || second.Reset {
		t.Fatalf("second page = %+v", second)
	}
}

func TestTailKeepsAPartialLineForTheNextCall(t *testing.T) {
	rec, path := tailLog(t)
	one, two := assistantLine("one"), strings.TrimSuffix(assistantLine("two"), "\n")
	appendTo(t, path, one+two)
	page := tailOf(t, rec, 0, "")
	if !reflect.DeepEqual(page.Lines, []string{"one"}) || page.Offset != int64(len(one)) || page.Ended {
		t.Fatalf("running page = %+v", page)
	}
	rec.State = "done"
	page = tailOf(t, rec, page.Offset, page.Cursor)
	if !reflect.DeepEqual(page.Lines, []string{"two"}) || !page.Ended || page.Offset != int64(len(one)+len(two)) {
		t.Fatalf("ended page = %+v", page)
	}
}

func TestTailOpenItemPrintsOnce(t *testing.T) {
	rec, path := tailLog(t)
	appendTo(t, path, `{"type":"item.started","item":{"id":"item_1","type":"command_execution","command":"go test ./..."}}`+"\n")
	page := tailOf(t, rec, 0, "")
	if !reflect.DeepEqual(page.Lines, []string{"> command go test ./..."}) {
		t.Fatalf("first lines = %q", page.Lines)
	}
	appendTo(t, path, `{"type":"item.completed","item":{"id":"item_1","type":"command_execution","command":"go test ./..."}}`+"\n")
	page = tailOf(t, rec, page.Offset, page.Cursor)
	if !reflect.DeepEqual(page.Lines, []string{}) {
		t.Fatalf("completed item printed again: %q", page.Lines)
	}
}

func TestTailResetsWhenTheLogPathChanges(t *testing.T) {
	rec, path := tailLog(t)
	appendTo(t, path, assistantLine("one"))
	first := tailOf(t, rec, -1, "")
	rec.Log = "task-1/attempt-2/stdout.log"
	retry := rec.LogPath()
	if err := os.MkdirAll(filepath.Dir(retry), 0o755); err != nil {
		t.Fatal(err)
	}
	appendTo(t, retry, assistantLine("retry one")+assistantLine("retry two"))
	page := tailOf(t, rec, first.Offset, first.Cursor)
	if !page.Reset || !reflect.DeepEqual(page.Lines, []string{"retry one", "retry two"}) {
		t.Fatalf("page after the retry = %+v", page)
	}
	if page.Cursor == first.Cursor {
		t.Fatal("the cursor still names the old log")
	}
}

func TestTailResetsWhenTheFileShrinks(t *testing.T) {
	rec, path := tailLog(t)
	appendTo(t, path, assistantLine("one"))
	page := tailOf(t, rec, 10000, "")
	if !page.Reset || !reflect.DeepEqual(page.Lines, []string{"one"}) {
		t.Fatalf("page = %+v", page)
	}
	// A cursor that does not decode starts over too.
	page = tailOf(t, rec, page.Offset, "not a cursor!")
	if !page.Reset || !reflect.DeepEqual(page.Lines, []string{"one"}) {
		t.Fatalf("page with a broken cursor = %+v", page)
	}
}

func TestTailCapsAt200Lines(t *testing.T) {
	rec, path := tailLog(t)
	rec.State = "done"
	var b strings.Builder
	for i := range 250 {
		b.WriteString(assistantLine("line " + strconv.Itoa(i)))
	}
	appendTo(t, path, b.String())
	page := tailOf(t, rec, 0, "")
	if len(page.Lines) != TailMaxLines || page.Lines[199] != "line 199" || page.Ended {
		t.Fatalf("first page: %d lines, ended %v", len(page.Lines), page.Ended)
	}
	page = tailOf(t, rec, page.Offset, page.Cursor)
	if len(page.Lines) != 50 || page.Lines[0] != "line 200" || !page.Ended {
		t.Fatalf("second page: %d lines, ended %v", len(page.Lines), page.Ended)
	}
}

// One log line that renders more than 200 lines is cut to 200, so a page
// never stalls on it.
func TestTailCutsOneLongLogLineTo200Lines(t *testing.T) {
	rec, path := tailLog(t)
	blocks := make([]string, 250)
	for i := range blocks {
		blocks[i] = `{"type":"text","text":"b` + strconv.Itoa(i) + `"}`
	}
	line := `{"type":"assistant","message":{"content":[` + strings.Join(blocks, ",") + `]}}` + "\n"
	appendTo(t, path, line+assistantLine("next"))
	page := tailOf(t, rec, 0, "")
	if len(page.Lines) != TailMaxLines || page.Offset != int64(len(line)) {
		t.Fatalf("page: %d lines, offset %d", len(page.Lines), page.Offset)
	}
}

func TestTailWithNoLogYet(t *testing.T) {
	rec := Record{ID: "d-abcdef", State: "running", WorkRoot: t.TempDir()}
	page := tailOf(t, rec, -1, "")
	if !reflect.DeepEqual(page.Lines, []string{}) || page.Offset != 0 || page.Cursor != "" || page.Ended {
		t.Fatalf("running page = %+v", page)
	}
	rec.State = "failed"
	if page = tailOf(t, rec, -1, ""); !page.Ended || page.State != "failed" {
		t.Fatalf("ended page = %+v", page)
	}
}
