package delegate

import (
	"strings"

	"github.com/lorem-dev/duolog/internal/core/config"
	coreroute "github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/task"
)

func place(cfg *config.Config, taskPath, root string, route coreroute.Route) (Placement, error) {
	p := Placement{Where: "self", Model: route.Model, Effort: route.Effort, Task: taskPath}
	refs, err := task.References(taskPath, root)
	if err != nil {
		return p, err
	}
	files := make(map[string]bool)
	lines := 0
	for _, ref := range refs {
		files[ref.Path] = true
		data, err := ref.Text(root)
		if err != nil {
			p.Where = "subagent"
			return p, nil //nolint:nilerr // an unreadable file is reported as a problem in p, not as an error
		}
		lines += strings.Count(string(data), "\n")
		if len(data) > 0 && data[len(data)-1] != '\n' {
			lines++
		}
	}
	if len(files) > cfg.Limits.SelfMaxReadFiles || lines > cfg.Limits.SelfMaxReadLines {
		p.Where = "subagent"
	}
	return p, nil
}
