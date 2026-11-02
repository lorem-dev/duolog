# How learning mode tunes routing for a project

Learning mode uses local task results to propose which agent, model and effort
should do each kind of work. It is off by default. The lead shows a proposed
change to the person before applying it.

## How to turn it on

Add this to `.duolog/config.yaml`:

```yaml
learning:
  enabled: true
```

A lead is the session you work in. Its SessionStart hook notes when learning
mode is on. Before a task, the lead runs:

```bash
duolog task start fix login
```

The command opens a task and prints the protocol: features, classes, steps and
escalation signals. With several leads, pass `--lead <id>` from SessionStart.
A worker cannot open or finish tasks, record outcomes or apply learned orders.

## How a delegation gets a class

A class describes the kind of work. It picks a target from an ordered list
called a ladder. The role still controls the worker's access and context.

Give `duolog delegate` the features you know, separated by commas. Start with
`output` so that the rule can identify the result you need:

```bash
duolog delegate docs --task .duolog/task.md --features output=docs,exemplar=yes
```

| Feature | Values |
|---|---|
| `output` | `code`, `plan`, `review`, `tests`, `docs`, `diagnosis` |
| `exemplar` | `yes`, `partly`, `no`: whether similar code exists |
| `components` | `1`, `2+` |
| `outside_evidence` | `yes`, `no` |
| `scope` | `1-3`, `4-20`, `20+`: expected files touched |
| `ambiguity` | `low`, `high` |
| `target` | `code`, `plan`: what a review checks |

Config rules match the features and role in order. The first match wins.
Use `--class <name>` to name a class directly. If no class or rule matches,
the role uses its own target and the journal records `unclassified`.

If the person names a target, pass it with `--use agent/model/effort`. Ask if
their choice is unclear. These runs count in the statistics, but receive no
profiling offer. A subagent of the lead's own vendor uses the agent's own
tools when the person asks for one.

For new structure, use three delegations with `--class skeleton`: `plan`,
then `review_final` of the plan, then `implement_complex`.

To leave a failed run, pass `--escalate-from <id> --reason <signal>`. The
result line supplies the delegation ID. Signals are `no_repro`, `no_lines`,
`inconsistent`, `not_green` and `out_of_turns`. A provider limit is not a
quality failure. Changing the person's explicit target needs their agreement.

## What the journal keeps and where

The local journal lives under `.duolog/journal/`:

| File or folder | What it keeps |
|---|---|
| `delegations.ndjson` | Target, class, reason, usage, cost, time and result per delegation |
| `tasks.ndjson` | Finished tasks and the person's outcomes |
| `verdicts.ndjson` | Comparison scores and the person's decisions |
| `declined.ndjson` | Names of class and rule proposals the person declined |
| `tasks/` | Local task files and patches for profiling |

Subagents run with the agent's own tools are not journalled: duolog cannot
see them. A delegation made through duolog is recorded even when the person
chose its target.

Run `duolog stats --classes` to see the observed quality, failures, cost and
time for each ladder entry. The plain `duolog stats` command still reports
token costs.

## What happens at the end of a task

Run `duolog task finish`. The lead shows the output to the user in its printed
order: a table of delegations, at most two profiling offers, any routing-change
notice, and the outcome question.

Each offer states its target, reason and estimated cost, including the judge.
The person chooses one, both or none. There are no offers for replays,
provider limits, tasks returned to the lead, or explicit person choices.

Record the person's outcome for the finished task:

```bash
duolog task outcome <task-id> accepted
```

The other outcomes are `small_fixes`, `reworked` and `dropped`. Add `--note`
for a short explanation. A later outcome replaces the earlier one when the
journal is read.

## How a profiling run compares two targets

A profiling run repeats the recorded work on another target from the same
starting tree. After the person chooses an offer, run:

```bash
duolog profile <delegation-id> --entry codex/sol/high
```

The entry names the agent, model and effort. Add `--chain` to replay later
steps of the same task and class. The chain stops at the next class. Only
the first step changes target; later steps use the targets that ran before.
Replays and provider limits do not count as chain steps.

The command uses a separate Git worktree, a checkout that shares Git history.
It rebuilds the recorded starting tree from the base commit and saved patch.
The main checkout and its staged changes stay as they were. A record without
a base commit or saved starting patch cannot be replayed.

The command runs the class's `checks` commands on both results. Commands use
spaces to separate arguments; shell operators and quoting have no special
meaning. A missing program has exit code `-1` and does not stop the comparison.

The worktree uses the fixed path `.duolog/profile/worktree` under the shared
state root. The command removes it on success, failure or interruption. It
cleans a stale worktree before the next run. If Codex refuses untrusted hooks,
the error names the path. Open `/hooks` there once to trust the hooks, then
run the command again.

