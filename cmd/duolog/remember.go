package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/gofrs/flock"
	"github.com/lorem-dev/duolog/internal/app/remember"
	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	corepolicy "github.com/lorem-dev/duolog/internal/core/policy"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/wiring"
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
			match = corepolicy.RememberPattern(command)
		}
		if len(roles) == 0 {
			roles = writerRoles(cfg)
		}
		rule := config.Rule{Action: "exec", Match: match, Decision: corepolicy.Allow, Roles: roles}
		// The rule must make this command allowed. A deny always wins, and
		// syntax the gate cannot read (pipes to a shell, substitutions, globs)
		// asks whatever the rules say, so saving a rule for it would mislead.
		policy := cfg.Policy
		policy.Rules = append(slices.Clone(policy.Rules), rule)
		for _, role := range roles {
			if _, ok := cfg.Roles[role]; !ok {
				return fmt.Errorf("unknown role %q", role)
			}
			if d := corepolicy.Evaluate(policy, corepolicy.LeadRules{}, mode.Edits, corepolicy.Action{Kind: "exec", Target: command, Role: role}); d.Kind != corepolicy.Allow {
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
		return remember.Allow(cmd.OutOrStdout(), root, match, wiring.NativeAgents())
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
	rule := config.Rule{Action: "mcp", Match: tool, Decision: corepolicy.Allow, Roles: roles}
	policy := cfg.Policy
	policy.Rules = append(slices.Clone(policy.Rules), rule)
	for _, role := range roles {
		if _, ok := cfg.Roles[role]; !ok {
			return fmt.Errorf("unknown role %q", role)
		}
		if d := corepolicy.Evaluate(policy, corepolicy.LeadRules{}, mode.Edits, corepolicy.Action{Kind: "mcp", Target: sampleTool(tool), Role: role, MCPExact: true}); d.Kind != corepolicy.Allow {
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
	return remember.AllowMCP(out, root, server, name, wiring.NativeAgents())
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
