# How to configure duolog

This page explains the config file: where duolog reads it, what each section
does, and how to check a role. The defaults are in
[config.default.yaml](../internal/core/config/config.default.yaml) in
`internal/core/config`.

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

A local file that Git tracks, for example one added with `git add -f`,
reaches everyone who clones the repository. duolog then reads it with the
rules of `.duolog/config.yaml`: it may only make paid runs, prices and the
account limits stricter, and duolog names each value it ignores. When Git
cannot say whether it tracks the file, duolog treats it as tracked. Outside
a Git checkout, such as a project downloaded as an archive, nothing shows
that the file is yours, so duolog reads it with the same rules, and
`duolog doctor` says why its opt-ins were ignored. Put your own opt-ins in
your user config there.

The user config folder is the one that Go's `os.UserConfigDir` returns. On
macOS it is `~/Library/Application Support`. On Linux it is `~/.config`.

An unknown key is an error. This catches typing mistakes, but it also means a
config that works with a newer duolog may fail with an older one.

## How to check your config in the editor

The config has a JSON Schema. Put this line first in `.duolog/config.yaml`:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/lorem-dev/duolog/main/duolog-config.schema.json
```

The link points to `main`, so you do not need to change it after an upgrade.
The schema on `main` can describe a newer version than yours. For a schema
that matches your installed binary, run `duolog config schema > schema.json`
and point your editor at that file. duolog generates the schema from its Go
types.

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

## How duolog balances subscriptions by pace

A new task starts on the role's `fallback` agent when the `use` agent runs
ahead of pace. This spreads work over both subscriptions, so neither runs out
before its window resets. The ladder `use`, then `fallback` still decides the
agent. Roles keep their names by kind of work.

An agent is ahead of pace in either of two cases:

- Its used share of the weekly window is more than `routing.margin` above the
  share of that window that has passed.
- Its five-hour window is more than `0.80` used.

The task moves only when all of these hold:

- The `use` agent is ahead of pace.
- The `fallback` is another agent, and it is not ahead of pace.
- The `fallback` runs on a subscription. A paid run or a run that can spill
  into overage is never a target.
- Both quota reads are fresh. Unknown or stale quota means no balancing, and
  the task stays on `use`.

Once a task has moved away, later tasks stay away until the `use` agent falls
below pace minus the margin. This keeps work from flipping back and forth at
the edge. duolog keeps this one flag per agent in its account state folder.

`duolog route`, `duolog delegate` and `duolog peer open` print the reason:

```text
$ duolog route implement
claude sonnet high
reason: implement -> claude sonnet: codex 61% of the week at 44% elapsed
```

On a workspace account the used share is the share of the usable part of the
window. A task you pin with `--use` or a profile is never moved.

| Key | Default | Meaning |
|---|---|---|
| `routing.balance` | `pace` | `pace` balances; `off` keeps the ladder as written |
| `routing.margin` | `0.10` | How far the used share may pass the elapsed share |

The shared `.duolog/config.yaml` may only tighten these keys. It cannot turn
`balance` off, and it cannot raise `margin`. duolog ignores such a value and
names it. Your user and local files may set either key freely.

## Which models the default roles use

Planning and the final review use Opus, because a mistake costs the most
there. Routine work uses cheaper Codex models. The sections below explain the
roles for a whole feature, for a bug and for reviews between tasks. A project
can change any role. The default deny rules and the calibration caps stay in
force, as the sections below say.

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

## Which role finds the cause of a bug

Give `debug` a bug whose cause can be found by reading one repository. It runs
on `sol` at medium effort, read-only, and keeps its thread for follow-up
questions. In the cost study, `sol` found the cause of four hard bugs at every
effort, for a third to a sixteenth of what Opus cost. Give `debug_hard` a bug
that crosses several components or needs logs and other evidence from outside
the repository. It runs on Opus and may edit files.

Reviews between tasks go to `review_intermediate` on `sol`. In the cost study,
`terra` cost half as much but made a false finding in half of its review runs,
and `sol` made none.

## How to give every worker the same rules

Put the project rules that every worker needs in `.duolog/brief.md`: the test
command, the tools to use, the files not to touch. duolog puts this file first
in each worker's input. The same start in every task lets the provider serve it
from its prompt cache. The file must be a regular file of at most 32 KiB; a
link is ignored, because it could point at a secret. The brief counts against
`wire.inline_max_bytes`, so a long brief leaves less room for Read files. A
resumed thread already holds the brief, so duolog does not send it again.

Every worker is also told not to commit, push or ask the user, and to edit a
changelog or docs only if the task lists them under Write.

## Why the config holds only model aliases

The config uses `fable`, `opus`, `sonnet`, `haiku`, `astra`, `sol`, `terra`
and `luna`. Claude Code resolves its own aliases when it runs. For Codex,
duolog picks the newest visible `gpt-<version>-<family>` from
`codex debug models`. duolog caches that list for up to 24 hours, and uses
an older cached list when the command fails. A full model name in a role
pins that version on purpose.

The models `fable` and `astra` are for manual use. duolog refuses them unless
you pass `--allow-manual-model`. The list is `limits.manual_only_models`.

## How the lead limits the worker

The lead's own settings are the ceiling:

- A worker never runs above the lead's effort. The scale is `low`, `medium`,
  `high`, `xhigh`, `max`, `ultra`. An unknown lead effort sets no ceiling.
- A worker never gets a wider permission mode than the lead. See
  [Permissions](permissions.md).
- A worker has `DUOLOG_DEPTH=1`, so it cannot delegate again.
  Claude workers and live Claude peers also start without the Agent and Task
  tools.

`duolog` never passes `--dangerously-bypass-approvals-and-sandbox` or
`bypassPermissions` to an agent.

## What the other sections do

| Section | What it sets |
|---|---|
| `wire` | The bus line limits and the inline size |
| `skill_tests` | `ask` (default), `always` or `never` for cross-model skill tests. Save a local preference with `duolog skill test --remember`. See [Skill tests](skills.md) |
| `limits` | Live sessions and the size of a task the lead may do itself. See [below](#how-many-sessions-run-at-once) |
| `routing` | Pace balancing of subscriptions. See [above](#how-duolog-balances-subscriptions-by-pace) |
| `models` | The agent and the prices for each alias. They drive admission of paid runs and the estimates of `duolog stats` and telemetry. Claude cache writes cost 1.25 times `input` for the five-minute cache and 2 times for the one-hour cache |
| `paid_runs` | Paid runs per agent with a daily USD cap, and the usable share of a workspace account. See [below](#how-paid-runs-are-allowed-and-capped) |
| `balance` | How old a quota read may be, and how learned window rates set the session tiers |
| `sessions` | When a resumed thread closes |
| `policy` | The rules for worker actions. See [Permissions](permissions.md) |
| `capabilities` | What to do when the other agent lacks a skill, plugin or server |
| `sync` | How agents and skills are generated. See [Sync](sync.md) |
| `env_passthrough` | The only variables a worker inherits. A free run still drops names that bill per token |
| `workers.inherit_plugins` | `false` (default): a Claude worker or peer turns off every plugin your user and project settings enable, except duolog's own. `true`: it loads them all, as your own sessions do. See [below](#what-workers-inherit) |
| `workers.compact_window` | `auto` (default): Claude workers and peers compact at the size `workers.compact_ladder` gives for your plan. `0`: Claude's own default. `100000` to `1000000`: the context size in tokens at which they compact. See [below](#what-workers-inherit) |
| `lead.compact_notes` | `true` (default): the lead's compaction keeps the open peers, asks, branches and pull requests, and lists the live peers again afterwards. `false` turns both off. See [below](#how-the-lead-keeps-its-state-through-compaction) |
| `rex` | Timeouts and capture settings for live sessions. See [Rex](rex.md) |
| `rex.stuck_notify_minutes` | Minutes before `peer wait` sends one reminder for an unanswered question. Default: `5`; `0` disables reminders |
| `rex.auto_answers` | Ordered `{match, answer}` rules for safe peer questions. Defaults to answering `Hooks need review` with `Continue without trusting`. A project list replaces the defaults; `[]` disables it. Trust and persistent approval grants are refused |
| `coauthors` | Enable worker credit and set commit co-author templates. See [Getting started](getting-started.md#how-to-credit-the-workers-in-commits) |
| `worker_modes` | The native permission mode a `delegate` worker starts in, per role and per model family. See [below](#which-layers-may-change-worker-modes) and [Permissions](permissions.md#a-workers-native-mode) |
| `telemetry` | The local history of runs, quota and routing. See [below](#which-layers-may-change-telemetry) and [Telemetry](telemetry.md) |

## How the lead keeps its state through compaction

When Claude compacts the lead's conversation, the summary can lose which peers
are open and what the lead was doing with them. With `lead.compact_notes: true`
(the default), `duolog install` adds a `PreCompact` hook to the lead's Claude
settings. The hook prints instructions that Claude adds to the summary: keep
the open peers with their task files and worktrees, the asks still open, and
the branches and pull requests the lead works on. It then prints the live
`duolog peer list` lines, at most 20. The session start that follows a
compaction prints the same peer lines again. Set `lead.compact_notes: false`
to print neither; the hook reads the setting when it runs. Workers and Codex
leads are not changed.

## What workers inherit

The default environment includes PATH, HOME and USER, plus `CODEX_HOME` and
`CLAUDE_CONFIG_DIR`. It leaves out `ANTHROPIC_API_KEY` and `OPENAI_API_KEY`:
a worker or peer that gets one is billed per token, even when you run on a
subscription. Windows also keeps SYSTEMROOT, USERPROFILE, APPDATA,
LOCALAPPDATA, TEMP, TMP, PATHEXT, COMSPEC and PSMODULEPATH. PSMODULEPATH
lets PowerShell find its modules without a slow search; in one check, each
cmdlet that loaded a module took 18 to 30 seconds without it. An explicit
`env_passthrough` replaces these defaults.
The worker always gets `DUOLOG_DEPTH=1`, its `DUOLOG_LEAD` and `DUOLOG_ROLE`.
Codex trust, hooks and capabilities read `CODEX_HOME` when set. Claude hooks,
permissions and capabilities read `CLAUDE_CONFIG_DIR` when set. Home lookups
use the OS home directory, including USERPROFILE on Windows.

A Codex worker runs its commands with `allow_login_shell=false`. A login shell
reruns your profile, which can reset PATH and lose tools such as `node` from
nvm. The worker keeps the PATH that duolog passes.

A Claude worker starts with auto memory off. Claude keeps that memory per
project path, so a worker's notes would reach the lead and every later session
of the project. It also starts with no MCP servers, because in the cost study
the configured servers added 7.4 thousand tokens to every call. A task that
names an `mcp:` or `plugin:` need keeps them, unless every MCP need is
bridged. A
`minimal` Claude worker skips every `CLAUDE.md`, the one in your home folder
too: Claude loads that file as project instructions when the project is inside
the home folder. A `minimal` worker of either agent keeps its skills only when
the task names a `skill:` or `plugin:` need.

A Claude worker or peer also starts with your plugins off. Every enabled
plugin adds its skills to every request, about 16 thousand tokens for a typical
set. duolog reads `enabledPlugins` from the same user, project and local
settings it reads for the route, and passes the worker an `enabledPlugins` map
in `--settings` that sets each enabled plugin to `false`, except duolog's own.
Your settings files are not changed, and the lead and your own sessions keep
their plugins. Set `workers.inherit_plugins: true` to keep the old behavior.
Codex workers are not changed here.

`workers.compact_window` sets the context size, in tokens, at which Claude
workers and peers compact. When it is not `0`, duolog sets
`CLAUDE_CODE_AUTO_COMPACT_WINDOW` to it in the `env` block of the `--settings`
that every Claude worker and peer gets. It is `auto` (the default), `0`
(Claude's own default) or a number from `100000` to `1000000`. The lead is not
touched: set the lead's own threshold with Claude's `/autocompact`. Codex
workers are not changed.

`auto` reads your Claude plan at worker start, from the same quota read that
admits the worker, and takes the window from `workers.compact_ladder`. A plan
that is not in the map, or one the read could not name, gets `0`: no entry.
The values below are first guesses and will be set again after a measurement.

| Plan | `workers.compact_ladder` default |
|---|---|
| `pro` (the $20 plan) | `250000` |
| `max` (the $100 plan) | `500000` |
| `ultra` (the $200 plan) | `700000` |

duolog takes the plan name from what Claude reports. If Claude reports the
$200 plan as `max`, as it does today, an `ultra` account gets the `max` value.

Your own `compact_ladder` replaces single keys, such as `pro: 300000`, and
adds plans (`free`, `team`, `business`, `enterprise`, `edu`). Each value is `0`
or from `100000` to `1000000`.

A free run never gets a name that can bill per token, even when
`env_passthrough` lists it. A free run is one that must stay on your
subscription. The names are matched by their shape, so a new key or provider
switch is caught too:

| Agent | Names a free run drops |
|---|---|
| Claude | `ANTHROPIC_` names that end with `_KEY`, `_TOKEN` or `_BASE_URL`; names that start with `CLAUDE_CODE_USE_` or `AWS_BEARER_TOKEN_`; `ANTHROPIC_PROFILE` and `ANTHROPIC_FEDERATION_RULE_ID` |
| Codex | `CODEX_API_KEY`; `OPENAI_` names that end with `_KEY`, `_TOKEN` or `_BASE_URL`; `OPENAI_FEDERATION_RULE_ID`; the key that the default provider names |

`CLAUDE_CODE_OAUTH_TOKEN` is a subscription login, so a free run gets it when
`env_passthrough` lists it. A Claude base URL passes too when it matches an
entry of `paid_runs.claude.subscription_endpoints`. Only a paid run of an
agent with `paid_runs.<agent>.allow` gets the other names; see
[below](#how-paid-runs-are-allowed-and-capped). duolog compares names only
and never prints a value.

Free Claude workers and peers always start with fast mode off. Their
`--settings` JSON holds `"fastMode": false`, and no duolog setting changes
that. duolog turns it off because fast mode can draw on usage credits even
inside a subscription window. A paid run keeps the fast mode your settings
choose, since it is paid anyway. duolog never changes the lead's settings or
environment: it only reports how the lead is billed, in `duolog quota` and
`duolog doctor`.

### If you use an API key on purpose

While a name that a free run drops is set in duolog's environment or listed
in `env_passthrough`, and paid runs are not allowed for that agent, the first
`delegate` or `peer open` of each lead adds one line per name. `duolog doctor`
warns every time. Without a registered lead, every launch adds the line. For
`ANTHROPIC_API_KEY` it reads:

```text
claude workers no longer get ANTHROPIC_API_KEY. To let them use it, set paid_runs.claude.allow: true with a daily_cap_usd above 0 and list the key in paid_runs.claude.env.
```

The warning of `duolog doctor` ends with an example to copy:

```text
Example config: paid_runs: {claude: {allow: true, daily_cap_usd: 5, env: [ANTHROPIC_API_KEY]}}
```

When the key was the agent's only login, the login check fails. duolog does not
start a browser login, which would switch your billing, whatever
`capabilities.on_auth_failure` says. The task goes to the lead with the same
line, or with `fail` the delegation fails with it.

To let workers use the key, allow paid runs for that agent with a daily cap,
in `.duolog/config.local.yaml` or your user config:

```yaml
paid_runs:
  claude: {allow: true, daily_cap_usd: 5}
