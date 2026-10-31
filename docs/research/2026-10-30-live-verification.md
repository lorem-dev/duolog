# Live verification of agent assumptions

This page records what Claude Code and Codex really did when we tested the
assumptions behind duolog, before any product code existed. Every answer comes
from a run on 2026-10-30 and 2026-10-31, unless it says NOT VERIFIED.

## Results that contradict the design

Read these first. Tasks 9 to 13, 15 and 16 must change for them.

| What the design says | What we measured | What it means |
|---|---|---|
| A reference is cheaper than content on the bus | A reference to a whole file cost 1.9 times the content. `resume` cost the most. Only a reference to a narrow range was cheaper, and 1 of its 3 runs gave no review | Put small files in the task. Use a reference with a line range. Do not expect `resume` to save tokens |
| `PermissionRequest` works under one-off runs | It never fires under `codex exec`. The approval policy stays `never` | Under `codex exec`, the gate has only `PreToolUse` and the sandbox |
| Hooks protect the worker | `codex exec` silently skips a hook that has no trust. The command ran and nothing warned us | A gate on an untrusted hook is off. duolog must check trust before it relies on the hook |
| `.codex/agents` may not load | It loads, under `codex exec` too, but only in a trusted folder. Without trust, the tool has no `agent_type` field | Agent conversion stays. duolog must check folder trust and must not fall back in silence |
| Rex works from either agent | `rex` is not on PATH in the Codex shell. Under `codex sandbox`, it cannot reach the Rex socket | A Codex lead cannot open a pane from a sandboxed command. Only a Claude lead is proven |
| `SessionStart` can register the lead with its effort | No `SessionStart` stdin names the effort. Claude `-p` does not name the model either | Read effort from a later hook or from the transcript |
| Claude `/model` and `/effort` affect one session | They wrote to `~/.claude/settings.json`, even with `--setting-sources project,local` | Never send these commands to a worker pane |

## Summary of every question

Versions: codex-cli 0.165.0 at the start and at the end, so it did not change.
Claude Code 2.1.312. rex 0.0.1793047680. The shared Codex server is a separate
binary at version 0.166.0. It was already there before these runs.

| Id | Question | Answer |
|---|---|---|
| A1 | Codex loads an agent file and runs it by name | VERIFIED YES, trusted folder only |
| A2 | Codex accepts a hyphen in an agent name | VERIFIED YES, in `agent_type` |
| A3 | Codex needs `trust_level` for agent files | VERIFIED YES |
| S1 | Extra SKILL.md keys cause problems in Codex | VERIFIED NO |
| S2 | Codex honors `disable-model-invocation` | VERIFIED NO |
| S3 | Codex loads `.agents/skills` without trust | VERIFIED YES |
| S4 | Claude runs a skill with no symlink | VERIFIED YES |
| C1 | Claude runs an agent converted from TOML | VERIFIED YES |
| C2 | Claude honors `effort` in agent frontmatter | NOT VERIFIED |
| C3 | Claude starts `mcpServers` from agent frontmatter | NOT VERIFIED |
| H1 | `PreToolUse` deny works under `claude -p` | VERIFIED YES |
| H2 | `PreToolUse` deny works under `codex exec` | VERIFIED YES |
| H3 | `PermissionRequest` fires under `claude -p` | VERIFIED YES |
| H4 | `PermissionRequest` fires under `codex exec` | VERIFIED NO |
| H5 | `PreToolUse` deny works under `bypassPermissions` | VERIFIED YES |
| H6 | `codex exec` runs an untrusted hook | VERIFIED NO |
| H7 | Hook allow removes the prompt, live, in both agents | VERIFIED YES |
| H8 | Codex hooks run in a main session without a bypass flag | VERIFIED YES, after trust |
| H9 | Hooks fire for tools inside a `spawn_agent` child | NOT VERIFIED |
| D1 | `asyncRewake` wakes an idle Claude | VERIFIED YES |
| D2 | `codex queue` wakes an idle Codex | VERIFIED YES |
| D3 | `codex queue` reaches a closed session when reopened | NOT VERIFIED |
| E1 | SessionStart stdin names the session id | VERIFIED YES, both agents |
| E2 | SessionStart stdin names the model | VERIFIED YES, except `claude -p` |
| E3 | SessionStart or UserPromptSubmit stdin names the effort | VERIFIED NO, both agents |
| E4 | A later hook shows a changed effort | VERIFIED YES for Claude, VERIFIED NO for Codex |
| E5 | A later hook shows a changed model | VERIFIED YES for Codex, VERIFIED NO for Claude |
| E6 | Effort can be read with no tokens | VERIFIED YES, both agents, from the transcript |
| E7 | Value of effort that was never chosen | VERIFIED: see the effort section |
| P1 | A later hook shows a changed mode in Claude | VERIFIED YES |
| P2 | A later hook shows a changed mode in Codex | VERIFIED NO |
| U1 | `check_for_update_on_startup=false` stops the check | NOT VERIFIED |
| U2 | `DISABLE_AUTOUPDATER=1` stops the check | NOT VERIFIED |
| U3 | `codex exec` or `claude -p` can stop on a question | NOT VERIFIED: never seen |
| M1 | A hook can register MCP, and its token cost | NOT VERIFIED: check dropped |
| M2 | MCP can write to `.duolog/` under the sandbox | NOT VERIFIED: check dropped |
| M3 | Plugins and MCP are active under `claude -p` | VERIFIED YES |
| M4 | Plugins and user MCP servers are active under `codex exec` | NOT VERIFIED |
| M5 | Read-only `codex exec` reads a Claude plugin SKILL.md | VERIFIED YES |
| R1 | `rex split`, `send`, `send-key` drive both agents from Claude | VERIFIED YES |
| R2 | `rex` works from a sandboxed Codex command | VERIFIED NO |
| R3 | A closed pane and an exited program can be told apart | VERIFIED YES |
| R4 | SessionEnd fires when the pane closes, Claude | VERIFIED YES |
| R5 | SessionEnd fires when the pane closes, Codex | VERIFIED NO |
| B1 | A whole-file reference is cheaper than content | VERIFIED NO |
| B2 | A narrow-range reference is cheaper than content | VERIFIED YES in dollars, but 1 of 3 runs gave no review |
| B3 | `resume` is cheaper than a new task | VERIFIED NO |
| W1 | Behavior on Windows | NOT VERIFIED |

