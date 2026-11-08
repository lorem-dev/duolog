package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
)

// ProtocolVersions are the MCP versions the server answers, newest first.
var ProtocolVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

// Server serves one MCP session on r and w until r ends or ctx is done.
type Server struct {
	App     App
	Enabled bool   // false: tools/list is empty and tools/call refuses
	Off     string // why tools are off, shown by tools/call
	Version string // duolog's version, for serverInfo
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type textContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type callResult struct {
	Content []textContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// Serve reads newline-delimited JSON-RPC 2.0 messages from r and writes the
// replies to w. Each tools/call runs on its own, so a long wait holds up no
// other message: ping, tools/list and other calls answer meanwhile. A
// notifications/cancelled ends the call it names, which then sends no reply,
// as MCP asks. The context a call reads ends when r ends, so a long call
// stops with the session; Serve returns once every call has ended.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	lines := make(chan []byte)
	readErr := make(chan error, 1)
	go readLines(ctx, cancel, r, lines, readErr)

	out := &replies{enc: json.NewEncoder(w)}
	calls := &runningCalls{byID: map[string]*runningCall{}}
	var wg sync.WaitGroup
	for line := range lines {
		var req request
		parsed := json.Unmarshal(line, &req) == nil
		switch {
		case parsed && len(req.ID) == 0 && req.Method == "notifications/cancelled":
			calls.cancel(req.Params)
		case parsed && len(req.ID) > 0 && req.Method == "tools/call":
			call, ok := calls.start(ctx, req.ID)
			if !ok {
				out.write(&response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{codeInvalidRequest, "invalid request: a call with this id is still running"}})
				break
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				resp := s.answer(call.ctx, req)
				if !calls.end(call) {
					out.write(resp)
				}
			}()
		default:
			out.write(s.handle(ctx, line))
		}
		if out.failed() != nil {
			cancel()
			break
		}
	}
	wg.Wait()
	if err := out.failed(); err != nil {
		return err
	}
	select {
	case err := <-readErr:
		return err
	default:
		return nil
	}
}

// replies writes replies to the output one at a time, as calls end in any
// order, and keeps the first error.
type replies struct {
	mu  sync.Mutex
	enc *json.Encoder
	err error
}

// write writes resp, unless it is nil (a notification) or a write failed.
func (o *replies) write(resp *response) {
	if resp == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.err == nil {
		o.err = o.enc.Encode(resp)
	}
}

func (o *replies) failed() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.err
}

// runningCall is one tools/call that has not ended: its context, which a
// cancel ends, and whether a notifications/cancelled did.
type runningCall struct {
	key       string
	ctx       context.Context
	stop      context.CancelFunc
	cancelled bool
}

// runningCalls are the calls that have not ended, by request id.
type runningCalls struct {
	mu   sync.Mutex
	byID map[string]*runningCall
}

// idKey is a request id as a map key: its JSON without blanks, so 7 and "7"
// stay apart.
func idKey(id json.RawMessage) string {
	var b bytes.Buffer
	if json.Compact(&b, id) != nil {
		return string(id)
	}
	return b.String()
}

// start registers a call with request id, under ctx, and is false when a
// call with that id is still running.
func (c *runningCalls) start(ctx context.Context, id json.RawMessage) (*runningCall, bool) {
	key := idKey(id)
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.byID[key]; ok {
		return nil, false
	}
	call := &runningCall{key: key}
	call.ctx, call.stop = context.WithCancel(ctx)
	c.byID[key] = call
	return call, true
}

// end drops call and reports whether a notifications/cancelled ended it.
func (c *runningCalls) end(call *runningCall) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.byID, call.key)
	call.stop()
	return call.cancelled
}

// cancel ends the call that the params of a notifications/cancelled name. A
// call that has ended, or an id that names none, is no error.
func (c *runningCalls) cancel(params json.RawMessage) {
	var p struct {
		RequestID json.RawMessage `json:"requestId"`
	}
	if json.Unmarshal(params, &p) != nil || len(p.RequestID) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if call, ok := c.byID[idKey(p.RequestID)]; ok {
		call.cancelled = true
		call.stop()
	}
}

