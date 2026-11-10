# What the live agent probes showed

Agent probes from 2026-10-30 and 2026-10-31 record the assumptions behind
duolog, with saved evidence and limits on what can be reproduced.

## How to interpret the results

A saved fixture proves only what it records. Live observations with missing
setup are not replayable; the table below names their limits.

## Which questions the probes answered

Versions: codex-cli 0.165.0 at the start and at the end, so it did not change.
Claude Code 2.1.312. rex 0.0.1793047680. The shared Codex server is a separate
binary at version 0.166.0. It was already there before these runs.

VERIFIED means a saved sample or script in this repository lets a reader check
the answer. OBSERVED means a real run showed it once or a few times, the
evidence text is on this page, and the full setup was not saved, so it is not
reproducible yet. NOT VERIFIED means no run showed it.

| Id | Question | Answer |
|---|---|---|
| A1 | Codex loads an agent file and runs it by name | OBSERVED YES, trusted folder only |
| A2 | Codex accepts a hyphen in an agent name | OBSERVED YES, in `agent_type` |
| A3 | Codex needs `trust_level` for agent files | OBSERVED YES |
| S1 | Extra SKILL.md keys cause problems in Codex | OBSERVED NO |
| S2 | Codex honors `disable-model-invocation` | OBSERVED NO |
| S3 | Codex loads `.agents/skills` without trust | OBSERVED YES |
| S4 | Claude runs a skill with no symlink | OBSERVED YES |
| C1 | Claude runs an agent converted from TOML | OBSERVED YES |
| C2 | Claude honors `effort` in agent frontmatter | NOT VERIFIED |
| C3 | Claude starts `mcpServers` from agent frontmatter | NOT VERIFIED |
| H1 | `PreToolUse` deny works under `claude -p` | OBSERVED YES |
| H2 | `PreToolUse` deny works under `codex exec` | OBSERVED YES |
| H3 | `PermissionRequest` fires under `claude -p` | OBSERVED YES |
| H4 | `PermissionRequest` fires under `codex exec` | OBSERVED NO |
| H5 | `PreToolUse` deny works under `bypassPermissions` | OBSERVED YES |
| H6 | `codex exec` runs an untrusted hook | OBSERVED NO |
| H7 | Hook allow removes the prompt, live, in both agents | OBSERVED YES |
| H8 | Codex hooks run in a main session without a bypass flag | OBSERVED YES, after trust |
| H9 | Hooks fire for tools inside a `spawn_agent` child | NOT VERIFIED |
| D1 | `asyncRewake` wakes an idle Claude | OBSERVED YES |
| D2 | `codex queue` wakes an idle Codex | OBSERVED YES |
| D3 | `codex queue` reaches a closed session when reopened | NOT VERIFIED |
| E1 | Saved SessionStart stdin names the session id | VERIFIED YES in all four samples |
| E2 | Saved SessionStart stdin names the model | VERIFIED YES, except the `claude -p` sample |
| E3 | Saved SessionStart or UserPromptSubmit stdin names the effort | VERIFIED NO in these samples |
| E4 | A later hook shows a changed effort | OBSERVED YES for Claude, OBSERVED NO for Codex |
| E5 | A later hook shows a changed model | OBSERVED YES for Codex, OBSERVED NO for Claude |
| E6 | Effort can be read with no new model call | OBSERVED YES, both agents, from later transcript records; the value can be stale and Codex can record null; change records were not saved. Immediate reading after a change is NOT VERIFIED |
| E7 | Value of effort that was never chosen | OBSERVED YES: Claude `medium` for Haiku 5.5; Codex `null` with `--ignore-user-config`. See the effort section |
| P1 | A later hook shows a changed mode in Claude | OBSERVED YES |
| P2 | A later hook shows a changed mode in Codex | OBSERVED NO |
| U1 | Codex startup update behavior | OBSERVED: no setting ran `brew upgrade --cask codex`; a later start with both flags did not. The effect of either flag alone is NOT VERIFIED |
| U2 | `DISABLE_AUTOUPDATER=1` stops the check | NOT VERIFIED |
| U3 | `codex exec` or `claude -p` can stop on a question | NOT VERIFIED: never seen |
| M1 | A hook can register MCP, and its token cost | NOT VERIFIED: check dropped |
| M2 | MCP can write to `.duolog/` under the sandbox | NOT VERIFIED: check dropped |
| M3 | `claude -p` discovers plugins and MCP servers | VERIFIED discovery in the init sample: plugins listed; servers connected, needs-auth and failed. Execution is NOT VERIFIED |
| M4 | Plugins and user MCP servers are active under `codex exec` | NOT VERIFIED |
| M5 | Read-only `codex exec` reads a Claude plugin SKILL.md | OBSERVED YES |
| R1 | `rex split`, `send`, `send-key` drive both agents from Claude | OBSERVED YES |
| R2 | `rex` works from a sandboxed Codex command | OBSERVED NO |
| R3 | A closed pane and an exited program can be told apart | OBSERVED YES |
| R4 | SessionEnd fires when the pane closes, Claude | OBSERVED YES |
| R5 | SessionEnd fires when the pane closes, Codex | OBSERVED NO |
| B1 | A whole-file reference cost less than content in these runs | VERIFIED NO for estimated cost; comparable review quality is NOT VERIFIED |
| B2 | The directed 16-line task cost less than whole-file content | VERIFIED YES for estimated cost only; different tasks. Comparable quality and the threshold are NOT VERIFIED |
| B3 | The second `resume` turn cost less than new content in these runs | VERIFIED NO for estimated cost; comparable review quality is NOT VERIFIED |
| W1 | Behavior on Windows | NOT VERIFIED |

