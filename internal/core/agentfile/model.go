package agentfile

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// NonScalar marks a header value that was a list or a mapping.
const NonScalar = "\x00non-scalar"

// SortedKeys returns the keys of m in order.
func SortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// CommonFields checks the fields that both formats share and returns them.
func CommonFields(fields map[string]string, modelKey, effortKey string) (name, desc, model, effort string, err error) {
	for _, key := range []string{"name", "description", modelKey, effortKey} {
		if fields[key] == NonScalar {
			return "", "", "", "", fmt.Errorf("%s must be a string", key)
		}
	}
	name, desc = fields["name"], fields["description"]
	if strings.TrimSpace(name) == "" {
		return "", "", "", "", errors.New("name is missing or empty")
	}
	if name != filepath.Base(name) || strings.ContainsAny(name, `/\:`) || strings.HasPrefix(name, ".") {
		return "", "", "", "", fmt.Errorf("name %q is not a plain file name", name)
	}
	if strings.TrimSpace(desc) == "" {
		return "", "", "", "", errors.New("description is missing or empty")
	}
	return name, desc, fields[modelKey], fields[effortKey], nil
}

// CheckKeys refuses a key that is neither known nor dropped.
func CheckKeys(keys, known, drop []string) error {
	for _, key := range keys {
		if slices.Contains(known, key) || slices.Contains(drop, key) {
			continue
		}
		return fmt.Errorf("key %q has no counterpart; add it to sync.agents.drop_keys to discard it", key)
	}
	return nil
}

// CodexName applies sync.agents.codex_name to the name of an agent.
func CodexName(strategy, name string) (string, error) {
	switch strategy {
	case "", "keep":
		return name, nil
	case "underscore":
		return strings.ReplaceAll(name, "-", "_"), nil
	}
	return "", fmt.Errorf("sync.agents.codex_name must be keep or underscore, got %q", strategy)
}