// readLines sends each non-empty line of r to lines and closes it at the end.
// When r ends it cancels the session context, so a running call stops.
func readLines(ctx context.Context, cancel context.CancelFunc, r io.Reader, lines chan<- []byte, readErr chan<- error) {
	defer close(lines)
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			select {
			case lines <- line:
			case <-ctx.Done():
				return
			}
		}
		if err != nil {
			cancel()
			if !errors.Is(err, io.EOF) {
				readErr <- err
			}
			return
		}
	}
}

// handle answers one message; nil means no reply (a notification).
func (s *Server) handle(ctx context.Context, line []byte) *response {
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		return &response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{codeParse, "parse error: " + err.Error()}}
	}
	return s.answer(ctx, req)
}

// answer answers the parsed message req; nil means no reply (a
// notification).
func (s *Server) answer(ctx context.Context, req request) *response {
	if len(req.ID) == 0 { // a notification: never answered
		return nil
	}
	fail := func(code int, msg string) *response {
		return &response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{code, msg}}
	}
	if req.Method == "" {
		return fail(codeInvalidRequest, "invalid request: no method")
	}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &p); err != nil {
				return fail(codeInvalidParams, "invalid params: "+err.Error())
			}
		}
		version := ProtocolVersions[0]
		if slices.Contains(ProtocolVersions, p.ProtocolVersion) {
			version = p.ProtocolVersion
		}
		return &response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": ServerName, "version": s.Version},
		}}
	case "ping":
		return &response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}
	case "tools/list":
		tools := []Tool{}
		if s.Enabled {
			tools = Tools()
		}
		return &response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": tools}}
	case "tools/call":
		res, rerr := s.call(ctx, req.Params)
		if rerr != nil {
			return &response{JSONRPC: "2.0", ID: req.ID, Error: rerr}
		}
		return &response{JSONRPC: "2.0", ID: req.ID, Result: res}
	default:
		return fail(codeMethodNotFound, "method not found: "+req.Method)
	}
}

func toolError(text string) *callResult {
	return &callResult{Content: []textContent{{Type: "text", Text: text}}, IsError: true}
}

func (s *Server) call(ctx context.Context, params json.RawMessage) (*callResult, *rpcError) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.Name == "" {
		return nil, &rpcError{codeInvalidParams, "invalid params: tools/call needs a tool name"}
	}
	if !s.Enabled {
		return toolError(s.Off), nil
	}
	args := p.Arguments
	if len(bytes.TrimSpace(args)) == 0 || bytes.Equal(bytes.TrimSpace(args), []byte("null")) {
		args = []byte("{}")
	}
	o := s.dispatch(ctx, p.Name, args)
	if o.perr != nil {
		return nil, o.perr
	}
	out, err := o.out, o.err
	if err != nil {
		return toolError(err.Error()), nil
	}
	text, merr := json.Marshal(out)
	if merr != nil {
		return toolError(fmt.Sprintf("duolog could not encode the %s result: %v", p.Name, merr)), nil
	}
	return &callResult{Content: []textContent{{Type: "text", Text: string(text)}}}, nil
}

// outcome is what one tool ran: its output or its error, or a protocol fault.
type outcome struct {
	out  any
	err  error
	perr *rpcError
}

// dispatch decodes args into the named tool's input and calls App.
func (s *Server) dispatch(ctx context.Context, name string, args []byte) outcome {
	switch name {
	case ToolDelegate:
		return invoke(args, func(in DelegateInput) (any, error) { return s.App.Delegate(ctx, in) })
	case ToolPeer:
		return invoke(args, func(in PeerInput) (any, error) { return s.App.Peer(ctx, in) })
	case ToolAnswerAsk:
		return invoke(args, func(in AnswerAskInput) (any, error) { return s.App.AnswerAsk(ctx, in) })
	case ToolStatus:
		return invoke(args, func(struct{}) (any, error) { return s.App.Status(ctx) })
	}
	return outcome{perr: &rpcError{codeInvalidParams, "invalid params: unknown tool " + name}}
}

func invoke[In any](args []byte, call func(In) (any, error)) outcome {
	var in In
	if perr := decodeStrict(args, &in); perr != nil {
		return outcome{perr: perr}
	}
	out, err := call(in)
	return outcome{out: out, err: err}
}

// decodeStrict decodes args into v and refuses a field v does not have.
func decodeStrict(args []byte, v any) *rpcError {
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return &rpcError{codeInvalidParams, "invalid params: " + err.Error()}
	}
	return nil
}
