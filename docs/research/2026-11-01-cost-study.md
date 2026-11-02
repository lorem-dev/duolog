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
could reach denied files. Reviews of 1.1.0 by Codex and Opus found more:
`uniq - .duolog/learned.yaml` wrote the learned rules from a read-only role,
`file -m.env` printed a secret, `grep -X x .env` hid the file, `jq -n env`
printed API keys, and `remember` widened `python3.12`, `awk` and `uv run`
commands. 1.1.0 checks the options of `grep`, `jq`, `sort`, `uniq` and `file`,
leaves out `diff` and `yq`, and keeps such commands exact in `remember`.

## Which model to give a worker task

The study also ran worker tasks without duolog, two runs for each cell, on
simple tasks: the cause or the bug sat in one or two files. Cost is the mean
USD per run.

| Task | `gpt-6.1-terra` | `gpt-6.1-sol` | Opus |
|---|---|---|---|
| Write tests (4 mutants to catch) | 0.056, caught 4 of 4 | 0.158, caught 4 of 4 | 0.980, caught 4 of 4 |
| Review a diff with a planted bug | 0.045, found 2 of 2, 1 false remark | 0.092, found 2 of 2 | 0.652, found 2 of 2 |
| Find the cause of a bug | 0.043, right | 0.099, right | 0.449, right |

On four harder bugs, where the symptom was far from the cause, each model ran
at five effort levels (84 runs). A blind grader scored each answer from 0 to 3.

| Effort | `gpt-6.1-terra` | `gpt-6.1-sol` | Opus |
|---|---|---|---|
| low | 2.75, 0.06 USD | 3.0, 0.15 USD | 2.75, 0.23 USD |
| medium | 3.0, 0.06 USD | 3.0, 0.18 USD | 2.88, 0.44 USD |
| high | 2.75, 0.07 USD | 3.0, 0.27 USD | 3.0, 0.52 USD |
| max | 3.0, 0.25 USD | 3.0, 0.35 USD | 3.0, 2.97 USD |

Effort above `high` raised no model's score, and Opus at `max` cost six times
Opus at `high`. No default role asks for `xhigh` or `max`. Only one of the four
bugs told the models apart. `gpt-6.1-sol` made no false claim in any run;
Opus at low effort made three. So the `debug` role, which reads one
repository, runs on `sol` at medium effort. The bugs were still easier than a
real investigation with logs and several services, so `debug_hard` keeps Opus
until such cases are measured.

## The skeleton of a new module

A later round asked each model to lay out a new module with no example in the
repository: once a port of a small Go library to a Python package, once a Go
service skeleton with a CLI, config, health checks, metrics, a linter, a
Makefile, a Dockerfile and CI jobs. Each run got a brief: the file layout, the
allowed libraries, a list of what not to build, and the open points to ask
about. Each model ran once at low, medium and high effort (18 runs). A blind
grader used a rubric of five criteria and a total from 0 to 3.

| Task, model | USD | Lines | Total | Decided silently | Questions raised |
|---|---|---|---|---|---|
| Port, `gpt-6.1-terra` | 0.08 | 334 | 2.33 | 2.0 | 5.0 |
| Port, `gpt-6.1-sol` | 0.42 | 706 | 2.67 | 2.3 | 5.3 |
| Port, Opus | 1.52 | 713 | 3.00 | 0 | 10.7 |
| Skeleton, `gpt-6.1-terra` | 0.09 | 734 | 3.00 | 1.3 | 3.7 |
| Skeleton, `gpt-6.1-sol` | 0.26 | 1025 | 3.00 | 2.0 | 4.3 |
| Skeleton, Opus | 1.04 | 1079 | 3.00 | 0.7 | 10.0 |

With the brief, no model bloated the skeleton: 330 to 1180 lines a run. An
earlier skeleton of the same kind, written by Opus from a plan with no such
brief, had 8.5 thousand lines and 25 additions nobody asked for. The extra
work came from the plan, which the worker then followed. The models differ
in what they decide without asking: Opus listed 10 to 12 open points and
decided almost nothing alone, Codex decided one to three of them silently.
In the skeleton task `sol` and Opus each broke a rule of the brief. A reviewer
needed 15 to 25 minutes a run, more than any model cost. Effort did not change
the mean total.

So keep the plan and the brief with the lead, write the list of what not to
build into the brief, and ask a Codex worker to list the decisions it made.

## What the study did not show

- Each setup ran once per task. Runs of one task can differ up to 30 times in
  tokens (Bai et al. 2026, arxiv.org/html/2604.22750), so 3 to 5 repeats are
  needed before these numbers are averages.
- The worker tasks ran once or twice per cell. The review and debug results
  rest on a few runs each, and the hard bugs could be found by reading one
  repository.

## Related pages

- [Budgets](../budgets.md): the cost targets
- [Configuration](../configuration.md): the `planner` role and the brief
- [Permissions](../permissions.md): how the gate reads commands
- [Practices and models](2026-10-30-practices-and-models.md): the evidence
  behind the default models
