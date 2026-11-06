package workerenv

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/pelletier/go-toml/v2"
)

// Shape is how duolog starts an agent: a worker (claude -p, codex exec and
// calibration cells), an interactive peer pane, or the lead, which duolog
// only reports on.
type Shape string

// Variant says whether a launch may be billed: the free variant keeps route
// names and fast mode away, the paid one exists only with the agent's
// paid_runs opt-in.
type Variant string

const (
	Worker Shape   = "worker"
	Peer   Shape   = "peer"
	Lead   Shape   = "lead"
	Free   Variant = "free"
	Paid   Variant = "paid"
)

// ProcessInput describes one launch. Environ is the environment the process
// is built from, duolog's own for a worker or peer and the observed one for
// the lead. Layers are the agent's settings layers, already read with
// ReadLayer, from the lowest precedence to the highest: SettingsLayers'
// order with the argv layers before managed policy. ProviderEnvKey is the
// env_key of Codex's default provider.
// Prefix is the program and its leading words, by default the agent's name;
// for the lead it is the observed command and stays as it is.
// ProtectedRoots are the private usage stores a Claude worker's sandbox
// denies. ExtraEnv holds NAME=value pairs duolog adds for this launch, such
// as bridged MCP servers' variables and the run id; they pass the same route
// detection as the environment, so a route name among them is withheld from
// a free process and is a paid signal of a paid one.
// Launch holds the words a worker or peer launch puts right after the prefix:
// the operation, Codex's exec for a worker, and the native permission flags
// with Codex's sandbox pins. They are part of the admitted process, so what
// the gate assessed is what starts; quota and login reads never use them.
type ProcessInput struct {
	Agent                          string
	Shape                          Shape
	Variant                        Variant
	Config                         *config.Config
	Locations                      Locations
	Environ                        []string
	LeadID, Role, PeerID, StateDir string
	Minimal                        bool
	ExtraSettings                  map[string]any
	Layers                         []LayerFacts
	ProviderEnvKey                 string
	Prefix                         []string
	ProtectedRoots                 []string
	ExtraEnv                       []string
	Launch                         []string
}

// Process is what a launch gets: its environment, the argv prefix with the
// global overrides, and Claude's --settings JSON. Those three may hold values
// and never reach JSON. The rest is names only: the route signals, the route
// names withheld from the process (Dropped), the subscription endpoints that
// matched, whether fast mode is pinned off, the provider env_key the process
// was built with, and a key for the verdict cache.
type Process struct {
	Agent                    string
	Shape                    Shape
	Variant                  Variant
	Env                      []string `json:"-"`
	Prefix                   []string `json:"-"`
	Launch                   []string `json:"-"`
	Settings                 string   `json:"-"`
	Signals                  []Signal
	Dropped, EndpointEntries []string
	FastPinned               bool
	ProviderEnvKey           string
	CacheKey                 string
}

// codexSpeedPins are the Codex settings recorded as supported for pinning a
// speed or service tier off, by key, with the TOML value that does it. None
// is confirmed yet (open question 4 of the design), so duolog adds no guessed
// flag; a speed-shaped setting is the route:unknown-speed signal instead.
var codexSpeedPins = map[string]string{}

