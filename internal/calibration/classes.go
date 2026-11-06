package calibration

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	learnio "github.com/lorem-dev/duolog/internal/app/learn"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/learn"
	"github.com/lorem-dev/duolog/internal/journal"
	"go.yaml.in/yaml/v3"
)

// ClassesProposal is what the classes step offers: classes and rules for
// recurring work no rule fits, and survey checks that differ from the config.
type ClassesProposal struct {
	Proposals []learn.ClassProposal
	Checks    map[string][]string // class -> checks that differ from config
}

// ChecksName is the name the checks of class go by in a skip list, next to
// the names of the class proposals.
func ChecksName(class string) string { return "checks:" + class }

// ProposeClassesFrom treats every candidate that is not excluded as one
// delegation and proposes classes as learning mode does for the journal:
// five unclassified candidates with one role and feature set make a class.
// Everything else keeps the defaults.
func ProposeClassesFrom(cfg *config.Config, cands []Candidate, s Survey) ClassesProposal {
	var ds []journal.Delegation
	for _, c := range cands {
		if c.Excluded != "" {
			continue
		}
		class := c.Class
		if class == "" {
			class = learn.Unclassified
		}
		ds = append(ds, journal.Delegation{ID: c.ID, Task: c.ID, Role: c.Role, Features: c.Features, RuleClass: class, Class: class})
	}
	p := ClassesProposal{Proposals: learn.ProposeClasses(cfg, ds, nil)}
	for class, checks := range s.Checks {
		current, ok := cfg.Learning.Classes[class]
		if !ok || slices.Equal(checks, current.Checks) {
			continue
		}
		if p.Checks == nil {
			p.Checks = map[string][]string{}
		}
		p.Checks[class] = slices.Clone(checks)
	}
	return p
}

// ApplyClasses writes the proposals and checks of p, except those named in
// skip, into the project's config in one edit. A skip name that matches
// nothing is an error, so a typo never applies what the person meant to
// leave out. With nothing left to write the file stays as it is.
func ApplyClasses(root string, cfg *config.Config, p ClassesProposal, skip []string) (old, updated []byte, err error) {
	names := map[string]bool{}
	for _, q := range p.Proposals {
		names[q.Name] = true
	}
	for class := range p.Checks {
		names[ChecksName(class)] = true
	}
	for _, name := range skip {
		if !names[name] {
			return nil, nil, fmt.Errorf("no classes proposal named %q", name)
		}
	}
	proposals := slices.DeleteFunc(slices.Clone(p.Proposals), func(q learn.ClassProposal) bool { return slices.Contains(skip, q.Name) })
	classes := slices.DeleteFunc(slices.Sorted(maps.Keys(p.Checks)), func(class string) bool { return slices.Contains(skip, ChecksName(class)) })
	if len(proposals) == 0 && len(classes) == 0 {
		old, err = os.ReadFile(filepath.Join(root, ".duolog", "config.yaml"))
		if os.IsNotExist(err) {
			err = nil
		}
		return old, old, err
	}
	return learnio.EditConfig(root, func(l *yaml.Node) error {
		rules := cfg.Learning.Classify
		for _, q := range proposals {
			var err error
			if rules, err = learnio.AddClass(l, rules, q); err != nil {
				return err
			}
		}
		for _, class := range classes {
			if err := learnio.SetClassChecks(l, class, p.Checks[class]); err != nil {
				return err
			}
		}
		return nil
	}, func(loaded *config.Config) error {
		for _, q := range proposals {
			if err := learn.VerifyClass(loaded, q); err != nil {
				return err
			}
		}
		for _, class := range classes {
			if !slices.Equal(loaded.Learning.Classes[class].Checks, p.Checks[class]) {
				return fmt.Errorf("checks of class %q are overridden by a later config layer", class)
			}
		}
		return nil
	})
}
