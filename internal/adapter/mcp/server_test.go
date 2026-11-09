package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeApp struct {
	mu      sync.Mutex // calls run at once; mu guards calls
	calls   []string
	delIn   DelegateInput
	peerIn  PeerInput
	ansIn   AnswerAskInput
	err     error
	blocked chan struct{} // closed when a blocking delegate call starts
	sawDone chan struct{} // closed when the blocking call sees ctx.Done
}

func (f *fakeApp) called(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
}

func (f *fakeApp) Delegate(ctx context.Context, in DelegateInput) (DelegateOutput, error) {
	f.called(ToolDelegate)
	f.delIn = in
	if f.blocked != nil {
		close(f.blocked)
		<-ctx.Done()
		close(f.sawDone)
		return DelegateOutput{}, ctx.Err()
	}
	return DelegateOutput{State: "running", TaskPath: "t"}, f.err
}

func (f *fakeApp) Peer(ctx context.Context, in PeerInput) (PeerOutput, error) {
	f.called(ToolPeer)
	f.peerIn = in
	return PeerOutput{ID: in.ID}, f.err
}

func (f *fakeApp) AnswerAsk(ctx context.Context, in AnswerAskInput) (AnswerAskOutput, error) {
	f.called(ToolAnswerAsk)
	f.ansIn = in
	return AnswerAskOutput{Line: "ask 3 allowed"}, f.err
}

func (f *fakeApp) Status(ctx context.Context) (StatusOutput, error) {
	f.called(ToolStatus)
	return StatusOutput{}, f.err
}

// session runs Serve on pipes and lets a test send and read lines.
type session struct {
	t    *testing.T
	in   *io.PipeWriter
	out  *bufio.Reader
	done chan error
}

func start(t *testing.T, s *Server) *session {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := s.Serve(context.Background(), inR, outW)
		outW.Close()
		done <- err
	}()
	t.Cleanup(func() { inW.Close(); outR.Close() })
	return &session{t: t, in: inW, out: bufio.NewReader(outR), done: done}
}

func (s *session) send(line string) {
	s.t.Helper()
	if _, err := io.WriteString(s.in, line+"\n"); err != nil {
		s.t.Fatal(err)
	}
}

