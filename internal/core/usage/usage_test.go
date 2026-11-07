package usage

import (
	"math"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
	"go.yaml.in/yaml/v3"
)

// defaults decodes the embedded config.default.yaml, without any user or
// project layer.
func defaults(t *testing.T) *config.Config {
	t.Helper()
	var cfg config.Config
	if err := yaml.Unmarshal(config.DefaultYAML(), &cfg); err != nil {
		t.Fatal(err)
	}
	return &cfg
}

func TestCostMatchesSummarize(t *testing.T) {
	cfg := defaults(t)
	if usd, ok := Cost(cfg, "codex", "gpt-6.1-sol", Tokens{Input: 1_000_000}); !ok || math.Abs(usd-2.00) > 1e-9 {
		t.Fatal(usd, ok)
	}
	if _, ok := Cost(cfg, "codex", "no-such-model", Tokens{Input: 1}); ok {
		t.Fatal("unknown model priced")
	}
}

// Claude charges a five-minute cache write at 1.25x and a one-hour write at
// 2x the input price, so the same tokens cost more as writes than as input.
func TestCacheWritesCostMoreThanInput(t *testing.T) {
	cfg := defaults(t)
	cost := func(tokens Tokens) float64 {
		t.Helper()
		usd, ok := Cost(cfg, "claude", "opus", tokens)
		if !ok {
			t.Fatal("opus unpriced")
		}
		return usd
	}
	plain := cost(Tokens{Input: 1_000_000})
	minute := cost(Tokens{Input: 1_000_000, Write: 1_000_000})
	hour := cost(Tokens{Input: 1_000_000, Write: 1_000_000, WriteHour: 1_000_000})
	if math.Abs(plain-4) > 1e-9 || math.Abs(minute-5) > 1e-9 || math.Abs(hour-8) > 1e-9 {
		t.Fatalf("plain %v, five-minute writes %v, one-hour writes %v; want 4, 5, 8", plain, minute, hour)
	}
}
