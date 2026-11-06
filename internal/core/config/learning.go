package config

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// LearnedRoutingFile holds the ladder orders a person approved with
// `duolog learn`. It is generated; the orders never go into config.yaml.
const LearnedRoutingFile = "routing.learned.yaml"

type Learning struct {
	Enabled          bool                     `yaml:"enabled" jsonschema_description:"Turns learning mode on; defaults to false, and with it off nothing changes."`
	Threshold        float64                  `yaml:"threshold" jsonschema_description:"Cost-first classes pick the cheapest entry whose quality minus the margin reaches this, 0 to 3; defaults to 2.5."`
	QualityMargin    float64                  `yaml:"quality_margin" jsonschema_description:"Quality-first classes pick a cheaper entry within this distance of the best one; defaults to 0.25."`
	PriorRuns        float64                  `yaml:"prior_runs" jsonschema_description:"How many judged runs a prior quality is worth; defaults to 2."`
	Uncertainty      float64                  `yaml:"uncertainty" jsonschema_description:"The margin is this divided by the square root of prior runs plus judged weight; defaults to 0.5."`
	ProfileBelowRuns int                      `yaml:"profile_below_runs" jsonschema_description:"Task finish offers to profile an entry with less judged weight than this; defaults to 3."`
	Judges           []LadderEntry            `yaml:"judges" jsonschema_description:"Judge targets in order; the first whose agent neither side used judges; replaced as a whole by a layer."`
	Classify         []ClassRule              `yaml:"classify" jsonschema_description:"Ordered rules that map task features to a class; the first match wins and a layer replaces the list as a whole."`
	Classes          map[string]Class         `yaml:"classes" jsonschema_description:"Task classes; overrides merge by class and field into the ten defaults, and a new name adds a class."`
	Learned          map[string]LearnedLadder `yaml:"-" jsonschema:"-"`
}

type ClassRule struct {
	When  map[string]string `yaml:"when" jsonschema_description:"Feature names and the values they must have; every pair must match."`
	Class string            `yaml:"class" jsonschema_description:"Class chosen when every pair in when matches."`
}

type Class struct {
	Description string                   `yaml:"description" jsonschema_description:"What kind of work the class covers; shown to the lead when it classifies a task."`
	Priority    string                   `yaml:"priority" jsonschema_description:"quality picks the best entry unless a cheaper one is close; cost picks the cheapest good enough entry." jsonschema:"enum=quality,enum=cost"`
	Roles       []string                 `yaml:"roles" jsonschema_description:"Roles whose work this class routes."`
	Ladder      []LadderEntry            `yaml:"ladder" jsonschema_description:"Targets in configured order, cheapest first unless priors say otherwise."`
	Steps       map[string][]LadderEntry `yaml:"steps" jsonschema_description:"Ladders for single roles of the class, which override ladder for that role."`
	Rubric      []string                 `yaml:"rubric" jsonschema_description:"Questions the judge scores a run against."`
	Checks      []string                 `yaml:"checks" jsonschema_description:"Commands or checks that count toward the score."`
}

type LadderEntry struct {
	Agent  string  `yaml:"agent" jsonschema_description:"Agent that runs this entry." jsonschema:"enum=claude,enum=codex"`
	Model  string  `yaml:"model" jsonschema_description:"Model alias from models, or a deliberate gpt- or claude- version pin."`
	Effort string  `yaml:"effort" jsonschema_description:"Requested reasoning effort." jsonschema:"enum=low,enum=medium,enum=high,enum=xhigh,enum=max,enum=ultra"`
	Prior  float64 `yaml:"prior" jsonschema_description:"Guessed quality from 0 to 3, worth prior_runs judged runs."`
}

func (e LadderEntry) Target() Target {
	return Target{Agent: e.Agent, Model: e.Model, Effort: e.Effort}
}

func (e LadderEntry) Key() string { return e.Agent + "/" + e.Model + "/" + e.Effort }

var efforts = []string{"low", "medium", "high", "xhigh", "max", "ultra"}