Each run saves `side.patch` and `answer.json` in a new `profile-<n>` folder
inside `.duolog/journal/tasks/<task-id>/<delegation-id>/`. Pass `--lead <id>`
when several leads are active. Use `--allow-manual-model` for a deliberate
model version pin that is absent from the live model list. Workers cannot
run profiling commands.

A judge compares the results using the class's rubric. It sees the recorded
task, patches and answers under neutral labels A and B. It does not receive
the entry names or learn which side is the original. The label order is
random. The judge uses a vendor absent from both sides when possible.
Otherwise it uses the first configured judge in `learning.judges`.

The judge scores each criterion from 0 to 3. Criteria marked `(count)` also
require a count for each side. Its task and inputs stay in
`.duolog/judge/<verdict-id>/` under the current work root. An invalid answer
creates no verdict. The error gives the saved answer path for inspection.

The command prints the scores and reasons in original/profile order, the
check totals, and separate replay and judge costs. It then asks the lead to
show the result to the person. After they confirm the scores, record it:

```bash
duolog verdict <verdict-id> confirm
```

To correct scores, quote each criterion and give its original/profile scores:

```bash
duolog verdict <verdict-id> correct "structure matches what was asked=1/3"
```

Each correction needs at least one known criterion with scores from 0 to 3.
Criteria left out keep the judge's scores. Add `--note` for a short explanation.
Workers cannot record verdict decisions. The latest decision replaces earlier
decisions for that verdict; it applies to the saved judge scores.

The verdict and decision go to `.duolog/journal/verdicts.ndjson`. A comparison
affects quality estimates only after the person confirms or corrects it.
The judge call does not count as another task delegation.

## How duolog learn changes the order

Run `duolog learn` to see a proposed order as a diff, with its evidence.
Quality-first classes prefer the best quality estimate, allowing a cheaper
entry close to it. Cost-first classes prefer the cheapest entry that clears
the quality threshold after accounting for uncertainty.

Starting quality estimates come from configured priors. Confirmed or corrected
comparisons count at full weight. Task outcomes and failures count at half
weight. Provider limits and work returned to the lead add no observations.
A winning profile can add a target outside the configured ladder.

After the person approves the diff, run:

```bash
duolog learn --apply
```

This writes `.duolog/routing.learned.yaml` only if an order changed. Changed
statistics alone do not cause a write. Class definitions stay in
`.duolog/config.yaml`; learned orders and numbers stay in the generated file.

## How duolog proposes a class or classification rule

`duolog learn` shows class proposals after the learned-order report. Each
proposal includes evidence, a config diff, and commands to apply or decline it.
`duolog task finish` mentions when a proposal is ready, before asking for the
task outcome.

Five unclassified delegations with the same role and full feature set trigger
a new class proposal. Features may be empty. The class name combines the role
and output, such as `review_final_review`. Without an output, the suffix is
`misc`, such as `review_final_misc`.

The new class uses cost-first selection and the role's preferred and fallback
targets. Its rubric checks correctness against the tests and brief, and false
claims. A classification rule selects the new class for that role and feature
set.

Three delegations with the same stated class, rule class and feature set
trigger a rule proposal when those classes differ. Its name is `rule:<class>`.
It changes classification to the stated class without adding a new class.
Replays and provider limits do not count toward either threshold.

Several feature groups can produce the same proposal name. Duolog offers the
largest group first and breaks ties by the sorted feature set. The apply and
decline flags act on the group currently offered under that name. Other groups
remain available on later runs. Once the class exists, later groups add only
classification rules for it.

After the person approves a proposal, apply its shown name:

```bash
duolog learn --apply-class review_final_misc
```

This edits `.duolog/config.yaml` and preserves its comments. It keeps the full
effective classification list because YAML lists replace lower layers. The
new rule goes before the first matching rule. A failed config load restores
the old file. A later config layer that hides the new rule also makes the
application fail and restores the old file.

To stop offering a proposal, record the person's decision:

```bash
duolog learn --decline-class review_final_misc
```

Declines stay in `.duolog/journal/declined.ndjson`, with the proposal name and
the group it identifies. New observations do not revive that declined group.
Declines do not change the learned routing file. Workers can read proposals
but cannot apply or decline them. Plain `duolog learn` leaves the project's
config unchanged.

## How to go back to the defaults

Delete `.duolog/routing.learned.yaml` to restore the configured ladders.
The journal remains available for future proposals. Set `learning.enabled`
to `false` to turn learning mode off.

## Related pages

- [Configuration](configuration.md): learning settings and project overrides
- [Getting started](getting-started.md): install duolog and delegate a task
- [Permissions](permissions.md): the worker's access and permission requests
