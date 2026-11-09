package config

import (
	"sync"

	"go.yaml.in/yaml/v3"
)

// Routing says how a new task picks between a role's agents beyond the
// ladder.
type Routing struct {
	Balance string  `yaml:"balance" jsonschema_description:"Balance subscriptions by pace: pace starts a new task on the role's fallback while its use agent runs ahead of its weekly window, off keeps the ladder as written; defaults to pace. The shared project config may not turn it off." jsonschema:"enum=pace,enum=off"`
	Margin  float64 `yaml:"margin" jsonschema_description:"How far the used share of the weekly window may exceed the elapsed share before an agent counts as ahead of pace, and how far below pace it must fall to count as back, 0 or more and below 1; defaults to 0.10. The shared project config may only lower it." jsonschema:"minimum=0,exclusiveMaximum=1"`
	// AgentOrder is the order in which a task with no fallback agent of its
	// own takes one: the first agent that is registered, enabled, available
	// and not the use agent.
	AgentOrder []string `yaml:"agent_order" jsonschema_description:"Order in which an agent is taken when a role has no fallback agent of its own: the first registered, enabled, available agent that is not the role's use agent; defaults to claude, codex."`
	// SmallTaskFiles is the most files a write task may name in its Write
	// section and still run in the lead; 0 turns the rule off.
	SmallTaskFiles int `yaml:"small_task_files" jsonschema_description:"Most files a task's Write section may name for a writing role to run in the lead instead of a worker, because a hand-over has a fixed cost; 0 turns it off, defaults to 2. Read-only roles and reviews always get a worker. A Write section that is missing or names a glob or a folder does not qualify. Only the user config and a local config of your own may set it; the shared project config may not." jsonschema:"minimum=0"`
}

// SmallTaskRoles are the roles that write files: a task of one of them that
// changes few files runs in the lead.
var SmallTaskRoles = []string{"implement", "mechanical", "tests", "docs", "planner"}

// DefaultAgentOrder is routing.agent_order when the config sets none.
func DefaultAgentOrder() []string { return []string{"claude", "codex"} }

// embeddedRouting is the routing section of the embedded config.default.yaml.
var embeddedRouting = sync.OnceValue(func() (out struct {
	Routing Routing `yaml:"routing"`
}) {
	_ = yaml.Unmarshal(defaultConfig, &out)
	return out
})

// DefaultRouting returns the embedded routing settings.
func DefaultRouting() Routing { return embeddedRouting().Routing }
