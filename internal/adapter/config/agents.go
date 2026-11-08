package config

import (
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/core/config"
)

// LoadOptions is what Load needs to know about the registered agents. wiring
// builds it from the registry.
type LoadOptions struct {
	// Agents are the registered agents' descriptors, in registry order.
	Agents []agent.Descriptor
}

// BuiltinAgents stands for the registry where Load gets no options: the tests
// of this layer and the security boundary's callers, whose function bodies may
// not change in this task. It carries only the facts that Load reads, and
// goes when those callers pass wiring.ConfigOptions. A wiring test holds it
// equal to the registry's descriptors on those facts.
func BuiltinAgents() []agent.Descriptor {
	return []agent.Descriptor{
		{Name: "claude", Display: "Claude", Config: agent.ConfigFacts{SubscriptionEndpoints: true}},
		{Name: "codex", Display: "Codex", Config: agent.ConfigFacts{SkillsDir: ".agents/skills"}},
	}
}

// agentsOf returns the descriptors Load works with: the options' agents, or
// the built-in ones.
func agentsOf(opts []LoadOptions) []agent.Descriptor {
	for _, o := range opts {
		if len(o.Agents) > 0 {
			return o.Agents
		}
	}
	return BuiltinAgents()
}

// descriptorOf returns the descriptor called name, the zero one when none is.
func descriptorOf(agents []agent.Descriptor, name string) agent.Descriptor {
	for _, a := range agents {
		if a.Name == name {
			return a
		}
	}
	return agent.Descriptor{}
}

// mappingEntry returns the value of key in a mapping node, or nil.
func mappingEntry(node *yaml.Node, key string) *yaml.Node {
	if node.Kind == yaml.AliasNode {
		node = node.Alias
	}
	return mappingValue(node, key)
}

func agentNames(agents []agent.Descriptor) []string {
	names := make([]string, len(agents))
	for i, a := range agents {
		names[i] = a.Name
	}
	return names
}

// agentKeyType is the type of an agent key of a flat agent map, or nil when
// typ has none: paid_runs, coauthors and a worker mode hold one key per agent
// beside their own keys.
func agentKeyType(typ reflect.Type) reflect.Type {
	switch typ {
	case reflect.TypeFor[config.PaidRuns]():
		return reflect.TypeFor[config.PaidAgent]()
	case reflect.TypeFor[config.Coauthors](), reflect.TypeFor[config.WorkerMode]():
		return reflect.TypeFor[string]()
	}
	return nil
}

// checkAgentSwitchKey checks one entry of the agents map: the key is a
// registered agent, and only an agent whose descriptor names a default skills
// folder has the skills_dir key.
func checkAgentSwitchKey(keyNode, value *yaml.Node, typ reflect.Type, path string, agents []agent.Descriptor) error {
	if typ != reflect.TypeFor[map[string]config.AgentSwitch]() {
		return nil
	}
	key := keyNode.Value
	if !slices.Contains(agentNames(agents), key) {
		return atLine(keyNode.Line, fmt.Errorf("%s.%s: unknown key", path, key))
	}
	if descriptorOf(agents, key).Config.SkillsDir == "" {
		if at := mappingEntry(value, "skills_dir"); at != nil {
			return atLine(at.Line, fmt.Errorf("%s.%s.skills_dir: unknown key", path, key))
		}
	}
	return nil
}

