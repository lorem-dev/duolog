package launch

import "time"

// Layer is one settings source an agent reads at startup. Name says which
// kind it is (user, project, local, argv, profile, managed) and is what
// output shows; Managed marks policy that duolog's --settings cannot
// override. The argv layer holds the argument itself in Path: Claude's
// --settings takes a file or JSON text, Codex's -c a key=value pair. An empty
// Path is policy duolog found but cannot read. Profile is the Codex profile
// the launch selects with --profile, which wins over the file's own profile
// key; empty when the launch selects none.
type Layer struct {
	Name, Path, Profile string
	Managed             bool
}

// LayerFacts is what one layer says about the billing route, by name only.
// Layer is the layer's Name, for the signals Build derives from these facts.
// EnvNames are the variable names the layer sets (Claude's env block) or
// names as a credential (the env_key of each Codex provider it defines);
// EndpointEntries are the subscription_endpoints entries its base URLs
// matched. FastMode is the layer's fastMode, nil when unset. Modified feeds
// the verdict cache. An unreadable layer counts as paid. For Codex, Provider
// is the model provider the layer selects at its top level, "" for none, and
// Providers are the providers it defines; Profile is the profile its own
// profile key selects, LaunchProfile the one the launch selects with
// --profile, and Profiles the profiles it defines. Codex merges the layers
// before it resolves either, so Build resolves a selection made in one layer
// against a definition in another.
type LayerFacts struct {
	Layer                     string
	Signals                   []Signal
	EnvNames, EndpointEntries []string
	FastMode                  *bool
	Modified                  time.Time
	Unreadable                bool
	Provider                  string
	Providers                 map[string]ProviderFacts
	Profile, LaunchProfile    string
	Profiles                  map[string]ProfileFacts
}

// ProviderFacts is what a layer's definition of a Codex model provider says,
// by name only: whether it passes the first-party test, and its env_key.
type ProviderFacts struct {
	FirstParty bool
	EnvKey     string
}

// ProfileFacts is what a layer's definition of a Codex profile says, by name
// only: the route signals its keys raise (a login method other than
// chatgpt, a base URL override, a speed-shaped key) and the model provider
// it selects, "" for none.
type ProfileFacts struct {
	Signals  []string
	Provider string
}

// Locations are the places the layers of one launch shape come from: the
// home folder, the project root, the platform and the shape's environment.
// Profile is the Codex profile the launch selects with --profile, taken from
// its arguments.
type Locations struct {
	Home, Root, GOOS, Profile string
	Environ                   []string
}
