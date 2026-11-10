# What duolog terms mean

Definitions for duolog and the
[hand-over study](research/2026-11-10-hand-over-study.md).

## What the parts of duolog mean

| Word | What it means |
|---|---|
| <a id="agent"></a>Agent | Claude Code or Codex |
| <a id="session"></a>Session | One running conversation with an agent |
| <a id="lead"></a>Lead | The session you work in. It plans the work, hands it over and checks the result |
| <a id="worker"></a>Worker | A session that the lead started for one task. It works alone and returns a summary and a diff |
| <a id="role"></a>Role | A kind of work, such as `implement` or `review_final`. Each role has its own agent and model |
| <a id="route"></a>Route | The agent, model and effort that do a role |
| <a id="delegate"></a>Delegate | `duolog delegate <role>`: start a worker for a task and wait for its answer |
| <a id="planner"></a>Planner | The role for a whole feature. It reads the code, implements, runs the checks and lists the risks. It runs on Codex by default |
| <a id="peer"></a>Peer | A second live session in a Rex pane, opened with `duolog peer open <role>` when you ask for one |
| <a id="subagent"></a>Subagent | A helper that the lead's own agent starts, without duolog |
| <a id="run-here"></a>Run here | duolog's answer when a task is too small for a worker: the lead does it, or its own subagent |
| <a id="gate"></a>Gate | The duolog hook that checks every worker command against the project rules |
| <a id="bus"></a>Bus | The file that sessions use to send short messages to each other |
| <a id="task"></a>Task | A work request in a `task.md` file, with Goal, Read, Write, Limits and Check |
| <a id="worktree"></a>Worktree | A second Git checkout of the same repository, so that a worker does not touch your files |

## Which setups the hand-over study compares

Opus and Sonnet are Claude models; sol is a Codex model.

| Name | Lead | Who does the work |
|---|---|---|
| <a id="setup-a"></a>A | Opus | The lead itself, without duolog |
| <a id="setup-b"></a>B | Codex | The lead itself, without duolog |
| <a id="setup-clp"></a>CLp | Opus | A [planner](#planner) on Codex, through [delegate](#delegate) |
| <a id="setup-cls"></a>CLs | Opus | A [planner](#planner) set to Sonnet |
| <a id="setup-xd"></a>XD | Opus | A [worker](#worker) through [delegate](#delegate) |
| <a id="setup-xds"></a>XDS | Sonnet | A [worker](#worker) through [delegate](#delegate) |
| <a id="setup-xm"></a>XM | Opus | The lead itself, after it read the code around the task |
| <a id="setup-xs"></a>XS | Opus | One Sonnet [subagent](#subagent), after the lead read the code |
| <a id="setup-gs"></a>GS | Opus | One Sonnet [subagent](#subagent), without duolog |
| <a id="setup-p"></a>P | Opus | A live [peer](#peer), driven with duolog commands |
| <a id="setup-m"></a>M | Opus | A live [peer](#peer), driven with the duolog MCP tools |

## Which tasks the hand-over study uses

Each task is a real commit checked by hidden tests.

| Name | Size | What the task is |
|---|---|---|
| <a id="task-g1"></a>g1 | Small, 2 files | Give the install script renderer the right output file name. From this repository |
| <a id="task-g2"></a>g2 | Medium | Find the lead from a linked worktree of a Git submodule. From this repository |
| <a id="task-g3"></a>g3 | Large, many files | Help a Claude lead remember its open workers when it compacts its context: a new setting, a hook, the schema and the docs. From this repository |
| <a id="task-t1"></a>t1 | Small, 2 files | Fix a line-ending bug in schema files, in one private Python project |
| <a id="task-t2"></a>t2 | Medium, 8 files | A change across 8 files of the same private project |

## Related pages

- [Getting started](getting-started.md): install, set up and send a task
- [Hand-over study](research/2026-11-10-hand-over-study.md): what each setup
  cost
- [How to write the documentation](contributing.md): the approved words