// Build makes the environment, the --settings JSON and the argv prefix of
// one agent, shape and variant. Every launch and every check of that shape
// uses it, so that a check sees the route the launch will use.
//
// Worker and peer: the free variant passes env_passthrough without route
// names of either agent, listed or not; only a Claude base URL whose value
// matches a subscription endpoint passes, by entry name. Fast mode is pinned
// off. The paid variant needs paid_runs.<agent>.allow; it adds the agent's
// paid env, lets listed route names through, and leaves fast mode to the
// person's settings. Both get duolog's PATH, depth, lead and role, the peer
// its pane, and Claude its updater and memory switches.
//
// Lead: the environment and prefix come back as observed, with no worker
// variables, settings or pin; only its signals are reported.
//
// Fast mode: where nothing pins it off, the paid variant and the lead, an
// effective fastMode true of the layers is the fastMode signal of the layer
// that set it. The caller's extras never set fast mode.
func Build(in ProcessInput) (Process, error) {
	if in.Agent != "claude" && in.Agent != "codex" {
		return Process{}, fmt.Errorf("process: unknown agent %q", in.Agent)
	}
	if !slices.Contains([]Shape{Worker, Peer, Lead}, in.Shape) {
		return Process{}, fmt.Errorf("process: unknown shape %q", in.Shape)
	}
	if in.Variant != Free && in.Variant != Paid {
		return Process{}, fmt.Errorf("process: unknown variant %q", in.Variant)
	}
	if in.Config == nil {
		return Process{}, errors.New("process: no config")
	}
	paid := in.Config.PaidRuns.Agent(in.Agent)
	if in.Variant == Paid && !paid.Allow {
		return Process{}, fmt.Errorf("process: paid_runs.%s.allow is false, so %s has no paid variant", in.Agent, in.Agent)
	}
	goos := in.Locations.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	p := Process{Agent: in.Agent, Shape: in.Shape, Variant: in.Variant, ProviderEnvKey: in.ProviderEnvKey}
	prefix := slices.Clone(in.Prefix)
	if len(prefix) == 0 {
		prefix = []string{in.Agent}
	}
	for _, layer := range in.Layers {
		p.Signals = append(p.Signals, layer.Signals...)
		p.EndpointEntries = append(p.EndpointEntries, layer.EndpointEntries...)
	}
	if in.Shape == Lead || in.Variant == Paid {
		if layer, on := effectiveFast(in.Layers); on {
			p.Signals = append(p.Signals, Signal{Name: "fastMode", Layer: layer})
		}
	}
	providerKeys := []string{in.ProviderEnvKey}
	if in.Agent == "codex" {
		// The launch words start the process too, so a speed-shaped
		// override among them is the gate's to see as well.
		p.Signals = append(p.Signals, codexArgvSpeed(in.Prefix, in.Locations)...)
		p.Signals = append(p.Signals, codexArgvSpeed(in.Launch, in.Locations)...)
		signals, keys := resolveCodex(in.Layers)
		p.Signals = append(p.Signals, signals...)
		providerKeys = append(providerKeys, keys...)
	}

	sel := envSelection{agent: in.Agent, goos: goos, environ: in.Environ, providerEnvKeys: providerKeys,
		endpoints: endpoints(in.Config, in.Agent)}
	if in.Shape == Lead {
		p.Env, p.Prefix = slices.Clone(in.Environ), prefix
		sel.report()
	} else {
		names := in.Config.EnvPassthrough
		if in.Variant == Paid {
			names = append(slices.Clone(names), paid.Env...)
		}
		sel.pass(names, in.Variant == Paid)
		parent, _ := lookupName(in.Environ, "PATH", goos)
		path, err := pathWith(parent)
		if err != nil {
			return Process{}, err
		}
		// The extras go first: one that repeats a passed name replaces it.
		extras := sel.extra(in.ExtraEnv, in.Variant == Paid)
		p.Env = append(slices.Clone(sel.pairs), path, "DUOLOG_DEPTH=1", "DUOLOG_LEAD="+in.LeadID, "DUOLOG_ROLE="+in.Role)
		if in.Shape == Peer {
			p.Env = append(p.Env, "DUOLOG_PEER="+in.PeerID)
		}
		p.Env = append(p.Env, extras...)
		switch in.Agent {
		case "claude":
			// A worker's auto memory would land in the project's memory, read
			// by the lead.
			p.Env = append(p.Env, "DISABLE_AUTOUPDATER=1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1")
			p.FastPinned = in.Variant == Free
			p.Settings = renderClaudeSettings(in.StateDir, in.Minimal, in.ExtraSettings, in.ProtectedRoots, p.FastPinned)
			p.Prefix = append(prefix, "--settings", p.Settings) //nolint:gocritic // prefix is Build's own clone of in.Prefix, handed to p
		case "codex":
			overrides, signals, pinned, err := codexOverrides(in.ExtraSettings, in.Variant == Free)
			if err != nil {
				return Process{}, err
			}
			p.Signals = append(p.Signals, signals...)
			p.FastPinned = pinned
			p.Prefix = append(prefix, overrides...) //nolint:gocritic // prefix is Build's own clone of in.Prefix, handed to p
		}
		p.Launch = slices.Clone(in.Launch)
	}
	p.Signals = append(p.Signals, sel.signals...)
	p.Dropped = sel.dropped
	p.EndpointEntries = append(p.EndpointEntries, sel.entries...)

	slices.SortFunc(p.Signals, func(a, b Signal) int {
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return strings.Compare(a.Layer, b.Layer)
	})
	p.Signals = slices.Compact(p.Signals)
	p.Dropped, p.EndpointEntries = sortedUnique(p.Dropped), sortedUnique(p.EndpointEntries)
	p.CacheKey = cacheKey(p, in, paid)
	return p, nil
}