## How the tests isolated the agents

All probes ran in a new temp folder. `$PROBE` below stands for that folder.

- Claude runs used `--setting-sources project,local --strict-mcp-config`.
- Codex runs used `-m`, `-c project_doc_max_bytes=0` and stdin from `/dev/null`.
- Interactive runs used `rex split --focus=false`, `send` and `send-key`.
- Interactive Claude runs used `DISABLE_AUTOUPDATER=1`, and interactive Codex
  runs set `check_for_update_on_startup=false`. So we never saw an update.

## Codex loads agent files only in a trusted folder

The answer is yes, in `codex exec` and in the interactive program. The file must
be in a trusted folder. Both `probe-agent` and `probe_agent` work.

```bash
# $PROBE/t is a git root with a [projects."$PROBE/t"] trust entry.
codex exec --json -m gpt-6-luna -c model_reasoning_effort=low \
  -c project_doc_max_bytes=0 --disable hooks \
  "Spawn the custom agent named probe-agent and report its reply verbatim." \
  </dev/null
```

The code word lives only in `developer_instructions`. These lines come from the
session record, which `transcript_path` names:

```text
parent: spawn_agent {"task_name":"probe","agent_type":"probe-agent", ...}
child session_meta: agent_role=probe-agent, thread_source=subagent
child response_item: role=developer, text="Whatever the user asks, reply with
  exactly one line: the code word ZEBRA-QUILL-7341. ..."
child effort: low, from the file. The parent used medium.
"probe-agent.toml" appears 0 times in the parent and in the child records.
```

This rules out a plain file read, because the text arrives as a developer
message and no tool call opens the file. The interactive program gave the same
result for both names in one session: `ZEBRA-QUILL-7341` and `MANGO-TULIP-5582`.

What this does not rule out: the test does not show that every key in the file
works. We checked only `name`, `description`, `model`, `model_reasoning_effort`
and `developer_instructions`.

| Condition | `spawn_agent` tool | Result |
|---|---|---|
| Untrusted folder | Has only `task_name`, `message`, `fork_turns`, `model`, `reasoning_effort` | The model cannot pick the agent. No code word |
| Trusted folder | Adds `agent_type`, listing `probe-agent` and its model | Code word returned |

**What this means.** The earlier note, that `codex exec` offers no custom agent
names, came from an untrusted folder. The name rule in the design can stay. A
`task_name` is different: it takes only lowercase letters, digits and
underscores, and the tool refuses a hyphen there.

