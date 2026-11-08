package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/lorem-dev/duolog/internal/adapter/mcp"
)

// mcpAnswerAsk answers a permission ask through the CLI's decide path, so the
// tool has the rights of `duolog allow|deny <n>` from the lead's shell and
// returns its errors unchanged. reason is accepted and ignored: the CLI has no
// reason flag.
func (d *cmdDeps) mcpAnswerAsk(_ context.Context, in mcp.AnswerAskInput) (mcp.AnswerAskOutput, error) {
	if isWorker() {
		return mcp.AnswerAskOutput{}, errors.New("workers cannot answer permission requests")
	}
	var verb string
	switch in.Decision {
	case "allow":
		verb = "allowed"
	case "deny":
		verb = "denied"
	default:
		return mcp.AnswerAskOutput{}, errors.New("decision is allow or deny")
	}
	if in.Ask < 1 {
		return mcp.AnswerAskOutput{}, fmt.Errorf("invalid request number %q", strconv.FormatInt(in.Ask, 10))
	}
	if err := d.decide(in.Decision, in.Ask, ""); err != nil {
		return mcp.AnswerAskOutput{}, err
	}
	return mcp.AnswerAskOutput{Line: "ask " + strconv.FormatInt(in.Ask, 10) + " " + verb}, nil
}
