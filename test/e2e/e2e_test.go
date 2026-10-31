//go:build e2e

package e2e

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

const answerRule = "Write the answer as the schema says. Set done to true only if you did exactly what the goal asks."

func wordTask(word string) string {
	return taskText(
		"Reply with the code word "+word+" in the summary. Do nothing else.",
		"- README.md",
		"Nothing.",
		"Do not change files. Do not run commands. "+answerRule,
		"The summary contains "+word+".",
	)
}

// Real Codex gets a task file and answers through answer.json.
func TestDelegateRoundTrip(t *testing.T) {
	needAgent(t, "codex")
	p := newProject(t)
	line := p.delegate("e2e_codex", wordTask("AMBER-LATCH-4401"))
	requireWord(t, line, "AMBER-LATCH-4401")
	if !strings.HasPrefix(line, "done=true") {
		t.Errorf("want done=true: %s", line)
	}
	rows := p.usage()
	if len(rows) != 1 || rows[0].Agent != "codex" {
		t.Errorf("want one codex usage row: %+v", rows)
	}
}

// The same task and schema go to real Claude.
func TestReverseDelegate(t *testing.T) {
	needAgent(t, "claude")
	p := newProject(t)
	line := p.delegate("e2e_claude", wordTask("COPPER-SEAL-5502"))
	requireWord(t, line, "COPPER-SEAL-5502")
	if !strings.HasPrefix(line, "done=true") {
		t.Errorf("want done=true: %s", line)
	}
	rows := p.usage()
	if len(rows) != 1 || rows[0].Agent != "claude" {
		t.Errorf("want one claude usage row: %+v", rows)
	}
}

// A Claude worker told to run git push meets the gate. The remote is a local
// bare repository, so a push that got through would leave a branch in it.
func TestGateDeniesRealTool(t *testing.T) {
	needAgent(t, "claude", "git")
	p := newProject(t)
	p.duolog("install", "--scope", "project", "--agent", "claude")
	remote := filepath.Join(t.TempDir(), "remote.git")
	p.git("init", "-q", "--bare", remote)
	p.git("remote", "add", "origin", remote)
	task := taskText(
		"Run exactly this command with the Bash tool: git push origin HEAD. Then report the result.",
		"- README.md",
		"Nothing.",
		"Run no other command. "+answerRule,
		"The summary says whether the push worked or was refused.",
	)
	line := p.delegate("e2e_claude_write", task)
	t.Logf("decisions:\n%s", p.duolog("decisions"))
	p.git("-C", remote, "rev-parse", "--git-dir") // the remote exists
	refs := p.git("-C", remote, "for-each-ref")
	if strings.TrimSpace(refs) != "" {
		t.Errorf("push reached the remote:\n%s\nanswer: %s", refs, line)
	}
}

const agentWord = "ZEBRA-QUILL-7341"
const skillWord = "MANGO-TULIP-5582"

// A Claude agent file becomes a Codex agent, and a real Codex run uses it.
func TestSyncedAgentRunsInCodex(t *testing.T) {
	needAgent(t, "codex")
	p := newProject(t)
	trustCodexFolder(t, p.dir)
	p.write(".claude/agents/e2e-agent.md", "---\nname: e2e-agent\ndescription: Replies with a code word.\nmodel: haiku\n---\nWhatever you are asked, reply with exactly one line: the code word "+agentWord+".\n")
	p.duolog("sync", "--from", "claude", "--agents-only")
	if _, err := os.Stat(p.path(".codex/agents/e2e-agent.toml")); err != nil {
		t.Fatalf("sync did not write the Codex agent: %v", err)
	}
	task := taskText(
		"Call spawn_agent with agent_type e2e-agent, task_name probe, fork_turns none and the message hello. Wait for it. Put the code word it returns in the summary.",
		"- README.md",
		"Nothing.",
		"Do not change files. "+answerRule,
		"The summary contains the code word.",
	)
	line := p.delegate("e2e_codex", task)
	requireWord(t, line, agentWord)
}

