package policy

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/core/mode"
)

// An assignment that points a Docker client at a daemon on this machine is
// not a file the command reads or writes, so the gate judges the command
// after it as if it stood alone. Before, DOCKER_HOST=unix:///x/docker.sock
// go test ./... asked the person because the socket lay outside the work
// root, and the Docker-backed tests never ran in a worker.
func TestLocalDockerHostJudgesTheCommandAfterIt(t *testing.T) {
	p := policy(t)
	root := t.TempDir()
	judge := func(m mode.Mode, command string) Decision {
		return Evaluate(p, LeadRules{}, m, Action{Kind: "exec", Target: command, Role: "implement", Work: root, Cwd: root})
	}
	same := func(m mode.Mode, command, alone string) {
		t.Helper()
		got, want := judge(m, command), judge(m, alone)
		if got != want {
			t.Errorf("%s: %q: %+v, want %+v as for %q", m, command, got, want, alone)
		}
	}
	for _, m := range []mode.Mode{mode.Full, mode.Edits, mode.Ask} {
		for _, command := range []string{
			`DOCKER_HOST=unix:///x/.colima/default/docker.sock go test ./...`,
			`DOCKER_HOST=unix:///x/docker.sock go test ./...`,
			`DOCKER_HOST="unix:///x/docker.sock" go test ./...`,
			`DOCKER_HOST=tcp://localhost:2375 go test ./...`,
			`DOCKER_HOST=tcp://localhost go test ./...`,
			`DOCKER_HOST=tcp://127.0.0.1:2375 go test ./...`,
			`DOCKER_HOST=tcp://[::1]:2375 go test ./...`,
			`go vet ./... && DOCKER_HOST=unix:///x/docker.sock go test ./...`,
		} {
			check(t, p, LeadRules{}, m, Action{Kind: "exec", Target: command, Role: "implement", Work: root, Cwd: root}, Allow)
		}
		// The command after the assignment keeps its own decision.
		same(m, `DOCKER_HOST=unix:///x/docker.sock docker ps`, `docker ps`)
		same(m, `DOCKER_HOST=unix:///x/docker.sock cat ../peer/x`, `cat ../peer/x`)
		same(m, `DOCKER_HOST=unix:///x/docker.sock rm -rf ../peer`, `rm -rf ../peer`)
		same(m, `DOCKER_HOST=tcp://localhost:2375 make check`, `make check`)
	}
}

// Any other assignment keeps today's ask: one that changes how a program or
// duolog runs, a remote daemon, and a socket path that the shell could turn
// into another one.
func TestOtherAssignmentsKeepTheirAsk(t *testing.T) {
	p := policy(t)
	root := t.TempDir()
	for _, m := range []mode.Mode{mode.Full, mode.Edits, mode.Ask} {
		for _, command := range []string{
			`PATH=/x go test ./...`,
			`PATH=/x DOCKER_HOST=unix:///x/docker.sock go test ./...`,
			`DOCKER_HOST=unix:///x/docker.sock PATH=/x go test ./...`,
			`LD_PRELOAD=/x/y.so go test ./...`,
			`DYLD_INSERT_LIBRARIES=/x/y.dylib go test ./...`,
			`GIT_CONFIG_GLOBAL=/x/gitconfig go test ./...`,
			`DUOLOG_MODE=full go test ./...`,
			`CLAUDE_CONFIG_DIR=/x go test ./...`,
			`CODEX_HOME=/x go test ./...`,
			`DOCKER_CONFIG=/x go test ./...`,
			`DOCKER_HOST=tcp://example.com:2375 go test ./...`,
			`DOCKER_HOST=tcp://localhost.example.com:2375 go test ./...`,
			`DOCKER_HOST=ssh://user@host go test ./...`,
			`DOCKER_HOST=unix://x/docker.sock go test ./...`,
			`DOCKER_HOST=unix:///x/../y/docker.sock go test ./...`,
			`DOCKER_HOST=unix://~/docker.sock go test ./...`,
			`DOCKER_HOST=unix://$HOME/docker.sock go test ./...`,
			`DOCKER_HOST=unix:///x/*.sock go test ./...`,
			`docker_host=unix:///x/docker.sock go test ./...`,
			`DOCKER_HOST=unix:///x/docker.sock`,
		} {
			a := Action{Kind: "exec", Target: command, Role: "implement", Work: root, Cwd: root}
			if d := Evaluate(p, LeadRules{}, m, a); d.Kind != Ask {
				t.Errorf("%s: %q: %+v, want an ask", m, command, d)
			}
		}
	}
}

// The denies that read the raw line still see the command after a local
// DOCKER_HOST.
func TestLocalDockerHostKeepsTheDenies(t *testing.T) {
	p := policy(t)
	root := t.TempDir()
	for _, command := range []string{
		`DOCKER_HOST=unix:///x/docker.sock claude -p hi`,
		`DOCKER_HOST=unix:///x/docker.sock codex exec hi`,
		`DOCKER_HOST=unix:///x/docker.sock duolog telemetry`,
		`DOCKER_HOST=unix:///x/docker.sock cat .duolog/state.json`,
		`DOCKER_HOST=unix:///x/docker.sock git push --force`,
	} {
		a := Action{Kind: "exec", Target: command, Role: "implement", Work: root, Cwd: root}
		if d := Evaluate(p, LeadRules{}, mode.Full, a); d.Kind == Allow {
			t.Errorf("%q: %+v, want no allow", command, d)
		}
	}
}
