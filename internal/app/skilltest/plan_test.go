package skilltest

import (
	"os"
	"strings"
	"testing"
)

func TestDuologRegressionPlan(t *testing.T) {
	f, err := os.Open("testdata/duolog.plan.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	p, err := ReadPlan(f)
	if err != nil || len(p.Situations) != 9 {
		t.Fatalf("%+v %v", p, err)
	}
	for _, answer := range []string{"Use the Agent tool with sonnet and low effort", "spawn_agent model=terra reasoning_effort=low"} {
		if !p.Situations[0].Check.Match(answer) {
			t.Errorf("rejected %s", answer)
		}
	}
	if p.Situations[0].Check.Match("duolog delegate to an agent with sonnet") {
		t.Fatal("accepted repeat delegation")
	}
	if !p.Situations[3].Check.Match("gh pr checks --watch\nduolog peer wait peer-ab12") {
		t.Fatal("rejected independent waits")
	}
	if p.Situations[3].Check.Match("gh pr checks --watch && duolog peer wait peer-ab12") {
		t.Fatal("accepted chained waits")
	}
	if len(p.ForAgent("claude").Situations) != 8 || len(p.ForAgent("codex").Situations) != 9 {
		t.Fatal("Codex situation was not filtered")
	}
	last := p.Situations[len(p.Situations)-1]
	if last.ID != "s11" {
		t.Fatalf("expected sandbox regression, got %s", last.ID)
	}
	for _, answer := range []string{"Run it outside the sandbox", "Request escalation", "Set sandbox_permissions to require_escalated"} {
		if !last.Check.Match(answer) {
			t.Errorf("rejected %s", answer)
		}
	}
	if last.Check.Match("Retry the same command in the sandbox") {
		t.Fatal("accepted retry inside sandbox")
	}
}

func TestChecksAndStrictPlan(t *testing.T) {
	p, err := ReadPlan(strings.NewReader("situations:\n- id: s1\n  text: What next?\n  check:\n    contains: [peer, answer]\n    absent: [allow]\n    starts: DUOLOG\n    regex: 'answer\\s+p1'\n"))
	if err != nil {
		t.Fatal(err)
	}
	c := p.Situations[0].Check
	for _, tt := range []struct {
		answer string
		want   bool
	}{
		{"duolog PEER ANSWER p1", true}, {"duolog peer answer p1 allow", false},
		{"other duolog peer answer p1", false}, {"duolog peer answer p2", false}, {"", false},
	} {
		if got := c.Match(tt.answer); got != tt.want {
			t.Errorf("%q: %v", tt.answer, got)
		}
	}
	for _, bad := range []string{
		"situations: []", "situations: [{id: s1, text: x, check: {regex: '['}}]",
		"situations: [{id: s1, text: x, check: {typo: x}}]",
		"situations: [{id: s1, text: x, check: {starts: x}}, {id: s1, text: y, check: {starts: y}}]",
		"situations: [{id: s1, text: x, check: {}}]",
		"situations: []\n---\nsituations: []",
	} {
		if _, err := ReadPlan(strings.NewReader(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestGradeRejectsBadAnswers(t *testing.T) {
	p, err := ReadPlan(strings.NewReader("situations: [{id: s1, text: x, check: {starts: yes}}]"))
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{}`, `null`, `{"s1":false}`, `{"s1":""}`, `{"s1":"yes"} trailing`, `{"s1":"no"}`} {
		if p.Grade(raw)["s1"] {
			t.Errorf("accepted %s", raw)
		}
	}
	if !p.Grade(`{"s1":"YES please"}`)["s1"] {
		t.Fatal("rejected answer")
	}
}

func TestAgentSpecificSituations(t *testing.T) {
	p, err := ReadPlan(strings.NewReader(`situations:
  - {id: shared, text: Shared question, check: {starts: yes}}
  - {id: claude, agents: [claude], text: Claude question, check: {starts: yes}}
  - {id: codex, agents: [codex], text: Codex question, check: {starts: yes}}
  - {id: both, agents: [claude, codex], text: Both question, check: {starts: yes}}
`))
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"claude", "codex"} {
		other := "claude"
		if agent == "claude" {
			other = "codex"
		}
		selected := p.ForAgent(agent)
		if len(selected.Situations) != 3 {
			t.Fatalf("%s: %+v", agent, selected)
		}
		prompt := selected.Prompt("skill")
		if strings.Contains(prompt, strings.ToUpper(other[:1])+other[1:]+" question") {
			t.Fatalf("%s received other agent's situation: %s", agent, prompt)
		}
		graded := selected.Grade(`{"shared":"yes","claude":"yes","codex":"yes","both":"yes"}`)
		if _, present := graded[other]; present {
			t.Errorf("graded skipped %s", other)
		}
		if !graded[agent] || !graded["shared"] || !graded["both"] {
			t.Fatalf("%s: %+v", agent, graded)
		}
	}
	if len(p.Situations) != 4 {
		t.Fatal("filter changed original plan")
	}
	if _, err := ReadPlan(strings.NewReader("situations: [{id: x, agents: [typo], text: question, check: {starts: yes}}]")); err == nil {
		t.Fatal("accepted unknown agent")
	}
}
