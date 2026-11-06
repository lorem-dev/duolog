package workerenv

import (
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
)

// codexLayerFacts reads files as the user and project Codex layers.
func codexLayerFacts(t *testing.T, files ...string) []LayerFacts {
	t.Helper()
	names := []string{"user", "project", "project"}
	contents := map[string]string{}
	var layers []Layer
	for i, content := range files {
		path := "/layers/" + names[i] + string(rune('0'+i)) + "/config.toml"
		contents[path] = content
		layers = append(layers, Layer{Name: names[i], Path: path})
	}
	var facts []LayerFacts
	for _, layer := range layers {
		facts = append(facts, ReadLayer("codex", layer, nil, fakeRead(contents), fakeStat))
	}
	return facts
}

func freeCodex(t *testing.T, layers []LayerFacts, environ []string, goos string) Process {
	t.Helper()
	cfg := &config.Config{EnvPassthrough: []string{"PATH", "vendor_secret", "GATEWAY_SECRET"}}
	cfg.PaidRuns.UsableShare = 0.5
	p, err := Build(ProcessInput{Agent: "codex", Shape: Worker, Variant: Free, Config: cfg,
		Locations: Locations{GOOS: goos}, Environ: environ, Layers: layers})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestCodexProviderAcrossLayers is review finding Codex 2: the project selects
// a provider that only the user file defines, with a paid base URL and a
// bearer token. Each file alone says nothing; together they are paid, and a
// selected provider no layer defines is paid as unresolved.
func TestCodexProviderAcrossLayers(t *testing.T) {
	user := "[model_providers.gateway]\nbase_url = \"https://gw.example.com/v1\"\n" +
		"experimental_bearer_token = \"x\"\nenv_key = \"GATEWAY_SECRET\"\n"
	project := "model_provider = \"gateway\"\n"
	environ := []string{"PATH=/usr/bin", "GATEWAY_SECRET=s3cret"}
	p := freeCodex(t, codexLayerFacts(t, user, project), environ, "linux")
	if !slices.Contains(p.Signals, Signal{Name: "model_provider", Layer: "user"}) {
		t.Errorf("signals = %v, want model_provider from the user layer", p.Signals)
	}
	if _, ok := envValue(p.Env, "GATEWAY_SECRET"); ok || !slices.Contains(p.Dropped, "GATEWAY_SECRET") {
		t.Errorf("free process env %v dropped %v: the provider's env_key must be withheld", p.Env, p.Dropped)
	}

	// The built-in openai id is the ChatGPT subscription (a live Codex 0.165
	// read): selected with no definition, or with one that overrides no
	// route, it is first party. Any other id no layer defines is unresolved.
	selectOpenAI := "model_provider = \"openai\"\n"
	for _, c := range []struct {
		name  string
		files []string
		want  []Signal
	}{
		{"other id undefined", []string{"", "model_provider = \"nowhere\"\n"},
			[]Signal{{Name: "model_provider:unresolved", Layer: "project"}}},
		{"first-party id across layers", []string{"[model_providers.mine]\nrequires_openai_auth = true\n",
			"model_provider = \"mine\"\n"}, nil},
		{"no selection", []string{user}, nil},
		{"openai undefined", []string{"", selectOpenAI}, nil},
		{"openai selected in the user layer", []string{selectOpenAI}, nil},
		{"openai defined without overrides", []string{"[model_providers.openai]\nname = \"OpenAI\"\n", selectOpenAI}, nil},
		{"openai in its own layer without overrides", []string{selectOpenAI + "[model_providers.openai]\nname = \"OpenAI\"\nrequires_openai_auth = true\n"}, nil},
		{"openai with env_key", []string{"[model_providers.openai]\nenv_key = \"GATEWAY_SECRET\"\n", selectOpenAI},
			[]Signal{{Name: "model_provider", Layer: "user"}}},
		{"openai with base_url", []string{"[model_providers.openai]\nbase_url = \"https://gw.example.com/v1\"\n", selectOpenAI},
			[]Signal{{Name: "model_provider", Layer: "user"}}},
		{"openai with a bearer token", []string{"[model_providers.openai]\nexperimental_bearer_token = \"x\"\n", selectOpenAI},
			[]Signal{{Name: "model_provider", Layer: "user"}}},
		{"openai with headers", []string{"[model_providers.openai]\nhttp_headers = {X-Key = \"x\"}\n", selectOpenAI},
			[]Signal{{Name: "model_provider", Layer: "user"}}},
		{"openai with env headers", []string{"[model_providers.openai]\nenv_http_headers = {X-Key = \"GATEWAY_SECRET\"}\n", selectOpenAI},
			[]Signal{{Name: "model_provider", Layer: "user"}}},
		{"openai with auth", []string{"[model_providers.openai.auth]\ncommand = \"helper\"\n", selectOpenAI},
			[]Signal{{Name: "model_provider", Layer: "user"}}},
		{"openai without openai auth", []string{"[model_providers.openai]\nrequires_openai_auth = false\n", selectOpenAI},
			[]Signal{{Name: "model_provider", Layer: "user"}}},
		{"openai overridden in the selecting layer", []string{selectOpenAI + "[model_providers.openai]\nbase_url = \"https://gw.example.com/v1\"\n"},
			[]Signal{{Name: "model_provider", Layer: "user"}}},
	} {
		p := freeCodex(t, codexLayerFacts(t, c.files...), environ, "linux")
		if !slices.Equal(p.Signals, c.want) {
			t.Errorf("%s: signals %v, want %v", c.name, p.Signals, c.want)
		}
	}
}

// TestCodexProfileAcrossLayers is re-review finding N1: the project selects a
// profile that only the user file defines, and the profile selects a paid
// gateway. Codex merges the layers before it resolves the profile, so the
// selection and the definition count together, in either direction. A
// selected profile no layer defines is unresolved, which is paid, and the
// profile's other route keys count from the layer that defines them.
func TestCodexProfileAcrossLayers(t *testing.T) {
	gateway := "[model_providers.gateway]\nbase_url = \"https://gw.example.com/v1\"\n" +
		"experimental_bearer_token = \"x\"\nenv_key = \"GATEWAY_SECRET\"\n"
	environ := []string{"PATH=/usr/bin", "GATEWAY_SECRET=s3cret"}

	// The review's input: the user file defines the profile and the gateway,
	// the trusted project only selects the profile.
	user := "[profiles.work]\nmodel_provider = \"gateway\"\n" + gateway
	p := freeCodex(t, codexLayerFacts(t, user, "profile = \"work\"\n"), environ, "linux")
	if !slices.Contains(p.Signals, Signal{Name: "model_provider", Layer: "user"}) {
		t.Errorf("project selects, user defines: signals = %v, want model_provider from the user layer", p.Signals)
	}
	if _, ok := envValue(p.Env, "GATEWAY_SECRET"); ok || !slices.Contains(p.Dropped, "GATEWAY_SECRET") {
		t.Errorf("free process env %v dropped %v: the gateway's env_key must be withheld", p.Env, p.Dropped)
	}

	// The reverse: the user selects, the project defines.
	project := "[profiles.work]\nmodel_provider = \"gateway\"\n" + gateway
	p = freeCodex(t, codexLayerFacts(t, "profile = \"work\"\n", project), environ, "linux")
	if !slices.Contains(p.Signals, Signal{Name: "model_provider", Layer: "project"}) {
		t.Errorf("user selects, project defines: signals = %v, want model_provider from the project layer", p.Signals)
	}

	for _, c := range []struct {
		name  string
		files []string
		want  []Signal
	}{
		{"profile no layer defines", []string{"", "profile = \"nowhere\"\n"},
			[]Signal{{Name: "profile:unresolved", Layer: "project"}}},
		{"login method from the defining layer", []string{"[profiles.work]\nforced_login_method = \"api\"\n", "profile = \"work\"\n"},
			[]Signal{{Name: "forced_login_method", Layer: "user"}}},
		{"openai base URL", []string{"[profiles.work]\nopenai_base_url = \"https://gw.example.com\"\n", "profile = \"work\"\n"},
			[]Signal{{Name: "openai_base_url", Layer: "user"}}},
		{"chatgpt base URL", []string{"profile = \"work\"\n", "[profiles.work]\nchatgpt_base_url = \"https://gw.example.com\"\n"},
			[]Signal{{Name: "chatgpt_base_url", Layer: "project"}}},
		{"speed key", []string{"[profiles.work]\nservice_tier = \"\"\n", "profile = \"work\"\n"},
			[]Signal{{Name: "route:unknown-speed", Layer: "user"}}},
		{"the last selection wins", []string{"profile = \"paid\"\n[profiles.paid]\nopenai_base_url = \"https://gw.example.com\"\n" +
			"[profiles.plain]\nmodel = \"m\"\n", "profile = \"plain\"\n"}, nil},
		{"profile provider wins over the top level", []string{"model_provider = \"gateway\"\n[profiles.work]\nmodel_provider = \"openai\"\n" +
			gateway, "profile = \"work\"\n"}, nil},
		{"profile without a provider keeps the top level", []string{"model_provider = \"gateway\"\n[profiles.work]\nmodel = \"m\"\n" +
			gateway, "profile = \"work\"\n"}, []Signal{{Name: "model_provider", Layer: "user"}}},
		{"unselected profile", []string{"[profiles.work]\nopenai_base_url = \"https://gw.example.com\"\n"}, nil},
	} {
		p := freeCodex(t, codexLayerFacts(t, c.files...), environ, "linux")
		if !slices.Equal(p.Signals, c.want) {
			t.Errorf("%s: signals %v, want %v", c.name, p.Signals, c.want)
		}
	}

	// A launch's --profile wins over every file's profile key.
	layers := []Layer{{Name: "user", Path: "/u/config.toml", Profile: "picked"}, {Name: "project", Path: "/p/config.toml", Profile: "picked"}}
	files := fakeRead(map[string]string{
		"/u/config.toml": "[profiles.picked]\nopenai_base_url = \"https://gw.example.com\"\n",
		"/p/config.toml": "profile = \"plain\"\n[profiles.plain]\nmodel = \"m\"\n",
	})
	facts := []LayerFacts{ReadLayer("codex", layers[0], nil, files, fakeStat), ReadLayer("codex", layers[1], nil, files, fakeStat)}
	if p := freeCodex(t, facts, environ, "linux"); !slices.Equal(p.Signals, []Signal{{Name: "openai_base_url", Layer: "user"}}) {
		t.Errorf("--profile across layers: signals %v", p.Signals)
	}
}

// TestWindowsProviderEnvKeyFolded is review finding Codex 7: on Windows a
// custom provider credential named in lower case must still be a route name,
// withheld from the free process.
func TestWindowsProviderEnvKeyFolded(t *testing.T) {
	cfg := &config.Config{EnvPassthrough: []string{"PATH", "vendor_secret"}}
	cfg.PaidRuns.UsableShare = 0.5
	for _, environ := range [][]string{{"Path=C:\\bin", "vendor_secret=s3cret"}, {"Path=C:\\bin", "VENDOR_SECRET=s3cret"}} {
		p, err := Build(ProcessInput{Agent: "codex", Shape: Worker, Variant: Free, Config: cfg,
			Locations: Locations{GOOS: "windows"}, Environ: environ, ProviderEnvKey: "vendor_secret"})
		if err != nil {
			t.Fatal(err)
		}
		for _, pair := range p.Env {
			if strings.HasSuffix(pair, "=s3cret") {
				t.Errorf("environ %v: free process got %q", environ, pair)
			}
		}
		if len(p.Dropped) != 1 {
			t.Errorf("environ %v: dropped %v, want the provider key", environ, p.Dropped)
		}
	}
}