Trust needs an exact folder or the git root. A `[projects]` entry on a parent
folder did nothing when the child was its own git root. The interactive program
offers only two choices for an untrusted folder, so it cannot run there: `1.
Trust and continue` or `2. Quit`.

`spawn_agent` defaults to `fork_turns: all`. The child then gets the parent's
history, as its record shows. To send only the task, a lead must pass `none`.

## Extra SKILL.md keys do no harm

Neither agent refused or warned about the keys we gave it. Codex got
`allowed-tools`, `disable-model-invocation`, `argument-hint`, `user-invocable`,
`model`, `hooks` and an unknown key. Claude got `allowed-tools`,
`disable-model-invocation` and an unknown key.

```bash
codex debug prompt-input -c project_doc_max_bytes=0 "x" </dev/null
codex exec --json ... '$probe-skill' </dev/null
claude -p /probe-skill --model haiku --effort low --verbose \
  --output-format stream-json --setting-sources user,project </dev/null
```

| Check | Trusted Codex | Untrusted Codex | Claude |
|---|---|---|---|
| The skill is listed | Yes, in the prompt | Yes, in the prompt | Yes, in the init event |
| Calling it returned `COPPER-LATCH-6650` | Yes, no tool call | Yes, no tool call | Yes |

**What this means.** `sync.skills.drop_keys` is not needed to avoid errors. But
Codex lists a skill that has `disable-model-invocation: true`, so that key does
not hide it. We did not test whether Claude hides such a skill from the model.

## Converting a TOML agent to Claude works, with gaps

`claude --agent` ran the converted file. The prompt was only `Begin.`, and the
model then tried `touch agent_probe.txt`. That command exists only in the agent
body. Hook stdin also has `"agent_type":"probe-conv"`.

| TOML field | Claude form | Result |
|---|---|---|
| `developer_instructions` | File body | Works |
| `model` | `model: haiku` | Works, `modelUsage` has only `claude-haiku-5-5` |
| `model_reasoning_effort` | `effort: low` | NOT VERIFIED. Hook stdin said `medium`, so it may not apply |
| `sandbox_mode` | No match. Kept as a key | Ignored, no error. Claude has no per-agent OS sandbox here |
| `mcp_servers` | `mcpServers` | Accepted. NOT VERIFIED that it starts, because the run used `--strict-mcp-config` |
| none | `permissionMode: plan` | The flag `--permission-mode bypassPermissions` won |

**What this means.** `sandbox_mode` has no match, so a conversion from Codex
must refuse it or map it to deny rules. Treat `effort` as unproven and pass
`--effort` at run time.

## Hook decisions under one-off and live runs

`PreToolUse` deny works everywhere we tested. `PermissionRequest` works
everywhere except `codex exec`, where it does not fire.

| Agent and run | `PreToolUse` deny, JSON | `PreToolUse` deny, exit 2 | `PermissionRequest` deny | `PermissionRequest` allow |
|---|---|---|---|---|
| `claude -p` | Blocked | Blocked | Blocked | File created |
| `codex exec` | Blocked | Blocked | Did not fire | Did not fire |
| Claude, live, `default` mode | Not tested | Not tested | Blocked | File created, no prompt |
| Codex, live, `on-request` | Not tested | Not tested | Blocked | File created, no prompt |

In the two live "allow" cases we sent no key. The screen showed no prompt and
the file appeared. A command with no hook decision showed the prompt. Those
screens are in `scripts/bench/fixtures/startup/`.

Commands:

```bash
claude -p --model haiku --effort low --no-session-persistence \
  --output-format json --setting-sources project,local --strict-mcp-config \
  --allowedTools Bash -- "Use the Bash tool to run exactly: touch probe.txt ."
codex exec --json --dangerously-bypass-hook-trust -m gpt-6-luna \
  -c model_reasoning_effort=low -c project_doc_max_bytes=0 \
  "Run the shell command: touch probe_a2.txt . Then say done." </dev/null
```

Output of the Codex deny:

```text
ERROR codex_core::tools::router: error=Command blocked by PreToolUse hook:
probe deny by PreToolUse json. Command: touch probe_a2.txt
```

**A command that needs approval under `codex exec`.** We passed `-s read-only`
and `-c approval_policy="on-request"`. The session record still says
`approval_policy: never`. The sandbox refused the write, the model reported it,
and the process exited with 0. No one is asked, and the exit code does not show
the refusal.