```

`paid_runs.claude.env` already lists `ANTHROPIC_API_KEY`, and the Codex one
lists `OPENAI_API_KEY`. Add a name there only when a paid run needs another
variable. The line and the warning stop once the name passes. duolog still
tries the subscription first, and spends at most the cap in any 24 hours.
Calibration on a paid route also needs `paid_runs.<agent>.calibrate: true`.
See
[Calibration](calibration.md#what-calibration-costs-and-how-it-limits-spend).

## How paid runs are allowed and capped

duolog spends no money on a worker or peer unless you allow it for that
agent, and then at most a daily cap in USD. Paid runs are off by default.

Before each run, duolog decides how the run would be billed. It reads the
run's environment, every settings layer, its arguments and the free quota
reads, never a plan or provider name:

| Verdict | Meaning |
|---|---|
| `subscription` | The run uses a subscription window, and paid overage is off |
| `subscription_overage` | As above, but a run past the window would cost money |
| `paid` | Billed per token or credit, or duolog cannot tell |

An unknown value, a settings file it cannot read and a failed or stale quota
read all count as `paid`, so a doubt never spends money. A delegation tries
every free candidate of the role, `use` then `fallback`, before a paid run. A
target you pin with `--use` or a profile keeps its agent. When no candidate
may run, the task goes to the lead with one line that names the signal and
its layer, never its value.

| Key | Default | Meaning |
|---|---|---|
| `paid_runs.<agent>.allow` | `false` | Lets that agent's workers and peers run on a paid route |
| `paid_runs.<agent>.daily_cap_usd` | `0` | The most its paid runs may spend in the last 24 hours. Must be above 0 with `allow`; a config without it is an error that names the key |
| `paid_runs.<agent>.env` | the agent's API key | Optional. Variables that only a paid run of the agent gets |
| `paid_runs.<agent>.calibrate` | `false` | Lets calibration use a paid route. Needs `allow` too |
| `paid_runs.<agent>.account` | `auto` | `workspace` uses only `usable_share` of each window. Workspace evidence in the quota read always wins |
| `paid_runs.usable_share` | `0.5` | The share of each window duolog uses on a workspace account, above 0 and at most 1 |
| `paid_runs.claude.subscription_endpoints` | none | Named base URLs that count as your subscription, such as a company gateway in front of the claude.ai login |

The cap covers the last 24 hours, not a calendar day, so it cannot be spent
twice around midnight. A paid run starts only when the spend of the last 24
hours, the runs in flight at their estimates and this run's estimate stay
within the cap. The estimate comes from the tokens of earlier runs of the
same kind and target, or a high starting bound without them, times the
prices in `models`. A run in progress is not
stopped at the cap; the next admission sees what it spent. Each agent runs
one paid task at a time.

A peer pane keeps the route and the billing it opened with: its environment,
its home folder and the settings layers of its project. Each task sent to it
is admitted again, against the cap and the windows as they are then, and
counts as paid when the pane opened paid. A send from a command that would
put the pane on another route, such as a shell without the pane's API key,
is refused; close the pane with `duolog peer close` and open it again.

A finished run counts the larger of two costs: its tokens times the prices,
and the cost the agent's CLI reports. The prices leave out fast mode and
cloud surcharges, so the spend is a lower bound of the bill. A paid target
without a price is refused. The prices now drive admission of paid runs, not
only the estimates of `duolog stats`.

duolog never spends paid overage, workspace credits or reset credits on
purpose, and `allow` does not change that. On `subscription_overage` it
stops before the window runs out and refuses a run whose estimate does not
fit in what is left. A run that still crossed into overage counts as paid,
and the next admission treats that agent as `subscription_overage`.

The spend and the in-flight runs live in duolog's user state, next to the
account state, because the money belongs to the account and not to one
project. `duolog quota` and `duolog doctor` show the verdict of each agent's
worker, peer and lead, with the signals, the account kind and a line such as
`paid allowed: 1.20 of 5 USD in 24 h, 0.40 in flight`.

duolog frees a slot and its estimate only when it can prove that the run or
the pane ended. A hold it cannot settle, such as a pane whose `peer open`
was stopped after the pane started, stays and shows in `duolog quota` and
`duolog doctor` as a `held:` line with the reason. Close the pane if it
still runs, then end the hold with `duolog peer release <id>`. The command
refuses while the pane or the command that holds it still runs.

You share `.duolog/config.yaml` in Git, so a cloned repository must not turn
on spending. That file may only make these stricter: `allow: false`,
`calibrate: false`, a lower cap, a lower `usable_share` or
`account: workspace`. Its `models` may raise a price but not lower one, may
not move an alias to another agent or family, and may not price a new alias,
which paid runs then refuse. duolog ignores a looser value there, and
`duolog quota` and `duolog doctor` name it. Turn paid runs on in an untracked
`.duolog/config.local.yaml` of a Git checkout or in your user config.

duolog reads managed settings files, but managed policy outside files is not
checked. A device management profile on macOS or a Windows registry policy
can still turn on a paid route or fast mode for Claude, and `duolog doctor`
says so.

## How many sessions run at once

duolog limits live sessions per agent, by the size of its subscription, and
for all agents together. A larger window holds more parallel work, and a
window duolog has not measured gets the cautious value.

A live session is a running worker process, such as a delegation, fresh or
resumed, or a calibration cell. An open peer pane counts, idle or busy. An
idle resumable thread runs no process and does not count. The count belongs
to the account, across every lead and project.

| Key | Default | Meaning |
|---|---|---|
| `limits.max_total_sessions` | `10` | Live sessions of all agents together |
| `limits.sessions_by_tier.small` | `3` | Live sessions of an agent at the small tier |
| `limits.sessions_by_tier.medium` | `5` | The same at the medium tier |
| `limits.sessions_by_tier.large` | `10` | The same at the large tier |
| `limits.session_wait_seconds` | `60` | How long a delegation waits for a free slot |

The tier comes from a measure, never from a price or a plan name: the share
of a quota window that one million weighted tokens uses, learned from earlier
runs. A small tier is about a personal plan of 20 USD a month, medium a window
about 5 times larger and large about 20 times larger. An agent with no
measured rate yet is `small`, and so is one with a `paid` verdict or a
workspace account. A paid run or a workspace account runs one task at a time
per agent, even when the tier allows more sessions. An idle peer pane runs no
task, so it keeps no delegation waiting; its next send waits its turn
instead. A tier steps down at once
and steps up only past its cut by `balance.margin`.

`limits.max_live_sessions` still applies: it limits the resumable threads and
panes per lead and agent. When both limits apply, the lower limit wins.

When a slot is full:

- `duolog delegate` waits up to `limits.session_wait_seconds`, first come first
  served. When a slot frees, it checks the quota and the cap again. After the
  wait it tries the role's next candidate. When none has a slot, the task goes
  to the lead with a line such as
  `claude sessions 3 of 3 (tier small); waited 60 s`.
- `duolog peer open` is refused at once, since a pane stays open for long. The
  line names the open sessions. `duolog peer send` opens no session.
- Calibration cells wait for a slot without a time limit, and
  `duolog calibrate status` says the run is waiting for a session.

Only your user config may raise `max_total_sessions` or a tier above its
default, for example when your plan allows more parallel work than duolog has
measured yet. Every other layer, the shared `.duolog/config.yaml` and
`.duolog/config.local.yaml`, whether Git ignores the local file or not, may
only lower these keys. A value above the default there is capped at the
default, and `duolog doctor` says so. Small, medium and large are separate
limits: raising one tier does not raise the next. This example in your user
config runs at most 4 sessions, and each agent at most 3:

```yaml
limits:
  max_total_sessions: 4
  sessions_by_tier: {medium: 3, large: 3}