## How the tests isolated the agents

The earlier run used a new temp folder. Its notes describe these defaults;
they do not provide complete setup for every probe:

- Most Claude runs used `--setting-sources project,local --strict-mcp-config`.
  The skill command below used `user,project` instead.
- Codex runs used `-m`, `-c project_doc_max_bytes=0` and stdin from `/dev/null`.
- Interactive runs used `rex split --focus=false`, `send` and `send-key`.
- The earlier report lists update settings for the probes. A separate live
  Codex observation had no update setting and ran an upgrade command. See U1.

## How to check the saved evidence without a model call

Run this from the repository root. It reads fixtures only. It does not start
an agent, call Rex, change settings or collect new measurements.

```bash
uv run scripts/bench/probes/check_saved_evidence.py
```

It checks E1 to E3, M3 discovery and the B1 to B3 cost comparisons. The prices
are USD per million tokens: 0.10 input, 0.01 cached input and 0.50 output.
It prints the means from `fixtures/results/handoff-bench.csv`. Whole-token
means use truncation, as in the original table; costs round to five decimals.
VERIFIED covers these saved samples, not every run.

### Which live setups cannot be rebuilt

Original temporary files and session records were removed. Fixtures retain
inputs and selected outputs, but these live setups cannot be reconstructed:

| Answers | Missing setup or evidence |
|---|---|
| A1 to A3 | Full agent TOML files, project config, trust entries and parent and child transcripts. The excerpt below gives selected fields only |
| S1 to S4 | Full SKILL.md bodies, key values, folder layout and per-run settings |
| C1 | Original TOML, converted agent file and conversion command. Hook stdin records `probe-conv` and its tool request, not how it was loaded |
| H1 to H8 | Hook scripts, full settings, matchers, trust state and per-case outputs. Hook stdin alone does not prove a decision was honored |
| D1 and D2 | Rewake script, repeat guard, hook settings and complete queue and reply sequence |
| E4 to E7 | Changed-value assistant and turn_context records and the command sequence. Startup fixtures do not contain transcript effort |
| P1 and P2 | Before and after mode records and exact interactive sequence |
| M5 | Plugin installation, exact SKILL.md file and complete command output |
| R1 to R5 | Full pane setup, commands and closure records. Saved split and wait JSON do not show all lifecycle comparisons |

U1 is a supplied observation, not an isolated test of one update setting.
The command fragments below are historical notes, not live replay
instructions.

## The earlier run reported agent loading after folder trust