// cleanSkillsDir checks a skills folder and returns it in its clean form: a
// path below the project root, written with slashes. It refuses an empty
// value, an absolute path, a drive letter, a backslash, a path that leaves the
// root or is the root, and a folder that is .git, .duolog or an agent's own
// agents or Claude skills folder, or holds or sits inside one. Case does not
// matter for these, because the file system may ignore it.
func cleanSkillsDir(dir string) (string, error) {
	switch {
	case strings.TrimSpace(dir) == "":
		return "", fmt.Errorf("must not be empty")
	case strings.HasPrefix(dir, "/") || (len(dir) > 1 && dir[1] == ':'):
		return "", fmt.Errorf("%q must be relative to the project root, not absolute", dir)
	case strings.Contains(dir, `\`):
		return "", fmt.Errorf("%q must use slashes, not backslashes", dir)
	}
	clean := path.Clean(dir)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%q must stay below the project root", dir)
	}
	// Windows drops a trailing dot or space from a name, so ".claude./skills"
	// would be ".claude/skills" there.
	for _, part := range strings.Split(clean, "/") {
		if strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return "", fmt.Errorf("%q: a folder name must not end with a dot or a space", dir)
		}
	}
	if err := checkTakenFolders(dir, strings.ToLower(clean)); err != nil {
		return "", err
	}
	return clean, nil
}

// checkTakenFolders refuses a lower-case clean skills folder that is .git or
// .codex itself, or that is, holds or sits inside a folder that belongs to
// something else. The subfolders of .codex stay free, because Codex looks for
// skills there.
func checkTakenFolders(dir, lower string) error {
	if lower == ".codex" {
		return fmt.Errorf("%q must not be .codex itself", dir)
	}
	for _, taken := range []string{".git", ".claude/skills", ".claude/agents", ".codex/agents", ".duolog"} {
		if lower == taken || strings.HasPrefix(lower, taken+"/") || (taken != ".git" && strings.HasPrefix(taken, lower+"/")) {
			return fmt.Errorf("%q must not hold, be or sit inside %s", dir, taken)
		}
	}
	return nil
}

// skillsDirEntries returns the skills_dir node of each agent entry in the
// agents section of a layer, by agent name.
func skillsDirEntries(layer *yaml.Node) map[string]*yaml.Node {
	out := map[string]*yaml.Node{}
	if layer == nil {
		return out
	}
	if layer.Kind == yaml.DocumentNode && len(layer.Content) > 0 {
		layer = layer.Content[0]
	}
	section := mappingEntry(layer, "agents")
	if section == nil || section.Kind != yaml.MappingNode {
		return out
	}
	for i := 0; i+1 < len(section.Content); i += 2 {
		if at := mappingEntry(section.Content[i+1], "skills_dir"); at != nil {
			out[section.Content[i].Value] = at
		}
	}
	return out
}

// userSkillsDirError refuses skills_dir in the user config: the folder belongs
// to the repository, so only the project files may set it.
func userSkillsDirError(layer *yaml.Node) error {
	names := slices.Sorted(maps.Keys(skillsDirEntries(layer)))
	if len(names) == 0 {
		return nil
	}
	return fmt.Errorf("agents.%s.skills_dir: set it in .duolog/config.yaml or .duolog/config.local.yaml of the project, not in the user config", names[0])
}

// mainCheckout returns the main checkout of the Git repository that holds
// root, or "" when root is not a checkout, or is the main checkout itself.
func mainCheckout(root string) string {
	// Hooks call this on most tool calls, so a stuck git must not hold them.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--git-common-dir")
	cmd.Env = CleanGitEnv(os.Environ())
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	common := strings.TrimSpace(string(out))
	if !filepath.IsAbs(common) {
		common = filepath.Join(root, common)
	}
	common = filepath.Clean(common)
	if filepath.Base(common) != ".git" {
		return ""
	}
	main := filepath.Dir(common)
	if a, err := os.Stat(root); err == nil {
		if b, err := os.Stat(main); err == nil && os.SameFile(a, b) {
			return ""
		}
	}
	return main
}

// ProjectSkillsDirs reads agents.<name>.skills_dir from .duolog/config.yaml
// and .duolog/config.local.yaml under root, the later file winning, for the
// agents whose descriptor has the key. Commands read it from the folder whose
// files they write, which in a linked worktree is the worktree and not the
// checkout that holds the state. A value that is not valid is an error that
// names the key.
func ProjectSkillsDirs(root string, agents []agent.Descriptor) (map[string]string, error) {
	dirs := map[string]string{}
	for _, file := range []string{"config.yaml", "config.local.yaml"} {
		path := filepath.Join(root, ".duolog", file)
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) && file == "config.local.yaml" {
			// Git does not carry the local file into a linked worktree, and
			// Load reads the one of the main checkout.
			if main := mainCheckout(root); main != "" {
				path = filepath.Join(main, ".duolog", file)
				data, err = os.ReadFile(path)
			}
		}
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fileError(path, err)
		}
		if err := readSkillsDirs(dirs, data, agents); err != nil {
			return nil, fileError(path, err)
		}
	}
	return dirs, nil
}

// readSkillsDirs adds the skills_dir values of one config layer to dirs.
func readSkillsDirs(dirs map[string]string, data []byte, agents []agent.Descriptor) error {
	var layer yaml.Node
	if err := yaml.Unmarshal(data, &layer); err != nil {
		return err
	}
	for name, at := range skillsDirEntries(&layer) {
		if descriptorOf(agents, name).Config.SkillsDir == "" {
			continue
		}
		if at.Kind != yaml.ScalarNode || at.Tag != "!!str" {
			return fmt.Errorf("agents.%s.skills_dir: must be a string", name)
		}
		dir, err := cleanSkillsDir(at.Value)
		if err != nil {
			return fmt.Errorf("agents.%s.skills_dir: %w", name, err)
		}
		dirs[name] = dir
	}
	return nil
}

// validateAgentMaps checks the agent maps of the merged config.
func validateAgentMaps(cfg *config.Config, agents []agent.Descriptor) error {
	if err := validateAgents(cfg, agents); err != nil {
		return err
	}
	return validatePaidRuns(cfg, agents)
}

// validateAgents finishes the agent maps: every registered agent has a switch,
// off when nothing switched it on, an agent's paid settings that name no
// account take auto, and the model rows name registered agents and agree with
// model_map. An agent in routing.agent_order that is not registered is
// skipped when a fallback is chosen, not refused here.
func validateAgents(cfg *config.Config, agents []agent.Descriptor) error {
	if cfg.Agents == nil {
		cfg.Agents = map[string]config.AgentSwitch{}
	}
	for _, a := range agents {
		s := cfg.Agents[a.Name]
		if s.Enabled == nil {
			off := false
			s.Enabled = &off
		}
		if s.SkillsDir != nil {
			dir, err := cleanSkillsDir(*s.SkillsDir)
			if err != nil {
				return fmt.Errorf("agents.%s.skills_dir: %w", a.Name, err)
			}
			s.SkillsDir = &dir
		}
		cfg.Agents[a.Name] = s
		if p, ok := cfg.PaidRuns.Agents[a.Name]; ok && p.Account == "" {
			p.Account = "auto"
			cfg.PaidRuns.Agents[a.Name] = p
		}
	}
	names := agentNames(agents)
	for i, row := range cfg.Sync.Agents.ModelRows {
		for _, key := range slices.Sorted(maps.Keys(row)) {
			if !slices.Contains(names, key) {
				return fmt.Errorf("sync.agents.model_rows[%d].%s: unknown key", i, key)
			}
		}
	}
	return cfg.Sync.Agents.CheckRows()
}