// ParseEntryKey reads agent/model/effort back into an entry with no prior.
func ParseEntryKey(key string) (LadderEntry, error) {
	parts := strings.Split(key, "/")
	if len(parts) != 3 {
		return LadderEntry{}, fmt.Errorf("entry %q: want agent/model/effort", key)
	}
	if parts[0] != "claude" && parts[0] != "codex" {
		return LadderEntry{}, fmt.Errorf("entry %q: unknown agent %q", key, parts[0])
	}
	if parts[1] == "" {
		return LadderEntry{}, fmt.Errorf("entry %q: empty model", key)
	}
	if !slices.Contains(efforts, parts[2]) {
		return LadderEntry{}, fmt.Errorf("entry %q: unknown effort %q", key, parts[2])
	}
	return LadderEntry{Agent: parts[0], Model: parts[1], Effort: parts[2]}, nil
}

// Features are the facts a classification rule may test, and their values.
// A rule may also test role, with any value.
var Features = map[string][]string{
	"exemplar":         {"yes", "partly", "no"},
	"components":       {"1", "2+"},
	"outside_evidence": {"yes", "no"},
	"output":           {"code", "plan", "review", "tests", "docs", "diagnosis"},
	"scope":            {"1-3", "4-20", "20+"},
	"ambiguity":        {"low", "high"},
	"target":           {"code", "plan"},
}

// CheckFeature is the one check of a feature name and value.
func CheckFeature(key, value string) error {
	if key == "role" {
		return nil
	}
	values, ok := Features[key]
	if !ok {
		names := []string{"role"}
		for name := range Features {
			names = append(names, name)
		}
		sort.Strings(names[1:])
		return fmt.Errorf("unknown feature %q (known: %s)", key, strings.Join(names, ", "))
	}
	if !slices.Contains(values, value) {
		return fmt.Errorf("%s: %q is not one of %s", key, value, strings.Join(values, ", "))
	}
	return nil
}

type LearnedRouting struct {
	Ladders map[string]LearnedLadder `yaml:"ladders"`
}

type LearnedLadder struct {
	Order []string             `yaml:"order"`
	Stats map[string]EntryStat `yaml:"stats"`
}

type EntryStat struct {
	Delegations      int     `yaml:"delegations"`
	Weight           float64 `yaml:"weight"`
	Quality          float64 `yaml:"quality"`
	Failures         int     `yaml:"failures"`
	USD              float64 `yaml:"usd"`
	Seconds          float64 `yaml:"seconds"`
	CalibrationTasks int     `yaml:"calibration_tasks,omitempty"` // calibration tasks with evidence: a failed check, a decided verdict or an unmatched pass
}

// LadderKey names the ladder a role uses: the step ladder when the class has
// one for the role, else the class ladder.
func LadderKey(class, role string, c Class) string {
	if _, ok := c.Steps[role]; ok {
		return class + "." + role
	}
	return class
}

// Ladder returns the key and entries for a class and role: the configured
// ladder with the learned order, when there is one, first.
func (l Learning) Ladder(class, role string) (string, []LadderEntry) {
	c, ok := l.Classes[class]
	if !ok {
		return "", nil
	}
	key := LadderKey(class, role, c)
	configured := c.Ladder
	if steps, ok := c.Steps[role]; ok {
		configured = steps
	}
	learned, ok := l.Learned[key]
	if !ok || len(learned.Order) == 0 {
		return key, slices.Clone(configured)
	}
	byKey := make(map[string]LadderEntry, len(configured))
	for _, e := range configured {
		byKey[e.Key()] = e
	}
	var out []LadderEntry
	seen := map[string]bool{}
	for _, name := range learned.Order {
		if seen[name] {
			continue
		}
		e, ok := byKey[name]
		if !ok {
			parsed, err := ParseEntryKey(name)
			if err != nil {
				continue
			}
			parsed.Prior = l.Threshold
			e = parsed
		}
		seen[name] = true
		out = append(out, e)
	}
	for _, e := range configured {
		if !seen[e.Key()] {
			out = append(out, e)
		}
	}
	return key, out
}
