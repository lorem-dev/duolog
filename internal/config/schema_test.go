package config

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	validator "github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

func schemaBytes(t *testing.T) []byte {
	t.Helper()
	b, err := Schema()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSchemaFileIsCurrent(t *testing.T) {
	b, err := os.ReadFile("../../duolog-config.schema.json")
	if err != nil || !bytes.Equal(b, schemaBytes(t)) {
		t.Fatal("schema is stale; run make schema-write")
	}
}

func TestSchemaIdIsSchemaURL(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(schemaBytes(t), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["$id"] != SchemaURL || doc["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Fatalf("unexpected schema identity: %v", doc)
	}
}

func TestModelineNamesSchemaURL(t *testing.T) {
	first := strings.SplitN(string(defaultConfig), "\n", 2)[0]
	if first != "# yaml-language-server: $schema="+SchemaURL {
		t.Fatalf("unexpected modeline: %s", first)
	}
}

func TestEveryFieldHasDescription(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(schemaBytes(t), &doc); err != nil {
		t.Fatal(err)
	}
	var walk func(any, string)
	walk = func(v any, path string) {
		switch v := v.(type) {
		case map[string]any:
			if props, ok := v["properties"].(map[string]any); ok {
				for name, raw := range props {
					prop := raw.(map[string]any)
					if desc, ok := prop["description"].(string); !ok || strings.TrimSpace(desc) == "" {
						t.Errorf("missing description: %s.%s", path, name)
					}
				}
			}
			for name, child := range v {
				walk(child, path+"."+name)
			}
		case []any:
			for _, child := range v {
				walk(child, path)
			}
		}
	}
	walk(doc, "schema")
}

func compiledSchema(t *testing.T) *validator.Schema {
	t.Helper()
	var doc any
	if err := json.Unmarshal(schemaBytes(t), &doc); err != nil {
		t.Fatal(err)
	}
	c := validator.NewCompiler()
	if err := c.AddResource(SchemaURL, doc); err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile(SchemaURL)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func yamlDocument(t *testing.T, b []byte) any {
	t.Helper()
	var doc any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestDefaultsPassSchema(t *testing.T) {
	if err := compiledSchema(t).Validate(yamlDocument(t, defaultConfig)); err != nil {
		t.Fatal(err)
	}
}

func TestSchemaRejectsMistakes(t *testing.T) {
	s := compiledSchema(t)
	for _, tc := range mistakeCases {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.Validate(yamlDocument(t, []byte(tc.yaml))); err == nil {
				t.Fatal("schema accepted invalid configuration")
			}
		})
	}
}

func TestConfigSchemaReadsNothing(t *testing.T) {
	root := isolatedRoot(t)
	binary := filepath.Join(t.TempDir(), "duolog")
	if os.PathSeparator == '\\' {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "../../cmd/duolog")
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, b)
	}
	writeConfig(t, filepath.Join(root, ".duolog", "config.yaml"), "broken: [")
	cmd := exec.Command(binary, "config", "schema")
	cmd.Dir = root
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("schema command: %v\n%s", err, b)
	}
	checkedIn, err := os.ReadFile("../../duolog-config.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, checkedIn) {
		t.Fatal("command output differs from schema file")
	}
}
