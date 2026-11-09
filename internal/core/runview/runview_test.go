package runview

import (
	"reflect"
	"strings"
	"testing"
)

func render(lines ...string) []string {
	var r Renderer
	var out []string
	for _, l := range lines {
		out = append(out, r.Render([]byte(l))...)
	}
	return out
}

func TestClaudeMessagesAndToolCalls(t *testing.T) {
	got := render(
		`{"type":"system","subtype":"init","model":"m"}`,
		`{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"Reading the file."}]}}`,
		`{"type":"assistant","message":{"id":"m1","content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"internal/a.go"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"secret file text"}]}}`,
		`{"type":"assistant","message":{"id":"m2","content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"go   test\n./..."}}]}}`,
		`{"type":"result","subtype":"success"}`,
	)
	want := []string{"Reading the file.", "> Read internal/a.go", "> Bash go test ./..."}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestCodexMessagesAndToolCalls(t *testing.T) {
	got := render(
		`{"type":"thread.started","thread_id":"x"}`,
		`{"type":"item.started","item":{"id":"i1","type":"command_execution","command":"/bin/zsh -lc 'ls -la'","status":"in_progress"}}`,
		`{"type":"item.completed","item":{"id":"i1","type":"command_execution","command":"/bin/zsh -lc 'ls -la'","aggregated_output":"hidden output","exit_code":0}}`,
		`{"type":"item.completed","item":{"id":"i2","type":"agent_message","text":"Done with it."}}`,
		`{"type":"item.completed","item":{"id":"i3","type":"file_change","changes":[{"path":"a/b.go","kind":"update"}]}}`,
		`{"type":"item.completed","item":{"id":"i4","type":"reasoning","text":"thinking"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":1}}`,
	)
	want := []string{"> command /bin/zsh -lc 'ls -la'", "Done with it.", "> file_change a/b.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestUnknownAndBrokenLinesAreSkipped(t *testing.T) {
	got := render("", "not json", `{"type":"brand.new","x":1}`, `{"type":"assistant","message":"odd"}`, `{"type":"assistant","message":{"content":[7,{"type":"mystery"}]}}`, `[1,2]`, `{"type":"item.completed","item":5}`)
	if len(got) != 0 {
		t.Fatalf("got %q", got)
	}
}

func TestLongCommandsAreShortenedAndControlBytesDropped(t *testing.T) {
	got := render(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"echo `+strings.Repeat("x", 300)+`\u001b[31m"}}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"a\u001b]0;title\u0007b\nc"}]}}`)
	if len(got) != 2 || len(got[0]) > 120 || !strings.HasSuffix(got[0], "...") {
		t.Fatalf("command line = %q", got)
	}
	if strings.ContainsAny(got[0]+got[1], "\x1b\x07") {
		t.Fatalf("control bytes kept: %q", got)
	}
	if !strings.Contains(got[1], "b\nc") {
		t.Fatalf("message lost its lines: %q", got[1])
	}
}

func TestToolWithoutKnownInputPrintsItsName(t *testing.T) {
	got := render(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"TodoWrite","input":{"todos":[]}}]}}`)
	if !reflect.DeepEqual(got, []string{"> TodoWrite"}) {
		t.Fatalf("got %q", got)
	}
}
