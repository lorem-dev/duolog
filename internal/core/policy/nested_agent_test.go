package policy

import (
	"testing"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

// A worker that starts claude or codex itself runs outside duolog's routing
// and quota. Under a full lead a policy ask becomes allow, so the default
// policy must deny these execs outright.
func TestWorkersCannotStartAgents(t *testing.T) {
	home := t.TempDir()
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "APPDATA"} {
		t.Setenv(k, home)
	}
	cfg, err := configfile.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{
		"claude -p hi", "claude", "codex exec hi", "/usr/local/bin/claude -p hi", "git status && codex exec x", "claude.exe -p hi",
		// Claude review I4, Codex review 7: Windows launchers and package runners.
		"claude.cmd -p hi", "codex.cmd exec hi", "claude.ps1 -p hi", "codex.ps1 exec hi", "claude.CMD -p hi", "codex.bat exec hi",
		`C:\Users\x\AppData\Roaming\npm\codex.cmd exec hi`, "& claude.ps1 -p hi",
		"npx @anthropic-ai/claude-code -p hi", "npx -y @anthropic-ai/claude-code@latest -p hi", "pnpm dlx @anthropic-ai/claude-code -p hi",
		"npm exec @anthropic-ai/claude-code -- -p hi", "npm x @openai/codex exec hi", "bunx @openai/codex exec hi", "bun x @openai/codex exec hi",
		"yarn dlx @openai/codex exec hi", "pnpx @openai/codex exec hi", "npx.cmd @openai/codex exec hi",
		"node /usr/local/lib/node_modules/@anthropic-ai/claude-code/cli.js -p hi", "node.exe C:/npm/node_modules/@openai/codex/bin/codex.js exec hi",
		// Prefixes that run the command they wrap.
		"env claude -p hi", "FOO=1 claude -p hi", "time codex exec hi", "nohup claude -p hi &", "cmd /c claude -p hi", "env FOO=1 npx @openai/codex exec hi",
		// Re-review m3: npm: specifiers, tarballs and prefix options with values.
		"deno run -A npm:@anthropic-ai/claude-code -p hi", "deno run -A npm:@openai/codex@1.2 exec hi",
		"npx https://registry.npmjs.org/@anthropic-ai/claude-code/-/claude-code-1.0.0.tgz -p hi",
		"npx ./anthropic-ai-claude-code-1.0.0.tgz -p hi", "npx /tmp/openai-codex-0.9.tgz exec hi",
		"exec -a x claude -p hi", "timeout -s KILL 5 claude -p hi", "timeout --signal=KILL 5 codex exec hi", "sudo -s claude -p hi", "sudo -u root claude -p hi",
	} {
		a := Action{Kind: "exec", Target: command, Role: "implement"}
		if d := Evaluate(cfg.Policy, LeadRules{}, mode.Full, a); d.Kind != Deny {
			t.Errorf("%q: %+v", command, d)
		}
	}
	for _, command := range []string{"claudette build", "codexgen test", "npx prettier --check .", "npm exec eslint .", "pnpm dlx tsc --noEmit", "deno run -A npm:prettier .", "npx ./prettier-3.0.0.tgz .", "sudo -u claude whoami"} {
		if d := Evaluate(cfg.Policy, LeadRules{}, mode.Full, Action{Kind: "exec", Target: command, Role: "implement"}); d.Kind == Deny {
			t.Errorf("%q denied: %+v", command, d)
		}
	}
}