// Environ is the environment a free worker of agent gets from environ under
// cfg, as Build makes it without settings layers, extras or a lead: what a
// check that starts no worker reads to judge the settings folders that
// worker's agent loads, its home, CLAUDE_CONFIG_DIR and CODEX_HOME. A launch
// still judges the process it builds itself.
func Environ(cfg *config.Config, agent string, environ []string) ([]string, error) {
	p, err := Build(ProcessInput{Agent: agent, Shape: Worker, Variant: Free, Config: cfg, Environ: environ})
	return p.Env, err
}

// effectiveFast reports whether the layers leave fast mode on, by the value
// of the last layer that sets one, and names that layer. Managed policy comes
// last, so it wins as it does in Claude. A layer that already holds the
// signal, managed policy, needs no second one.
func effectiveFast(layers []LayerFacts) (string, bool) {
	var last *LayerFacts
	for i := range layers {
		if layers[i].FastMode != nil {
			last = &layers[i]
		}
	}
	if last == nil || !*last.FastMode || slices.ContainsFunc(last.Signals, func(s Signal) bool { return s.Name == "fastMode" }) {
		return "", false
	}
	if last.Layer == "" {
		return "settings", true
	}
	return last.Layer, true
}

func sortedUnique(list []string) []string {
	slices.Sort(list)
	return slices.Compact(list)
}

// Argv is the command line of an operation: the prefix with its global
// overrides, then the operation and its own flags as given.
func (p Process) Argv(args ...string) []string {
	return append(slices.Clone(p.Prefix), args...)
}

// LaunchArgv is the command line of the launch itself: the prefix, the
// launch words the process was admitted with, then the run's own flags.
func (p Process) LaunchArgv(args ...string) []string {
	return append(append(slices.Clone(p.Prefix), p.Launch...), args...)
}

// envSelection sorts the names of one launch into what passes, the route
// names withheld and the signals of the route names that pass. Values are
// read to pass them on and to match a Claude base URL, and go nowhere else.
type envSelection struct {
	agent, goos     string
	providerEnvKeys []string
	environ         []string
	endpoints       map[string]string

	pairs, dropped, entries []string
	signals                 []Signal
}

// route reports whether name is a route of this agent, and whether it is
// one of either agent, which no free process gets. On Windows the provider
// credential names fold case like the variable names do.
func (s *envSelection) route(name string) (own, either bool) {
	key := name
	if s.goos == "windows" {
		key = strings.ToUpper(name)
	}
	own = RouteName(s.agent, key, "") || slices.ContainsFunc(s.providerEnvKeys, func(k string) bool {
		return k != "" && s.same(k, name)
	})
	other := "codex"
	if s.agent == "codex" {
		other = "claude"
	}
	return own, own || RouteName(other, key, "")
}

// trusted matches a Claude base URL against the subscription endpoints and
// records the entry's name.
func (s *envSelection) trusted(name, value string) bool {
	if s.agent != "claude" || !strings.HasSuffix(strings.ToUpper(name), "_BASE_URL") {
		return false
	}
	entry, ok := MatchEndpoint(value, s.endpoints)
	if ok {
		s.entries = append(s.entries, entry)
	}
	return ok
}

// pass selects the listed names that are set. Route names pass only with
// routes allowed, the paid variant; a trusted base URL passes either way.
// Each route name of this agent that is listed or set and does not pass is
// dropped, so the person hears of it.
func (s *envSelection) pass(names []string, routes bool) {
	var seen []string
	for _, name := range names {
		if s.skipped(name) || slices.ContainsFunc(seen, func(n string) bool { return s.same(n, name) }) {
			continue
		}
		seen = append(seen, name)
		value, set := lookupName(s.environ, name, s.goos)
		own, route := s.route(name)
		switch {
		// A paid process gets this agent's routes only, never the other's.
		case !route || routes && own:
			if !set {
				continue
			}
			s.pairs = append(s.pairs, name+"="+value)
			if own && !s.trusted(name, value) {
				s.signals = append(s.signals, Signal{Name: "env:" + name, Layer: "env"})
			}
		case own && set && s.trusted(name, value):
			s.pairs = append(s.pairs, name+"="+value)
		case own:
			s.dropped = append(s.dropped, name)
		}
	}
	for _, pair := range s.environ {
		name, _, _ := strings.Cut(pair, "=")
		if own, _ := s.route(name); own && !slices.ContainsFunc(seen, func(n string) bool { return s.same(n, name) }) {
			s.dropped = append(s.dropped, name)
		}
	}
}