```

`duolog quota` and `duolog doctor` show each agent's live sessions, its tier
and the window that decided it, and the total, such as
`sessions 3 of 10 in all`.

## How a worker borrows the lead's MCP server

A task names the MCP servers it relies on with `duolog delegate --needs
mcp:<name>`. By default, when the worker's agent lacks that server, duolog does
not start the worker. It tells the lead to do that part first
(`on_missing.mcp: lead_first`). With `bridge`, duolog instead hands the lead's
server to the worker for that one run:

```yaml
capabilities:
  on_missing: {mcp: bridge}
  mcp_tools: {tracker: [get_issue, search]}   # optional
```

Bridging is off by default because it gives the server's credentials to the
other vendor's agent. Turn it on only for servers you would also configure in
that agent yourself.

A worker never installs a plugin. When Claude Code in a peer pane offers one,
such as its LSP plugin recommendation, duolog selects `No, not now` and never
`Yes, install`, `Never for this plugin` or `Disable all ...`, because those
change your own settings. With `on_missing.plugin: lead_first`, the default,
the lead gets one `note` on the bus per peer and plugin. It names the plugin,
its marketplace and what triggered the offer, with the command to install it
yourself, such as `claude plugin install gopls-lsp@claude-plugins-official`.
With any other value the offer is declined and the lead is not told. A
`rex.auto_answers` rule cannot answer these offers. An offer with no option
that declines for now goes to the lead as a question.

duolog reads the server from the lead's agent config and translates it:

| Lead | Where duolog reads it | Worker gets it as |
|---|---|---|
| Claude | `~/.claude.json` (user and local scope) and the project's `.mcp.json` | `-c mcp_servers.<name>.<field>=...` options of `codex exec` |
| Codex | `config.toml` in `CODEX_HOME`, and the project's `.codex/config.toml` when Codex trusts the folder | A temporary file for `--mcp-config` |

Only the servers the task names are bridged, never all of them. Claude's order
applies: local scope, then project, then user. A `.mcp.json` server counts only
after you approved it in Claude, and the project's own `.claude/settings.json`
and `settings.local.json` approve nothing until you trusted the folder. Codex
merges the project layer over the user layer, key by key.

Every env and header value of a bridged server moves into the worker's
environment under a new name, such as `DUOLOG_MCP_TRACKER_1`. The translated
config names the variable: `env_http_headers` for Codex and
`${DUOLOG_MCP_TRACKER_1}` for Claude. A value that the lead's config reads
from a variable, such as `${TRACKER_TOKEN}` or `bearer_token_env_var`, is read
from the environment of `duolog delegate`. The Claude file is written with
mode 0600 in the task folder and removed after the run. On Windows the mode
only sets the read-only flag, and the file keeps the access rules of the task
folder. A url and literal stdio args pass as written,
so a token inside a url path reaches the worker as it is. A Codex worker hides
the new variables from its shell commands with `shell_environment_policy`: as
`filters`, or added to the `exclude` list when the worker's policy uses the
older form, since Codex refuses both forms together.

A Claude worker's Bash tool can read the variables. Claude has two ways to
hide variables, and neither fits:

- `CLAUDE_CODE_SUBPROCESS_ENV_SCRUB=1` removes variables whose name or value
  looks like a credential from Bash, hooks and stdio servers. In our check it
  also emptied those variables in the `${VAR}` headers of an HTTP server, so
  the server listed no tools. A name that does not look like a credential
  still expands, but Bash sees it too.
- `sandbox.credentials` removes variables from sandboxed Bash commands only.
  It needs Claude's sandbox, which also limits the network of every command,
  and a command that Claude retries outside the sandbox keeps them.

With the gate on, commands that print the environment, such as `env`,
`printenv` or `echo $VAR`, ask the lead. In `full` mode the gate allows what it
would ask, so do not bridge a server to a Claude worker in `full` mode unless
you accept that the worker can read its token.

A Codex stdio server gets only a short default environment, so its variables
pass under their own names with `env_vars`, and the worker's Codex gets them
too. So a server is not bridged when it sets a name the worker already has with
another value, a name the agents or the system read, such as
`OPENAI_BASE_URL`, `CODEX_HOME`, `PATH` or a `*_PROXY`, or a `DUOLOG_` name.
On Windows these names compare without case.

`mcp_tools` limits what a server offers, bridged or the worker's own, and the
same list allows those tools in the gate. Codex gets `enabled_tools`, so it
sees only those tools. Claude gets `--allowedTools mcp__<server>__<tool>`; it
still sees the other tools, and the gate asks the lead before each of them.
Without a list, the whole server is bridged, and every tool asks. See
[Permissions](permissions.md#how-a-workers-mcp-tool-calls-are-checked).

A Claude worker whose only MCP needs are bridged starts with those servers and
no others (`--strict-mcp-config`). When the task also needs one of the
worker's own servers, the bridged servers are added to its own. Then its
other stdio servers start with the bridged variables in their environment too.
A Codex worker always keeps its own servers, and the bridged ones are added. A
server of the same name in the worker's Codex config, even a disabled one,
stops the bridge, because the options would merge into it.

Some servers cannot be bridged. The task then goes to the lead, as with
`lead_first`, with one line that says why:

- A server that signs in with OAuth, such as Codex `auth`, `oauth` or `scopes`
  and Claude `oauth`. A remote server with no credentials in config counts as
  OAuth too, because a public server and an OAuth server look the same.
- A server whose headers come from a helper command.
- Claude connectors and plugin servers, which no config file lists.
- SSE and unknown transports. A Codex server with a `cwd` other than the
  project, because Claude cannot set one.
- A url with a query or user info, a url that reads a variable, and stdio args
  that read a variable, for a Codex worker. Codex takes these as text, so the
  value would be in argv.
- A variable that the lead's environment does not have.

`duolog caps` marks each server `bridgeable` or says why it is not, and
`duolog doctor` names the servers each agent can lend.

We checked both directions with Codex 0.165.0 and Claude Code 2.1.316
(OBSERVED). A Codex worker called a tool of an HTTP server with a token header
from the Claude lead's `~/.claude.json`. A Claude worker called a tool of an
HTTP server with a token header from the Codex lead's `config.toml`. The token
was in no file under `.duolog/` and in no argument of the worker.

## What happens when a worker cannot sign in

Before a worker starts, duolog checks two logins. A failed login would
otherwise end the run only after the lead waited for it.

| What | How duolog checks it, with no model call |
|---|---|
| The worker's agent | `codex login status` or `claude auth status --json` |
| An MCP server with a token in config | One MCP `initialize` request with the headers the worker gets. Only HTTP 401, 403 or an error that names the token count |
| An MCP server that signs in with OAuth | `codex mcp list --json` or `claude mcp get <name>` |

A passed check is kept for five minutes in `.duolog/cache/auth.json`, which
holds only the time. A stdio server is not checked, because it would have to
start. A server that checks its token only when a tool runs passes too; one
server in our check accepted `initialize` with a wrong token. The
request does not follow a redirect, which would take the headers to another
url. A `.mcp.json` server you did not approve, or one you turned off, gets no
check at all. The worker is
told to stop with `auth:mcp:<server>` in `needs` when a server refuses it, and
duolog reads a refused agent login from the worker's errors. Both get the same
treatment as a failed check. In our check, a Codex worker given a wrong token
this way stopped, and the lead got the line that names the config file.

`capabilities.on_auth_failure` decides what happens next:

| Value | What happens |
|---|---|
| `login_then_lead` | The default. duolog runs the agent's own login once: `codex login`, `claude auth login`, `codex mcp login <name>` or `claude mcp login <name>`. If it works, the worker starts. Otherwise the task goes to the lead |
| `lead` | The task goes to the lead at once, with the login command to show the person |
| `fail` | `duolog delegate` fails |

The login waits up to `login_timeout_seconds` (180 by default) for the person
to finish it in the browser. When it needs a terminal that duolog does not
have, the result line has `needs=auth:<agent>` or `needs=auth:mcp:<name>` and
the command for the person, such as `! codex mcp login tracker`.

When the person closes the login, or it times out or fails, the task goes to
the lead, as with `on_missing.mcp: lead_first`. The line names the agent or the
server and the reason, never the token. After a closed or failed login, duolog
does not ask for it again until the lead's session ends; after a timeout it
may, since the person can finish it from a terminal. Only a server the task
named with `--needs` gets a login. The login command is quoted for a shell. A
server with a token in config has nothing to log in to: the line names the
config entry that holds the token. duolog runs at most one login for each
delegation.

## What a Codex worker's sandbox blocks

A write role runs Codex in `workspace-write`. The cost study saw these limits
with `codex exec` (OBSERVED, Codex 0.165.0 on macOS):

| What fails | Why | What to do |
|---|---|---|
| Tests that use a local database or Redis | The sandbox blocks the network, even localhost | Give the role `network: true` in [`worker_modes`](#which-layers-may-change-worker-modes) in your user config. duolog sets `network_access` and `writable_roots` on every worker's command line, so `.codex/config.toml` does not change them for workers |
| `uv run` | Its cache lock is outside the project | Run `.venv/bin/python -m pytest` |
| `npx` | Its cache in `~/.npm` is outside the project | Run `node_modules/.bin/<tool>` |
| `git status` with Git LFS | LFS writes to `.git/lfs/tmp` | Name the check in the task instead |

Put the working forms in `.duolog/brief.md`, so that every worker uses them.

## How learning mode chooses a target for each kind of work

Learning mode gives each delegation a task class, and the class picks the
target from a ladder of agent, model and effort entries. It is off by default,
and with it off nothing changes. See [Learning mode](learning.md) for the task
workflow and journal. The `learning` keys and their defaults:

| Key | Default | What it does |
|---|---|---|
| `enabled` | `false` | Turns learning mode on |
| `threshold` | `2.5` | Cost-first: the cheapest entry whose quality minus the margin reaches this |
| `quality_margin` | `0.25` | Quality-first: a cheaper entry this close to the best one wins |
| `prior_runs` | `2` | How many judged runs a prior quality is worth |
| `uncertainty` | `0.5` | The margin is this divided by the square root of prior runs plus judged weight |
| `profile_below_runs` | `3` | Task finish offers to profile an entry with less judged weight |
| `judges` | opus, then sol, both `high` | The first judge whose agent neither side used |
| `classify` | twelve rules | Ordered rules from task features to a class; the first match wins |
| `classes` | ten classes | Roles, ladders, priors and the judge rubric of each class |

A layer replaces `classify` as a whole list, as YAML lists do, so a project
that adds one rule restates the others. `classes` merge per class and field, so
a project can add a class or change one ladder and keep the rest.

## How calibration limits its spend

The `calibration` section caps what a calibration run may spend and which
past commits it uses as tasks. Calibration runs only when you ask the lead for
it; see [Calibration](calibration.md). The order it proposes routes tasks only
while `learning.enabled` is `true`. The keys and their defaults:

| Key | Default | What it does |
|---|---|---|
| `max_weekly_share` | `0.10` | Estimated weekly spend allowance per agent for the whole calibration. Admission checks it before each call; actual spend can exceed it |
| `max_window_share` | `0.5` | Estimated five-hour spend allowance before the run pauses until the window resets; at most `0.5`. Actual spend can exceed it |
| `cell_max_minutes` | `45` | Time cap for one cell and for one run of its checks; a task estimated above it is left out |
| `cell_max_tokens` | `1500000` | The highest token estimate a task may have to be admitted. The runners report usage when a worker ends, so a cell whose reported usage is above it is `failed_limit` with reason `token cap` and no checks: it records its usage, gives no result, and the run goes on |
| `cell_max_turns` | `100` | Turn cap for one cell: `--max-turns` for a Claude worker, advice in the brief for Codex, which has no such option |
| `history_days` | `90` | Days of history mined first, widened when a class has fewer than 3 tasks |
| `filter.min_files`, `filter.max_files` | `1`, `20` | Changed files of a mined commit, without generated and vendored files |
| `filter.min_lines`, `filter.max_lines` | `10`, `400` | Changed lines of a mined commit |
| `filter.large_max_files`, `filter.large_max_lines` | `40`, `1500` | Limits of a larger stratum, used only when the project's work is large |

Set a cap lower to spend less. The lead does not raise the caps; only you
edit them. The shared `.duolog/config.yaml` can only lower the two share
caps: a higher value there is ignored, and `duolog calibrate status` names
it. Raise the weekly cap, up to `1`, in `.duolog/config.local.yaml` or your
user config. The five-hour cap is at most `0.5`; a higher value is an error.
The lead reserve, 15% of the weekly and 10% of the five-hour window, applies
on top and is not a setting. The caps limit estimated spend, so a run can
spend more than them; see
[Calibration](calibration.md#what-calibration-costs-and-how-it-limits-spend).

## Which layers may change worker modes

`worker_modes` sets the native mode a `duolog delegate` worker starts in.
Without it, workers get duolog's default flags: Claude `acceptEdits` at most,
Codex `workspace-write` at most. Peers ignore the section.

Even without this section, every Codex `workspace-write` worker and peer
runs with no network and no writable roots beyond the project, whatever
`.codex/config.toml` says; to give a worker network back, add a role entry
with `network: true` here in your user config.

```yaml
worker_modes:
  roles:
    implement:
      claude: acceptEdits
      codex: workspace-write
      network: true
      writable_roots: ["~/.cache/go-build"]
    debug_hard: {claude: auto}
  families:
    luna: {codex: read-only}
    haiku: {claude: acceptEdits}