The command used `codex exec --json`, model `gpt-6-luna`, effort `low`,
`project_doc_max_bytes=0` and `--disable hooks`. Its prompt asked to spawn
`probe-agent`. The full agent file and trust setup were not saved, so A1 to A3
are OBSERVED but not repeatable tests.

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

The earlier run inferred agent loading from the developer message and the
reported absence of a file-read call. The excerpt alone cannot establish the
absence of all file reads. The interactive program reportedly gave the same
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

## The skill run reported no errors for the tested keys

The earlier run reported no refusal or warning for its key set. Codex got
`allowed-tools`, `disable-model-invocation`, `argument-hint`, `user-invocable`,
`model`, `hooks` and an unknown key. Claude got `allowed-tools`,
`disable-model-invocation` and an unknown key.

The notes name `codex debug prompt-input`, `codex exec` with `$probe-skill`,
and `claude -p /probe-skill`. The Claude command used
`--setting-sources user,project`, unlike the isolation rule above. The skill
files and full commands were not saved. S1 to S4 are OBSERVED.

| Check | Trusted Codex | Untrusted Codex | Claude |
|---|---|---|---|
| The skill is listed | Yes, in the prompt | Yes, in the prompt | Yes, in the init event |
| Calling it returned `COPPER-LATCH-6650` | Yes, no tool call | Yes, no tool call | Yes |

The listing does not establish every key's effect. Claude's hiding of
skills with `disable-model-invocation` was not tested.

## The earlier run reported a converted Claude agent

C1 is OBSERVED because the source and converted files were not saved.
The following table records the earlier interpretation, not a repeatable
conversion test. The report says `claude --agent` ran the converted file.
The prompt was only `Begin.`, and the model then tried `touch agent_probe.txt`.
That command exists only in the agent
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

## The earlier run reported these hook decisions

The Codex exec fixture records the model reporting a blocked command, not
the hook script.

| Agent and run | `PreToolUse` deny, JSON | `PreToolUse` deny, exit 2 | `PermissionRequest` deny | `PermissionRequest` allow |
|---|---|---|---|---|
| `claude -p` | Blocked | Blocked | Blocked | File created |
| `codex exec` | Blocked | Blocked | Did not fire | Did not fire |
| Claude, live, `default` mode | Not tested | Not tested | Blocked | File created, no prompt |
| Codex, live, `on-request` | Not tested | Not tested | Blocked | File created, no prompt |

In the two live "allow" cases we sent no key. The screen showed no prompt and
the file appeared. A command with no hook decision showed the prompt. Those
approval-prompt screens are in `scripts/bench/fixtures/startup/`. The saved
screens do not prove the reported allow cases.

Incomplete historical commands (hook setup was not saved):

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

## The earlier run reported hook firing after trust

Project hooks reportedly ran only after trust: UserPromptSubmit first,
then SessionStart in the next session.

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

## The earlier run reported delivery to idle sessions

D1 and D2 are OBSERVED without their full setup and response records.
The report says `asyncRewake` woke Claude with no key from us in two cases:
6 seconds after start with no prompt, and 25 seconds after a prompt, when the
turn had
ended. The model got this text, and Claude also fired `UserPromptSubmit` for it:

```text
<task-notification><summary>Stop hook feedback</summary></task-notification>
<system-reminder>Stop hook blocking error from command "SessionStart:startup":
wake probe cl-ci-rw-ss code RWK-4471</system-reminder>
```

The hook entry used `type: command` and `asyncRewake: true`. The command
body and repeat guard were not saved. D1 is OBSERVED.

`codex queue` woke a live session, and the session answered with no key from us.
This worked in embedded mode and on the shared server.

The command used `codex queue --thread` with a live thread id and a message
asking for `PONG-9913`. The full queue and response records were not saved.
D2 is OBSERVED.

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

| Agent | Hook way | Read saved records with no new model call | Later record after a change |
|---|---|---|---|
| Claude | `effort.level` and `CLAUDE_EFFORT`, in `PreToolUse` and `PermissionRequest` only | `effort` on each `assistant` line of `transcript_path` | `/effort high` changed all of them. Hook: `medium`, then `high` |
| Codex | None | `turn_context.effort` in `transcript_path` | `/model` to terra and low changed it from `medium` to `low` |

