package config

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/lorem-dev/duolog/internal/core/config"
)

// restrictProjectModels removes from a shared layer each models value that
// would make a paid run look cheaper than the layers below price it, before
// the layer is merged. Prices drive admission against the daily cap, so the
// layer may raise input, cached and output of an alias, but not lower them,
// re-point an alias at another agent or family, or price an alias the layers
// below do not have: such an alias keeps its other keys and no price, which
// paid admission refuses. A missing base price counts as 0. The comparison
// is negated so that NaN never passes. The nodes are resolved mappings.
func restrictProjectModels(base, layer *yaml.Node, projectFile string) ([]config.ConfigNotice, error) {
	aliases := mappingValue(layer, "models")
	if aliases == nil || aliases.Kind != yaml.MappingNode {
		return nil, nil
	}
	baseModels := mappingValue(base, "models")
	var notices []config.ConfigNotice
	for i := 0; i+1 < len(aliases.Content); i += 2 {
		alias, settings := aliases.Content[i].Value, aliases.Content[i+1]
		if settings.Kind != yaml.MappingNode {
			continue
		}
		baseAlias, prefix := mappingValue(baseModels, alias), "models."+alias+"."
		var err error
		settings.Content, err = keepEntries(settings.Content, func(key string, value *yaml.Node) (bool, error) {
			baseValue := mappingValue(baseAlias, key)
			switch key {
			case "input", "cached", "output":
				var price, basePrice float64
				if err := decodeOptional(value, &price); err != nil {
					return false, err
				}
				if baseAlias == nil {
					if price != 0 {
						notices = append(notices, config.ConfigNotice{Key: prefix + key, Reason: fmt.Sprintf(
							"%s in %s is ignored: a shared config may not price a model the user config does not; paid runs refuse it",
							formatNumber(price), projectFile)})
					}
					return false, nil
				}
				if err := decodeOptional(baseValue, &basePrice); err != nil {
					return false, err
				}
				if !(price >= basePrice) {
					notices = append(notices, config.ConfigNotice{Key: prefix + key, Reason: fmt.Sprintf(
						"%s in %s is ignored: a shared config may only raise model prices; %s applies",
						formatNumber(price), projectFile, formatNumber(basePrice))})
					return false, nil
				}
			case "agent", "family":
				if baseAlias != nil && (baseValue == nil || baseValue.Value != value.Value) {
					applies := "none"
					if baseValue != nil {
						applies = baseValue.Value
					}
					notices = append(notices, config.ConfigNotice{Key: prefix + key, Reason: fmt.Sprintf(
						"%s in %s is ignored: a shared config may not re-point a priced model; %s applies",
						value.Value, projectFile, applies)})
					return false, nil
				}
			}
			return true, nil
		})
		if err != nil {
			return nil, err
		}
	}
	return notices, nil
}

// projectRestrictors are the rules of a shared layer: each removes the values
// that would widen what the layers below allow.
var projectRestrictors = []func(base, layer *yaml.Node, file string) ([]config.ConfigNotice, error){
	restrictProjectPaid, restrictProjectSessionLimits, restrictProjectRouting, restrictProjectModels,
}

// sharedLocal names the local layer in notices when it counts as shared, ""
// when it is the person's own. It is the person's own only in a Git
// checkout that does not track it: a committed copy reaches everyone who
// clones the repository, and outside a checkout, as in a downloaded archive
// or a copy without .git, nothing proves that the person wrote it. When Git
// cannot answer, the file counts as tracked, so a layer of unknown origin
// cannot turn spending on.
func sharedLocal(root string) string {
	var stderr bytes.Buffer
	cmd := exec.Command("git", "-C", root, "ls-files", "-z", "--", localFile)
	cmd.Env = append(cmd.Environ(), "LC_ALL=C")
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	var exit *exec.ExitError
	switch {
	case err == nil && len(out) == 0:
		return ""
	case errors.As(err, &exit) && strings.Contains(stderr.String(), "not a git repository"):
		return outsideLocal
	}
	return trackedLocal
}

// outsideNotice says, once, why a local layer outside a Git checkout had
// values ignored and where the person's own go.
var outsideNotice = config.ConfigNotice{Key: localFile, Reason: "outside a Git checkout nothing proves that it is your own, " +
	"so it may only make paid runs, prices and session limits stricter; put your own opt-ins in the user config"}
