package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/gate"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
)

// newRememberCommand saves a command a person approved as an allow rule for
// workers, like "don't ask again" in the agents. It runs as the lead's own shell
// command, so the agent asks the person before it can widen the policy.
func newRememberCommand() *cobra.Command {
	var match string
	var roles []string
	native := false
	cmd := &cobra.Command{Use: "remember <command>", Short: "Allow a command for workers from now on", Args: cobra.MinimumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if isWorker() {
			return fmt.Errorf("workers cannot change the policy")
		}
		command := strings.Join(strings.Fields(strings.Join(args, " ")), " ")
		if command == "" {
			return fmt.Errorf("name the command to remember")
		}
		root, err := projectRoot()
		if err != nil {
			return err
		}
		cfg, err := config.Load(root)
		if err != nil {
			return err
		}
		if match == "" {
			match = rememberPattern(command)
		}
		if len(roles) == 0 {
			roles = writerRoles(cfg)
		}
		rule := config.Rule{Action: "exec", Match: match, Decision: gate.Allow, Roles: roles}
		// The rule must make this command allowed. A deny always wins, and
		// syntax the gate cannot read (pipes to a shell, substitutions, globs)
		// asks whatever the rules say, so saving a rule for it would mislead.
		policy := cfg.Policy
		policy.Rules = append(slices.Clone(policy.Rules), rule)
		for _, role := range roles {
			if _, ok := cfg.Roles[role]; !ok {
				return fmt.Errorf("unknown role %q", role)
			}
			if d := gate.Evaluate(policy, gate.LeadRules{}, mode.Edits, gate.Action{Kind: "exec", Target: command, Role: role}); d.Kind != gate.Allow {
				return fmt.Errorf("a rule cannot allow %q for %s: %s (%s)", command, role, d.Reason, d.Rule)
			}
		}
		// Open writes .duolog/.gitignore first, so the file stays out of Git.
		if _, err := store.Open(root); err != nil {
			return err
		}
		path := filepath.Join(root, ".duolog", config.LearnedFile)
		added, err := addLearnedRule(path, rule)
		if err != nil {
			return err
		}
		if !added {
			fmt.Fprintf(cmd.OutOrStdout(), "already remembered for workers: %s\n", match)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "remembered for workers: exec %q allow for %s in %s\n", match, strings.Join(roles, ", "), path)
		}
		if !native {
			return nil
		}
		// The same approval for the agents themselves, in the files their own
		// "don't ask again" writes, so the lead is not asked again either.
		claude, err := addClaudeAllow(filepath.Join(root, ".claude", "settings.local.json"), match)
		if err != nil {
			return err
		}
		codex, err := addCodexRule(filepath.Join(root, ".codex", "rules", "duolog.rules"), match)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "claude: %s\ncodex: %s (outside the sandbox, trusted project only)\n", claude, codex)
		return nil
	}}
	// Off by default: a Codex prefix rule also runs the command outside the
	// Codex sandbox, which is more than the worker rule grants.
	cmd.Flags().BoolVar(&native, "native", false, "Also allow it for the lead in .claude/settings.local.json and .codex/rules/duolog.rules")
	cmd.Flags().StringVar(&match, "match", "", "Glob to allow instead of the one derived from the command")
	cmd.Flags().StringSliceVar(&roles, "role", nil, "Role the rule applies to (repeat); default: every writing role")
	return cmd
}

// anyProgram runs code or fetches it from its arguments, so "python *" would
// allow every script. A version suffix counts too: python3.12 is python.
var anyProgram = []string{"python", "node", "nodejs", "deno", "bun", "ruby", "perl", "php", "lua", "julia", "rscript", "java", "dotnet", "awk", "gawk", "nawk", "mawk",
	"osascript", "curl", "wget", "npx", "pnpx", "bunx", "uvx", "pipx", "ssh", "scp", "rsync", "docker", "podman", "kubectl"}

// runSubcommands start a program named in the arguments: "uv run *" or
// "go run *" would allow any script.
var runSubcommands = []string{"run", "exec", "dlx", "x"}

