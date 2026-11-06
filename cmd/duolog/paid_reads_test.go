package main

import (
	"maps"

	"github.com/lorem-dev/duolog/internal/testbin"
)

// Recorded first-party subscription replies of the free quota reads: Claude's
// /usage stream and auth status, and Codex's app-server methods. Windows reset
// far in the future, overage is off and no route signal shows.
const (
	claudeUsageReply  = `{"type":"system"}` + "\n" + `{"usage_report":{"rate_limits":{"limits":[{"kind":"session","percent":15,"resets_at":"2099-01-01T00:00:00Z"},{"kind":"weekly_all","percent":50,"resets_at":"2099-01-08T00:00:00Z"}],"extra_usage":{"is_enabled":false,"used_credits":0,"monthly_limit":null}}}}` + "\n"
	claudeAuthReply   = `{"loggedIn":true,"authMethod":"claude.ai","subscriptionType":"max"}`
	codexAccountRead  = `{"jsonrpc":"2.0","id":3,"result":{"account":{"type":"chatgpt","planType":"plus"},"requiresOpenaiAuth":true}}`
	codexLimitsRead   = `{"jsonrpc":"2.0","id":2,"result":{"rateLimits":{"planType":"plus","primary":{"usedPercent":13,"windowDurationMins":300,"resetsAt":4070908800},"secondary":{"usedPercent":20,"windowDurationMins":10080,"resetsAt":4071513600},"ordinaryUsageAllowed":true}}}`
	codexProviderRead = `{"jsonrpc":"2.0","id":4,"result":{"defaultProviderId":"openai","data":[{"id":"openai","name":"OpenAI","baseUrl":null,"requiresOpenaiAuth":true,"envKey":null,"experimentalBearerToken":null,"auth":null,"aws":null}]}}`
)

// subscriptionReads adds to a fake agent's scenario the replies of the free
// quota read that every gated delegation makes first, for a subprocess that
// runs the real duolog binary. Keys name the operation, so they match after
// the worker process's --settings or -c prefix.
func subscriptionReads(agent string, worker testbin.Scenario) testbin.Scenario {
	responses := maps.Clone(worker.Responses)
	if responses == nil {
		responses = map[string]testbin.Scenario{}
	}
	switch agent {
	case "claude":
		responses["-p /usage"] = testbin.Scenario{Stdout: claudeUsageReply}
		responses["auth status"] = testbin.Scenario{Stdout: claudeAuthReply}
	case "codex":
		responses["app-server"] = testbin.Scenario{SleepMS: 10000, RPCResponses: map[string]string{
			"initialize": `{"jsonrpc":"2.0","id":1,"result":{}}`, "account/read": codexAccountRead,
			"account/rateLimits/read": codexLimitsRead, "modelProvider/list": codexProviderRead,
		}}
	}
	worker.Responses = responses
	return worker
}
