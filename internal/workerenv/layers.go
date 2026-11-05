package workerenv

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/trust"
	"github.com/pelletier/go-toml/v2"
)

// Layer is one settings source an agent reads at startup. Name says which
// kind it is (user, project, local, argv, profile, managed) and is what
// output shows; Managed marks policy that duolog's --settings cannot
// override. The argv layer holds the argument itself in Path: Claude's
// --settings takes a file or JSON text, Codex's -c a key=value pair. An empty
// Path is policy duolog found but cannot read.
type Layer struct {
	Name, Path string
	Managed    bool
}

// LayerFacts is what one layer says about the billing route, by name only.
// EnvNames are the variable names the layer sets (Claude's env block) or
// names as a credential (the env_key of Codex's selected provider);
// EndpointEntries are the subscription_endpoints entries its base URLs
// matched. FastMode is the layer's fastMode, nil when unset. Modified feeds
// the verdict cache. An unreadable layer counts as paid.
type LayerFacts struct {
	Signals                   []Signal
	EnvNames, EndpointEntries []string
	FastMode                  *bool
	Modified                  time.Time
	Unreadable                bool
}

// Locations are the places the layers of one launch shape come from: the
// home folder, the project root, the platform and the shape's environment.
type Locations struct {
	Home, Root, GOOS string
	Environ          []string
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
	// duolog cannot read it, so it adds an unreadable managed layer. The
	// platform code that discovers such policy replaces it.
	NonFilePolicy = func(goos string) bool { return false }
	// codexTrusted reports whether Codex trusts the project folder.
	codexTrusted = trust.FolderIn
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
func codexLayers(loc Locations) []Layer {
	layers := []Layer{{Name: "user", Path: filepath.Join(codexHome(loc), "config.toml")}}
	if loc.Root == "" {
		return layers
	}
	if trusted, err := codexTrusted(loc.Home, loc.Root); err != nil || !trusted {
		return layers
	}
	for _, folder := range projectFolders(loc) {
		layers = append(layers, Layer{Name: "project", Path: filepath.Join(folder, ".codex", "config.toml")})
	}
	return layers
}

func codexHome(loc Locations) string {
	if dir, ok := lookupEnv(loc, "CODEX_HOME"); ok && dir != "" {
		return dir
	}
	return filepath.Join(loc.Home, ".codex")
}

// projectFolders returns the project root and the folders above it, the
// outermost first. It stops below the home folder, whose agent folder holds
// the user layer, or at the top of the file system.
func projectFolders(loc Locations) []string {
	if loc.Root == "" {
		return nil
	}
	home := ""
	if loc.Home != "" {
		home = filepath.Clean(loc.Home)
	}
	var folders []string
	for dir := filepath.Clean(loc.Root); dir != home; {
		folders = append(folders, dir)
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
// login method, base URLs and the selected provider. Values are never kept or
// returned; the only value compared is a Claude base URL, against endpoints.
// Helpers are never run. A missing optional file is absent; a missing argv
// file, a read error or malformed content is unreadable, which counts as
// paid. Each layer stands alone: a later one never clears a route another
// layer sets.
func ReadLayer(agent string, layer Layer, endpoints map[string]string,
	read func(string) ([]byte, error), stat func(string) (os.FileInfo, error)) LayerFacts {
	var facts LayerFacts
	data, inline, err := layerData(agent, layer, read)
	if errors.Is(err, fs.ErrNotExist) && layer.Name != "argv" {
		return LayerFacts{}
	}
	if err != nil {
		return unreadable(layer)
	}
	if !inline {
		if info, err := stat(layer.Path); err == nil {
			facts.Modified = info.ModTime()
		}
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
	return LayerFacts{Unreadable: true, Signals: []Signal{{Name: "unreadable", Layer: layer.Name}}}
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
	RequiresOpenAIAuth bool   `toml:"requires_openai_auth"`
	BearerToken        any    `toml:"experimental_bearer_token"`
	Auth               any    `toml:"auth"`
	AWS                any    `toml:"aws"`
	BaseURL            any    `toml:"base_url"`
}

// firstParty mirrors Codex's own first-party test, and also needs no base
// URL override.
func (p codexProvider) firstParty() bool {
	return p.RequiresOpenAIAuth && p.EnvKey == "" && p.BearerToken == nil && p.Auth == nil && p.AWS == nil && p.BaseURL == nil
}

type codexDoc struct {
	codexView
	Profile        *string                  `toml:"profile"`
	Profiles       map[string]codexView     `toml:"profiles"`
	ModelProviders map[string]codexProvider `toml:"model_providers"`
}

// codexFacts reads a Codex config document and the profile it selects. A
// login method other than chatgpt, a base URL override, and a selected
// provider defined here that fails the first-party test are signals. A
// provider defined in another layer is left to the provider list read.
func codexFacts(facts *LayerFacts, layer Layer, data []byte) bool {
	var doc codexDoc
	if toml.Unmarshal(data, &doc) != nil {
		return false
	}
	views := []codexView{doc.codexView}
	if doc.Profile != nil {
		if profile, ok := doc.Profiles[*doc.Profile]; ok {
			views = append(views, profile)
		}
	}
	signal := func(name string) { facts.Signals = append(facts.Signals, Signal{Name: name, Layer: layer.Name}) }
	selected := ""
	for _, view := range views {
		if view.ForcedLoginMethod != nil && *view.ForcedLoginMethod != "chatgpt" {
			signal("forced_login_method")
		}
		if view.OpenAIBaseURL != nil {
			signal("openai_base_url")
		}
		if view.ChatGPTBaseURL != nil {
			signal("chatgpt_base_url")
		}
		if view.ModelProvider != nil {
			selected = *view.ModelProvider
		}
	}
	if provider, ok := doc.ModelProviders[selected]; ok && selected != "" {
		if provider.EnvKey != "" {
			facts.EnvNames = append(facts.EnvNames, provider.EnvKey)
		}
		if !provider.firstParty() {
			signal("model_provider")
		}
	}
	return true
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