// rememberPattern lets the arguments vary only after a program and its plain
// subcommand words, up to three words: ".venv/bin/ruff check app/a.py" becomes
// ".venv/bin/ruff check *" and "npm run lint -- --fix" "npm run lint *". A
// word with an option dash, a path or a value ends the plain words. A program
// with no subcommand, one that runs any code, or a run subcommand is
// remembered exactly as it was approved.
func rememberPattern(command string) string {
	words := strings.Fields(command)
	base := 1
	for base < len(words) && base < 3 && !strings.HasPrefix(words[base], "-") && !strings.ContainsAny(words[base], "/.=:@") {
		base++
	}
	program := strings.TrimRight(strings.ToLower(strings.TrimSuffix(filepath.Base(words[0]), ".exe")), "0123456789.")
	if base == 1 || base == len(words) || slices.Contains(anyProgram, program) || slices.Contains(runSubcommands, words[base-1]) {
		return command
	}
	return strings.Join(words[:base], " ") + " *"
}

func writerRoles(cfg *config.Config) []string {
	var roles []string
	for name, role := range cfg.Roles {
		if role.Sandbox == "workspace-write" {
			roles = append(roles, name)
		}
	}
	sort.Strings(roles)
	return roles
}

func addLearnedRule(path string, rule config.Rule) (bool, error) {
	var file struct {
		Rules []config.Rule `yaml:"rules"`
	}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if err == nil {
		if err := yaml.Unmarshal(data, &file); err != nil {
			return false, fmt.Errorf("%s: %w", path, err)
		}
	}
	if slices.ContainsFunc(file.Rules, func(have config.Rule) bool {
		return have.Action == rule.Action && have.Match == rule.Match && slices.Equal(have.Roles, rule.Roles)
	}) {
		return false, nil
	}
	file.Rules = append(file.Rules, rule)
	out, err := yaml.Marshal(file)
	if err != nil {
		return false, err
	}
	header := "# Commands a person allowed for workers with `duolog remember`. Deny rules still win.\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, append([]byte(header), out...), 0o644)
}

// nativePrefix turns a remembered glob into the command prefix the agents match:
// "ruff check *" is the prefix "ruff check"; a glob with no trailing " *" is exact.
func nativePrefix(match string) (prefix string, anyArgs bool) {
	if strings.HasSuffix(match, " *") {
		return strings.TrimSuffix(match, " *"), true
	}
	return match, false
}

// addClaudeAllow adds Bash(prefix:*) or Bash(command) to permissions.allow,
// keeping every other setting in the file.
func addClaudeAllow(path, match string) (string, error) {
	prefix, anyArgs := nativePrefix(match)
	entry := "Bash(" + prefix + ")"
	if anyArgs {
		entry = "Bash(" + prefix + ":*)"
	}
	settings := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			return "", fmt.Errorf("%s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	permissions, _ := settings["permissions"].(map[string]any)
	if permissions == nil {
		permissions = map[string]any{}
	}
	allow, _ := permissions["allow"].([]any)
	for _, have := range allow {
		if have == entry {
			return "already " + entry, nil
		}
	}
	permissions["allow"] = append(allow, entry)
	settings["permissions"] = permissions
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	return entry, os.WriteFile(path, append(out, '\n'), 0o644)
}

// addCodexRule appends a prefix_rule to the project's Codex rules. Codex reads
// <repo>/.codex/rules/*.rules only in a trusted project, and an allowed prefix
// runs outside the sandbox without a prompt, as Codex's own approvals do.
func addCodexRule(path, match string) (string, error) {
	prefix, _ := nativePrefix(match)
	quoted := make([]string, 0, 4)
	for _, word := range strings.Fields(prefix) {
		quoted = append(quoted, fmt.Sprintf("%q", word))
	}
	rule := fmt.Sprintf("prefix_rule(pattern = [%s], decision = \"allow\", justification = \"remembered with duolog remember\")\n", strings.Join(quoted, ", "))
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if strings.Contains(string(data), rule) {
		return "already " + strings.TrimSpace(rule), nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	defer f.Close()
	_, err = f.WriteString(rule)
	return strings.TrimSpace(rule), err
}