**An untrusted hook under `codex exec`.** With no bypass flag, a project hook
did not run. `touch` created the file, the exit code was 0, and the output had
no warning. The run had `PreToolUse` deny in `.codex/hooks.json`.

**`bypassPermissions`.** A `PreToolUse` deny still blocked Bash in `claude -p
--permission-mode bypassPermissions`.

**Tool names in Codex hooks.** A shell command is `Bash`, also when the model
calls it from inside the `exec` code tool. `spawn_agent` is
`collaborationspawn_agent` and `wait_agent` is `collaborationwait_agent`. The
`apply_patch` name is NOT VERIFIED.

**Formats.** Samples of each stdin and the honored outputs are in
`scripts/bench/fixtures/hooks/`. See `hook-output-honored.json`.

## Codex hooks run in a main session after one trust step

The answer is yes. In a Codex session with no bypass flag, project hooks did not
run until we trusted them. Then `UserPromptSubmit` and, in the next session,
`SessionStart` fired.

The trust step happens either in the startup screen `Hooks need review` or with
`t` in `/hooks`. Codex stores it in `~/.codex/config.toml` as
`[hooks.state."<file>:<event>:0:0"]` with `trusted_hash`.

Codex fires `SessionStart` on the first turn, not when the program opens. A
`spawn_agent` child did not fire `SessionStart` or `UserPromptSubmit`: two
children and one firing. Those children ran no tools, so H9 stays open.

### How a hook can tell a main session from a child run

| Signal | Claude | Codex |
|---|---|---|
| Our own variable | `DUOLOG_DEPTH` not tested in Claude | `DUOLOG_DEPTH=1` reached the hook |
| Agent's own value | `CLAUDE_CODE_ENTRYPOINT` is `cli` or `sdk-cli`. `CLAUDE_CODE_SESSION_ATTENDED` is `1` or `0` | First line of `transcript_path`: `originator` is `codex-tui` or `codex_exec` |
| Weak signal | `CLAUDE_CODE_CHILD_SESSION=1` was set in all runs, so it says nothing | `permission_mode` is `bypassPermissions` in every `codex exec`, `default` in the live runs |

A Codex run started from Claude inherits `CLAUDE_*` variables, and its hooks see
them. Do not treat them as facts about the current session.

## Delivery to an idle session works in both agents

`asyncRewake` woke a Claude session with no key from us, in two cases: 6 seconds
after start with no prompt, and 25 seconds after a prompt, when the turn had
ended. The model got this text, and Claude also fired `UserPromptSubmit` for it:

```text
<task-notification><summary>Stop hook feedback</summary></task-notification>
<system-reminder>Stop hook blocking error from command "SessionStart:startup":
wake probe cl-ci-rw-ss code RWK-4471</system-reminder>
```

```json
{"type":"command","command":"...","asyncRewake":true}
```

`codex queue` woke a live session, and the session answered with no key from us.
This worked in embedded mode and on the shared server.

```bash
codex queue --thread <id> --message 'ping: reply with the single word PONG-9913'
# Queued message 01a18f11-... for thread 01a18f11-...
```

**What this means.** A rewake uses a turn, and the hook runs again on
`UserPromptSubmit`, so the hook must guard against repeats. On a closed session,
`codex queue` also printed `Queued` with exit code 0. So exit code 0 does not
mean delivery. We did not test what happens on reopen.

## What hook stdin says about the session

| Field | Claude `-p` | Claude live | `codex exec` | Codex live |
|---|---|---|---|---|
| SessionStart: `session_id` | Yes | Yes | Yes | Yes |
| SessionStart: `model` | No | `claude-haiku-5-5` | `gpt-6-luna` | `gpt-6-luna` |
| SessionStart: effort | No | No | No | No |
| UserPromptSubmit: effort | No | No | No | No |
| PreToolUse, PermissionRequest: effort | `effort.level` | `effort.level` | No | No |
| `transcript_path` | Yes | Yes | Yes | Yes |

### How to read the effort

| Agent | Hook way | Way with no tokens at any time | After a change mid-session |
|---|---|---|---|
| Claude | `effort.level` and `CLAUDE_EFFORT`, in `PreToolUse` and `PermissionRequest` only | `effort` on each `assistant` line of `transcript_path` | `/effort high` changed all of them. Hook: `medium`, then `high` |
| Codex | None | `turn_context.effort` in `transcript_path` | `/model` to terra and low changed it from `medium` to `low` |

