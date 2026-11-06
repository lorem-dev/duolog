package synctest

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/agentfile"
	"github.com/lorem-dev/duolog/internal/core/config"
)

// Fixture is the data that FormatContract checks one format against.
type Fixture struct {
	// To is the agent that Source is converted for.
	To string
	// Config and Resolve are the settings of the conversion.
	Config  config.AgentSync
	Resolve func(alias string) (string, error)
	// Source is a hand-written agent file of the format and Parsed what it
	// parses to for To.
	Source []byte
	Parsed agentfile.Agent
	// Agent is an agent in the form of the format's own file and Rendered the
	// file that it renders to.
	Agent    agentfile.Agent
	Rendered []byte
	// SkillSrc is a SKILL.md; with Drop dropped it converts to SkillOut and
	// the warnings SkillWarnings.
	SkillSrc, SkillOut []byte
	Drop               []string
	SkillWarnings      []string
}

// NewFormat returns the format under test.
type NewFormat func(t *testing.T) agentfile.Format

// FormatContract runs the cases that every sync format passes.
func FormatContract(t *testing.T, newFormat NewFormat, fx Fixture) {
	t.Run("dirs", func(t *testing.T) { checkDirs(t, newFormat(t)) })
	t.Run("parse maps the model through model_map", func(t *testing.T) { checkParse(t, newFormat(t), fx) })
	t.Run("render is byte for byte and checks", func(t *testing.T) { checkRender(t, newFormat(t), fx) })
	t.Run("render refuses an agent with no name", func(t *testing.T) {
		if _, err := newFormat(t).RenderAgent(agentfile.Agent{}); err == nil {
			t.Fatal("RenderAgent of an empty agent returned nil")
		}
	})
	t.Run("skill drops keys and warns", func(t *testing.T) { checkSkill(t, newFormat(t), fx) })
}

func checkDirs(t *testing.T, f agentfile.Format) {
	t.Helper()
	d := f.Dirs()
	if d.Agents == "" || d.Skills == "" || !strings.HasPrefix(d.Ext, ".") {
		t.Fatalf("Dirs() = %+v, want an agents folder, a skills folder and an extension", d)
	}
}

func checkParse(t *testing.T, f agentfile.Format, fx Fixture) {
	t.Helper()
	opts := agentfile.AgentOpts{Config: fx.Config, To: fx.To, Resolve: fx.Resolve}
	got, err := f.ParseAgent("a"+f.Dirs().Ext, fx.Source, opts)
	if err != nil {
		t.Fatal(err)
	}
	if got != fx.Parsed {
		t.Fatalf("ParseAgent = %#v, want %#v", got, fx.Parsed)
	}
}

func checkRender(t *testing.T, f agentfile.Format, fx Fixture) {
	t.Helper()
	got, err := f.RenderAgent(fx.Agent)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, fx.Rendered) {
		t.Fatalf("RenderAgent = %q, want %q", got, fx.Rendered)
	}
	if err := f.Check("a"+f.Dirs().Ext, got); err != nil {
		t.Fatalf("Check of the rendered file: %v", err)
	}
	if err := f.Check("a"+f.Dirs().Ext, []byte("\x00not an agent file")); err == nil {
		t.Fatal("Check of garbage returned nil")
	}
}

func checkSkill(t *testing.T, f agentfile.Format, fx Fixture) {
	t.Helper()
	out, warns, err := f.Skill(fx.SkillSrc, fx.Drop)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, fx.SkillOut) {
		t.Fatalf("Skill = %q, want %q", out, fx.SkillOut)
	}
	if !slices.Equal(warns, fx.SkillWarnings) {
		t.Fatalf("Skill warnings = %q, want %q", warns, fx.SkillWarnings)
	}
}
