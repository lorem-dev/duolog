package session

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// LeadState is the most recent native transcript observation. Empty fields are
// unknown; in particular, a missing effort must not cap a worker's effort.
type LeadState struct {
	Model, Effort, ApprovalPolicy string
	Mode                          mode.Mode
}

// LeadRoute is one observation of how the lead itself was started: its
// command line and its environment, as the lead's own process tree holds
// them. Only a caller that saw the lead may make one; duolog's own
// environment never stands for the lead. It feeds reports only.
type LeadRoute struct {
	Agent   string
	Argv    []string
	Environ []string
}

// LeadInput is the Build input that reports the lead from observation r: the
// lead shape, the free variant, and the observed argv and environment byte
// for byte, which Build passes on unchanged. home and root place its
// settings layers; a Codex --profile in the argv selects its profile. ok is
// false without an observation, so the lead reports unknown rather than the
// worker's verdict. Nothing built from it is ever run or admitted.
func LeadInput(cfg *config.Config, r LeadRoute, home, root string) (workerenv.ProcessInput, bool) {
	if r.Agent != "claude" && r.Agent != "codex" || len(r.Argv) == 0 || r.Environ == nil {
		return workerenv.ProcessInput{}, false
	}
	loc := workerenv.Locations{Home: home, Root: root, GOOS: runtime.GOOS, Environ: slices.Clone(r.Environ)}
	if r.Agent == "codex" {
		for i, arg := range r.Argv {
			if value, ok := strings.CutPrefix(arg, "--profile="); ok {
				loc.Profile = value
			} else if (arg == "--profile" || arg == "-p") && i+1 < len(r.Argv) {
				loc.Profile = r.Argv[i+1]
			}
		}
	}
	return workerenv.ProcessInput{Agent: r.Agent, Shape: workerenv.Lead, Variant: workerenv.Free, Config: cfg,
		Locations: loc, Environ: slices.Clone(r.Environ), Prefix: slices.Clone(r.Argv)}, true
}

// ReadLeadState searches backwards from 64 KiB to at most the final 4 MiB. Missing files and malformed or
// incomplete records are ordinary unknown observations, not hook failures.
func ReadLeadState(agent, transcriptPath string) (LeadState, error) {
	var state LeadState
	if agent != "claude" && agent != "codex" {
		return state, nil
	}
	f, err := os.Open(transcriptPath)
	if err != nil {
		return state, nil
	}
	defer f.Close()
	return readLeadState(agent, f)
}

func readLeadState(agent string, f io.ReadSeeker) (LeadState, error) {
	var state LeadState
	readTail(f, func(complete []byte) bool {
		state = parseLeadState(agent, complete)
		return state.Model != "" || state.Mode != ""
	})
	return state, nil
}

// readTail passes the complete lines of a growing tail of f, from 64 KiB to at
// most the final 4 MiB, to found until it reports a result.
func readTail(f io.ReadSeeker, found func([]byte) bool) {
	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return
	}
	var data []byte
	var scanned int64
	for window := int64(64 * 1024); ; window *= 4 {
		window = min(window, size, 4*1024*1024)
		start := size - window
		if _, err := f.Seek(start, io.SeekStart); err != nil {
			return
		}
		prefix, err := io.ReadAll(io.LimitReader(f, window-scanned))
		if err != nil {
			return
		}
		data = append(prefix, data...)
		scanned = window
		complete := data
		if start > 0 {
			if i := bytes.IndexByte(complete, '\n'); i >= 0 {
				complete = complete[i+1:]
			} else {
				complete = nil
			}
		}
		if found(complete) || window == size || window == 4*1024*1024 {
			return
		}
	}
}

