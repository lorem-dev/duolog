package caps

import (
	"slices"
	"sort"
	"strings"
)

const (
	KindSkill  = "skill"
	KindPlugin = "plugin"
	KindMCP    = "mcp"
)

// Item holds names only. Source is the absolute SKILL.md path of a skill and
// is empty for the other kinds. No env, header or token is ever stored.
// Bridgeable marks an MCP server from a config file that a worker of the other
// agent can borrow; NoBridge says why one cannot be.
type Item struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Source     string `json:"source,omitempty"`
	Bridgeable bool   `json:"bridgeable,omitempty"`
	NoBridge   string `json:"no_bridge,omitempty"`
}

// Inventory is what one agent has. Unknown means the list could not be read
// without a model call, so nothing may be claimed about what it lacks.
type Inventory struct {
	Agent   string `json:"agent"`
	Items   []Item `json:"items"`
	Unknown bool   `json:"unknown,omitempty"`
	// UnknownKinds lists kinds whose list is known to be incomplete, for
	// example mcp for Claude (connectors and plugin-provided servers are not
	// in any file).
	UnknownKinds []string `json:"unknown_kinds,omitempty"`
}

// KindUnknown reports whether nothing may be claimed as absent for kind.
func (inv Inventory) KindUnknown(kind string) bool {
	if inv.Unknown {
		return true
	}
	for _, k := range inv.UnknownKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// Lendable splits servers into those another agent could borrow, sorted, and
// those it cannot, with why says why: why gives the reason a server cannot go
// to that agent, or "" when it can.
func Lendable(servers map[string]Server, why func(Server) string) (ok []string, not map[string]string) {
	not = map[string]string{}
	for name, s := range servers {
		if reason := why(s); reason != "" {
			not[name] = reason
		} else {
			ok = append(ok, name)
		}
	}
	sort.Strings(ok)
	return ok, not
}

// Mark adds the servers from an agent's config files to its items, with what
// bridging can do: why gives the reason a server cannot go to the worker's
// agent, or "" when it can. The result is normalized.
func Mark(items []Item, servers map[string]Server, why func(Server) string) []Item {
	ok, not := Lendable(servers, why)
	for _, name := range ok {
		items = append(items, Item{Kind: KindMCP, Name: name})
	}
	for name := range not {
		items = append(items, Item{Kind: KindMCP, Name: name})
	}
	for i := range items {
		if items[i].Kind != KindMCP {
			continue
		}
		if reason, found := not[items[i].Name]; found {
			items[i].NoBridge = reason
		} else {
			items[i].Bridgeable = slices.Contains(ok, items[i].Name)
		}
	}
	return Normalize(items)
}

// Normalize drops repeated items, keeping the first of each kind and name,
// and sorts the rest by kind, then name.
func Normalize(items []Item) []Item {
	seen := make(map[[2]string]bool)
	out := []Item{}
	for _, it := range items {
		k := [2]string{it.Kind, it.Name}
		if !seen[k] {
			seen[k] = true
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Diff returns the items of a that b lacks. It is directional.
func Diff(a, b Inventory) []Item {
	out := []Item{}
	for _, it := range a.Items {
		if _, ok := find(b, it.Kind, it.Name); !ok && !b.KindUnknown(it.Kind) {
			out = append(out, it)
		}
	}
	return out
}

func find(inv Inventory, kind, name string) (Item, bool) {
	for _, it := range inv.Items {
		if it.Kind != kind {
			continue
		}
		if it.Name == name || (kind == KindSkill && strings.HasSuffix(name, ":"+it.Name)) {
			return it, true
		}
	}
	return Item{}, false
}
