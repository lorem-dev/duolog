package gate

import "testing"

func TestRememberPattern(t *testing.T) {
	for command, want := range map[string]string{
		".venv/bin/ruff check app/a.py": ".venv/bin/ruff check *",
		"make generate_types":           "make generate_types",
		"jq .name package.json":         "jq .name package.json",
		"npm run lint -- --fix":         "npm run lint *",
		// A program with no subcommand stays exact: pytest -p loads any plugin.
		"pytest -q tests/a.py": "pytest -q tests/a.py",
		"go test ./...":        "go test *",
		// A program that runs any code in its arguments stays exact.
		"python -m pytest tests/a.py":     "python -m pytest tests/a.py",
		"node scripts/gen.js":             "node scripts/gen.js",
		"python3.12 -m pytest tests/a.py": "python3.12 -m pytest tests/a.py",
		"awk -f formatter.awk a.txt":      "awk -f formatter.awk a.txt",
		"uv run script.py":                "uv run script.py",
		"go run ./cmd/x":                  "go run ./cmd/x",
		"docker compose up web":           "docker compose up web",
		"make check FOO=1":                "make check FOO=1",
		"npm --prefix web run lint":       "npm --prefix web run lint",
	} {
		if got := RememberPattern(command); got != want {
			t.Errorf("%q: %q, want %q", command, got, want)
		}
	}
}