// extra selects the pairs duolog adds for one launch, such as a bridged MCP
// server's variables, by the same route rule as pass: a route name of either
// agent never reaches a free process, and this agent's is dropped there; a
// paid process passes this agent's, as a signal of layer "mcp", unless it is
// a trusted base URL. duolog's own names pass.
func (s *envSelection) extra(pairs []string, routes bool) []string {
	var out []string
	for _, pair := range pairs {
		name, value, _ := strings.Cut(pair, "=")
		own, route := s.route(name)
		switch {
		case !route:
			out = s.replace(out, name, pair)
		case own && routes:
			out = s.replace(out, name, pair)
			if !s.trusted(name, value) {
				s.signals = append(s.signals, Signal{Name: "env:" + name, Layer: "mcp"})
			}
		case own && s.trusted(name, value):
			out = s.replace(out, name, pair)
		case own:
			s.dropped = append(s.dropped, name)
		}
	}
	return out
}

// replace adds an accepted extra pair so that its name is in the process once,
// with this value, as the process would see it: an earlier pair of the name,
// passed or extra, goes, and so does the signal that pair raised.
func (s *envSelection) replace(out []string, name, pair string) []string {
	isName := func(p string) bool { n, _, _ := strings.Cut(p, "="); return s.same(n, name) }
	s.pairs = slices.DeleteFunc(s.pairs, isName)
	s.signals = slices.DeleteFunc(s.signals, func(g Signal) bool {
		n, ok := strings.CutPrefix(g.Name, "env:")
		return ok && s.same(n, name)
	})
	return append(slices.DeleteFunc(out, isName), pair)
}

// report takes the signals of an observed environment that is used as it is.
func (s *envSelection) report() {
	for _, name := range envNames(s.environ) {
		if own, _ := s.route(name); own {
			if value, _ := lookupName(s.environ, name, s.goos); !s.trusted(name, value) {
				s.signals = append(s.signals, Signal{Name: "env:" + name, Layer: "env"})
			}
		}
	}
}

// skipped names never pass on: PATH is set separately, a DUOLOG_ or REX_ name
// would let a worker act as the lead or rearrange the lead's terminal, and
// duolog sets Claude's updater and memory switches itself. Reserved names are
// refused in any case on every system, as Passthrough refuses them.
func (s *envSelection) skipped(name string) bool {
	upper := name
	if s.goos == "windows" {
		upper = strings.ToUpper(name)
	}
	return strings.EqualFold(name, "PATH") || Reserved(name) ||
		s.agent == "claude" && (upper == "DISABLE_AUTOUPDATER" || upper == "CLAUDE_CODE_DISABLE_AUTO_MEMORY")
}

func (s *envSelection) same(a, b string) bool {
	return a == b || s.goos == "windows" && strings.EqualFold(a, b)
}

// lookupName finds name in environ, the last entry winning, with Windows
// names compared without case.
func lookupName(environ []string, name, goos string) (string, bool) {
	return lookupEnv(Locations{GOOS: goos, Environ: environ}, name)
}

// envNames lists the distinct names of environ in order.
func envNames(environ []string) []string {
	var names []string
	for _, pair := range environ {
		if name, _, _ := strings.Cut(pair, "="); !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names
}

func endpoints(cfg *config.Config, agent string) map[string]string {
	if agent == "claude" {
		return cfg.PaidRuns.Claude.SubscriptionEndpoints
	}
	return nil
}

// pathWith puts this executable's directory before parent, a search path.
func pathWith(parent string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate duolog for worker hooks: %w", err)
	}
	path := filepath.Dir(exe)
	if parent != "" {
		path += string(os.PathListSeparator) + parent
	}
	return "PATH=" + path, nil
}

