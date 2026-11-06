# Changes

<!--
  RULES FOR THIS FILE. scripts/version/version.py enforces the limits, and CI
  fails when one is broken.

  Audience: someone deciding whether to upgrade. This is not a commit log.

  LIMITS, all hard:
    15 bullets in one version section, across its subsections.
    160 characters in one bullet.
    2 lines in one bullet, with 80 characters in a line.
    One level of bullets. No code blocks, tables or paragraphs inside a section.

  Over a limit, merge related entries into one that names the feature, or cut
  what does not affect a user. A long entry belongs in the docs: link to them.

  What goes in:
    - A feature, option or command that a user can now use.
    - A change in behaviour that would otherwise surprise them.
    - A fix for something that was broken in a released version.
    - A removal, or anything that needs action on upgrade.

  What stays out:
    - A fix for a bug that no released version had.
    - Refactoring, tests, CI and dependency bumps with no visible effect.
    - The reasoning. One clause of why is fine when it changes what a reader does.

  Form:
    - One entry per user-visible thing. Present tense, active voice.
    - Name the command or option in backticks, so that a reader can search for it.
    - ASCII only.

  Sections: `## Development` collects unreleased entries and is always first. A
  version heading is `## 1.2.3`, with no date. Inside a section, use only
  `### Added`, `### Changed`, `### Fixed`, `### Removed` and `### Security`.

  `version.py bump` turns Development into a version section, and `version.py
  notes` extracts that section as the body of the GitHub release, so the heading
  format matters.
-->

## Development

### Added

- `worker_modes` sets a delegate worker's native mode per role and family,
  never above the lead's; a looser mode needs a gate preflight.
- `paid_runs.<agent>.allow` lets workers and peers run paid when out of quota,
  up to `daily_cap_usd` in 24 hours; `calibrate` does so within the plan's USD.
- `limits.max_total_sessions` (10) and `limits.sessions_by_tier` (3, 5, 10 by
  measured window) limit live sessions per account, across all projects.
- A delegation waits up to `limits.session_wait_seconds` (60) for a session
  slot; `peer open` is refused at once, and calibration cells wait.
- `paid_runs.usable_share` (0.5) keeps a workspace account to that share of
  each window; paid and workspace agents run one task at a time.
- `paid_runs.claude.subscription_endpoints` names gateway URLs that count as
  the subscription.
- `duolog quota` and `duolog doctor` show billing by agent and signal, spend,
  sessions and `held:` panes; `duolog peer release <id>` frees a closed one.
- `routing.balance: pace` starts a new task on the role's `fallback` while its
  `use` agent runs ahead of its window, never onto a paid route.
- `peer list --long`, `peer show`, `peer view` and a `duolog-peers` skill show
  peers; the lead hears of stalled peers and expired asks. See docs/rex.md.

### Changed

- Codex workers get no network and no extra writable roots, even without
  `worker_modes`; give a role `network: true` in `worker_modes` to restore it.
- Free workers, peers and calibration drop per-token API keys even if listed in
  `env_passthrough`, so calibration starts; `duolog doctor` shows the fix.
- The prices in `models` now drive admission of paid runs. They leave out fast
  mode and cloud surcharges, so spend is a lower bound.
- `.duolog/config.yaml` may only tighten, as raising `models` prices; so may a
  `.duolog/config.local.yaml` Git tracks or outside Git. Opt in at user level.

### Security

- Codex peers ignore `network_access` and `writable_roots` in
  `.codex/config.toml`, so a cloned repository cannot widen their sandbox.
- Workers cannot start `claude` or `codex` through any path, `.cmd` or `.ps1`
  launcher, `npx`-style runner or `env` prefix.

## 1.4.1

### Added

- `duolog lead register` makes the current session the lead when the
  project's hooks never ran, such as after a restart outside the folder.
- `duolog peer answer --reason` types one line into a peer that the answer
  left interrupted, such as why a permission was denied.
- `duolog doctor` says whether Claude trusts the project folder; until it does,
  Claude ignores the project's `permissions.allow`.
- Each peer may write its report to `.duolog/tasks/<peer-id>/report.md`;
  `peer status` and `wait` show the path.

