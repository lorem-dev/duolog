// Package mcpauth checks the credentials a static HTTP MCP server would get,
// with one initialize request over net/http. It prints and stores no value.
package mcpauth

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/kit"
	"github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/core/caps"
)

// initialize is the first MCP request. A server that checks its token at all
// checks it here; one that checks only on a tool call passes, and a rejection
// during the run reaches the lead through the worker's needs.
const initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"duolog-preflight","version":"1"}}}`

var authWords = regexp.MustCompile(`(?i)unauthori[sz]ed|invalid[ _-]?token|expired[ _-]?token|token[ _-]?expired|forbidden|authentication (failed|required)`)

// Probe checks a static HTTP server with the credentials the worker gets.
type Probe struct {
	client *http.Client
}

// New returns the real probe. A redirect would take the credential headers to
// another url, so the check reads the first answer only.
func New() *Probe {
	return &Probe{client: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// Check sends initialize with the credentials of def. Only 401, 403 or an error
// that names authentication count; a network failure does not stop the worker.
// The problem it returns names no agent or server: the caller knows both.
func (c *Probe) Check(ctx context.Context, def caps.Server) *agent.LoginProblem {
	u, headers, err := kit.Request(def, os.LookupEnv)
	if err != nil {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(initialize))
	if err != nil {
		return nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if session := resp.Header.Get("Mcp-Session-Id"); session != "" {
		end, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
		if err == nil {
			for k, v := range headers {
				end.Header.Set(k, v)
			}
			end.Header.Set("Mcp-Session-Id", session)
			if r, err := c.client.Do(end); err == nil {
				r.Body.Close()
			}
		}
	}
	rejected := resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden
	if !rejected && bytes.Contains(body, []byte(`"error"`)) && authWords.Match(body) {
		rejected = true
	}
	if !rejected {
		return nil
	}
	return &agent.LoginProblem{Static: true, Where: def.File, Reason: fmt.Sprintf("the server rejected the token from config (HTTP %d)", resp.StatusCode)}
}
