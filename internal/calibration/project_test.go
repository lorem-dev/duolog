package calibration

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestRenderProjectIsStable(t *testing.T) {
	p := sampleProject()
	a, err := RenderProject(p)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := RenderProject(p)
	if !bytes.Equal(a, b) {
		t.Fatalf("renders differ:\n%s\n---\n%s", a, b)
	}
	if !strings.HasPrefix(string(a), "# Shared calibration evidence for this project. Written by duolog calibrate propose --apply-project.\n") {
		t.Fatalf("render lacks the header:\n%s", a)
	}
}

func TestValidateProjectRefusesVersionPins(t *testing.T) {
	for _, fam := range []string{"claude-opus-9", "gpt-9"} {
		p := sampleProject()
		p.Evidence = map[string][]EvidenceRow{"docs": {{Agent: "claude", Family: fam, Effort: "high", Tasks: 3, Date: "2026-11-05", DuologVersion: "0.9.0"}}}
		if err := ValidateProject(p); err == nil || !strings.Contains(err.Error(), "family") {
			t.Fatalf("ValidateProject(%s) = %v, want a family error", fam, err)
		}
	}
	p := sampleProject()
	p.Evidence = map[string][]EvidenceRow{"docs": {{Agent: "claude", Family: "opus", Effort: "high", Tasks: 3, Date: "2026-11-05", DuologVersion: "0.9.0"}}}
	if err := ValidateProject(p); err != nil {
		t.Fatalf("ValidateProject(opus/high) = %v", err)
	}
}

func TestValidateProjectRefusesUnknownFamilies(t *testing.T) {
	for _, row := range []EvidenceRow{
		{Agent: "claude", Family: "sonnet-latest"},
		{Agent: "claude", Family: "robin@example.invalid"},
		{Agent: "claude", Family: "sol"},
		{Agent: "codex", Family: "opus"},
	} {
		row.Effort, row.Tasks, row.Date, row.DuologVersion = "low", 3, "2026-11-05", "0.9.0"
		p := sampleProject()
		p.Evidence = map[string][]EvidenceRow{"docs": {row}}
		if err := ValidateProject(p); err == nil || !strings.Contains(err.Error(), "family") {
			t.Errorf("ValidateProject(%s/%s) = %v, want a family error", row.Agent, row.Family, err)
		}
	}
	p := sampleProject()
	p.Evidence = map[string][]EvidenceRow{"docs": {{Agent: "codex", Family: "terra", Effort: "low", Tasks: 3, Date: "2026-11-05", DuologVersion: "0.9.0"}}}
	if err := ValidateProject(p); err != nil {
		t.Fatalf("ValidateProject(codex/terra) = %v", err)
	}
}

func TestValidateProjectRefusesPrivateFeatures(t *testing.T) {
	for _, features := range []map[string]string{
		{"cost_usd": "42"},
		{"quota": "75%"},
		{"author": "Robin Sample"},
		{"scope": "robin@example.invalid"},
		{"role": "Robin Sample <robin@example.invalid>"},
		{"role": "Signed-off-by: Robin Sample"},
	} {
		p := sampleProject()
		p.Tasks[0].Features = features
		if err := ValidateProject(p); err == nil {
			t.Errorf("ValidateProject accepts features %v", features)
		}
		if _, _, err := WriteProject(stateDir(t), p); err == nil {
			t.Errorf("WriteProject publishes features %v", features)
		}
	}
	p := sampleProject()
	p.Tasks[0].Features = map[string]string{"output": "code", "scope": "4-20", "components": "2+", "role": "implement"}
	if err := ValidateProject(p); err != nil {
		t.Fatalf("ValidateProject(classification features) = %v", err)
	}
}

func TestValidateProjectRefusesIdentitiesInStatements(t *testing.T) {
	for _, statement := range []string{
		"Ask robin@example.invalid before changing the cache.",
		"Ask Robin Sample <robin@example.invalid> before changing the cache.",
		"Change the cache.\n\nCo-Authored-By: Robin Sample",
		"Change the cache.\n  Reviewed-by: Robin Sample",
	} {
		p := sampleProject()
		p.Tasks[0].Statement = statement
		if err := ValidateProject(p); err == nil {
			t.Errorf("ValidateProject accepts statement %q", statement)
		}
	}
}

