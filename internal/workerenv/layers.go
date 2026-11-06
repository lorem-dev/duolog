package workerenv

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/adapter/kit"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
	"github.com/pelletier/go-toml/v2"
)

// resolveCodex resolves the Codex profile and model provider across layers,
// as Codex does after it merges them. The launch's --profile wins over every
// profile key, and otherwise the last key wins. Every layer that defines the
// selected profile adds the signals of its definition, and the last one that
// names a provider there selects it over the top-level selections. A
// selected profile that no layer defines, and no profile file the launch
// names holds, is profile:unresolved, which is paid.
func resolveCodex(layers []LayerFacts) ([]Signal, []string) {
	provider, providerBy := "", ""
	profile, profileBy, launch := "", "", false
	for _, layer := range layers {
		if layer.Provider != "" {
			provider, providerBy = layer.Provider, layer.Layer
		}
		if layer.LaunchProfile != "" {
			profile, profileBy, launch = layer.LaunchProfile, "profile", true
		} else if layer.Profile != "" && !launch {
			profile, profileBy = layer.Profile, layer.Layer
		}
	}
	var signals []Signal
	if profile != "" {
		defined := false
		for _, layer := range layers {
			// The profile file a launch names is a definition when it exists.
			if launch && layer.Layer == "profile" && !layer.Modified.IsZero() {
				defined = true
			}
			p, ok := layer.Profiles[profile]
			if !ok {
				continue
			}
			defined = true
			for _, name := range p.Signals {
				signals = append(signals, Signal{Name: name, Layer: layer.Layer})
			}
			if p.Provider != "" {
				provider, providerBy = p.Provider, layer.Layer
			}
		}
		if !defined {
			signals = append(signals, Signal{Name: "profile:unresolved", Layer: profileBy})
		}
	}
	more, keys := resolveCodexProvider(layers, provider, providerBy)
	return append(signals, more...), keys
}

// resolveCodexProvider resolves the Codex model provider that layer by
// selected against the definitions in every layer. A definition in any layer
// that fails the first-party test is the model_provider signal of that
// layer, since layers merge and a later one cannot remove a base URL or a
// token; a selection no layer defines is model_provider:unresolved, both
// paid, except the built-in openai, which needs no definition. It returns
// the definitions' env_key names, which no free process gets.
func resolveCodexProvider(layers []LayerFacts, selected, by string) ([]Signal, []string) {
	if selected == "" {
		return nil, nil
	}
	var signals []Signal
	var keys []string
	defined := false
	for _, layer := range layers {
		p, ok := layer.Providers[selected]
		if !ok {
			continue
		}
		defined = true
		if !p.FirstParty {
			signals = append(signals, Signal{Name: "model_provider", Layer: layer.Layer})
		}
		if p.EnvKey != "" {
			keys = append(keys, p.EnvKey)
		}
	}
	if !defined && selected != builtinOpenAI {
		signals = append(signals, Signal{Name: "model_provider:unresolved", Layer: by})
	}
	return signals, keys
}

var (
	// listFiles names the regular files in dir; tests replace it.
	listFiles = func(dir string) ([]string, error) {
		entries, err := os.ReadDir(dir)
		var names []string
		for _, entry := range entries {
			if !entry.IsDir() {
				names = append(names, entry.Name())
			}
		}
		return names, err
	}
	// NonFilePolicy reports whether managed Claude policy exists outside
	// files, such as a device management profile or the Windows registry.
	// duolog cannot read it, so it adds an unreadable managed layer. This
	// release does not look for such policy (ruling 3): it finds none, runs
	// are not refused for it, and doctor and the docs name it as a limit.
	// The platform code that discovers such policy replaces it.
	NonFilePolicy = func(goos string) bool { return false }
	// codexTrusted reports whether the Codex config in codexHome trusts the
	// project folder root; tests replace it.
	codexTrusted = codexTrustedIn
	// accountHome is the home folder of duolog's own account, read from the
	// account rather than from HOME or USERPROFILE; empty when it is
	// unknown. Tests replace it.
	accountHome = func() string {
		u, err := user.Current()
		if err != nil {
			return ""
		}
		return u.HomeDir
	}
)

// SettingsLayers lists the settings files agent reads for a launch at loc,
// from the lowest precedence to the highest. A file may be missing; ReadLayer
// treats it as absent. The argv layers come from ArgvLayers.
func SettingsLayers(agent string, loc Locations) []Layer {
	switch agent {
	case "claude":
		return claudeLayers(loc)
	case "codex":
		return codexLayers(loc)
	}
	return nil
}

