package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lorem-dev/duolog/internal/adapter/kit"
	"github.com/lorem-dev/duolog/internal/core/config"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/core/usage"
)

// Probe checks Codex for doctor: its model call and its metadata commands.
type Probe struct{}

// Model returns the newest visible model of the luna family.
func (Probe) Model(cfg *config.Config, visible []route.Entry) (string, error) {
	model, ok := route.Latest(cfg.Models["luna"].Family, visible)
	if !ok {
		return "", fmt.Errorf("no visible model for luna probe")
	}
	return model, nil
}

// Metadata runs the codex binary with args.
func (Probe) Metadata(ctx context.Context, args ...string) ([]byte, error) {
	return kit.Metadata(ctx, Facts.Binaries[0], args...)
}

// Probe makes one model call, the empty-call form from the live verification
// bench, read-only and with an explicit output file, and checks that the
// answer is "ok".
func (Probe) Probe(ctx context.Context, _ *config.Config, p corelaunch.Process, dir, model string, started func(pid int) error) (usage.Probed, error) {
	args := []string{"exec", "--json", "--skip-git-repo-check", "-m", model, "-c", "model_reasoning_effort=low", "-c", "project_doc_max_bytes=0", "-s", "read-only", "--ephemeral", "-o", filepath.Join(dir, "answer.json"), "Reply with the word ok."}
	output, err := kit.RunProbe(ctx, p, dir, args, started)
	measured := readProbeUsage(output)
	if err != nil {
		return measured, err
	}
	answer, err := os.ReadFile(filepath.Join(dir, "answer.json"))
	if err != nil {
		return measured, err
	}
	if strings.TrimSpace(string(answer)) != "ok" {
		return measured, fmt.Errorf("missing expected answer")
	}
	return measured, nil
}

// readProbeUsage reads the usage of a probe's output: Codex's turn.completed
// events.
func readProbeUsage(output []byte) usage.Probed {
	var u usage.Probed
	scan := bufio.NewScanner(bytes.NewReader(output))
	scan.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scan.Scan() {
		var event struct {
			Type  string `json:"type"`
			Usage *struct {
				Input  int64 `json:"input_tokens"`
				Cached int64 `json:"cached_input_tokens"`
				Output int64 `json:"output_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(scan.Bytes(), &event) != nil || event.Type != "turn.completed" || event.Usage == nil {
			continue
		}
		x := event.Usage
		if x.Input < 0 || x.Cached < 0 || x.Output < 0 || x.Cached > x.Input {
			continue
		}
		u.Input, u.Cached, u.Output, u.Known = u.Input+x.Input, u.Cached+x.Cached, u.Output+x.Output, true
	}
	return u
}
