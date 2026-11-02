# How to configure duolog

This page explains the config file: where duolog reads it, what each section
does, and how to check a role. The defaults are in
[internal/config/config.default.yaml](../internal/config/config.default.yaml).

## Where duolog reads the config

duolog merges four layers. A later layer changes only the fields it names. A
list, such as `policy.rules`, is one field, so a later list replaces the earlier
one. The default deny rules stay in every case.

| Order | File | Use |
|---|---|---|
| 1 | The embedded default | The rules that ship with duolog |
| 2 | `duolog/config.yaml` in your user config folder | Your rules for all projects |
| 3 | `.duolog/config.yaml` | The project rules. Commit this file |
| 4 | `.duolog/config.local.yaml` | Your private changes. Git ignores it |

The user config folder is the one that Go's `os.UserConfigDir` returns. On
macOS it is `~/Library/Application Support`. On Linux it is `~/.config`.

An unknown key is an error. This catches typing mistakes, but it also means a
config that works with a newer duolog may fail with an older one.

## How to check your config in the editor

The config has a JSON Schema. Put this line first in `.duolog/config.yaml`:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/lorem-dev/duolog/main/duolog-config.schema.json
```

The link works only after the public repository exists. Until then, run
`duolog config schema > schema.json` and point your editor at that file.

## How a role chooses an agent, model and effort

A role names a kind of work. It has these fields:

| Field | Meaning |
|---|---|
| `use` | The agent, model alias and effort that do the work |
| `fallback` | Where the work goes when the `use` agent is unavailable |
| `sandbox` | `read-only` or `workspace-write`. The most the worker may do to files |
| `context` | `minimal` drops the project instructions, the skill list and, for Claude, every `CLAUDE.md`; `project` keeps them |
| `session` | `fresh` starts a new run; `resume` continues a thread |

To see the result for one role, run:

```bash
duolog route review_final
duolog route review_final --json
```

To see every role and its model, run `duolog roster`. Add `--versions` to see
which Codex version each alias resolves to.

## Which role takes a whole feature

Give a whole feature to `planner`. It reads the code, implements, runs the
checks and lists the risks and the hunks to review. It runs on `sol` with
minimal context and keeps its thread, so a follow-up round reuses the
worker's cache. In the cost study, an Opus lead that handed a large feature to
`planner` and did not read the code itself cost about half as much as an Opus
lead that read the code first, at the same test results. See
[the cost study](research/2026-11-01-cost-study.md).

Keep a small fix in the lead. A handoff has a fixed cost: on a two-file bug,
one Codex session alone cost less than any setup with duolog.

## How to give every worker the same rules

Put the project rules that every worker needs in `.duolog/brief.md`: the test
command, the tools to use, the files not to touch. duolog puts this file first
in each worker's input. The same start in every task lets the provider serve it
from its prompt cache. The file must be a regular file of at most 32 KiB; a
link is ignored, because it could point at a secret.

Every worker is also told not to commit, push or ask the user, and to edit a
changelog or docs only if the task lists them under Write.

## Why the config holds only model aliases

The config uses `fable`, `opus`, `sonnet`, `haiku`, `astra`, `sol`, `terra`
and `luna`. Claude Code resolves its own aliases when it runs. For Codex,
duolog picks the newest visible `gpt-<version>-<family>` from
`codex debug models`. A full model name in a role pins that version on
purpose.

The models `fable` and `astra` are for manual use. duolog refuses them unless
you pass `--allow-manual-model`. The list is `limits.manual_only_models`.

## How the lead limits the worker

The lead's own settings are the ceiling:

- A worker never runs above the lead's effort. The scale is `low`, `medium`,
  `high`, `xhigh`, `max`, `ultra`. An unknown lead effort sets no ceiling.
- A worker never gets a wider permission mode than the lead. See
  [Permissions](permissions.md).
- A worker has `DUOLOG_DEPTH=1`, so it cannot delegate again.

`duolog` never passes `--dangerously-bypass-approvals-and-sandbox` or
`bypassPermissions` to an agent.

## What the other sections do

| Section | What it sets |
|---|---|
| `wire` | The bus line limits and the inline size |
| `limits` | Live sessions and the size of a task the lead may do itself |
| `models` | The agent and the prices for each alias. Prices drive `duolog stats` |
| `sessions` | When a resumed thread closes |
| `policy` | The rules for worker actions. See [Permissions](permissions.md) |
| `capabilities` | What to do when the other agent lacks a skill, plugin or server |
| `sync` | How agents and skills are generated. See [Sync](sync.md) |
| `env_passthrough` | The only variables a worker inherits |
| `rex` | Timeouts for live sessions. See [Rex](rex.md) |

## What workers inherit

The default environment includes PATH, HOME and USER, plus
`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `CODEX_HOME` and `CLAUDE_CONFIG_DIR`.
Windows also keeps SYSTEMROOT, USERPROFILE, APPDATA, LOCALAPPDATA, TEMP, TMP,
PATHEXT, COMSPEC and PSMODULEPATH. Without PSMODULEPATH, each PowerShell cmdlet
that loads a module takes 18 to 30 seconds. An explicit `env_passthrough`
replaces these defaults.
The worker always gets `DUOLOG_DEPTH=1`, its `DUOLOG_LEAD` and `DUOLOG_ROLE`.
Codex trust, hooks and capabilities read `CODEX_HOME` when set. Claude hooks,
permissions and capabilities read `CLAUDE_CONFIG_DIR` when set. Home lookups
use the OS home directory, including USERPROFILE on Windows.

A Codex worker runs its commands with `allow_login_shell=false`. A login shell
reruns your profile, which can reset PATH and lose tools such as `node` from
nvm. The worker keeps the PATH that duolog passes.

A Claude worker starts with auto memory off. Claude keeps that memory per
project path, so a worker's notes would reach the lead and every later session
of the project. It also starts with no MCP servers, because the user's servers
added 7.4 thousand tokens to every call in the cost study. A task that names an
`mcp:` or `plugin:` need keeps them. A `minimal` Claude worker skips every
`CLAUDE.md`, the one in your home folder too: Claude loads that file as project
instructions when the project is inside the home folder.

## What a Codex worker's sandbox blocks

A write role runs Codex in `workspace-write`. The cost study saw these limits
with `codex exec` (OBSERVED, Codex 0.165.0 on macOS):

| What fails | Why | What to do |
|---|---|---|
| Tests that use a local database or Redis | The sandbox blocks the network, even localhost | Set `network_access = true` under `[sandbox_workspace_write]` in the project's `.codex/config.toml` |
| `uv run` | Its cache lock is outside the project | Run `.venv/bin/python -m pytest` |
| `npx` | Its cache in `~/.npm` is outside the project | Run `node_modules/.bin/<tool>` |
| `git status` with Git LFS | LFS writes to `.git/lfs/tmp` | Name the check in the task instead |

Put the working forms in `.duolog/brief.md`, so that every worker uses them.

## Related pages

- [Getting started](getting-started.md): install and first task
- [Permissions](permissions.md): policy rules and modes
- [Practices and models](research/2026-10-30-practices-and-models.md): the
  evidence behind the defaults
- [Cost study](research/2026-11-01-cost-study.md): what duolog cost against
  one agent, and why 1.1.0 changed the gate and the roles
