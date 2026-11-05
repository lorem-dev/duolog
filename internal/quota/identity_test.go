package quota

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/testbin"
)

const (
	accountMarker = "RAW-ACCOUNT-MARKER"
	orgMarker     = "RAW-ORG-MARKER"
)

type identityCall struct {
	Agent, Kind string
	Raw         []byte
}

// recordSink is an identity sink that keeps what it was given, and fails
// with err.
func recordSink(calls *[]identityCall, err error) func(agent, kind string, raw []byte) error {
	return func(agent, kind string, raw []byte) error {
		*calls = append(*calls, identityCall{agent, kind, raw})
		return err
	}
}

func claudeIdentityAuth(fields string) string {
	return `{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty","subscriptionType":"pro"` + fields + `}`
}

func codexIdentityAccount(fields string) string {
	return `{"jsonrpc":"2.0","id":3,"result":{"account":{"type":"chatgpt","planType":"plus"` + fields + `},"requiresOpenaiAuth":true}}`
}

func installIdentityAgent(t *testing.T, agent, identity string) {
	t.Helper()
	isolate(t)
	if agent == "claude" {
		testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{
			freeClaude("-p"):                 {Stdout: "{\"type\":\"system\"}\n" + claudeReply + "\n"},
			freeClaude("auth status --json"): {Stdout: identity},
		}})
		return
	}
	testbin.Install(t, "codex", testbin.Scenario{RPCResponses: map[string]string{
		"initialize": `{"id":1,"result":{}}`, "account/rateLimits/read": codexReply,
		"account/read": identity, "modelProvider/list": codexProviderReply,
	}})
}

// The identity fields of a free reply go from the decoder to the sink and
// nowhere else: the snapshot and the errors never hold them. A reply
// without them hands the sink nothing, a read that got no reply calls no
// sink, and a failing sink fails the read.
func TestIdentitySinkReceivesTransientReplyFields(t *testing.T) {
	for _, tc := range []struct {
		name, agent, identity string
		want                  []identityCall
	}{
		{"claude", "claude", claudeIdentityAuth(`,"email":"` + accountMarker + `","orgId":"` + orgMarker + `"`),
			[]identityCall{{"claude", IdentityAccount, []byte(accountMarker)}, {"claude", IdentityOrg, []byte(orgMarker)}}},
		{"claude missing", "claude", claudeIdentityAuth(`,"email":"","orgId":7`),
			[]identityCall{{"claude", IdentityAccount, nil}, {"claude", IdentityOrg, nil}}},
		{"claude unread", "claude", "not json " + accountMarker, nil},
		{"codex", "codex", codexIdentityAccount(`,"email":"` + accountMarker + `","workspaceName":"` + orgMarker + `"`),
			[]identityCall{{"codex", IdentityAccount, []byte(accountMarker)}, {"codex", IdentityOrg, []byte(orgMarker)}}},
		{"codex missing", "codex", codexIdentityAccount(`,"workspaceName":null`),
			[]identityCall{{"codex", IdentityAccount, nil}, {"codex", IdentityOrg, nil}}},
		{"codex unread", "codex", `{"jsonrpc":"2.0","id":3,"error":{"message":"` + accountMarker + `"}}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			installIdentityAgent(t, tc.agent, tc.identity)
			var calls []identityCall
			r := Reader{Version: "test", Timeout: 3 * time.Second, IdentitySink: recordSink(&calls, nil)}
			s, err := r.Read(context.Background(), tc.agent)
			if err != nil && !strings.HasSuffix(tc.name, "unread") {
				t.Fatal(err)
			}
			if !slices.EqualFunc(calls, tc.want, func(a, b identityCall) bool {
				return a.Agent == b.Agent && a.Kind == b.Kind && string(a.Raw) == string(b.Raw) && (a.Raw == nil) == (b.Raw == nil)
			}) {
				t.Fatalf("sink calls %+v, want %+v", calls, tc.want)
			}
			data, _ := json.Marshal(s)
			for _, text := range []string{string(data), errorText(err)} {
				if strings.Contains(text, "RAW-") {
					t.Fatalf("raw identity kept: %s", text)
				}
			}
		})
	}

	for _, agent := range []string{"claude", "codex"} {
		t.Run(agent+" sink fails", func(t *testing.T) {
			identity := claudeIdentityAuth(`,"email":"` + accountMarker + `"`)
			if agent == "codex" {
				identity = codexIdentityAccount(`,"email":"` + accountMarker + `"`)
			}
			installIdentityAgent(t, agent, identity)
			var calls []identityCall
			r := Reader{Version: "test", Timeout: 3 * time.Second, IdentitySink: recordSink(&calls, errors.New("sink failed: "+accountMarker))}
			s, err := r.Read(context.Background(), agent)
			if err == nil || err.Error() != "account identity not recorded" {
				t.Fatalf("read with a failing sink: %v", err)
			}
			if len(s.Windows) != 0 || len(calls) != 1 {
				t.Fatalf("failed identity refresh still returned quota: %+v, calls %d", s, len(calls))
			}
		})
	}
}
