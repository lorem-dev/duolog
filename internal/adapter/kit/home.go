package kit

import (
	"path/filepath"

	"github.com/lorem-dev/duolog/internal/trusthome"
)

// Home is the config folder of an agent for a process whose environment is
// environ: the value of variable (CLAUDE_CONFIG_DIR, CODEX_HOME) when it is
// set, else def under the process's home folder (.claude, .codex). A process
// without a home folder and without the variable is trusthome.ErrNoHome: the
// agent would fall back to a home that environ does not name.
func Home(environ []string, variable, def string) (string, error) {
	if dir := trusthome.Getenv(environ, variable); dir != "" {
		return dir, nil
	}
	home, err := trusthome.HomeIn(environ)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, def), nil
}
