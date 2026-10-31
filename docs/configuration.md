# How to configure duolog

This page explains the config file: where duolog reads it, what each section
does, and how to check a role. The defaults are in
[internal/config/config.default.yaml](../internal/config/config.default.yaml).

## Where duolog reads the config

duolog merges four layers. A later layer changes only the fields it names.

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
| `context` | `minimal` drops the project instructions; `project` keeps them |
| `session` | `fresh` starts a new run; `resume` continues a thread |

To see the result for one role, run:

```bash
duolog route review_final
duolog route review_final --json
```

To see every role and its model, run `duolog roster`. Add `--versions` to see
which Codex version each alias resolves to.

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
| `wire` | The language of tasks, the bus line limits and the inline size |
| `limits` | Live sessions, depth, and the size of a task the lead may do itself |
| `models` | The agent and the prices for each alias. Prices drive `duolog stats` |
| `sessions` | When a resumed thread closes |
| `policy` | The rules for worker actions. See [Permissions](permissions.md) |
| `capabilities` | What to do when the other agent lacks a skill, plugin or server |
| `sync` | How agents and skills are generated. See [Sync](sync.md) |
| `env_passthrough` | The only variables a worker inherits |
| `rex` | Timeouts for live sessions. See [Rex](rex.md) |

## Related pages

- [Getting started](getting-started.md): install and first task
- [Permissions](permissions.md): policy rules and modes
- [Practices and models](research/2026-10-30-practices-and-models.md): the
  evidence behind the defaults
