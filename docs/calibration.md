# How calibration gives a project a starting model order

Calibration replays a project's past work on several agents, models and
efforts, judges the results, and proposes the order per class that learning
mode starts from. A class is a kind of work, such as `docs` or `review`. It
runs only when you ask the lead for it. No hook, doctor check or skill
suggests it. Neither agent can start the `duolog-calibrate` skill on its own.

## What calibration does and when it runs

Calibration finds the project's checks and representative past tasks. It runs
those tasks on the ladder entries of each class and proposes an order from the
results. Learning mode then keeps refining the order on real tasks.

The proposed order routes tasks only while learning mode is on. Learning mode
is off by default. Turn it on with `learning.enabled: true` in
`.duolog/config.yaml` before you apply the order; see
[Learning mode](learning.md#how-to-turn-it-on). With learning mode off, an
applied order changes no route, and `duolog calibrate propose` and
`duolog learn` say so in one line.

Ask the lead in your own words, for example "calibrate duolog for this
project". The bundled `duolog-calibrate` skill then runs the steps below with
`duolog calibrate` commands. A worker that tries gets the error
`workers cannot run duolog calibrate`.

Calibration needs Git 2.40 or newer. Task statements, trees and patches go to
the model providers of the ladder entries and the judges, as in any
delegation.

## What the checks can do on your machine

Survey, mining and every cell run the project's own check commands, such as
`go test ./...`. duolog runs these commands itself, outside the worker gate.
They run as your user, with duolog's environment, and the credentials in it.
Survey and mining run them before any model call and before you approve a
cost.

A check runs code from the project's history. It can read your files, use the
network and change files outside the exported tree. duolog does not install
dependencies, but a check may do that itself. Run calibration only on a
history and checks that you trust.

## Where the two parts of a profile live

A profile has a project part, which you can share in Git, and a personal part,
which stays on your machine.

| Part | What it holds | Where | In Git |
|---|---|---|---|
| Project | Survey facts, classes and their checks, the task list (source refs and statements, with fenced code and detected author data removed), and per class the results by family and effort | `.duolog/calibration/project.yaml`; classes and checks go to `.duolog/config.yaml` | After you review and commit it |
| Personal | Runs, cells, patches, judge verdicts and your confirmations, token and time figures, cost estimates, quota readings and plan approvals, measured quota rates, check results and dropped tasks; the order you applied | `.duolog/calibration/runs/<run-id>/`, `.duolog/calibration/rates.json`, `.duolog/journal/calibration.ndjson` and `.duolog/routing.learned.yaml` | No |

`.duolog/` lives in the main checkout, so every linked worktree sees the
personal part without a commit. The project part never holds costs, quota
figures, account facts or names. duolog writes it only when you approve its
diff at the end. It refuses the whole file if any field holds an email
address, an author trailer, a cost or quota figure, or the name of a person
from the history. It also refuses a row whose `duolog_version` is not a
version such as `1.3.0`.

Bot and service identities, such as a name ending in `[bot]` or a `noreply@`
address of the service itself, are not people and are not on that name list.
Outside the statements, only names of two or more words count, so a committer
called `ci` or `test` does not refuse a command or a path.

These checks do not find every private detail. Indented or inline code,
private identifiers and people who are not in the Git history can remain.
Review the full project-part diff before you share it.

`duolog calibrate propose --apply-project` writes the file; it does not add or
commit it. Re-run `duolog install` after an upgrade, so that duolog's
`.duolog/.gitignore` lets Git see the file. If Git still leaves it out, run
`git check-ignore -v .duolog/calibration/project.yaml` to find the parent
ignore rule.

Cell trees live in `<user cache dir>/duolog/calibration/<project-id>/tree-<n>`,
and the judge folder sits beside them. `duolog calibrate plan` prints both
paths. Survey and mining export to temporary folders. Saved runs, with their
patches, statements and verdicts, have no automatic retention limit. Stop a
run before you remove its folder. Removing a run folder does not remove its
journal evidence or the order you applied; see
[Learning mode](learning.md#how-to-go-back-to-the-defaults).

Calibration always works on the main checkout. Run from a linked worktree,
`duolog calibrate` still surveys and mines the main checkout's HEAD and
reads its working copy for the Codex hooks; the worktree's branch is not
used. To calibrate on a branch, check it out in the main checkout first.

## The steps, and where the lead stops

Each step is one command. The lead shows its output and stops where the table
says, so you decide before anything costs more.

| Step | Command | What it does | Lead stops for |
|---|---|---|---|
| 1 | `duolog calibrate survey` | Reads manifests, CI files and history, times one check run on HEAD, and proposes checks per class. No model calls | |
| 2 | `duolog calibrate classes` | Shows the config diff for classes and checks | Your approval or edits; then `classes --apply`, with `--skip <name>` for each part you leave out |
| 3 | `duolog calibrate mine` | Finds tasks in local history and documents, checks them and samples them per class. No model calls | |
| 4 | `duolog calibrate plan` | Shows the costed plan per class | One approval: `duolog calibrate plan --approve <hash>` |
| 5 | `duolog calibrate run` | Runs the cells in order, one line per cell | A pause or stop, which it reports |
| 6 | `duolog calibrate judge` | Judges pairs that both passed the checks, blind | Your confirmation or correction of every verdict |
| 7 | `duolog calibrate propose` | Shows the order per class and the project-part diff | `duolog learn --apply` and `duolog calibrate propose --apply-project`, each only when you say so |

`duolog calibrate survey --new` starts a new run even when an unfinished one
exists. `duolog calibrate judge --class <name>` judges and shows one class.
With several live leads, pass `--lead <id>` to `run`, `resume` and `judge`.

### How mining picks tasks

With a Git remote, mining also asks `gh` for up to 200 merged pull requests
and reads the issues they link. It reports a failed GitHub read and skips it.

Mining prefers to take at most a third of a class's tasks from one top-level
directory. A class needs 3 tasks in its first round. When that rule leaves
fewer, mining looks further back in history. Some projects keep all their
code in fewer than 3 top-level directories, such as `src/`. There the first
round takes the tasks from the other directories first, and fills up from the
one the rule held back. A class with fewer than 3 usable tasks is skipped
with `fewer than 3 tasks`.

Mining checks each candidate by running the checks on its parent and its
change. It checks at most 10 candidates per place in a class's rounds and
reserve: 60 in a small project, 90 in a larger one. It prints this bound
first, and gives the rest the reason `validation budget reached`.

Mining removes fenced code blocks from every statement, whether it comes
from a plan, a design document, an issue or a pull request. Code indented by
four spaces is not a fence and stays. A statement needs a rewrite in the run
when fewer than 30 words remain, or less than half of its words. It also
needs one when it omits a function or type that the hidden tests call.
Statements from commit messages always need a rewrite. Only the names are
checked, not their signatures: a statement can name a function the tests call
and still omit the arguments or return type they expect.

### How a cell runs

A cell is one task run on one ladder entry. Each cell works in a fresh tree
exported from the task's parent commit. The tree holds the files as Git stores
them, so `export-ignore` and `export-subst` in `.gitattributes` change nothing.
The tree folder stays the same from run to run.

A cell runs only when the duolog gate is active for its agent in the tree.
For Claude, duolog writes the gate hooks into the tree's
`.claude/settings.local.json` when your settings and the plugin do not run
the gate there already. A Claude cell is refused when `disableAllHooks` is
set in the commit's `.claude/settings.json`, in the tree's local settings or
in your user settings, since Claude then runs no hook at all. For Codex, you
trust the tree folder in Codex once and approve its hooks:

1. Run `duolog calibrate plan`. When the plan holds a Codex entry, it names
   the folder and puts the project's `.codex/hooks.json` in it.
2. Open Codex in that folder, trust it, and approve its hooks in `/hooks`.

duolog keeps that hooks file in the folder between cells and runs, so the
approval holds. It never edits the Codex config. The run readies the tree and
checks its isolation and trust before it admits and charges the cell. If the
folder or its hooks are not trusted, the run stops with a message that names
the folder, and the cell is not charged. Approve them there, then run
`duolog calibrate resume`.

These limits end a cell:

| Limit | Default | What happens |
|---|---|---|
| Time cap, `calibration.cell_max_minutes` | `45` | The worker stops; the cell is done with reason `time cap` and no checks |
| Turn cap, `calibration.cell_max_turns` | `100` | A Claude worker gets it as `--max-turns` and stops there; the cell is `failed_limit` with reason `turn cap` and no checks, so it gives no result, and the run goes on. Codex has no such option, so for Codex the cap is advice in the brief and the time cap is the hard stop |
| Check time cap | the time cap | One run of the class checks stops with every process it started; the cell fails with reason `check time cap` |

`calibration.cell_max_tokens` defaults to `1500000`. Mining leaves out a task
whose high token estimate is above it. The runners report usage when a worker
ends, so a cell whose reported usage is above the cap is `failed_limit` with
reason `token cap` and no checks: it records its usage, gives no result, and
the run goes on with the next cell.

The checks run on the candidate's tree, its dependency manifests and lock
files included. duolog puts back every test the candidate deleted, unless the
reference deletes it too, and names it under `restored` in the cell's
`result.json`. Files Git ignores, such as `node_modules/` or a virtualenv,
are left alone.

When the candidate changes a file that may define how a check runs, the cell
is `check_setup_changed`: its checks are no pass and no failure, and it is
not judged. That covers any `Makefile` or `.mk` file, the `package.json`
scripts block and its `jest` or `vitest` settings, the `tool` sections of
`pyproject.toml`, the pytest section of `setup.cfg`, `pytest.ini`, `tox.ini`,
`conftest.py`, jest and vitest config files, CI configuration, files under
`scripts/` and any file a check command names. They count whether or not a
check reaches them: duolog does not follow which target or script a check
calls through another file. The changed files are named under
`setup_changed` in `result.json`. The reference's own changes make no
exception.

The survey and mining stop a check run at the same check time cap. The
survey drops the command that runs past it and says so. A command after it
is `not run`: the survey neither uses it nor counts it as failing, and the
next `duolog calibrate survey` tries it again. Mining leaves out a task whose
checks run past the cap, with the reason `check time cap`.

Judge calls and statement rewrites work in one more folder, the judge folder
beside the trees. It holds only the files of one call, a Git repository made
afresh for that call, the project's Codex hooks and, for Claude, the gate as a
tree gets it. When a Codex judge or rewrite is in the plan,
`duolog calibrate plan` puts the hooks there and names the folder: open Codex
in it once, trust it and approve its hooks, as for the tree. A call without an
active gate is refused before it is admitted and costs nothing.

A rewritten statement passes the same checks as a mined one. The run refuses
a rewrite that holds a person's name from the history, an email address, an
author trailer, or a cost, quota, usage or token figure, and gives the task's
cells to a reserve task instead.

In step 6, `duolog calibrate judge` first lists the statements it rewrote from
a diff, then the `check_setup_changed` cells with the files they changed,
how their checks went and their patch. Such a cell stays out of the results;
read its patch and drop the task if the change makes the task unfair. Drop a
task you do not want with `duolog calibrate drop <task>`.
Record each verdict with `duolog verdict <id> confirm` or
`duolog verdict <id> correct "<criterion>=<a>/<b>"`.

## What calibration costs and how it limits spend

The caps are limits on estimated spend, not on the final bill. Calibration
checks quota before each call. It stops admitting calls when its estimated
spend reaches the approved cap. A call can cost more than its estimate. A
running call can also reach a provider limit before the next check.

Calibration runs on your subscription only. While `env_passthrough` lists
`ANTHROPIC_API_KEY` or `OPENAI_API_KEY`, `duolog calibrate run`, `resume` and
`judge` refuse to start, before they read quota or admit any cost. A worker
with a key is billed per token, outside the quota that the caps read. Remove
the key from `env_passthrough` and keep the other names; see
[Configuration](configuration.md#if-you-use-an-api-key-on-purpose).

duolog does not turn off provider credits or extra usage. It stops when a
quota reading is rejected or reports a reached spend control. Check your
billing settings before a run.

The plan shows, per class, the tasks, entries, cells, a token and USD
equivalent range, the share of each quota window from `duolog quota`, wall
time, the stop rules and sample statements. When no quota rate is known, the
share says `unknown, pilot first`. The run then starts with a pilot of 2 cells
under the same approval and estimates again.

The plan also costs the judge calls and the statements to rewrite, and counts
them toward the cap. It assumes every pair of entries passes every task and
each of its judges sees the pair once. The swapped order is costed for half
of the tasks, and the plan shows the most calls, with every pair swapped, as
an `up to` figure. The plan names the judges, with model and effort, and its
hash covers them and the classes' rubrics.

These limits apply to each agent:

| Limit | Default | What happens |
|---|---|---|
| Weekly cap, `calibration.max_weekly_share` | `0.10` | Calibration admits steps while its estimated spend stays within 10% of a weekly window. The plan is cut to fit |
| Five-hour cap, `calibration.max_window_share` | `0.5` | The run pauses when its estimated spend reaches 50% of a five-hour window, and continues after the window resets |
| Lead reserve | 15% weekly, 10% five-hour | A cell starts only if the lead keeps this much |

You can set either cap lower in `.duolog/config.yaml`, which you share in Git.
A higher cap there is ignored, and `duolog calibrate status` says so: a
teammate cannot raise what calibration spends for you. Only your own
`.duolog/config.local.yaml` or user config can raise the weekly cap, up to
`1`. The five-hour cap is at most `0.5`; a higher value is an error. The lead
never raises the caps. The run and its judge calls keep to the caps and cell
limits of the plan you approved. A stricter value in the config applies
without a new approval, a looser one only after you plan and approve again.

### How the run counts spend

The weekly points of each agent's judge calls and rewrites are its judge
share. The plan shows that share and keeps it free of cells: a cell runs only
if the judge share still to spend fits under the cap after it. When the run's
spend leaves too little room for that share, the run stops with `re-approve`.
A rewrite takes from the share only up to its estimate. Its cost above the
estimate, and a rewrite for a reserve task, count toward the cap but do not
take from the share.

Before each cell, judge call or statement rewrite, the run reads quota,
cached for up to 120 seconds. With no measured rate, it counts a cell as 5%
of the five-hour window and 1% of the weekly window. A judge call or rewrite
counts that bound scaled by its tokens over a cell's high estimate in its
class, and at least a twentieth of it. The plan counts them the same way. So
without a rate it says how many cells fit per agent before the cap, for
example `at most 10 cells before the cap`. The first judge call that moves the
weekly reading gives its agent a measured rate. A call that moves none leaves
the agent at the scaled bound. For a Codex model with its own limit, the
reserve applies to that limit too.

A provider often shows spend minutes late. So the run keeps the lead reserve
against the reading plus the charges of its own steps that the reading may
not show yet. A charge stays until a reading taken after its step ended has
risen by at least the charge, or the window resets. A rise that may be your
own work keeps the charge, so spend the run cannot place counts against
calibration. A window that reports no reset time counts from the run's first
reading and pauses for five hours.

Each cell, judge call or rewrite is counted at its estimate before the worker
starts, so an interrupted or failed attempt still counts, once. A measured
rise above the estimate is added after the step ends. A step that delegation
refuses before its worker starts, for its task, its lead or a sign-in check,
is not counted: the charge is taken back, and a cell waits to run again. The
run stops with `quota unavailable` when it cannot read quota, before or after
a step, so it never runs blind.

## How to stop and resume a run

The run stops by itself and prints `stopped: <reason>`. The reasons are
`lead reserve`, `provider limit`, `approved cap`, `quota unavailable` and
`re-approve`. At a five-hour cap it prints
`paused until <time>; resume with duolog calibrate resume`. You can also stop
it with Ctrl-C. Ctrl-C during a cell, while the worker or the checks run,
records no result for that cell: the checks stop, and resume runs the cell
again.

Ask the lead to resume later. It runs `duolog calibrate resume`, which skips
done cells, runs again the cells left `running`, and reads quota again. An
interrupted cell stays counted, and its rerun counts as a new attempt. Run and
resume both check that the commits, class checks, judges and rubrics still
match the approved plan.

After `re-approve`, run `duolog calibrate plan` again and approve the new
plan. The new plan keeps the cells that ran, with their results and spend, and
estimates the cells left from the tokens the done cells used. The spend so far
counts toward the cap. Until you approve it, the plan approved before stays in
force, and showing the plan never changes a cell.

The approved total does not reset with a quota window, so an agent at the
approved cap has its cells left skipped as `cut to cap`. The other agent's
cells go on. When none are left, the run part ends with
`stopped: approved cap` and prints `next: duolog calibrate judge` to judge
the cells it has.

`duolog calibrate judge` stops the same way between two judge calls, with
`next: duolog calibrate judge`. At a five-hour cap it prints
`paused until <time>; judge again with duolog calibrate judge`. Run it again
later: a pair keeps the calls it made, and only the rest run. At the approved
cap judging ends, and the pairs left stay unjudged. Judging refuses judges or
rubrics that changed since the approval; plan and approve again first.

`duolog calibrate plan` works after the run part too. The new approval keeps
the cells that ran, their spend and the verdicts given. With no cell left to
run it prints `next: duolog calibrate judge`. A pair whose first calls were
made under other judges or another rubric is then judged again from the
start, and its verdict says so in a note.

`duolog calibrate status` shows the step, the cells by state, the spend
against each cap, and any pause or stop.

## What calibration cannot isolate

Calibration hides the solution from each candidate, but some limits remain:

- **Old public history.** A model may have seen an older public commit in
  training. The plan says how many tasks are older than 90 days, and mining
  takes recent tasks first. Statements never hold the commit id or message.
- **A leaking tree.** The tree holds no later commits, reference patch,
  hidden tests or other results. A cell whose tree cannot guarantee that is
  marked `leakage_exposed` and does not count.
- **Judge-only tasks.** A docs or mechanical task with no test that fails
  before the change is judged against the reference alone. It counts at half
  weight.
- **The checks themselves.** They run outside the gate with your
  environment. See
  [the checks section](#what-the-checks-can-do-on-your-machine).

## How the evidence joins learning mode

Calibration results join the journal tagged `source: calibration`. A failed
check counts as correctness 0 without your confirmation. A judged result
counts only after you confirm or correct its verdict.

- Each entry needs 3 tasks with evidence before calibration can move it in a
  ladder. A task has evidence for an entry when the entry failed the checks,
  when you decided the verdict on it, or when the entry was the only one to
  pass while another entry has a result on the task. A pass that waits for
  your verdict does not count yet. Neither does a pass with no other result
  on its task, as when the other cells were cut. Otherwise, or when the
  ranking finds no clear winner, the class keeps its order and the proposal
  says `no difference found`.
- Live results keep accumulating at their own weights and outweigh
  calibration over time.
- A new calibration run replaces the earlier run's results for the classes it
  covers.
- A teammate who pulls `.duolog/calibration/project.yaml` gets a proposal
  seeded from it with `duolog calibrate propose`, without running anything.
  The seed is used only while they have no calibration results of their own.
  It records each model alias as its family and keeps the half weight of
  judge-only tasks, so the seed weighs the evidence as it counted for the
  person who ran the calibration.

## Related pages

- [Learning mode](learning.md): task classes, the journal and learned orders
- [Configuration](configuration.md): the calibration settings
- [Permissions](permissions.md): what the worker gate covers
- [Skill tests](skills.md): the bundled skills
- [Budgets](budgets.md): cost targets and what we measured
