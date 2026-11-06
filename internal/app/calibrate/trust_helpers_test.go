package calibration

import (
	"context"

	"github.com/lorem-dev/duolog/internal/app/launch/launchtest"
)

// trustDeps are the Deps whose trust reads are the real adapters', which read
// the temporary homes the tests set.
func trustDeps() Deps {
	d := Deps{Agents: map[string]Agent{}}
	for name, t := range launchtest.Trusts() {
		d.Agents[name] = Agent{Trust: t}
	}
	return d
}

// NewTreeAt, NewJudgeSlot and CheckTreeTrust run the Deps methods of the same
// names on trustDeps, so the tests of the tree and trust checks read as they
// did when these were package functions.
func NewTreeAt(ctx context.Context, repo, commit, dir string, environ []string) (func() error, error) {
	return trustDeps().NewTreeAt(ctx, repo, commit, dir, environ)
}

func NewJudgeSlot(ctx context.Context, repo, dir string, files map[string][]byte, environ []string) (func() error, error) {
	return trustDeps().NewJudgeSlot(ctx, repo, dir, files, environ)
}

func CheckTreeTrust(agent, dir string, environ []string) error {
	return trustDeps().CheckTreeTrust(agent, dir, environ)
}
