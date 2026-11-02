// Package learning classifies delegations and chooses ladder entries for them.
package learning

import (
	"fmt"
	"strings"

	"github.com/lorem-dev/duolog/internal/config"
)

// Unclassified is the class recorded when no rule matches.
const Unclassified = "unclassified"

// ParseFeatures reads "output=code,exemplar=no" and checks every name and
// value with config.CheckFeature.
func ParseFeatures(s string) (map[string]string, error) {
	features := map[string]string{}
	if strings.TrimSpace(s) == "" {
		return features, nil
	}
	for _, pair := range strings.Split(s, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok {
			return nil, fmt.Errorf("feature %q: want name=value", pair)
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if err := config.CheckFeature(key, value); err != nil {
			return nil, fmt.Errorf("feature %w", err)
		}
		features[key] = value
	}
	return features, nil
}

// Classify returns the class of the first rule whose every when pair equals
// the feature (or the role), and "" when no rule matches.
func Classify(rules []config.ClassRule, role string, features map[string]string) string {
	for _, rule := range rules {
		matched := true
		for name, want := range rule.When {
			got, ok := features[name]
			if name == "role" {
				got, ok = role, true
			}
			if !ok || got != want {
				matched = false
				break
			}
		}
		if matched {
			return rule.Class
		}
	}
	return ""
}