| Limitation | Consequence |
|---|---|
| Effort appears in a later assistant record (Claude) or turn_context (Codex) | Existing records may be stale after a change. Reading them costs no model call; producing a turn can spend tokens. Immediate reading is unverified |
| Changed-value records were not retained | E4-E7 remain observations |
| Codex can record null effort | Neither it nor a catalog default proves the server's chosen effort |
| CLAUDE_EFFORT at SessionStart/UserPromptSubmit | Absent in fresh panes; a nested low-effort run inherited high. Unreliable |
| Claude effort with none chosen | Haiku 5.5 showed medium in hooks and on screen, live and in -p; Opus/Sonnet were not measured |
| Codex effort with --ignore-user-config | Null in turn_context; catalog lists a default (medium for gpt-6-luna), but server choice was not observed |
| Model after a switch | Codex names it in later hooks; Claude only in transcript message.model (haiku then sonnet) |
| Model/effort switches | Claude writes user settings. Codex's session-only choice left config.toml unchanged; other choices are unverified |

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

**Startup update observation (U1).** A live Codex started with no update
setting ran `brew upgrade --cask codex` by itself. A later start with
`--disable in_app_updates -c check_for_update_on_startup=false` did not run
that command. This observation does not separate the two settings. It also
does not prove that no background check occurred. The full startup commands
and captures were not saved, so this is an observation, not a replayable test.
The versions above stayed unchanged; running the command does not establish
that an upgrade completed.

The earlier probes also recorded these narrower observations. They do not
isolate either setting. U2 stays NOT VERIFIED.

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
Codex printed `hit your usage limit` in an `error` and a `turn.failed` event,
with `try again at 8:48 AM` (see `limits/codex-usage.jsonl`).

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
for each server. The saved init sample lists `plannotator`, `superpowers`
and `figma` as plugins. It reports `codegraph: connected`, `figma: needs-auth`
and `unreal-editor: failed`. This verifies discovery only. Listing a plugin
does not show that it ran. A connected server is not evidence of a tool call;
needs-auth and failed do not show a working connection. Plugin execution and
MCP tool execution under this run are NOT VERIFIED.

For `codex exec`, the session listed `mcp__codex_apps__` tools.
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

## The earlier run compared a closed pane and an exited program

The split fixture records `block_ids[0]`; the wait fixture records exit 3.
The reported lifecycle differences below remain OBSERVED.

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

## The bus: a directed narrow review saved estimated cost

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

The source script builds `target.go`, `task.md` and `task-range.md` itself.
For a future live rerun, this is the complete command from the repository root.
It starts agents and spends tokens; it was not run during this correction.

```bash
bench_root=$(mktemp -d)
uv run scripts/bench/handoff_bench.py "$bench_root"
```

The script records usage and command counts, not review answers. Its range
form points to lines 1225-1240, which contain the planted bug. Content and
whole-file forms ask for a whole-file review. These tasks are not equivalent.
The range form used more input tokens than content, but had lower estimated
cost because of its cache use. Comparable review quality is NOT VERIFIED.

**Why a reference costs more.** `input_tokens` adds up all model calls in a
turn. In one run, the seven calls summed exactly to the reported 134,953. Each
model call sends the whole context again, mostly from cache. The whole-file
reference runs used 4 or 5 commands. In the range run whose text we read first,
the model said it would read project guidance first.

**Cost comparisons and unverified threshold estimates.**

| Choice | Result |
|---|---|
| Content or whole-file reference | Content had lower estimated cost at this size. Comparable review quality is NOT VERIFIED |
| Content or narrow reference | The old estimate of about 5,000 tokens of file, about 1,100 lines here, is NOT VERIFIED. The tasks differ and 1 of 3 range runs failed |
| `resume` or a new task | The second turn cost more than a new task with content in these runs. Comparable review quality is NOT VERIFIED |
| Hand over or do it yourself, lead on Opus | Empty-call lower bound only: about 100 tokens of file. NOT VERIFIED as a break-even point |
| Hand over or do it yourself, lead on Sonnet | Empty-call lower bound only: about 200 tokens of file. NOT VERIFIED as a break-even point |
| Hand over or do it yourself, lead on Haiku or Luna | Empty-call lower bound only. "Never cheaper" applies only when the lead input price is at or below the worker input price |

