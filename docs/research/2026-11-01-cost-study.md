# Cost study on 2026-11-01

This page records a study that compared sessions with and without duolog on
five tasks from one private project, and the changes in 1.1.0 that follow
from it. One participant ran it on one macOS machine with Claude Code 2.1.312
to 2.1.316, codex-cli 0.165.0 and duolog 1.0.0. Each setup ran once per task,
so every number is OBSERVED, not an average.

## What the study found

One Codex session alone was the cheapest setup on all five tasks. By tests and
by a blind review, its result was not worse than the others on four of them.

An Opus lead with duolog 1.0.0 defaults cost about as much as one Opus session:
4.96 against 5.40 USD on tasks 1 to 3. The lead read the code itself, checked
the workers' work again and spent many turns on duolog itself.

An Opus lead that only handed out work, with a `planner` worker on Codex, cost
about half as much as the default Opus lead on the two large tasks: 1.36
against 2.72 USD and 1.49 against 3.23 USD, at the same hidden test results.
On task 4 it got the best blind review.

Most workers that stopped early stopped on the gate, and most of those stops
came from how the gate read the command, not from a policy rule.

## The tasks and the setups

The project is a web service with a Python backend and a React frontend. Its
main author wrote the tasks. Each task has the hidden tests of the real commit.

| Task | Kind | Size of the real commit |
|---|---|---|
| 1 | Small frontend bug | 2 files |
| 2 | Backend bug across several layers | 7 files |
| 3 | Backend feature with a new setting | 9 files |
| 4 | Large backend feature | About 20 files |
| 5 | Large full-stack feature | About 20 files |

| Setup | What ran |
|---|---|
| A | One Opus session |
| B | One Codex session, `gpt-6.1-sol` |
| CX | A Codex lead with duolog |
| CL | An Opus lead with duolog |
| CLp | An Opus lead that only hands out work, with a `planner` worker |

## What each setup cost

Estimated USD per run, from the agents' own logs. A dash means the setup did
not run on that task.

| Task | B | CX | CLp | CL | A |
|---|---|---|---|---|---|
| 1 | 0.35 | 0.59 | - | 1.25 | 1.59 |
| 2 | 0.73 | 0.57 | - | 1.21 | 1.23 |
| 3 | 0.52 | 0.77 | - | 2.50 | 2.58 |
| 4 | 0.81 | 0.94 | 1.36 to 1.61 | 2.72 | - |
| 5 | 1.32 | 1.73 | 1.49 to 1.78 | 3.23 | - |

Every setup in the table passed the hidden tests on tasks 2 to 4. On tasks 1
and 5, every setup failed the same hidden test, which checks one detail of the
real commit rather than the behavior. For task 1 the study added a neutral
behavior test, which every setup passed.

A blind reviewer saw the diffs without their setup names. On task 3, one Opus
was best, and B and CX had small defects. On task 4, CLp was best. A variant
of CLp with `gpt-6.1-terra` as the implementer failed 3 of 79 tests and got a
"rework" verdict.

## Why duolog cost more

| Finding | Evidence |
|---|---|
| The lead read the code before it handed out the work | 60 to 110 thousand characters of code in the lead's context |
| duolog upkeep filled the lead's context | 14 to 52 thousand characters of answers, decisions, diffs and help pages |
| Read-only commands stopped workers | 5 of 14 worker calls denied on task 1: quoted patterns, `rg -g`, `sed -n` |
| A denial did not name the command | The lead read `--help`, `mode` and the config schema to find it |
| A Codex lead's workers could not write | `workspace-write` with `on-request` counted as `ask` |
| Workers asked for a ticket number or tried to commit | Project rules reached the workers, not only the lead |
| Install backups landed in the work tree | `git add -A` added 843 lines of `.duolog.bak` files |
| A fixed short timeout made a runner test fail | 15 of 31 runs on the study's machine |

## What 1.1.0 changes

| Change | Why |
|---|---|
| Quotes that only group one word read as that word | Agents quote patterns by habit |
| `rg` and `grep` option values are not paths; `sed -n 'N,Mp'` is a read | These were false asks |
| More read commands: `jq`, `sort`, `stat`, `tree` and others, with option checks | Agents use them to read |
| Check tools of each ecosystem allowed for write roles | Workers stopped on `ruff`, `eslint` and `tsc` |
| A denial names the command, and the worker retries once with a plainer form | A stopped worker costs a full restart |
| `duolog remember` saves a command the user allowed | The same ask came back in every run |
| A `planner` role for whole features | The dispatcher setup cost about half |
| `.duolog/brief.md` leads each worker's input | One place for project rules, cached by the provider |
| Every worker is told not to commit or ask the user | Those steps belong to the lead |
| A Codex lead in `workspace-write` gives `edits` | Codex itself edits there without asking |
| Install backups go under `.duolog/backup/` | Git ignores that folder |

A review of the study's first patch by two agents found eight ways around the
gate in it, and the patch fixed them. Our own review then found more in the new
read commands: `sort -no file` and `sort --compress-program` wrote and ran
programs, `jq -f` could print a secret in its error, and `diff -r` and `yq`
could reach denied files. 1.1.0 checks the options of `jq` and `sort` and
leaves out `diff` and `yq`.

## What the study did not show

- Each setup ran once per task. Runs of one task can differ up to 30 times in
  tokens (Bai et al. 2026, arxiv.org/html/2604.22750), so 3 to 5 repeats are
  needed before these numbers are averages.
- On simple worker tasks (tests, review, finding a cause), `gpt-6.1-terra`
  matched Opus at a tenth of the price or less. On four harder bugs,
  `gpt-6.1-sol` found the right cause at every effort level. These bugs were
  still easier than a real investigation with logs and several services.
- With every MCP server on, each Claude model call carried 7.4 thousand more
  tokens. duolog does not yet start workers with only the servers they need.

## Related pages

- [Budgets](../budgets.md): the cost targets
- [Configuration](../configuration.md): the `planner` role and the brief
- [Permissions](../permissions.md): how the gate reads commands
- [Practices and models](2026-10-30-practices-and-models.md): the evidence
  behind the default models
