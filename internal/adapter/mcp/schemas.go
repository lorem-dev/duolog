package mcp

import "encoding/json"

// Tool is one MCP tool definition, as tools/list returns it.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// The schemas are compact JSON literals: no whitespace, no title keys and no
// $ref (Claude Code's tool-search path is not verified with it). Their total
// size is pinned by SchemaBudgetBytes.
const (
	strList = `{"type":"array","items":{"type":"string"}}`

	// taskSchema is the one task object that delegate and peer share.
	taskSchema = `{"type":"object","properties":{` +
		`"goal":{"type":"string","description":"What to do and why, one paragraph"},` +
		`"read":` + strList + `,"write":` + strList + `,"limits":` + strList + `,` +
		`"run":{"type":"array","items":{"type":"string"},"description":"Commands besides checks, one per item"},` +
		`"check":{"type":"array","items":{"type":"string"},"description":"One command per item"}` +
		`},"required":["goal"]}`

	delegateSchema = `{"type":"object","properties":{` +
		`"action":{"type":"string","enum":["run","result"]},` +
		`"role":{"type":"string"},` +
		`"task":` + taskSchema + `,` +
		`"task_path":{"type":"string"},` +
		`"use":{"type":"string"},` +
		`"needs":` + strList + `,` +
		`"wait":{"type":"boolean"},` +
		`"skip_preflight":{"type":"boolean"},` +
		`"force":{"type":"boolean"},` +
		`"worktree":{"type":"string"},` +
		`"timeout_minutes":{"type":"integer","minimum":1}` +
		`}}`

	peerSchema = `{"type":"object","properties":{` +
		`"action":{"type":"string","enum":["open","send","wait","answer","status","close","list"]},` +
		`"id":{"type":"string"},` +
		`"role":{"type":"string"},` +
		`"worktree":{"type":"string","description":"For open: path or name under .worktrees"},` +
		`"task":` + taskSchema + `,` +
		`"option":{"type":"string"},` +
		`"reason":{"type":"string"},` +
		`"timeout_minutes":{"type":"integer","minimum":1}` +
		`},"required":["action"]}`

	answerAskSchema = `{"type":"object","properties":{` +
		`"ask":{"type":"integer","minimum":1},` +
		`"decision":{"type":"string","enum":["allow","deny"]},` +
		`"reason":{"type":"string"}` +
		`},"required":["ask","decision"]}`

	statusSchema = `{"type":"object","properties":{}}`
)

// Tools returns the four tool definitions in their fixed order.
func Tools() []Tool {
	return []Tool{
		{
			Name:        ToolDelegate,
			Description: "Hand a task to a worker; waits and returns the result (wait=false: at once); if running, call action=result with id as task_path; run_here: do it yourself.",
			InputSchema: json.RawMessage(delegateSchema),
		},
		{
			Name:        ToolPeer,
			Description: "Drive a live Rex peer: open, send a task, wait (returns after timeout_minutes, default 10), answer its question, status, close, list.",
			InputSchema: json.RawMessage(peerSchema),
		},
		{
			Name:        ToolAnswerAsk,
			Description: "Allow or deny a worker's or peer's permission ask by number, as duolog allow/deny does.",
			InputSchema: json.RawMessage(answerAskSchema),
		},
		{
			Name:        ToolStatus,
			Description: "Live peers, open asks with numbers, running and finished delegations, load and quota.",
			InputSchema: json.RawMessage(statusSchema),
		},
	}
}