### Fixed

- `duolog stats`, telemetry and learning costs price Claude cache writes at
  1.25x or 2x input; `stats` shows a `CACHE WRITE` column.
- `duolog peer status` reports a Claude peer stopped by a denied prompt as
  `interrupted`; `peer send` types over Claude's dim input suggestion.
- Workers may stop and read their own background tasks and read their own
  saved tool results without asking; `DUOLOG_` and `REX_` stay out in any case.
- `duolog skill test` turns off only Codex's MCP servers, by bare name, and a
  calibration cell over `cell_max_tokens` ends as over the limit.
- Live peers get a duolog run id, and two projects with the same folder name
  no longer share Rex session numbers.
- A release's `install.ps1` and `install.sh` install that release, not the
  latest; `install.ps1` adds duolog to the user PATH and keeps other variables.
- The gate sees the command behind a keyword or wrapper, judges every
  redirect, and finds an outside path after `--glob`-style options.
- A worker can no longer approve its own ask by running `duolog.cmd` or
  `duolog.ps1` on Windows.
- A delegated task inlines no protected store and names the Read references
  it could not read; warnings on a long result line are no longer cut off.
- `duolog remember` runs under a lock, and telemetry runs one prune pass when
  many duolog processes start at once.
- `install.sh` works with busybox `sh` and `wget`, so Alpine needs no curl or
  bash; CI checks it and the static binaries on Alpine and old Linux.

## 1.4.0

### Added

- `duolog quota [--json]` reads Claude and Codex subscription limits without a
  model call and clears stale unavailable marks when quota is available.
- `duolog calibrate`, run by the `duolog-calibrate` skill on request, replays
  past tasks, has you confirm blind verdicts and proposes an order per class.
- Calibration admits steps within an estimated 10% of each weekly window and
  refuses to run while `env_passthrough` lists an API key.
- With learning mode off, `duolog learn` and `calibrate propose` say that an
  order routes nothing until `learning.enabled: true`.
- Local telemetry is on by default and keeps usage for up to 30 days on this
  machine; `duolog telemetry off` stops it. See docs/telemetry.md.

### Changed

- Duolog Rex sessions with the same project folder name get `(n)` suffixes in
  creation order; a single session keeps its plain name.
- Peer panes decline plugin install offers and, with the default
  `on_missing.plugin: lead_first`, tell the lead.

### Fixed

- Read-only roles can use quoted patterns and `/dev/null` redirects without
  asking.
- `duolog peer wait` reports only its own peer's asks, skips asks whose
  asker stopped waiting, and no longer takes a busy pane for finished.
- Claude workers start without the Agent and Task tools, so they cannot bypass
  the gate with native subagents.
- Limit reset times with a zone are parsed, and a limit mark without a reset
  time expires.
- `duolog peer open` reads the current `rex new` reply; `peer send` types a
  short line and clears a failed try, so text is no longer typed twice.

### Security

- Workers get no `ANTHROPIC_API_KEY` or `OPENAI_API_KEY` and no Claude fast
  mode. Key-only logins fail; add a key to the default `env_passthrough` list.
- The gate denies worker requests for private duolog state, recursive searches
  included; see docs/permissions.md for its limits.
- Asks that name paths outside the project, `>|` redirects included, need you:
  a lead cannot allow them, and you run `duolog allow N --lead ID`.

## 1.3.0

### Added

- `duolog skill test` checks skills across models with plans and cost approval;
  `duolog-skill-check` guides testing after changes.
- `duolog coauthors` credits workers by agent and model, with an optional
  commit message hook. Set `coauthors.enabled: false` to turn it off.
- `learning.enabled` routes delegations by class, with model ladders,
  escalation and a local journal; see [Learning mode](docs/learning.md).
- `duolog task start`, `task finish` and `task outcome` mark tasks and report
  which models ran, their results and their cost.
- `duolog profile` replays a delegation in a worktree; a blind judge compares
  results, and `duolog verdict` records your decision.
- `duolog learn --apply` writes an approved model order per class to
  `.duolog/routing.learned.yaml`.
- `duolog learn --apply-class` adds a proposed task class or rule;
  `--decline-class` stops offering that group.