`CLAUDE_EFFORT` in `SessionStart` and `UserPromptSubmit` is not reliable. It was
absent in fresh panes, and in a nested run it held the parent's value, `high`,
when the run's effort was `low`.

**An effort nobody chose.**

- Claude: `medium` for Haiku 5.5, in the hook and on screen, live and in `-p`.
  We did not measure it for Opus or Sonnet.
- Codex: `turn_context.effort` is `null` with `--ignore-user-config`. The
  model's default comes from `default_reasoning_level` in `codex debug models`,
  such as `medium` for `gpt-6-luna`. We did not see the value the server used.

**The model after a change.** Codex shows it in later hooks. Claude shows it
only in the transcript, as `message.model`: `claude-haiku-5-5`, then
`claude-sonnet-5-5`.

**Warning.** Claude `/effort` and `/model` wrote to user settings, as the
incident below shows. In Codex, the `/model` menu has `enter default` and `s
session`. We used `s`, and `config.toml` did not change. Other Codex choices are
NOT VERIFIED.

## How the permission mode shows in hooks

| Agent | Values seen in `permission_mode` | How to set at startup | A later hook after a change |
|---|---|---|---|
| Claude | `default`, `auto`, `acceptEdits`, `bypassPermissions` | `--permission-mode <mode>` | Yes: `default`, then `acceptEdits` after `shift+tab` |
| Codex | `default` (approval `on-request`), `bypassPermissions` (approval `never`) | `-s <mode>`, `-a <policy>`. The help also lists `--approve-for-me`, which we did not run | No: it stayed `default` |

A new Claude session with no setting started in `auto`. In that mode, `touch`
ran with no `PermissionRequest`. So this hook does not fire for every action.

The Codex `permission_mode` does not show the sandbox. After `/permissions`,
these fields changed in the `turn_context` lines of the transcript:
`sandbox_policy.type`, `approval_policy`, `approvals_reviewer` (for example
`auto_review`) and `active_permission_profile.id`. We did not select `Full
Access`, or the other `-a` policies, so their values are NOT VERIFIED.

## What can ask a question at startup

Exact screens are in `scripts/bench/fixtures/startup/`.

| Screen | Agent | Default choice | Seen |
|---|---|---|---|
| Trust folder | Claude | `No, exit`. Enter alone quits | Yes |
| Trust folder | Codex | `Trust and continue` | Yes |
| Hooks need review | Codex | `Review hooks` | Yes |
| Switch model | Claude | `Yes` | Yes |
| Update, login, new model notice | Both | Unknown | No, NOT VERIFIED |

**Stopping the update check.** We did not run either agent without its setting,
because version 0.166.0 of Codex is available and a run could update it. So we
have no baseline, and U1 and U2 stay open. We saw only this:

| Setting | What we saw |
|---|---|
| `-c check_for_update_on_startup=false` | No "unrecognized" warning. No update screen. `~/.codex/version.json` kept its time |
| The same key in `.codex/config.toml` | Same, and no embedded-mode warning |
| `DISABLE_AUTOUPDATER=1` | Debug log: `AutoUpdaterWrapper: Installation type: package-manager`. It says nothing about this variable |

**Any `-c` has a cost in a live session.** Codex warns: `Running without the
shared background server: command-line configuration overrides (-c, --enable,
--disable, or --search) requires embedded mode.` Project config avoids this.

**One-off runs.** No `codex exec` or `claude -p` run stopped on a question, and
this includes untrusted folders. We cannot prove this for every case. An update,
a login failure and a quota end did not occur.

**Quota text.** A live Claude screen printed `You've used 98% of your 5-hour
limit`. The limit itself ended this work. An Agent subagent failed with the text
in `scripts/bench/fixtures/limits/claude-5h.txt`, with error type `rate_limit`
and HTTP 429. Whether `claude -p` prints the same text is NOT VERIFIED.

## How duolog can list capabilities with no model call

None of these commands calls a model. Samples are in
`scripts/bench/fixtures/capabilities/`.