// claudeLayers lists the user file in the config directory, settings.json
// and then settings.local.json of the project root and each folder above it
// up to the home folder, and managed policy with its drop-in files.
func claudeLayers(loc Locations) []Layer {
	var layers []Layer
	dir, ok := lookupEnv(loc, "CLAUDE_CONFIG_DIR")
	if !ok || dir == "" {
		dir = filepath.Join(loc.Home, ".claude")
	}
	if dir != ".claude" {
		layers = append(layers, Layer{Name: "user", Path: filepath.Join(dir, "settings.json")})
	}
	folders := projectFolders(loc)
	for _, file := range []struct{ name, file string }{{"project", "settings.json"}, {"local", "settings.local.json"}} {
		for _, folder := range folders {
			layers = append(layers, Layer{Name: file.name, Path: filepath.Join(folder, ".claude", file.file)})
		}
	}
	root, sep := managedRoot(loc)
	layers = append(layers, Layer{Name: "managed", Path: root + sep + "managed-settings.json", Managed: true})
	dropIns := root + sep + "managed-settings.d"
	names, err := listFiles(dropIns)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		// A folder that cannot be listed may hold policy: unreadable.
		layers = append(layers, Layer{Name: "managed", Path: dropIns, Managed: true})
	default:
		slices.Sort(names)
		for _, name := range names {
			if strings.HasSuffix(name, ".json") && !strings.HasPrefix(name, ".") {
				layers = append(layers, Layer{Name: "managed", Path: dropIns + sep + name, Managed: true})
			}
		}
	}
	if NonFilePolicy(loc.GOOS) {
		layers = append(layers, Layer{Name: "managed", Managed: true})
	}
	return layers
}