### Changed

- Peers use detached project Rex sessions and disable native notifications;
  `peer wait` reminds once after `rex.stuck_notify_minutes` (default 5).
- Parallel work uses separate worktrees and peer tabs without taking focus;
  CodeGraph read tools are allowed for every role by default.
- A lead session start removes task folders and `duolog put` snapshots unchanged
  for 7 days, and the sync records of removed worktrees.
- `duolog peer` settles safe prompts and gates commands; `rex.auto_answers`
  configures replies. Workers keep routine summaries short.

### Fixed

- `duolog peer` waits for busy panes, removes closed entries, separates
  worktrees and tabs, and reports queued questions.

## 1.2.0

### Added

- `on_missing.mcp: bridge` lends a lead's MCP server to a worker of the other
  agent, credentials in env only; `duolog caps` shows what can be lent.
- The gate checks MCP tool calls as `mcp` actions; `capabilities.mcp_tools` and
  `duolog remember mcp:<server>/<tool>` allow tools without replacing rules.
- duolog checks a worker's login and its MCP servers before it starts, runs the
  agent's login once, and hands the task to the lead when that fails.
- A `debug` role finds the cause of a bug in one repository on Codex `sol`,
  read-only; `debug_hard` stays on Opus for bugs across components.
- The result line counts the choices a worker made alone as `decisions=<n>`
  and names files changed outside Write as `outside=<paths>`.
- `duolog doctor` warns when the `rex` in PATH is a script, not the Rex CLI;
  duolog then skips it.

### Changed

- `review_intermediate` runs on `sol` instead of `terra`, which made false
  findings in half of its review runs in the cost study.
- Claude workers also get `autoMemoryEnabled: false` in their settings.

## 1.1.0

### Added

- `duolog remember` allows a command the user approved for workers from now
  on; `--native` also allows it for the lead.
- A `planner` role takes a whole feature: it reads the code, implements and
  checks on Codex.
- duolog puts `.duolog/brief.md` first in every worker's input, for rules that
  all workers need.
- Write roles may run check tools such as `.venv/bin/ruff`,
  `node_modules/.bin/eslint`, `go vet` and `cargo clippy`.

### Changed

- The Claude skill runs report commands, such as `duolog doctor`, unprompted.
- The skills install the version they shipped with, not the latest release.
- The gate reads quoted words and more read commands, such as `sed -n 'N,Mp'`
  and `jq`; it checks `grep` options, and an unknown one asks.
- A Codex lead in `workspace-write` with approval `on-request` lets workers
  edit, as Codex does, except `.codex/`; `untrusted` still asks.
- A gate denial names the command, and the worker retries once in a plainer
  form before it stops.
- Workers are told not to commit or ask the user; Claude workers skip auto
  memory and unneeded MCP servers, and `minimal` skips skills and CLAUDE.md.
- `duolog install` keeps backups of project files under `.duolog/backup/`,
  which Git ignores.
- A task may use `# Goal` or `### Goal` headings as well as `## Goal`.

### Fixed

- `duolog sync` waits up to 15 seconds for another run, not 2, on a slow disk.
- A hook no longer ignores input that starts with a byte order mark, and an
  ended session no longer wakes for a message that came as it ended.
- Codex workers keep the PATH that duolog passes; a login shell no longer
  resets it.

## 1.0.0

### Added

- Roles send each kind of work to an agent, model and effort that you set in
  `.duolog/config.yaml`.
- `duolog delegate` runs a task on a worker and returns one short line.
- `duolog peer` opens a second agent in a Rex pane and reports its state.
- A permission gate decides worker actions from project rules, with no model
  call.
- Modes `plan`, `ask`, `edits` and `full`. A worker's effort never exceeds the
  lead's.
- At a provider limit, duolog starts no new session and prints `run here:`.
- `duolog sync` generates agent and skill files for the other agent.
- Plugins for Claude Code and Codex from a custom marketplace, and
  `duolog install`.
- `duolog doctor`, `roster`, `caps` and `stats` report setup, routes, gaps and
  cost.
- One-line installers for macOS, Linux and Windows that verify SHA-256.