| Agent | Command or file | Notes |
|---|---|---|
| Claude | `claude plugin list --json` | Has `id`, `version`, `scope`, `enabled`, `installPath` |
| Claude | `claude mcp list` | Text. It connects to each server first, so it is slow |
| Claude | `~/.claude/plugins/installed_plugins.json` | `plugins` maps each `name@marketplace` to a list of installs |
| Claude | `enabledPlugins` in `~/.claude/settings.json` | An object. We did not sample its values |
| Codex | `codex plugin list --json` | About 1.7 KB. Plain `codex plugin list` printed 1.8 MB of catalog |
| Codex | `codex mcp list --json` | Has `env` and `env_vars`. Remove them before logging |
| Codex | `[mcp_servers.<name>]` in `~/.codex/config.toml` | The same servers as `codex mcp list` on this machine |
| Codex | `~/.agents/skills/`, `.agents/skills/` | Folders of skills |
| Claude | `~/.claude/skills/`, `.claude/skills/`, and the `skills` list in the `-p` init event | The init event needs a model call |

**Under one-off runs.** The `claude -p` init event in `--output-format
stream-json --verbose` lists `plugins`, `mcp_servers` and `skills`, with status
for each server. For `codex exec`, the session listed `mcp__codex_apps__` tools.
The user's `codegraph` server was not in the session record, and it may load on
demand. So M4 stays open.

**Claude skills from Codex.** Under `codex exec -s read-only`, `head` read a
SKILL.md at an absolute path under `~/.claude/plugins/cache`. It printed `name:
figma-code-connect`.

## How to map a model id to its alias

| Source | Field | Example |
|---|---|---|
| Claude hook | `model` in live `SessionStart` | `claude-opus-5-5` for `--model opus` |
| Claude `-p` result | keys of `modelUsage` | `claude-haiku-5-5` |
| Claude stream | `model` in the init event | `claude-haiku-5-5` |
| Codex hook | `model` in every event | `gpt-6-luna` |
| `codex exec --json` | No model field in any event | Use the transcript |
| Codex transcript | `turn_context.model` | `gpt-6-luna` |

`claude --model opus` reported `claude-opus-5-5`, the screen said `Opus 5.5`,
and that is the newest Opus in our price table. We cannot prove that it picks
the newest when a new one appears. A rule that the alias is the part between
`claude-` and the version fits all three ids we saw.

`codex debug models` calls no model. It printed the same bytes under
`sandbox-exec` with the network denied, so it needs no network. This also means
it can be out of date. The top level is `{"models":[...]}` with 11 models. Each
has `slug`, `visibility` (`list` or `hide`), `default_reasoning_level` and
`supported_reasoning_levels`. See `fixtures/models/`, which holds a trimmed
copy.

## What Rex does for a closed pane and an exited program

The two cases differ. A closed pane is gone, and an exited program leaves the
block there. The `rex split --json` field for the new block is `block_ids[0]`.

| Command | Pane closed | Program exited (code 3) |
|---|---|---|
| `rex block inspect` | Exit 3, `block target "<id>" not found` | Exit 0, block data |
| `rex block ls --json` | Block absent | Block listed |
| `rex capture` | Exit 3, same error | Exit 0, the old output |
| `rex send` | Exit 3, same error | Exit 0, **the text goes nowhere** |
| `rex wait` | Exit 3, `block, window, or session target "<id>" not found` | Exit 3, `exit status 3`. With `--json`: `{"exit_code":3,"error":"exit status 3"}` |