// ClaudeMode reads the permission mode that Claude last recorded in its
// transcript. A missing file or record is no observation and returns "".
func ClaudeMode(transcriptPath string) mode.Mode {
	f, err := os.Open(transcriptPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	var observed mode.Mode
	readTail(f, func(complete []byte) bool {
		for _, line := range bytes.SplitAfter(complete, []byte{'\n'}) {
			if len(line) == 0 || line[len(line)-1] != '\n' {
				continue
			}
			var record struct {
				PermissionMode string `json:"permissionMode"`
			}
			if json.Unmarshal(line, &record) == nil && record.PermissionMode != "" {
				observed = mode.Parse("claude", record.PermissionMode)
			}
		}
		return observed != ""
	})
	return observed
}

func parseLeadState(agent string, data []byte) LeadState {
	var state LeadState
	var nullEffort bool
	for _, line := range bytes.SplitAfter(data, []byte{'\n'}) {
		if len(line) == 0 || line[len(line)-1] != '\n' {
			continue
		}
		var record struct {
			Type    string `json:"type"`
			Effort  string `json:"effort"`
			Message struct {
				Model string `json:"model"`
			} `json:"message"`
			Payload struct {
				Model    string          `json:"model"`
				Effort   json.RawMessage `json:"effort"`
				Approval string          `json:"approval_policy"`
				Sandbox  struct {
					Type string `json:"type"`
				} `json:"sandbox_policy"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &record) != nil {
			continue
		}
		if agent == "claude" && record.Type == "assistant" {
			state = LeadState{Model: record.Message.Model, Effort: record.Effort}
		}
		if agent == "codex" && record.Type == "turn_context" {
			state = LeadState{Model: record.Payload.Model, ApprovalPolicy: record.Payload.Approval}
			_ = json.Unmarshal(record.Payload.Effort, &state.Effort)
			nullEffort = bytes.Equal(bytes.TrimSpace(record.Payload.Effort), []byte("null"))
			state.Mode = CodexMode(record.Payload.Sandbox.Type, record.Payload.Approval)
		}
	}
	if agent == "codex" && nullEffort && state.Model != "" {
		state.Effort = defaultEffort(state.Model)
	}
	return state
}

// CodexMode gives a Codex lead the mode of what Codex itself does without
// asking. In workspace-write with approval on-request, never or on-failure,
// Codex edits the project and asks only to leave the sandbox, so its workers
// may edit too. untrusted asks before most commands and edits, and an unknown
// approval policy counts as ask. An unknown sandbox is no observation.
func CodexMode(sandbox, approval string) mode.Mode {
	switch sandbox {
	case "read-only":
		return mode.Plan
	case "workspace-write", "danger-full-access":
		switch approval {
		case "never":
			if sandbox == "danger-full-access" {
				return mode.Full
			}
			return mode.Edits
		case "on-request", "on-failure":
			return mode.Edits
		}
		return mode.Ask
	}
	return ""
}

func defaultEffort(model string) string {
	// Metadata does not invoke a model. Bound it so a broken installation cannot
	// hold up a lead hook indefinitely, and never inherit hook stdin.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "codex", "debug", "models")
	cmd.Stdin = bytes.NewReader(nil)
	data, err := cmd.Output()
	if err != nil {
		return ""
	}
	var catalog struct {
		Models []struct {
			Slug    string `json:"slug"`
			Default string `json:"default_reasoning_level"`
		} `json:"models"`
	}
	if json.Unmarshal(data, &catalog) != nil {
		return ""
	}
	for _, entry := range catalog.Models {
		if entry.Slug == model {
			return entry.Default
		}
	}
	return ""
}

// Originator is a separate bounded header read: session_meta can be much older
// than the tail used for permission and effort decisions.
func Originator(transcriptPath string) string {
	f, err := os.Open(transcriptPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 1024*1024))
	if err != nil {
		return ""
	}
	line, _, _ := bytes.Cut(data, []byte{'\n'})
	var record struct {
		Type    string `json:"type"`
		Payload struct {
			Originator string `json:"originator"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &record) != nil || record.Type != "session_meta" {
		return ""
	}
	return record.Payload.Originator
}