```

An entry has these keys:

| Key | Meaning |
|---|---|
| `claude` | `plan`, `default`, `acceptEdits`, `auto` or `bypassPermissions` |
| `codex` | `read-only` or `workspace-write` |
| `network` | Role entries only. Asks Codex to let the worker's commands reach the network. Default: `false` |
| `writable_roots` | Role entries only. Absolute or `~/` folders Codex commands may write besides the project. Default: none |

duolog passes `network` and `writable_roots` to Codex as
`-c sandbox_workspace_write.network_access` and
`-c sandbox_workspace_write.writable_roots`. It passes both on every
`workspace-write` worker, `false` and `[]` when no entry allows more, so
Codex's own config files cannot widen a worker. Whether `codex exec` honours
these keys is NOT VERIFIED; the worker mode study measures it.

An unknown name is a load error. So is `danger-full-access`: a Codex worker
stays on `workspace-write` or `read-only`, and `network` and `writable_roots`
are the only way to loosen it. They need a role with a `workspace-write`
sandbox and `codex` unset or `workspace-write`. With `codex` unset they ask
for `workspace-write`, and they apply when the lead is in `edits` or `full`.

duolog refuses a root that is a file, a volume root, the home folder or a
parent of it, or a folder that holds or lies in credentials, agent and
duolog settings, or programs the lead runs: `~/.ssh`, `~/.aws`, `~/.gnupg`,
`~/.config/gh`, `~/.config/git`, `~/.docker`, `~/.kube`, the keychains and
keyrings, `~/Library/LaunchAgents`, `~/.claude`, `~/.codex`, and on
Windows the Credential Manager, Vault and DPAPI folders under `%APPDATA%`
and `%LOCALAPPDATA%`. It compares the folders after it resolves symlinks and
the macOS `/System/Volumes/Data` firmlinks, and without case where the
volume ignores case. `delegate` checks the roots
again before each run. Then it also refuses the folders on the worker PATH
that hold `duolog`, `claude` or `codex`, and duolog's state.

For one worker, the role entry and the family entry both count and the lower
one wins. Only a role entry can ask for more than duolog's default. A family
entry only lowers: alone it gives at most `acceptEdits` or `workspace-write`,
and one that asks more changes nothing. A read-only role ignores family
entries, so they do not set its gate to `plan`.
The result never goes above the lead's mode, and Claude `auto` needs a lead
in `auto` or `full`. `DUOLOG_MODE` can only lower the level further.
[Permissions](permissions.md#a-workers-native-mode) has the full resolution
and the gate preflight that refuses a looser run.

Only your user config may raise a value. `.duolog/config.yaml` and
`.duolog/config.local.yaml` may lower an entry, or add one at or below
duolog's own default: `acceptEdits` or `workspace-write`, no network and no
writable roots. Each layer lowers the entry as the layers before it left it,
so `config.local.yaml` cannot undo a lowering in `config.yaml`. A project
layer that changes a role under `roles` drops what that role's entry asks
above duolog's default, network and writable roots included. A family entry
also applies by the family your user config gives a model, so a project that
changes `models.<name>.family` cannot escape it. It applies by the families
of the role's route in your user config as well (its `use` and `fallback`
models and their `model_map` counterparts), so a learning ladder,
`routing.learned.yaml`, a `model_map`, a version pin or `--use` that moves
the role to another model cannot escape it either. A cloned project therefore
cannot start workers in a looser mode. `duolog doctor` names each ignored
value and shows the effective entries; `duolog route` shows what a role gets.

## Which layers may change telemetry

The `telemetry` section is yours, not the project's: only the embedded
default and your user config set it. A project layer may set one key,
`enabled: false`, which stops that project's run, permission and peer events.
It cannot turn telemetry on or change the other keys, so pruning never
depends on the project that runs it. `DUOLOG_TELEMETRY=off` turns telemetry
off everywhere and cannot turn it on. The keys and their defaults are in
[Telemetry](telemetry.md#how-long-data-stays).

Turning telemetry off stops collection. Existing records remain until
pruning removes them. Use `duolog telemetry purge` to delete this project's
telemetry now; see [Telemetry](telemetry.md) for account-wide deletion.

## Related pages

- [Getting started](getting-started.md): install and first task
- [Permissions](permissions.md): policy rules and modes
- [Learning mode](learning.md): task classes, the journal and learned orders
- [Calibration](calibration.md): a starting order from past work, on request
- [Telemetry](telemetry.md): the local history and how to turn it off
- [Practices and models](research/2026-10-30-practices-and-models.md): the
  evidence behind the defaults
- [Cost study](research/2026-11-01-cost-study.md): what duolog cost against
  one agent, and why 1.1.0 changed the gate and the roles