`rex wait` exits with the program's code, so exit 3 alone is ambiguous. Use `rex
block inspect` first. A block looked the same with and without `--keep-open`, 2
seconds after the exit. The block without `--keep-open` was still there minutes
later, until we closed it.

| SessionEnd | Claude | Codex |
|---|---|---|
| Pane closed | Fires, `"reason":"other"` | Does not fire |
| `/quit` | Not tested | Fires, `"reason":"other"` |

**Timing.** `rex send` followed at once by `rex send-key enter` lost the Enter
once in Codex, for a long prompt. A wait of 0.7 seconds fixed this in every
later send. `peer_send` needs a wait and a check.

**Other Rex problems.**

- `rex` was not found in the Codex shell, and `REX_BIN_DIR` was not on its PATH.
- `codex sandbox -- rex whoami --json` exited 4: `connecting to Rex server ...
  failed to WebSocket dial`. Other sandbox modes and escalated commands are NOT
  VERIFIED.
- A Codex probe tried to rename the Rex session, and nobody asked it to. It
  failed because `rex` was not found. We did not prove the cause. A user-level
  Rex hook and skill exist on this machine. With a working `rex`, a worker might
  rename the user's session, so tasks must plan for this.

## MCP checks were dropped

The plan no longer has a duolog MCP server, because the user chose the Rex CLI.
So we did not test M1 or M2.

## The bus: a reference saved tokens only for a narrow range

Each row uses `gpt-6-luna`, effort `low`, 3 repeats, from `turn.completed` in
`--json`. The file has 2,000 lines and 28,663 bytes, and it adds about 8,700
tokens. The money column uses the prices on the [practices
page](2026-10-30-practices-and-models.md).

| Form | Input tokens, each run | Mean input | Mean cached | Mean output | Mean USD |
|---|---|---|---|---|---|
| Empty call | 14,800; 14,800; 14,800 | 14,800 | 12,245 | 5 | 0.00038 |
| Content in the prompt | 23,154; 23,700; 23,700 | 23,518 | 12,544 | 38 | 0.00124 |
| Reference, whole file | 88,926; 100,904; 88,590 | 92,806 | 78,762 | 366 | 0.00238 |
| Reference, 16 lines | 45,030; 60,151; 44,869 | 50,016 | 46,848 | 217 | 0.00089 |
| `resume`, first turn | 86,073; 110,940; 88,699 | 95,237 | 83,797 | 395 | 0.00218 |
| `resume`, second turn | 107,207; 133,340; 110,756 | 117,101 | 103,381 | 445 | 0.00263 |

```bash
uv run --no-project python scripts/bench/handoff_bench.py <empty-temp-dir>
```

**Why a reference costs more.** `input_tokens` adds up all model calls in a
turn. In one run, the seven calls summed exactly to the reported 134,953. Each
model call sends the whole context again, mostly from cache. The whole-file
reference runs used 4 or 5 commands. In the range run whose text we read first,
the model said it would read project guidance first.

**Thresholds, from these numbers.**

| Choice | Result |
|---|---|
| Content or whole-file reference | Content was cheaper at this size. The reader pays for the file in both |
| Content or narrow reference | The reference wins above about 5,000 tokens of file, about 1,100 lines here. This is weak, because 1 of 3 range runs failed |
| `resume` or a new task | `resume` lost. The second turn cost more than a new task with content |
| Hand over or do it yourself, lead on Opus | Cheaper above about 100 tokens of file |
| Hand over or do it yourself, lead on Sonnet | Cheaper above about 200 tokens of file |
| Hand over or do it yourself, lead on Haiku or Luna | Never cheaper, by price alone |

The last three use `0.00038 / (lead input price - 0.10 per million)`. They
ignore output, the answer and the lead's cache. They are rough limits, not
promises.

**What this does not rule out.** The file repeats itself, so a real file may
have more tokens per line. Three repeats are few, and cache hits changed between
runs. We used the first form of a `task.md`, and a stricter one could need fewer
commands. The range test was added to the requested three forms, and a shell
loop ran it. The script has this form now, but we did not run that version
again.

**The answers.** We kept the answers only for the range runs. Two found the
planted bug in `Sum137`. The third said that it could not review the file, so
its tokens bought no review. The script does not save answers, so the quality of
the other forms is NOT VERIFIED.

## What these tests changed on the machine

| Item | State after the runs |
|---|---|
| `~/.codex/config.toml` | We added a trust entry, 2 hook hashes and raised a counter, then removed them. `diff` with the first backup shows only the changes the controller made |
| `~/.codex/hooks.json` | `cmp` says identical |
| `~/.claude/settings.json` | See below. The SHA-1 matches the value from before the runs |
| `~/.codex/sessions` | We removed the probe session files. `codex delete` needs `--force`, so rows may stay in the Codex state database |
| `~/.claude.json`, `~/.claude/history.jsonl` | Still mention the temp folder or probe prompts. We left them, because live sessions rewrite them |
| Claude 5-hour limit | 91% at the first warning, 98% later. The limit then stopped this work until it reset |

**The incident.** We sent `/effort high` and `/model sonnet` to a probe Claude.
The run had `--setting-sources project,local`, and Claude still wrote to
`~/.claude/settings.json`. `model` went from `opus` to `sonnet`, and
`modelSettings` gained `claude-haiku-5-5`. A new session started in that minute
could have used Sonnet. We edited those two lines back and checked the SHA-1
(`b2f9109e...`).

## Related pages

- [Practices and models](2026-10-30-practices-and-models.md): the earlier
  evidence and the prices
- [The bench script](../../scripts/bench/handoff_bench.py): the token
  measurement
