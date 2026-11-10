package codex

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"runtime"
)

// currentHookHash follows Codex rust-v0.165.0's normalized hook identity and
// version_for_toml: sorted JSON after TOML has omitted absent optional fields.
// It reads current configuration only and never records an approval.
func currentHookHash(data []byte, groupIndex, handlerIndex int) (string, error) {
	var doc struct {
		Hooks map[string][]struct {
			Matcher *string `json:"matcher"`
			Hooks   []struct {
				Type    string  `json:"type"`
				Command string  `json:"command"`
				Windows *string `json:"commandWindows"`
				Timeout *uint64 `json:"timeout"`
				Async   bool    `json:"async"`
				Status  *string `json:"statusMessage"`
				Limit   *uint64 `json:"additionalContextLimit"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", err
	}
	group := doc.Hooks["PreToolUse"][groupIndex]
	h := group.Hooks[handlerIndex]
	command := h.Command
	if runtime.GOOS == "windows" && h.Windows != nil {
		command = *h.Windows
	}
	timeout := uint64(600)
	if h.Timeout != nil {
		timeout = max(1, *h.Timeout)
	}
	hook := map[string]any{"type": h.Type, "command": command, "timeout": timeout, "async": h.Async}
	if h.Status != nil {
		hook["statusMessage"] = *h.Status
	}
	if h.Limit != nil && *h.Limit != 2500 {
		hook["additionalContextLimit"] = *h.Limit
	}
	identity := map[string]any{"event_name": "pre_tool_use", "hooks": []any{hook}}
	if group.Matcher != nil {
		identity["matcher"] = *group.Matcher
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(identity); err != nil {
		return "", err
	}
	canonical := bytes.TrimSuffix(b.Bytes(), []byte{'\n'})
	canonical = bytes.ReplaceAll(canonical, []byte(`\u2028`), []byte("\xe2\x80\xa8"))
	canonical = bytes.ReplaceAll(canonical, []byte(`\u2029`), []byte("\xe2\x80\xa9"))
	sum := sha256.Sum256(canonical)
	return fmt.Sprintf("sha256:%x", sum), nil
}