func (s *session) read() map[string]json.RawMessage {
	s.t.Helper()
	type res struct {
		line string
		err  error
	}
	ch := make(chan res, 1)
	go func() { l, err := s.out.ReadString('\n'); ch <- res{l, err} }()
	select {
	case r := <-ch:
		if r.err != nil {
			s.t.Fatalf("read: %v", r.err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(r.line), &m); err != nil {
			s.t.Fatalf("reply %q: %v", r.line, err)
		}
		return m
	case <-time.After(5 * time.Second):
		s.t.Fatal("no reply")
		return nil
	}
}

func (s *session) call(line string) map[string]json.RawMessage {
	s.t.Helper()
	s.send(line)
	return s.read()
}

func result(t *testing.T, m map[string]json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	if e, ok := m["error"]; ok {
		t.Fatalf("protocol error: %s", e)
	}
	var r map[string]json.RawMessage
	if err := json.Unmarshal(m["result"], &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func errCode(t *testing.T, m map[string]json.RawMessage) int {
	t.Helper()
	var e struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(m["error"], &e); err != nil {
		t.Fatalf("no error in %v", m)
	}
	return e.Code
}

func initLine(version string) string {
	return `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + version + `","capabilities":{},"clientInfo":{"name":"c","version":"1"}}}`
}

func TestInitializeEchoesKnownVersion(t *testing.T) {
	s := start(t, &Server{App: &fakeApp{}, Enabled: true, Version: "9.9.9"})
	r := result(t, s.call(initLine("2025-03-26")))
	if string(r["protocolVersion"]) != `"2025-03-26"` {
		t.Fatalf("protocolVersion = %s", r["protocolVersion"])
	}
	if !strings.Contains(string(r["capabilities"]), `"tools"`) {
		t.Fatalf("capabilities = %s", r["capabilities"])
	}
	var info struct{ Name, Version string }
	if err := json.Unmarshal(r["serverInfo"], &info); err != nil || info.Name != "duolog" || info.Version != "9.9.9" {
		t.Fatalf("serverInfo = %s", r["serverInfo"])
	}
}

func TestInitializeFallsBackToNewestVersion(t *testing.T) {
	s := start(t, &Server{App: &fakeApp{}, Enabled: true})
	r := result(t, s.call(initLine("1999-01-01")))
	if string(r["protocolVersion"]) != `"`+ProtocolVersions[0]+`"` {
		t.Fatalf("protocolVersion = %s", r["protocolVersion"])
	}
}

func TestInitializedNotificationGetsNoReply(t *testing.T) {
	s := start(t, &Server{App: &fakeApp{}, Enabled: true})
	s.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	// The next reply must belong to the ping, not to the notification.
	m := s.call(`{"jsonrpc":"2.0","id":7,"method":"ping"}`)
	if string(m["id"]) != "7" || string(m["result"]) != "{}" {
		t.Fatalf("reply = %v", m)
	}
}

func toolNames(t *testing.T, m map[string]json.RawMessage) []string {
	t.Helper()
	var l struct {
		Tools []struct{ Name string } `json:"tools"`
	}
	if err := json.Unmarshal(result2(t, m), &l); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range l.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func result2(t *testing.T, m map[string]json.RawMessage) json.RawMessage {
	t.Helper()
	if e, ok := m["error"]; ok {
		t.Fatalf("protocol error: %s", e)
	}
	return m["result"]
}

func TestToolsListWhenEnabled(t *testing.T) {
	s := start(t, &Server{App: &fakeApp{}, Enabled: true})
	got := strings.Join(toolNames(t, s.call(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)), ",")
	if got != "delegate,peer,answer_ask,status" {
		t.Fatalf("tools = %s", got)
	}
}

func TestToolsListEmptyWhenDisabled(t *testing.T) {
	s := start(t, &Server{App: &fakeApp{}, Enabled: false})
	m := s.call(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if !strings.Contains(string(m["result"]), `"tools":[]`) {
		t.Fatalf("result = %s", m["result"])
	}
}

func textOf(t *testing.T, r map[string]json.RawMessage) (string, bool) {
	t.Helper()
	var c []struct{ Type, Text string }
	if err := json.Unmarshal(r["content"], &c); err != nil || len(c) != 1 || c[0].Type != "text" {
		t.Fatalf("content = %s", r["content"])
	}
	var isErr bool
	if raw, ok := r["isError"]; ok {
		_ = json.Unmarshal(raw, &isErr)
	}
	return c[0].Text, isErr
}

func TestToolsCallRefusedWhenDisabled(t *testing.T) {
	app := &fakeApp{}
	s := start(t, &Server{App: app, Enabled: false, Off: "duolog tools are off here: test"})
	r := result(t, s.call(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"status","arguments":{}}}`))
	text, isErr := textOf(t, r)
	if !isErr || text != "duolog tools are off here: test" {
		t.Fatalf("text = %q isError = %v", text, isErr)
	}
	if len(app.calls) != 0 {
		t.Fatalf("App was called: %v", app.calls)
	}
}

func TestToolsCallRoutesToApp(t *testing.T) {
	app := &fakeApp{}
	s := start(t, &Server{App: app, Enabled: true})
	calls := []string{
		`{"name":"delegate","arguments":{"role":"implementer","task":{"goal":"g"},"wait":true}}`,
		`{"name":"peer","arguments":{"action":"send","id":"p1"}}`,
		`{"name":"answer_ask","arguments":{"ask":3,"decision":"allow"}}`,
		`{"name":"status"}`,
	}
	for i, p := range calls {
		r := result(t, s.call(`{"jsonrpc":"2.0","id":`+strconv.Itoa(i+1)+`,"method":"tools/call","params":`+p+`}`))
		text, isErr := textOf(t, r)
		if isErr || !json.Valid([]byte(text)) {
			t.Fatalf("call %d: text %q isError %v", i, text, isErr)
		}
	}
	if got := strings.Join(app.calls, ","); got != "delegate,peer,answer_ask,status" {
		t.Fatalf("calls = %s", got)
	}
	if app.delIn.Role != "implementer" || app.delIn.Task == nil || app.delIn.Task.Goal != "g" || app.delIn.Wait == nil || !*app.delIn.Wait {
		t.Fatalf("delegate input = %+v", app.delIn)
	}
	if app.peerIn.Action != "send" || app.peerIn.ID != "p1" {
		t.Fatalf("peer input = %+v", app.peerIn)
	}
	if app.ansIn.Ask != 3 || app.ansIn.Decision != "allow" {
		t.Fatalf("answer input = %+v", app.ansIn)
	}
}

func TestToolsCallUnknownFieldIsInvalidParams(t *testing.T) {
	app := &fakeApp{}
	s := start(t, &Server{App: app, Enabled: true})
	m := s.call(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"peer","arguments":{"action":"list","bogus":1}}}`)
	if code := errCode(t, m); code != -32602 {
		t.Fatalf("code = %d", code)
	}
	if len(app.calls) != 0 {
		t.Fatal("App was called")
	}
}

func TestAppErrorIsToolError(t *testing.T) {
	s := start(t, &Server{App: &fakeApp{err: errors.New("no such peer; run peer list")}, Enabled: true})
	r := result(t, s.call(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"peer","arguments":{"action":"status"}}}`))
	text, isErr := textOf(t, r)
	if !isErr || text != "no such peer; run peer list" {
		t.Fatalf("text = %q isError = %v", text, isErr)
	}
}

func TestUnknownMethod(t *testing.T) {
	s := start(t, &Server{App: &fakeApp{}, Enabled: true})
	if code := errCode(t, s.call(`{"jsonrpc":"2.0","id":6,"method":"nope"}`)); code != -32601 {
		t.Fatalf("code = %d", code)
	}
}

func TestBadJSONIsParseError(t *testing.T) {
	s := start(t, &Server{App: &fakeApp{}, Enabled: true})
	if code := errCode(t, s.call(`{not json`)); code != -32700 {
		t.Fatalf("code = %d", code)
	}
}

func TestServeEndsWhenStdinCloses(t *testing.T) {
	app := &fakeApp{blocked: make(chan struct{}), sawDone: make(chan struct{})}
	s := start(t, &Server{App: app, Enabled: true})
	s.send(`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"delegate","arguments":{"role":"r"}}}`)
	<-app.blocked
	s.in.Close()
	select {
	case <-app.sawDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the call never saw ctx.Done")
	}
	s.read() // the call's reply; Serve returns once it is written
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return")
	}
}

// replyID is the id of reply m.
func replyID(t *testing.T, m map[string]json.RawMessage) string {
	t.Helper()
	return string(m["id"])
}

// A long call does not hold up the others: ping, tools/list and another
// tools/call answer while it runs.
func TestOtherCallsAnswerDuringALongCall(t *testing.T) {
	app := &fakeApp{blocked: make(chan struct{}), sawDone: make(chan struct{})}
	s := start(t, &Server{App: app, Enabled: true})
	s.send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"delegate","arguments":{"role":"r","wait":true}}}`)
	<-app.blocked
	for _, line := range []string{
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"status"}}`,
	} {
		m := s.call(line)
		var want struct {
			ID json.RawMessage `json:"id"`
		}
		_ = json.Unmarshal([]byte(line), &want)
		if got := replyID(t, m); got != string(want.ID) {
			t.Fatalf("reply id %s for %s", got, line)
		}
		result(t, m)
	}
	s.in.Close()
	if got := replyID(t, s.read()); got != "1" {
		t.Fatalf("the long call's reply has id %s", got)
	}
}

// notifications/cancelled ends the call it names, which then sends no reply,
// and the server goes on answering.
func TestCancelledNotificationStopsTheCall(t *testing.T) {
	app := &fakeApp{blocked: make(chan struct{}), sawDone: make(chan struct{})}
	s := start(t, &Server{App: app, Enabled: true})
	s.send(`{"jsonrpc":"2.0","id":"wait-1","method":"tools/call","params":{"name":"delegate","arguments":{"role":"r","wait":true}}}`)
	<-app.blocked
	// One for a call that is not running changes nothing.
	s.send(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":99}}`)
	s.send(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"wait-1","reason":"the person pressed Esc"}}`)
	select {
	case <-app.sawDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the cancelled call never saw ctx.Done")
	}
	if got := replyID(t, s.call(`{"jsonrpc":"2.0","id":7,"method":"ping"}`)); got != "7" {
		t.Fatalf("the next reply has id %s, want 7: the cancelled call answered", got)
	}
	s.in.Close()
	select {
	case err := <-s.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return")
	}
}

// A second call with the id of one still running is refused, and the first
// one runs on.
func TestDuplicateRunningIDIsRefused(t *testing.T) {
	app := &fakeApp{blocked: make(chan struct{}), sawDone: make(chan struct{})}
	s := start(t, &Server{App: app, Enabled: true})
	s.send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"delegate","arguments":{"role":"r"}}}`)
	<-app.blocked
	m := s.call(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"status"}}`)
	if code := errCode(t, m); code != -32600 {
		t.Fatalf("code = %d", code)
	}
	s.in.Close()
	s.read()
}
