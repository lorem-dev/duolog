package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/gofrs/flock"
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
	cmd := &cobra.Command{Use: "remember <command> | mcp:<server>/<tool>", Short: "Allow a command or an MCP tool for workers from now on", Args: cobra.MinimumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
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
		if tool, ok := strings.CutPrefix(command, "mcp:"); ok {
			return rememberMCP(cmd, root, cfg, tool, roles, native)
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
		fmt.Fprintf(cmd.OutOrStdout(), "claude: %s\n", claude)
		// A Codex prefix rule also matches extra arguments and runs them outside
		// the sandbox, so an exact command gets no Codex rule.
		if _, anyArgs := nativePrefix(match); !anyArgs {
			fmt.Fprintln(cmd.OutOrStdout(), "codex: no rule; Codex rules match a prefix, and this command is exact")
			return nil
		}
		codex, err := addCodexRule(filepath.Join(root, ".codex", "rules", "duolog.rules"), match)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "codex: %s (outside the sandbox, trusted project only)\n", codex)
		return nil
	}}
	// Off by default: a Codex prefix rule also runs the command outside the
	// Codex sandbox, which is more than the worker rule grants.
	// Options after the first word belong to the command: remember npm --prefix web run lint.
	cmd.Flags().SetInterspersed(false)
	cmd.Flags().BoolVar(&native, "native", false, "Also allow it for the lead in .claude/settings.local.json and .codex/rules/duolog.rules")
	cmd.Flags().StringVar(&match, "match", "", "Glob to allow instead of the one derived from the command")
	cmd.Flags().StringSliceVar(&roles, "role", nil, "Role the rule applies to (repeat); default: every writing role")
	return cmd
}

// anyProgram runs code or fetches it from its arguments, so "python *" would
// allow every script and "make *" any makefile (make -f). A version suffix
// counts too: python3.12 is python.
var anyProgram = []string{"make", "python", "node", "nodejs", "deno", "bun", "ruby", "perl", "php", "lua", "julia", "rscript", "java", "dotnet", "awk", "gawk", "nawk", "mawk",
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

// addLearnedRule adds rule to learned.yaml once. The read, change and write hold
// one lock, so two remember runs at once keep both rules, and the file is
// replaced through a rename, so the gate never reads a half-written policy.
func addLearnedRule(path string, rule config.Rule) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	lock := flock.New(filepath.Join(filepath.Dir(path), "learned.lock"))
	if err := lock.Lock(); err != nil {
		_ = lock.Close()
		return false, err
	}
	defer func() { _ = lock.Unlock(); _ = lock.Close() }()
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
	return true, replaceFile(path, append([]byte(header), out...), 0o644)
}

// replaceFile writes data to a temporary file beside path and renames it over
// path, so a reader sees the old file or the new one, never part of it.
func replaceFile(path string, data []byte, perm os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(data)
	chmodErr := f.Chmod(perm)
	closeErr := f.Close()
	if err := errors.Join(writeErr, chmodErr, closeErr); err != nil {
		return err
	}
	return store.Rename(f.Name(), path)
}

// nativePrefix turns a remembered glob into the command prefix the agents match:
// "ruff check *" is the prefix "ruff check"; a glob with no trailing " *" is exact.
func nativePrefix(match string) (prefix string, anyArgs bool) {
	if strings.HasSuffix(match, " *") {
		return strings.TrimSuffix(match, " *"), true
	}
	return match, false
}

// rememberMCP saves an allow rule for one MCP tool, or a glob of tools, of
// one server. duolog cannot tell whether a tool writes, so the default roles
// are the writing roles, as for commands; --role names others.
func rememberMCP(cmd *cobra.Command, root string, cfg *config.Config, tool string, roles []string, native bool) error {
	server, name, ok := strings.Cut(tool, "/")
	if !ok || server == "" || name == "" || strings.ContainsAny(tool, " \t") {
		return fmt.Errorf("name the tool as mcp:<server>/<tool>, such as mcp:tracker/get_issue")
	}
	if len(roles) == 0 {
		roles = writerRoles(cfg)
	}
	rule := config.Rule{Action: "mcp", Match: tool, Decision: gate.Allow, Roles: roles}
	policy := cfg.Policy
	policy.Rules = append(slices.Clone(policy.Rules), rule)
	for _, role := range roles {
		if _, ok := cfg.Roles[role]; !ok {
			return fmt.Errorf("unknown role %q", role)
		}
		if d := gate.Evaluate(policy, gate.LeadRules{}, mode.Edits, gate.Action{Kind: "mcp", Target: sampleTool(tool), Role: role, MCPExact: true}); d.Kind != gate.Allow {
			return fmt.Errorf("a rule cannot allow mcp:%s for %s: %s (%s)", tool, role, d.Reason, d.Rule)
		}
	}
	if _, err := store.Open(root); err != nil {
		return err
	}
	path := filepath.Join(root, ".duolog", config.LearnedFile)
	added, err := addLearnedRule(path, rule)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if !added {
		fmt.Fprintf(out, "already remembered for workers: mcp %s\n", tool)
	} else {
		fmt.Fprintf(out, "remembered for workers: mcp %q allow for %s in %s\n", tool, strings.Join(roles, ", "), path)
	}
	if !native {
		return nil
	}
	// Claude names the tool mcp__<server>__<tool>; a rule of mcp__<server>
	// covers every tool of the server. Other globs have no Claude form.
	entry := "mcp__" + claudeName(server)
	switch {
	case strings.ContainsAny(server, "*?[{"):
		entry = ""
	case name == "*":
	case !strings.ContainsAny(name, "*?[{"):
		entry += "__" + claudeName(name)
	default:
		entry = ""
	}
	if entry == "" {
		fmt.Fprintln(out, "claude: no rule; Claude allows one MCP tool or a whole server, not a glob")
	} else {
		claude, err := addClaudeEntry(filepath.Join(root, ".claude", "settings.local.json"), entry)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "claude: %s\n", claude)
	}
	// Codex approves MCP tools per server in config.toml, and an entry there
	// for a server the project does not define would add a broken server.
	fmt.Fprintln(out, "codex: no rule; Codex sets MCP tool approval per server in its own config.toml")
	return nil
}

var claudeNameChars = regexp.MustCompile(`[^A-Za-z0-9_-]`)

func claudeName(s string) string { return claudeNameChars.ReplaceAllString(s, "_") }

// addClaudeAllow adds Bash(prefix:*) or Bash(command) to permissions.allow,
// keeping every other setting in the file.
func addClaudeAllow(path, match string) (string, error) {
	prefix, anyArgs := nativePrefix(match)
	entry := "Bash(" + prefix + ")"
	if anyArgs {
		entry = "Bash(" + prefix + ":*)"
	}
	return addClaudeEntry(path, entry)
}

// addClaudeEntry adds entry to permissions.allow, keeping every other setting.
func addClaudeEntry(path, entry string) (string, error) {
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

var firstAlternative = regexp.MustCompile(`\{([^,}]*)[^}]*\}|\[[^]]*\]|[*?]`)

// sampleTool turns a pattern such as {tracker,wiki}/get_* into one tool it
// covers, tracker/get_x, for the check that the new rule allows it.
func sampleTool(pattern string) string {
	return firstAlternative.ReplaceAllStringFunc(pattern, func(m string) string {
		if strings.HasPrefix(m, "{") {
			return firstAlternative.FindStringSubmatch(m)[1]
		}
		return "x"
	})
}