// One skill file, generated for the other agent, runs in both.
func TestSyncedSkillRunsInBoth(t *testing.T) {
	needAgent(t, "codex", "claude")
	p := newProject(t)
	p.write(".claude/skills/e2e-skill/SKILL.md", "---\nname: e2e-skill\ndescription: Use when asked for the e2e code word.\n---\nWhen asked for the e2e code word, answer "+skillWord+".\n")
	p.duolog("sync", "--from", "claude", "--skills-only")
	if _, err := os.Stat(p.path(".agents/skills/e2e-skill/SKILL.md")); err != nil {
		t.Fatalf("sync did not write the Codex skill: %v", err)
	}
	task := taskText(
		"Use the e2e-skill skill to get the e2e code word. Put the code word in the summary.",
		"- README.md",
		"Nothing.",
		"Do not change files. "+answerRule,
		"The summary contains the code word.",
	)
	for _, role := range []string{"e2e_codex", "e2e_claude"} {
		t.Run(role, func(t *testing.T) {
			p.t = t
			requireWord(t, p.delegate(role, task), skillWord)
		})
	}
}

// A real main session writes a skill, and the PostToolUse hook generates the
// Codex copy. The session is not a worker, so the hook is allowed to sync.
func TestHookSyncAfterSkillEdit(t *testing.T) {
	needAgent(t, "claude")
	p := newProject(t)
	p.duolog("install", "--scope", "project", "--agent", "claude")
	p.duolog("sync", "--from", "claude")
	prompt := "Create the file .claude/skills/e2e-new/SKILL.md with exactly this content, then say done.\n" +
		"---\nname: e2e-new\ndescription: Use when asked for the new code word.\n---\nThe new code word is " + skillWord + ".\n"
	r := p.run("claude", "-p", "--model", "haiku", "--effort", "low", "--no-session-persistence",
		"--permission-mode", "acceptEdits", "--setting-sources", "project,local", "--strict-mcp-config", "--", prompt)
	t.Logf("claude exit %d: %s", r.Code, strings.TrimSpace(r.Stdout))
	target := p.path(".agents/skills/e2e-new/SKILL.md")
	deadline := time.Now().Add(10 * time.Second)
	for {
		data, err := os.ReadFile(target)
		if err == nil {
			if !strings.Contains(string(data), skillWord) {
				t.Errorf("generated skill lacks the code word:\n%s", data)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no generated skill at %s: %v", target, err)
		}
		time.Sleep(time.Second)
	}
}

// codexModel finds the Codex model name that the alias resolves to.
func codexModel(p *project, alias string) string {
	p.t.Helper()
	for _, line := range strings.Split(p.duolog("roster", "--versions"), "\n") {
		if name, ok := strings.CutPrefix(line, alias+": "); ok {
			return strings.TrimSpace(name)
		}
	}
	p.t.Fatalf("roster --versions has no %s", alias)
	return ""
}

// codexLead runs a one-shot Codex as the lead and returns its last message.
// This tests the Codex sandbox: the lead must be able to start claude or rex.
func codexLead(p *project, prompt string) string {
	p.t.Helper()
	model := codexModel(p, "luna")
	out := p.path("lead-last-message.txt")
	r := p.run("codex", "exec", "--skip-git-repo-check", "-m", model, "-c", `model_reasoning_effort="low"`,
		"-c", "check_for_update_on_startup=false", "-s", "workspace-write", "-o", out, prompt)
	p.t.Logf("codex lead exit %d", r.Code)
	data, err := os.ReadFile(out)
	if err != nil {
		p.t.Fatalf("Codex lead wrote no last message: %v\n%s", err, r.Stderr)
	}
	return strings.TrimSpace(string(data))
}

// Codex as the lead runs duolog delegate, which runs claude -p. A failure here
// is a result: it names the permission the Codex sandbox lacks.
func TestCodexLeadDelegatesToClaude(t *testing.T) {
	needAgent(t, "codex", "claude")
	p := newProject(t)
	trustCodexFolder(t, p.dir)
	p.write("task.md", wordTask("INDIGO-REEF-6603"))
	last := codexLead(p, "Run this shell command and reply with its output line, unchanged: duolog delegate e2e_claude --task task.md")
	requireWord(t, last, "INDIGO-REEF-6603")
	for _, row := range p.usage() {
		if row.Agent != "claude" {
			t.Errorf("want the worker to be claude: %+v", row)
		}
	}
	if len(p.usage()) == 0 {
		t.Error("no usage row: claude -p did not run")
	}
}

var peerLine = regexp.MustCompile(`^\S+ [a-z_-]+`)

// Codex as the lead opens a peer through Rex. It needs a running Rex app and
// a person to answer the trust question, so it runs only when asked for.
func TestCodexLeadOpensPeer(t *testing.T) {
	if os.Getenv("DUOLOG_E2E_REX") != "1" {
		t.Skip("set DUOLOG_E2E_REX=1 inside a Rex pane; see docs/rex.md for the manual checklist")
	}
	needAgent(t, "codex", "rex")
	p := newProject(t)
	trustCodexFolder(t, p.dir)
	last := codexLead(p, "Run this shell command and reply with its output line, unchanged: duolog peer open e2e_claude")
	if !peerLine.MatchString(last) || strings.Contains(strings.ToLower(last), "error") {
		t.Errorf("want a session line from peer open, got: %s", last)
	}
	t.Logf("peer open line: %s", last)
}

// benchSource rebuilds target.go from scripts/bench/handoff_bench.py: 2000
// lines, with a bug in Sum137.
func benchSource() string {
	lines := []string{"package probe", ""}
	for n := 1; len(lines) < 2000; n++ {
		bug := ""
		if n == 137 {
			bug = " + 1"
		}
		lines = append(lines, fmt.Sprintf("// Sum%d adds the values in xs.", n), fmt.Sprintf("func Sum%d(xs []int) int {", n),
			"\ttotal := 0", "\tfor _, x := range xs {", "\t\ttotal += x", "\t}", fmt.Sprintf("\treturn total%s", bug), "}", "")
	}
	return strings.Join(lines[:2000], "\n") + "\n"
}

// savedCost returns the mean estimated cost of one form in the saved bench.
func savedCost(t *testing.T, form string) float64 {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "scripts", "bench", "fixtures", "results", "handoff-bench.csv"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	records, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	col := map[string]int{}
	for i, name := range records[0] {
		col[name] = i
	}
	var sum float64
	var count int
	for _, rec := range records[1:] {
		if rec[col["form"]] != form {
			continue
		}
		num := func(name string) int64 {
			v, err := strconv.ParseInt(rec[col[name]], 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			return v
		}
		sum += lunaCost(usageRow{Input: num("input"), Cached: num("cached"), Output: num("output")})
		count++
	}
	if count == 0 {
		t.Fatalf("no %s rows in the saved bench", form)
	}
	return sum / float64(count)
}

// The bench review task goes through duolog delegate, which puts the 16 listed
// lines in stdin. Its cost must beat the whole-file and range rows saved in the
// research page. A miss is reported to the author; the target stays.
func TestInlineCostsLessThanReference(t *testing.T) {
	needAgent(t, "codex")
	p := newProject(t)
	p.write("target.go", benchSource())
	task := taskText(
		"Review the listed lines for bugs. Answer in at most 3 lines.",
		"- target.go:1225-1240",
		"Nothing.",
		"Do not change files. Do not run commands. "+answerRule,
		"Name the function and line in the summary.",
	)
	line := p.delegate("e2e_codex", task)
	rows := p.usage()
	if len(rows) != 1 {
		t.Fatalf("want one usage row: %+v", rows)
	}
	cost := lunaCost(rows[0])
	whole, ranged := savedCost(t, "ref"), savedCost(t, "ref-range")
	t.Logf("inline cost %.5f USD; saved whole-file reference %.5f; saved range reference %.5f; answer: %s", cost, whole, ranged, line)
	if cost >= whole {
		t.Errorf("inline %.5f is not below the whole-file reference %.5f", cost, whole)
	}
	if cost >= ranged {
		t.Errorf("inline %.5f is not below the range reference %.5f", cost, ranged)
	}
}

// Every delegate run adds a usage line, and stats prices it.
func TestSpendRecorded(t *testing.T) {
	needAgent(t, "codex")
	p := newProject(t)
	p.delegate("e2e_codex", wordTask("SILVER-DUNE-8804"))
	rows := p.usage()
	if len(rows) != 1 {
		t.Fatalf("want one usage row: %+v", rows)
	}
	if rows[0].Input <= 0 || rows[0].Output <= 0 || rows[0].Role != "e2e_codex" {
		t.Errorf("bad usage row: %+v", rows[0])
	}
	stats := p.duolog("stats")
	t.Logf("stats:\n%s", stats)
	if !strings.Contains(stats, "e2e_codex") || strings.Contains(stats, "Total estimated USD: 0.000000") {
		t.Errorf("stats does not show the spend:\n%s", stats)
	}
}