// codexOverrides are the global -c overrides of a Codex worker or peer: the
// update check off, the caller's settings in key order, and in the free
// variant the recorded speed pins, last so that they win. A speed-shaped
// setting without a recorded pin is a signal, never a guessed value.
func codexOverrides(extra map[string]any, free bool) ([]string, []Signal, bool, error) {
	args := []string{"-c", "check_for_update_on_startup=false"}
	var signals []Signal
	keys := make([]string, 0, len(extra))
	for key := range extra {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		_, recorded := codexSpeedPins[key]
		if free && recorded {
			continue
		}
		value, err := tomlValue(extra[key])
		if err != nil {
			return nil, nil, false, fmt.Errorf("process: codex setting %s: %w", key, err)
		}
		args = append(args, "-c", key+"="+value)
		if speedShaped(key) && !recorded {
			signals = append(signals, Signal{Name: "route:unknown-speed", Layer: "duolog"})
		}
	}
	pinned := false
	if free {
		pins := make([]string, 0, len(codexSpeedPins))
		for key := range codexSpeedPins {
			pins = append(pins, key)
		}
		slices.Sort(pins)
		for _, key := range pins {
			args = append(args, "-c", key+"="+codexSpeedPins[key])
			pinned = true
		}
	}
	return args, signals, pinned, nil
}

// codexArgvSpeed finds speed-shaped -c overrides in an observed prefix.
func codexArgvSpeed(prefix []string, loc Locations) []Signal {
	var signals []Signal
	for _, layer := range ArgvLayers("codex", prefix, loc) {
		key, _, _ := strings.Cut(layer.Path, "=")
		if key = strings.TrimSpace(key); layer.Name == "argv" && speedShaped(key) {
			if _, recorded := codexSpeedPins[key]; !recorded {
				signals = append(signals, Signal{Name: "route:unknown-speed", Layer: "argv"})
			}
		}
	}
	return signals
}

// speedShaped reports whether a Codex setting key may choose a speed or
// service tier: its last part names a service tier, speed or fast mode. The
// rule goes by shape, as RouteName does, since no key is confirmed.
func speedShaped(key string) bool {
	last := strings.ToLower(key[strings.LastIndex(key, ".")+1:])
	return strings.Contains(last, "service_tier") || strings.Contains(last, "speed") || strings.Contains(last, "fast")
}

// tomlValue renders one value of a -c override.
func tomlValue(v any) (string, error) {
	data, err := toml.Marshal(map[string]any{"v": v})
	if err != nil {
		return "", err
	}
	value, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "v = ")
	if !ok || strings.Contains(value, "\n") {
		return "", errors.New("not a single TOML value")
	}
	return value, nil
}

// cacheKey hashes what the verdict of a launch depends on: the shape, the
// names in its environment, the route names withheld, the matched entries,
// the signals, each layer's modification time and facts, and the config
// that selects the variant. Values never enter it: a value that changes
// under the same name changes the key through the facts that Build and
// ReadLayer derive from it, such as a matched entry or a signal.
func cacheKey(p Process, in ProcessInput, paid config.PaidAgent) string {
	type layerKey struct {
		Layer                     string
		Modified                  int64
		Unreadable                bool
		FastMode                  *bool
		EnvNames, EndpointEntries []string
	}
	var layers []layerKey
	for _, layer := range in.Layers {
		layers = append(layers, layerKey{layer.Layer, layer.Modified.UnixNano(), layer.Unreadable, layer.FastMode,
			layer.EnvNames, layer.EndpointEntries})
	}
	names := envNames(p.Env)
	slices.Sort(names)
	data, _ := json.Marshal(struct {
		Agent                                   string
		Shape                                   Shape
		Variant                                 Variant
		EnvNames, Dropped, Entries, Passthrough []string
		Signals                                 []Signal
		Layers                                  []layerKey
		Paid                                    config.PaidAgent
		Endpoints                               map[string]string
		UsableShare                             float64
		ProviderEnvKey                          string
		FastPinned                              bool
	}{p.Agent, p.Shape, p.Variant, names, p.Dropped, p.EndpointEntries, in.Config.EnvPassthrough,
		p.Signals, layers, paid, endpoints(in.Config, in.Agent), in.Config.PaidRuns.UsableShare,
		in.ProviderEnvKey, p.FastPinned})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