// managedRoot is the folder of Claude's managed policy files and the path
// separator of the platform, which need not be the one the tests run on.
func managedRoot(loc Locations) (string, string) {
	switch loc.GOOS {
	case "darwin":
		return "/Library/Application Support/ClaudeCode", "/"
	case "windows":
		data, ok := lookupEnv(loc, "ProgramData")
		if !ok || data == "" {
			data = `C:\ProgramData`
		}
		return strings.TrimRight(data, `\/`) + `\ClaudeCode`, `\`
	}
	return "/etc/claude-code", "/"
}

// codexLayers lists config.toml in CODEX_HOME, and the .codex/config.toml of
// the project root and each folder above it when Codex trusts the project.
// The shape's CODEX_HOME decides trust, as it does for Codex itself. Each
// layer carries the profile the launch selects.
func codexLayers(loc Locations) []Layer {
	home := codexHome(loc)
	layers := []Layer{{Name: "user", Path: filepath.Join(home, "config.toml"), Profile: loc.Profile}}
	if loc.Root == "" {
		return layers
	}
	if trusted, err := codexTrusted(home, loc.Root); err != nil || !trusted {
		return layers
	}
	for _, folder := range projectFolders(loc) {
		layers = append(layers, Layer{Name: "project", Path: filepath.Join(folder, ".codex", "config.toml"), Profile: loc.Profile})
	}
	return layers
}

func codexHome(loc Locations) string {
	if dir, ok := lookupEnv(loc, "CODEX_HOME"); ok && dir != "" {
		return dir
	}
	return filepath.Join(loc.Home, ".codex")
}

// codexTrustedIn reads the project trust of config.toml in codexHome, as
// trust.FolderIn does for duolog's own CODEX_HOME.
func codexTrustedIn(codexHome, root string) (bool, error) {
	data, err := os.ReadFile(filepath.Join(codexHome, "config.toml"))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var doc struct {
		Projects map[string]struct {
			TrustLevel string `toml:"trust_level"`
		} `toml:"projects"`
	}
	if err := toml.Unmarshal(data, &doc); err != nil {
		return false, err
	}
	if root, err = filepath.Abs(root); err != nil {
		return false, err
	}
	want := pathutil.Clean(pathutil.Resolved(root))
	for path, project := range doc.Projects {
		if pathutil.Clean(pathutil.Resolved(path)) == want && project.TrustLevel == "trusted" {
			return true, nil
		}
	}
	return false, nil
}

// projectFolders returns the project root and the folders above it, the
// outermost first. It stops below the home folder, whose agent folder holds
// the user layer, or at the top of the file system. The home folder is the
// shape's HOME (loc.Home). When that is not the account's own home, the
// account's home is passed over: its agent folders hold that account's user
// settings, not a project's, and a test with a fake HOME and a project below
// the real one, as t.TempDir is on Windows, must not read them.
func projectFolders(loc Locations) []string {
	if loc.Root == "" {
		return nil
	}
	home := ""
	if loc.Home != "" {
		home = filepath.Clean(loc.Home)
	}
	account := accountHome()
	if account != "" {
		account = kit.Canonical(account)
	}
	var folders []string
	for dir := filepath.Clean(loc.Root); dir != home; {
		if account == "" || kit.Canonical(dir) != account {
			folders = append(folders, dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	slices.Reverse(folders)
	return folders
}

// lookupEnv finds key in the shape's environment, the last entry winning,
// with Windows names compared without case.
func lookupEnv(loc Locations, key string) (string, bool) {
	value, found := "", false
	for _, pair := range loc.Environ {
		name, v, _ := strings.Cut(pair, "=")
		if name == key || loc.GOOS == "windows" && strings.EqualFold(name, key) {
			value, found = v, true
		}
	}
	return value, found
}

// ArgvLayers returns the settings layers a launch's own arguments add:
// Claude's --settings, and Codex's --profile file and each -c override.
// Arguments after "--" are not options.
func ArgvLayers(agent string, args []string, loc Locations) []Layer {
	var layers []Layer
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		value := func(long string, short ...string) (string, bool) {
			if v, ok := strings.CutPrefix(arg, long+"="); ok {
				return v, true
			}
			if (arg == long || slices.Contains(short, arg)) && i+1 < len(args) {
				i++
				return args[i], true
			}
			return "", false
		}
		switch agent {
		case "claude":
			if v, ok := value("--settings"); ok {
				layers = append(layers, Layer{Name: "argv", Path: v})
			}
		case "codex":
			if v, ok := value("--profile", "-p"); ok {
				layers = append(layers, Layer{Name: "profile", Path: filepath.Join(codexHome(loc), v+".config.toml")})
			} else if v, ok := value("--config", "-c"); ok {
				layers = append(layers, Layer{Name: "argv", Path: v})
			}
		}
	}
	return layers
}

// errUnresolved is policy that exists but has no file to read.
var errUnresolved = errors.New("unresolved policy")

// ReadLayer reads the billing route fields of one layer: for Claude the env
// names, apiKeyHelper present, forceLoginMethod and fastMode; for Codex the
// login method, base URLs, speed-shaped keys, and the profiles and providers
// it selects and defines.
// Values are never kept or returned; the only value compared is a Claude base
// URL, against endpoints. Helpers are never run. A missing optional file is
// absent; a missing argv file, a read error, a file that cannot be stat'ed
// after its read, or malformed content is unreadable, which counts as paid.
// Each layer stands alone: a later one never clears a route another layer
// sets. Whether an ordinary fastMode true counts depends on the variant, so
// Build decides that from FastMode.
func ReadLayer(agent string, layer Layer, endpoints map[string]string,
	read func(string) ([]byte, error), stat func(string) (os.FileInfo, error)) LayerFacts {
	facts := LayerFacts{Layer: layer.Name}
	data, inline, err := layerData(agent, layer, read)
	if errors.Is(err, fs.ErrNotExist) && layer.Name != "argv" {
		return facts
	}
	if err != nil {
		return unreadable(layer)
	}
	if !inline {
		// Without a modification time the cache could keep a stale verdict.
		info, err := stat(layer.Path)
		if err != nil {
			return unreadable(layer)
		}
		facts.Modified = info.ModTime()
	}
	ok := false
	switch agent {
	case "claude":
		ok = claudeFacts(&facts, layer, data, endpoints)
	case "codex":
		ok = codexFacts(&facts, layer, data)
	}
	if !ok {
		return unreadable(layer)
	}
	slices.SortFunc(facts.Signals, func(a, b Signal) int { return strings.Compare(a.Name, b.Name) })
	facts.Signals = slices.Compact(facts.Signals)
	for _, list := range []*[]string{&facts.EnvNames, &facts.EndpointEntries} {
		slices.Sort(*list)
		*list = slices.Compact(*list)
	}
	return facts
}

// layerData returns the layer's content, and whether it came from argv
// rather than a file. A Codex -c pair becomes a TOML document.
func layerData(agent string, layer Layer, read func(string) ([]byte, error)) ([]byte, bool, error) {
	if layer.Path == "" {
		return nil, false, errUnresolved
	}
	if layer.Name == "argv" {
		if agent == "codex" {
			data, err := codexOverride(layer.Path)
			return data, true, err
		}
		if strings.HasPrefix(strings.TrimSpace(layer.Path), "{") {
			return []byte(layer.Path), true, nil
		}
	}
	data, err := read(layer.Path)
	return data, false, err
}

func unreadable(layer Layer) LayerFacts {
	return LayerFacts{Layer: layer.Name, Unreadable: true, Signals: []Signal{{Name: "unreadable", Layer: layer.Name}}}
}

// claudeFacts reads a Claude settings document. Env values stay raw JSON,
// except a base URL, which is compared with the endpoints and dropped.
func claudeFacts(facts *LayerFacts, layer Layer, data []byte, endpoints map[string]string) bool {
	var doc map[string]json.RawMessage
	if json.Unmarshal(data, &doc) != nil || doc == nil {
		return false
	}
	signal := func(name string) { facts.Signals = append(facts.Signals, Signal{Name: name, Layer: layer.Name}) }
	if raw, ok := doc["env"]; ok {
		var env map[string]json.RawMessage
		if json.Unmarshal(raw, &env) != nil {
			return false
		}
		for name, value := range env {
			facts.EnvNames = append(facts.EnvNames, name)
			key := name
			if runtime.GOOS == "windows" {
				key = strings.ToUpper(name)
			}
			if !RouteName("claude", key, "") {
				continue
			}
			if strings.HasSuffix(key, "_BASE_URL") {
				var url string
				if json.Unmarshal(value, &url) == nil {
					if entry, ok := MatchEndpoint(url, endpoints); ok {
						facts.EndpointEntries = append(facts.EndpointEntries, entry)
						continue
					}
				}
			}
			signal("env:" + name)
		}
	}
	if _, ok := doc["apiKeyHelper"]; ok {
		signal("apiKeyHelper")
	}
	if raw, ok := doc["forceLoginMethod"]; ok {
		var method string
		if json.Unmarshal(raw, &method) != nil || method != "claudeai" {
			signal("forceLoginMethod")
		}
	}
	if raw, ok := doc["fastMode"]; ok && string(raw) != "null" {
		var fast bool
		if json.Unmarshal(raw, &fast) == nil {
			facts.FastMode = &fast
			if fast && layer.Managed {
				signal("fastMode")
			}
		}
	}
	return readPlugins(facts, doc)
}

// readPlugins keeps the layer's enabledPlugins. It is no route, so a block
// that is not a map of booleans is skipped and never counts the layer paid.
func readPlugins(facts *LayerFacts, doc map[string]json.RawMessage) bool {
	if raw, ok := doc["enabledPlugins"]; ok {
		var plugins map[string]bool
		if json.Unmarshal(raw, &plugins) == nil {
			facts.EnabledPlugins = plugins
		}
	}
	return true
}

// codexView holds the route fields Codex reads at the top of a config file
// and in a profile.
type codexView struct {
	ForcedLoginMethod *string `toml:"forced_login_method"`
	OpenAIBaseURL     any     `toml:"openai_base_url"`
	ChatGPTBaseURL    any     `toml:"chatgpt_base_url"`
	ModelProvider     *string `toml:"model_provider"`
}

type codexProvider struct {
	EnvKey             string `toml:"env_key"`
	RequiresOpenAIAuth *bool  `toml:"requires_openai_auth"`
	BearerToken        any    `toml:"experimental_bearer_token"`
	Auth               any    `toml:"auth"`
	AWS                any    `toml:"aws"`
	BaseURL            any    `toml:"base_url"`
	HTTPHeaders        any    `toml:"http_headers"`
	EnvHTTPHeaders     any    `toml:"env_http_headers"`
}

// builtinOpenAI is the id of Codex's built-in OpenAI provider. A live Codex
// 0.165 read confirmed that it, like no selection, means the ChatGPT
// subscription. It is the one provider id duolog knows: every other id
// counts by its definition alone.
const builtinOpenAI = "openai"

// firstParty mirrors Codex's own first-party test, and also needs no base
// URL override.
func (p codexProvider) firstParty() bool {
	return p.RequiresOpenAIAuth != nil && *p.RequiresOpenAIAuth && p.EnvKey == "" && p.BearerToken == nil &&
		p.Auth == nil && p.AWS == nil && p.BaseURL == nil
}

// firstPartyAs is the first-party test of the provider defined as name. A
// definition of the built-in openai is the built-in provider while it
// overrides no route: no env_key, base URL, bearer token, headers, auth or
// AWS, and requires_openai_auth not false.
func (p codexProvider) firstPartyAs(name string) bool {
	if name != builtinOpenAI {
		return p.firstParty()
	}
	return p.EnvKey == "" && p.BaseURL == nil && p.BearerToken == nil && p.HTTPHeaders == nil &&
		p.EnvHTTPHeaders == nil && p.Auth == nil && p.AWS == nil &&
		(p.RequiresOpenAIAuth == nil || *p.RequiresOpenAIAuth)
}

type codexDoc struct {
	codexView
	Profile        *string                  `toml:"profile"`
	Profiles       map[string]codexView     `toml:"profiles"`
	ModelProviders map[string]codexProvider `toml:"model_providers"`
}

// codexFacts reads a Codex config document. At its top level, a login
// method other than chatgpt, a base URL override and any speed-shaped key
// are signals. The profile and provider selections and the profile and
// provider definitions are kept by name, with the signals of each profile's
// keys: Codex merges every layer before it resolves a profile or a
// provider, so Build resolves them across the layers (resolveCodex).
func codexFacts(facts *LayerFacts, layer Layer, data []byte) bool {
	var doc codexDoc
	var tree map[string]any
	if toml.Unmarshal(data, &doc) != nil || toml.Unmarshal(data, &tree) != nil {
		return false
	}
	for _, name := range viewSignals(doc.codexView, tree) {
		facts.Signals = append(facts.Signals, Signal{Name: name, Layer: layer.Name})
	}
	facts.LaunchProfile = layer.Profile
	if doc.Profile != nil {
		facts.Profile = *doc.Profile
	}
	profiles, _ := tree["profiles"].(map[string]any)
	for name, view := range doc.Profiles {
		if facts.Profiles == nil {
			facts.Profiles = map[string]ProfileFacts{}
		}
		table, _ := profiles[name].(map[string]any)
		p := ProfileFacts{Signals: viewSignals(view, table)}
		if view.ModelProvider != nil {
			p.Provider = *view.ModelProvider
		}
		facts.Profiles[name] = p
	}
	if doc.ModelProvider != nil {
		facts.Provider = *doc.ModelProvider
	}
	for name, p := range doc.ModelProviders {
		if facts.Providers == nil {
			facts.Providers = map[string]ProviderFacts{}
		}
		facts.Providers[name] = ProviderFacts{FirstParty: p.firstPartyAs(name), EnvKey: p.EnvKey}
		if p.EnvKey != "" {
			facts.EnvNames = append(facts.EnvNames, p.EnvKey)
		}
	}
	return true
}

// viewSignals names the route signals of one Codex view, the top level of a
// file or a profile, whose raw table is table.
func viewSignals(view codexView, table map[string]any) []string {
	var names []string
	if speedKey(table) {
		names = append(names, "route:unknown-speed")
	}
	if view.ForcedLoginMethod != nil && *view.ForcedLoginMethod != "chatgpt" {
		names = append(names, "forced_login_method")
	}
	if view.OpenAIBaseURL != nil {
		names = append(names, "openai_base_url")
	}
	if view.ChatGPTBaseURL != nil {
		names = append(names, "chatgpt_base_url")
	}
	return names
}

// codexNameTables hold entries keyed by the name of something else, a
// profile, provider, MCP server, project, hook or variable, rather than
// settings of the run: their keys say nothing about speed. Profiles count
// only through the selected one.
var codexNameTables = []string{"profiles", "model_providers", "mcp_servers", "projects", "hooks", "shell_environment_policy"}

// speedKey reports whether a Codex settings table has a speed-shaped key at
// any depth, whatever its value: false and empty count, since no setting is
// confirmed and Codex may read any of them as a tier. Values are not kept.
func speedKey(table map[string]any) bool {
	for key, value := range table {
		if slices.Contains(codexNameTables, key) {
			continue
		}
		if speedShaped(key) {
			return true
		}
		if sub, ok := value.(map[string]any); ok && speedKey(sub) {
			return true
		}
	}
	return false
}

// codexOverride turns a -c key=value pair into a TOML document. The value is
// TOML, or else a plain string, as Codex reads it; the key may be dotted.
func codexOverride(arg string) ([]byte, error) {
	key, raw, ok := strings.Cut(arg, "=")
	key = strings.TrimSpace(key)
	if !ok || key == "" {
		return nil, errors.New("override without key=value")
	}
	var value any = strings.TrimSpace(raw)
	var holder map[string]any
	if toml.Unmarshal([]byte("v = "+raw), &holder) == nil {
		value = holder["v"]
	}
	parts := strings.Split(key, ".")
	doc := map[string]any{}
	table := doc
	for _, part := range parts[:len(parts)-1] {
		next := map[string]any{}
		table[part] = next
		table = next
	}
	table[parts[len(parts)-1]] = value
	return toml.Marshal(doc)
}