// TestWriteProjectChecksEveryFreeTextField puts a private fact into each
// free-text field of the shared part in turn; neither the check of the
// preview nor the writer lets it into Git.
func TestWriteProjectChecksEveryFreeTextField(t *testing.T) {
	const name = "Robin Sample"
	for field, change := range map[string]func(p *ProjectPart){
		"statement figure":   func(p *ProjectPart) { p.Tasks[0].Statement = "Keep my weekly usage below 75% and spend below $42." },
		"statement tokens":   func(p *ProjectPart) { p.Tasks[0].Statement = "The cache run used 120K tokens; make it smaller." },
		"statement name":     func(p *ProjectPart) { p.Tasks[0].Statement = "Change the cache as Robin Sample asked." },
		"statement source":   func(p *ProjectPart) { p.Tasks[0].StatementSource = "robin@example.invalid" },
		"class":              func(p *ProjectPart) { p.Tasks[0].Class = "Signed-off-by: Robin" },
		"verification":       func(p *ProjectPart) { p.Tasks[0].Verification = "<robin@example.invalid>" },
		"source":             func(p *ProjectPart) { p.Tasks[0].Sources = []string{"pr #3 by robin@example.invalid"} },
		"hidden test":        func(p *ProjectPart) { p.Tasks[0].Hidden = []string{"robin@example.invalid/x_test.go"} },
		"survey language":    func(p *ProjectPart) { p.Survey.Languages = []string{"go", "quota 80%"} },
		"survey build":       func(p *ProjectPart) { p.Survey.Build = []string{"make USER=robin@example.invalid"} },
		"survey test":        func(p *ProjectPart) { p.Survey.Test = []string{"go test ./... # 30% of my weekly limit"} },
		"survey component":   func(p *ProjectPart) { p.Survey.Components = []string{"robin@example.invalid"} },
		"task id":            func(p *ProjectPart) { p.Tasks[0].ID = "robin@example.invalid" },
		"evidence class key": func(p *ProjectPart) { p.Evidence["robin@example.invalid"] = p.Evidence["docs"] },
		// A person's name is refused in every free-text field, not only
		// in the statements.
		"source name":         func(p *ProjectPart) { p.Tasks[0].Sources = []string{"pr #3 from Robin Sample"} },
		"hidden test name":    func(p *ProjectPart) { p.Tasks[0].Hidden = []string{"robin sample/x_test.go"} },
		"verification name":   func(p *ProjectPart) { p.Tasks[0].Verification = "Robin Sample" },
		"statement src name":  func(p *ProjectPart) { p.Tasks[0].StatementSource = "Robin Sample" },
		"survey test name":    func(p *ProjectPart) { p.Survey.Test = []string{"make test OWNER='Robin Sample'"} },
		"survey comp name":    func(p *ProjectPart) { p.Survey.Components = []string{"Robin Sample"} },
		"task id name":        func(p *ProjectPart) { p.Tasks[0].ID = "Robin Sample" },
		"evidence class name": func(p *ProjectPart) { p.Evidence["Robin Sample"] = p.Evidence["docs"] },
		"duolog version":      func(p *ProjectPart) { p.Evidence["docs"][0].DuologVersion = "1.3.0 by robin@example.invalid" },
		"duolog version name": func(p *ProjectPart) { p.Evidence["docs"][0].DuologVersion = "Robin Sample" },
		"duolog version none": func(p *ProjectPart) { p.Evidence["docs"][0].DuologVersion = "" },
	} {
		p := sampleProject()
		change(&p)
		if err := ValidateProject(p, name); err == nil {
			t.Errorf("%s: ValidateProject accepts %+v", field, p)
		}
		if _, _, err := WriteProject(stateDir(t), p, name); err == nil {
			t.Errorf("%s: WriteProject publishes %+v", field, p)
		}
	}
	if _, _, err := WriteProject(stateDir(t), sampleProject(), name); err != nil {
		t.Fatalf("WriteProject(sample) = %v", err)
	}
}

// TestValidateProjectOneWordNamesInStatementsOnly: a committer called "ci"
// or "test" does not refuse a command, path or component that holds the
// word, while a statement that holds it is still refused.
func TestValidateProjectOneWordNamesInStatementsOnly(t *testing.T) {
	names := []string{"ci", "test", "Robin Sample"}
	p := sampleProject()
	p.Survey.Test = []string{"go test ./...", "make ci"}
	p.Survey.Components = []string{"ci"}
	p.Tasks[0].Sources = []string{"commit abc123 on ci"}
	p.Tasks[0].Hidden = []string{"internal/cache/cache_test.go", "test/cache.sh"}
	p.Tasks[0].Verification = "go test ./internal/cache"
	if err := ValidateProject(p, names...); err != nil {
		t.Fatalf("ValidateProject with one-word names outside the statements = %v", err)
	}
	if _, _, err := WriteProject(stateDir(t), p, names...); err != nil {
		t.Fatalf("WriteProject with one-word names outside the statements = %v", err)
	}
	p.Tasks[0].Statement = "Make the cache test pass on ci."
	if err := ValidateProject(p, names...); err == nil {
		t.Error("ValidateProject accepts a statement holding one-word history names")
	}
}

func TestValidateProjectDuologVersions(t *testing.T) {
	for version, ok := range map[string]bool{
		"1.3.0": true, "v1.3.0": true, "1.4.0-rc.1": true, "1.3.1-SNAPSHOT-0a1b2c3": true, "1.3.0+dirty": true,
		"latest": false, "1.3": false, "1.3.0 (dev)": false, "1.3.0\nCo-Authored-By: x": false,
	} {
		p := sampleProject()
		p.Evidence["docs"][0].DuologVersion = version
		if err := ValidateProject(p); (err == nil) != ok {
			t.Errorf("ValidateProject(duolog_version %q) = %v, want ok %v", version, err, ok)
		}
	}
}

func TestProjectPartHasNoPersonalFields(t *testing.T) {
	banned := []string{"usd", "cost", "token", "quota", "author", "email", "account", "second", "minute"}
	for _, typ := range []reflect.Type{
		reflect.TypeOf(ProjectPart{}),
		reflect.TypeOf(SurveyFacts{}),
		reflect.TypeOf(ManifestTask{}),
		reflect.TypeOf(EvidenceRow{}),
	} {
		for i := 0; i < typ.NumField(); i++ {
			name := strings.ToLower(typ.Field(i).Name)
			for _, word := range banned {
				if strings.Contains(name, word) {
					t.Errorf("%s.%s contains %q", typ.Name(), typ.Field(i).Name, word)
				}
			}
		}
	}
}
