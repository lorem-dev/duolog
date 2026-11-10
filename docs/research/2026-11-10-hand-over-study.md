# What handing work over cost in November 2026

Costs of doing work in the lead, using a subagent, delegating to a
worker/planner, or opening a live peer. Setup and task names are in the
[glossary](../glossary.md#which-setups-the-hand-over-study-compares).

We ran 65 sessions on five tasks from 2026-11-08 to 2026-11-10, on one macOS
machine, with Claude Code 2.1.319 to 2.1.321, duolog 1.6.0 and a build
between 1.6.0 and 1.7.0. Each setup ran twice on each task. Every number is
OBSERVED: the median of two runs, not a statistic.

## What the study found

| When | Do this | Why |
|---|---|---|
| A whole feature, medium or large | Give it to the planner on Codex ([CLp](../glossary.md#setup-clp)) | It cost 38% less on [g2](../glossary.md#task-g2) and 45% less on [g3](../glossary.md#task-g3) than one Opus session, with the same test results |
| A small task, and the lead already read the code | Do it in the lead ([XM](../glossary.md#setup-xm)) or give it to a Sonnet subagent ([XS](../glossary.md#setup-xs)) | On [t1](../glossary.md#task-t1) both cost about 0.58 USD; a worker cost 1.66 USD |
| You want a cheap Claude lead | Use a Sonnet lead with `duolog delegate` ([XDS](../glossary.md#setup-xds)) | It cost 39% less on [g1](../glossary.md#task-g1) and 57% less on g3; one grader rated its g3 result lower |
| A medium or large task for a subagent ([GS](../glossary.md#setup-gs)) | Do not expect savings | It cost 14% more on g2 and 8% less on g3, and took longer |
| A planner on Sonnet ([CLs](../glossary.md#setup-cls)) | Do not use it | It cost 22% more on g2 and 24% more on g3 |
| Live peers ([P](../glossary.md#setup-p), [M](../glossary.md#setup-m)) | Open them only when you need one | On g2 they cost 3 times as much and took 3 times as long as one Opus session |
| The lowest price of all ([B](../glossary.md#setup-b)) | One Codex session | 0.14, 0.47 and 1.10 USD on g1 to g3, but it stopped for the person most often |

Each lead turn cost about 0.04 USD (correlation 0.92); savings came when
another session read the code. All runs passed hidden tests in substance.
Codex and Opus blind graders disagreed more than setups differed, so this
study does not rank quality.

## What each setup cost

Median USD and minutes of two runs. A dash means the setup did not run on
that task. [A](../glossary.md#setup-a) is one Opus session, the baseline.

| Setup | g1 | g2 | g3 | t1 | [t2](../glossary.md#task-t2) |
|---|---|---|---|---|---|
| [A](../glossary.md#setup-a) | 0.57, 3 | 1.25, 7.5 | 2.72, 11.8 | 0.59, 8.6 | 2.12, 15.2 |
| [B](../glossary.md#setup-b) | 0.14, 3.2 | 0.47, 7.6 | 1.10, 55 | - | - |
| [CLp](../glossary.md#setup-clp) | - | 0.78, 8.3 | 1.49, 14.9 | - | - |
| [CLs](../glossary.md#setup-cls) | - | 1.53, 14.6 | 3.38, 31.1 | - | - |
| [XDS](../glossary.md#setup-xds) | 0.35, 4.4 | - | 1.17, 13.1 | - | - |
| [XD](../glossary.md#setup-xd) | 0.84, 5.5 | - | 2.16, 13.6 | 1.66, 8.8 | 2.56, 16.2 |
| [GS](../glossary.md#setup-gs), [XS](../glossary.md#setup-xs) | - | 1.43, 16.9 | 2.50, 20.1 | 0.57, 6.7 | 1.27, 8.0 |
| [P](../glossary.md#setup-p) | 1.11, 10.2 | 3.78, 24 | 2.42, 29.3 | - | - |
| [M](../glossary.md#setup-m) | 1.30, 15.3 | 2.94, 34.1 | 2.40, 24.9 | - | - |

On t1 and t2 the lead first read the code around the task, and the cost
counts from the moment the task arrived. In the A row, t1 and t2 show
[XM](../glossary.md#setup-xm), the same Opus lead with the code already read.
XD on g3 shows only the second run: in the first, the Codex worker stopped on
a [gate](../glossary.md#gate) refusal, and an Opus worker did the work for
3.95 USD.

## What stopped the workers

- A Codex worker could not read its own skill when the skill folder was a
  link into the agent's config folder. Every delegated Codex worker stopped
  on this first. Fixed after 1.7.0.
- The gate stopped the code generators of this repository, so a planner
  could not finish g3 alone. The `Run` section of a
  [task](../glossary.md#task) and a project rule for the generators fix this.
- The gate read a quoted `rg` pattern and a `go test -run` value as shell
  syntax. Fixed in 1.7.0.
- A live peer left its result on its own branch, and its permission requests
  ran out of time while the lead waited. So peers stay opt-in.

## What the study did not show

- Two runs per setup show a direction, not how much results vary.
- The graders were two models, not people.
- In g3 with a live peer, the lead ran an older duolog from the task's own
  checkout. Those runs are not in the tables.
- Reading the code first costs money too: 0.5 to 0.9 USD without duolog and
  0.9 to 1.5 USD with it.

## Related pages

- [Glossary](../glossary.md): the setups, the tasks and the duolog words
- [Budgets](../budgets.md): cost targets and what we measured
- [Cost study](2026-11-01-cost-study.md): the earlier study on one private
  project
- [Configuration](../configuration.md): which role takes which work