The empty-call lower-bound formula is
`0.00038 / (lead input price - 0.10 per million)`. It omits worker input,
tools and output, the lead's answer-reading cost and cache effects. In this
model, handing over cannot save money when the lead's input price is at
most the worker's. No full hand-over break-even point was measured.

Limits: the synthetic file repeats; real files may have more tokens per
line. Three repeats and changing cache hits limit comparisons. A stricter
task format may reduce calls. The range form ran in a shell loop; the
updated script was not rerun. Answers were not retained. The earlier report
says two range runs found Sum137's bug and one refused review; other forms'
quality is unknown.

## What the second live round measured

Measured on 2026-10-31 with codex-cli 0.165.0, `gpt-6-luna` at low effort and
Claude `haiku` at low effort.

**The cache result.** `codex exec --output-schema` loses the prompt cache. The
probe is the saved
[probe file](../../scripts/bench/fixtures/cache/output-schema-probe.txt).
The same tiny task read 11520 cached input tokens without the flag and 0 with
it, twice. Putting the answer rule at the end of the stdin block kept 11520
cached. Turning off the update check also lifted the cache to 13696, which
duolog already does. So duolog no longer passes `--output-schema` to Codex. It
asks for one JSON object in plain text and reads the last valid object from the
final message, fenced or not. Claude keeps `--json-schema`.

**What the four live tests showed.**

| Test | Result |
|---|---|
| `TestDelegateRoundTrip` | Pass. Cached 11520 of 14952 input tokens |
| `TestSyncedSkillRunsInBoth` | Pass for Codex and Claude. The earlier Codex failure was a test defect: the test never trusted its temp folder, and Codex lists the project `.agents/skills` only for a trusted folder. One live run confirmed it (cached 41344 of 45535) |
| `TestCodexLeadDelegatesToClaude` | Skipped with a documented limit. See below |
| `TestInlineCostsLessThanReference` | Pass. Whole file inline 0.00137 USD against the saved whole-file reference 0.00238. The 16 lines inline 0.00049 against the saved range reference 0.00089 |

**The Codex lead.** Codex runs a login shell and resets `PATH`, so the lead
needs the full path to `duolog`. With the full path, a lead in the
`workspace-write` sandbox does run `duolog delegate`, and `claude -p` starts.
Claude then answers "Not logged in - Please run /login": the sandbox blocks the
macOS Keychain. `codex sandbox claude auth status` shows `loggedIn: false` in
the sandbox and `true` with `sandbox_mode="danger-full-access"`. Adding
`~/.claude` to the writable roots did not help. With
`--sandbox danger-full-access` the whole chain passed. A token in the
environment was not tested.

**One quality note.** The whole-file run named the right function but gave line
824, not the real line near 1230. The cheaper form was not checked for answer
quality beyond that.

## What happened when the hook binary was missing

<a id="missing-binary"></a>

Measured on real Claude Code 2.1.312. The setup registered exec-form hooks
with `"command":"duolog-missing-xyz"` and an `args` array for SessionStart,
UserPromptSubmit, PreToolUse and PostToolUse, with that executable absent.
Under `claude -p`, the tool ran, the process exited 0, and stderr was empty.
With a present binary, the same form's PreToolUse deny was honored.

The probe had no `onFailure: block`; current installed gate hooks set it.
Interactive failure display was not measured, and no replayable probe was
retained. See [current hook behavior](../permissions.md#where-the-checks-run).

## Why worker panes must not receive model switches

Sending `/effort high` and `/model sonnet` changed Claude's user settings,
even with `--setting-sources project,local`. The probe restored the two
changed fields and verified the original hash. A session started during
the change could have picked the wrong model. Pass model and effort at
startup; do not send these switches to worker panes.

## Related pages

- [Practices and models](2026-10-30-practices-and-models.md): the earlier
  evidence and the prices
- [The bench script](../../scripts/bench/handoff_bench.py): the token
  measurement
