package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/mcp"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/peer"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/policy"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/testutil/submodule"
)

func TestSubmoduleAskOwnershipAndWake(t *testing.T) {
	isolatedRouteConfig(t)
	t.Setenv("DUOLOG_DEPTH", "")
	t.Setenv("DUOLOG_LEAD", "shared-lead")
	f := submodule.New(t)
	for _, root := range []string{f.Superproject, f.Submodule} {
		submoduleFile(t, root, ".duolog/config.yaml", "{}\n")
	}
	reg, err := session.Open(project.LeadHome(f.Linked))
	if err != nil {
		t.Fatal(err)
	}
	lead := session.Lead{ID: "shared-lead", Target: config.Target{Agent: "codex"}, PID: os.Getpid(), Mode: mode.Full}
	if err := reg.RegisterLead(lead); err != nil {
		t.Fatal(err)
	}
	t.Chdir(f.Superproject)
	b, err := openBus()
	if err != nil {
		t.Fatal(err)
	}
	superAsk := mcpAsk(t, b, session.Address(lead), false, 0)
	t.Chdir(f.Linked)
	subBus, err := openBus()
	if err != nil {
		t.Fatal(err)
	}
	subAsk := mcpAsk(t, subBus, session.Address(lead), false, 0)
	if subAsk <= superAsk {
		t.Fatal("ask numbers are not globally increasing")
	}
	asks, err := leadAsks(f.Submodule)
	if err != nil || len(asks) != 2 {
		t.Fatal(asks, err)
	}
	t.Chdir(f.Superproject)
	if _, err := mcpAnswer(t, "allow", subAsk); err != nil {
		t.Fatal(err)
	}
	lines, err := b.Inbox("codex/ask.test")
	if err != nil || len(lines) != 1 {
		t.Fatal(lines, err)
	}
	var answer struct {
		Ref string `json:"ref"`
	}
	if err := json.Unmarshal(lines[0], &answer); err != nil {
		t.Fatal(err)
	}
	if answer.Ref != strconv.FormatInt(subAsk, 10) {
		t.Fatal(answer)
	}
	checkSubmoduleAskGuards(t, subAsk)
	t.Chdir(f.Linked)
	checkSubmoduleGateState(t, reg, f)
	if _, err := testDeps(t).mcpStatus(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.Submodule, ".duolog", "bus.ndjson")); !os.IsNotExist(err) {
		t.Fatal("local bus", err)
	}
}

func TestPeerSubmoduleCommandTelemetry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("live peers are unavailable on Windows")
	}
	peerTelemetryProject(t)
	f := submodule.New(t)
	submoduleFile(t, f.Superproject, ".duolog/config.yaml", "telemetry:\n  enabled: false\nroles:\n  plan:\n    use: {agent: codex, model: luna}\n    balance: false\n")
	submoduleFile(t, f.Submodule, ".duolog/config.yaml", "roles:\n  plan:\n    use: {agent: claude, model: haiku}\n    balance: false\n")
	reg, err := session.Open(project.LeadHome(f.Linked))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterLead(session.Lead{ID: "lead-1", Target: config.Target{Agent: "codex", Model: "sol"}, Mode: mode.Ask, PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(f.Superproject)
	peerRex(t, claudeIdleScreen())
	d := testDeps(t)
	ctx, done, err := d.peerCtxAt(context.Background(), "lead-1", false, 0, f.Linked)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := peer.Open(ctx, "plan")
	done()
	if err != nil {
		t.Fatal(err)
	}
	task := filepath.Join(f.Linked, "task.md")
	submoduleFile(t, f.Linked, "task.md", "## Goal\nx\n\n## Read\n- a.go\n\n## Write\n- a.go\n\n## Limits\nnone\n\n## Check\nnone\n")
	submoduleFile(t, f.Linked, "a.go", "package a\n")
	line := "Read task.md and do it. Do not call rex. Write the answer to " + policy.ReportPath(opened.ID) + ". Reply \"done\"."
	peerRex(t, claudeIdleScreen(), claudeIdleScreen(), claudeHolding(line), claudeAtWork())
	rel, err := filepath.Rel(f.Superproject, task)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executeBusCommand(t, "peer", "send", opened.ID, rel); err != nil {
		t.Fatal(err)
	}
	peerRex(t, claudeAtWork(), claudeIdleScreen(), claudeIdleScreen())
	if _, err := d.mcpPeer(context.Background(), mcp.PeerInput{Action: "wait", ID: opened.ID}); err != nil {
		t.Fatal(err)
	}
	peers, runs := peerEvents(t, f.Submodule)
	if len(peers) != 2 || peers[1].Op != "send" || peers[1].Agent != "claude" || len(runs) != 1 || runs[0].Role != "plan" || runs[0].Agent != "claude" {
		t.Fatal(peers, runs)
	}
}

func checkSubmoduleAskGuards(t *testing.T, answered int64) {
	t.Helper()
	b, err := openBus()
	if err != nil {
		t.Fatal(err)
	}
	reg, err := session.Open(b.Directory())
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterLead(session.Lead{ID: "foreign", Target: config.Target{Agent: "codex"}, PID: os.Getpid(), Mode: mode.Full}); err != nil {
		t.Fatal(err)
	}
	foreign := mcpAsk(t, b, "codex/lead.foreign", false, 0)
	for _, decision := range []string{"allow", "deny"} {
		if _, err := mcpAnswer(t, decision, foreign); err == nil {
			t.Fatal("foreign ask accepted")
		}
		if _, err := executeBusCommand(t, decision, strconv.FormatInt(foreign, 10)); err == nil {
			t.Fatal("foreign CLI ask accepted")
		}
	}
	t.Setenv("DUOLOG_DEPTH", "1")
	for _, decision := range []string{"allow", "deny"} {
		if _, err := executeBusCommand(t, decision, strconv.FormatInt(answered, 10)); err == nil {
			t.Fatal("worker CLI answer accepted")
		}
		if _, err := mcpAnswer(t, decision, answered); err == nil {
			t.Fatal("worker MCP answer accepted")
		}
	}
	t.Setenv("DUOLOG_DEPTH", "")
	person := mcpAsk(t, b, "codex/lead.shared-lead", true, 0)
	testDeps(t).AgentAbove = func([]string) bool { return true }
	if _, err := mcpAnswer(t, "allow", person); err == nil {
		t.Fatal("person-only ask accepted")
	}
}

func checkSubmoduleGateState(t *testing.T, reg *session.Registry, f submodule.Fixture) {
	t.Helper()
	t.Setenv("DUOLOG_DEPTH", "1")
	t.Setenv("DUOLOG_ROLE", "implement")
	t.Setenv("DUOLOG_MODE", "")
	shortAskWait(t)
	if out := runGate(t, "touch a.go"); out != "" {
		t.Fatal(out)
	}
	if err := reg.SetMode("shared-lead", mode.Plan); err != nil {
		t.Fatal(err)
	}
	if out := runGate(t, "touch a.go"); !strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Fatal(out)
	}
	data, err := os.ReadFile(filepath.Join(reg.Dir, "decisions.ndjson"))
	if err != nil || !strings.Contains(string(data), `"work":`) {
		t.Fatal(string(data), err)
	}
	if _, err := os.Stat(filepath.Join(f.Submodule, ".duolog", "decisions.ndjson")); !os.IsNotExist(err) {
		t.Fatal("work-local decision log", err)
	}
	t.Setenv("DUOLOG_DEPTH", "")
	if out, err := executeBusCommand(t, "decisions"); err != nil || !strings.Contains(out, "touch a.go") {
		t.Fatal(out, err)
	}
}
