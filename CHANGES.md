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

### Fixed

- A lead whose context is large tells you once that a fresh session can take
  over, then goes on working; it no longer stops to wait for your answer.
- The lead's notice line no longer names `run here` answers, which started no
  worker and which the lead already has.

## 1.9.1

### Fixed

- `duolog install` keeps duolog's hooks in a project's `.claude/settings.json`
  when the plugin is on; the plugin's own hooks step aside, so each runs once.
- Install reads the Claude plan from `claude auth status` and names its fields;
  a failed read keeps the lead's window instead of dropping to the smallest.
- `duolog doctor` gives each check its own time, and says when another duolog
  process holds the account state instead of a bare deadline error.
- MCP `delegate` with `wait: false` answers `run_here` before any quota read,
  and `running` answers and `status` name the admission wait.
- A new worktree gets Codex's approval of duolog's own hooks from the main
  checkout; a run that still cannot use Codex there falls back to Claude.
- A failed accounting step after the worker exits no longer marks a done run
  failed; MCP `action: result` finds runs in a nested project's submodule.

## 1.9.0

### Added

- `duolog runs` and `duolog watch` show `duolog delegate` runs from the CLI
  too, and MCP `status` lists them.
- `duolog statusline` prints the lead's worker line; `duolog install` sets it
  as Claude's `statusLine` if none is set, and `--status-line` adds it again.
- The lead's hooks say in one line when a run ends, a peer idles or an ask
  waits; a Claude `Stop` also sends OSC 9 (`lead.notify: false` turns it off).
- A desktop notice tells of an ask for the person and of a failed MCP run;
  `duolog doctor` says where to set Codex's `tui.notifications` for one.
- Codex leads get the duolog MCP server: `duolog install` adds
  `[mcp_servers.duolog]` to Codex's `config.toml`, keeping every other line.
- `duolog runs --snapshot [--fast]` prints this lead's peers, runs, asks and
  load as one JSON line; `--fast` reads no pane screen.
- A `/duolog-pane` pane in Claude Code lists peers and runs; the worker count
  above the prompt opens it. `duolog runs tail <id>` pages a run's log for it.
- `duolog install` installs the duolog plugin for Claude and Codex when it is
  missing (`--no-plugin` skips it); `duolog doctor` warns when it is missing.

### Changed

- `duolog runs` lists only this lead's delegate runs; duolog keeps the newest
  50 finished delegate records per project.
- `workers.compact_ladder` gives Pro 300000 tokens, up from 250000; the $200
  plan, which Claude reports as `max`, counts as `ultra` (700000).
- Workers may run `mkdir` and `git rev-parse`; a task may add sections such as
  `## Notes`; a rule like `UE_ROOT=* task ue:*` allows the variables it names.
- `duolog watch` prints a log line over 1 MiB as `> (output of N MiB not
  shown)`; a `watch --tab` tab closes 10 s after its run ends.

### Fixed

- duolog finds the right lead for Codex threads that share one app-server and
  for Claude bg spares, by the session ID the agent names.
- The gate no longer takes a package path that ends in `claude` or `codex`
  for an agent start in any place, as in `go test -v ./claude ./pkg`.
- `duolog delegate` refuses a task whose Read names a `.duolog/` file workers
  may not read, before a worker starts and stops on it.

## 1.8.2

### Fixed

- `duolog runs` and `duolog watch` find delegate runs started through the MCP
  server in duolog submodules from the superproject, and the reverse.

### Security

- Release binaries are built with Go 1.26.11, which fixes GO-2026-6859 in
  `net/http`.

## 1.8.1

### Added

- One lead serves a superproject and its duolog submodules; peers and
  `delegate` use each submodule's own rules.

### Changed

- `peer queue add --cwd` refuses submodules without `.duolog/config.yaml`.
- Close peers in a submodule before upgrading; its old lead state is no
  longer read.
- A "no window headroom" refusal shows its sum: used, held by running tasks,
  this task's cost and the reserve, such as `9% used + 5% this task + 10%`.

### Fixed

- Codex and Claude workers of a role with no history are no longer refused
  with "no window headroom" while the quota window has plenty of room left.

## 1.8.0

### Added

- A task's `Run` section lists commands besides checks, such as generators;
  `delegate` refuses before a worker starts when the gate would stop one.
- Install in one line: the release asset `install-prompt.md` lets Claude Code
  or Codex install duolog and ask whether to set up the current project.
- The `duolog-update` skill updates duolog, its plugin and the project's
  hooks; `duolog --version` and `duolog -V` print the version.

### Changed

- `install` names each Codex hook field it changes, old and new value; Codex
  skips that hook until you approve it again in `/hooks`.
- The lead runs `review_final` without asking first, and ends a turn only with
  a result or a decision for the person.
- `doctor` and project `install` warn when Codex does not trust the folder; a
  refusal names the folder and why a worktree did not get the trust.

### Fixed

- A delegate worker no longer stops when a project's workflow skill is missing;
  the project's other rules still apply.
- `duolog peer list` and `duolog runs` show a peer at work as `working`, not
  `starting`.
- A worktree gets the Codex trust of the lead's checkout also when `delegate`
  or `peer open` runs inside that worktree.
- `delegate` no longer crashes when the route changes during admission; a
  paid-run refusal says why the quota read failed.
- A worker reads a skill linked from `~/.agent/skills` into the agent's config
  folder; only that skill tree opens, not the config folder.
- `duolog skill test` reads an answer that a model wraps in one code fence.
- A Claude plan with only a weekly window, whose `/usage` shows none, no
  longer makes every Claude worker paid: one short Haiku call reads it.

### Security

- Workers can no longer run `install`, `uninstall` or `decisions`; a worker in
  a worktree keeps the main checkout's local denies.
- Decision and request logs, and ask summaries, withhold commands that carry
  credentials, such as `curl -u` or an `Authorization` header.

## 1.7.0

### Added

- `install` and a lead's next prompt tell you to restart running sessions when
  the MCP server or skills are new to them.
- `quota` reads a Codex enterprise seat's `individualLimit` as an `individual`
  window, and retries a passing Claude `/usage` failure.
- `duolog runs` lists live peers and delegate runs in one table; `duolog watch`
  follows one for the person, in the terminal or a Rex tab.
- A role with `balance: false` stays on its agent under pace balancing, as
  `planner` does; `review_cross` is a Codex review for a large batch.
- A lead with a large context is told to offer a fresh session when no worker
  runs; set `lead.fresh_session_hint`, or `0` to turn it off.

### Changed

- An ask only the person can answer waits 10 minutes; run `duolog install` for
  the longer hook timeouts, then approve the Codex hooks again.
- The MCP `delegate` tool waits and returns the whole result; with `wait: false`
  it returns at once, and a repeat of a running task returns that run.
- `delegate` names blocked Check commands before a worker starts; a task within
  `routing.small_task_files` answers `run here` (`--skip-preflight`, `--force`).
- Codex workers get Go caches under the temp folder, one per project, so Go
  builds need no `writable_roots`. A worker no longer hands its task on.
- Workers and peers are named after their role and lead; `duolog send` to a
  worker from anyone else goes to its lead.

### Fixed

- `doctor` names each `AWS_BEARER_TOKEN_*` variable that makes Claude workers
  paid, and no longer calls a Rex CLI binary a script.
- `duolog peer` and the MCP `peer` tool work in the peer's worktree and ignore
  the caller's Git variables; a peer can print its own id without an ask.
- The lead waits for busy peers (`peer wait --after-ask N`, the `Stop` hook) and
  brings their changes back to its starting checkout before reporting done.
- Workers read installed agent skills, and quoted `rg` patterns, `cmp` and
  `git worktree list` no longer stop them; `remember` keeps `learned.yaml`.

### Security

- Search and Git pathspec globs are judged by what they can expand to; reads of
  duolog state behind a wrapper are denied; built paths need the person.

## 1.6.0

### Added

- `install` adds `duolog mcp`, a lead-only MCP server with `delegate`, `peer`,
  `answer_ask` and `status`; see getting-started.
- `install` writes the duolog skills at user scope and refreshes them when the
  binary is newer, keeping one you edited; `uninstall` removes them everywhere.

## 1.5.2

### Added

- `install` keeps one marked block in the instruction files, with a usage line
  for agents, and replaces it on each run; `doctor` and `--check` report it.
- Codex trusts the lead's own linked worktrees and duolog's hooks there by
  itself, when their `.codex/` matches the root that Codex already trusts.

### Changed

- Claude workers and peers start in `auto`, not `acceptEdits`, under a lead at
  level `full` or a Claude lead in `auto`.
- A peer is told to put its final answer in its report file; `peer wait` and
  `status` print the report's start, or say that the task had none.
- Claude workers and peers are told to batch their reads into one turn and to
  use no `node -e` or `python -c`, which cuts their turns and cost.
- An unknown plan gets the smallest compact window, not Claude's 1M default,
  and `install` gives the lead the same window in `.claude/settings.local.json`.
- The lead's hooks repeat each open ask until it ends; the gate reads
  `$(go env GOROOT)/bin/go` as `go`, and `go env -w` asks the person.
- A quota refusal names its window; `doctor` warns about unapproved project
  MCP servers; `peer open --new` opens a second peer of a role.

### Fixed

- A lead registered in a submodule's main checkout is found from its linked
  worktrees; `--lead` names the state folder when it finds no live lead.
- `peer send` no longer refuses an idle Claude peer with a dim suggestion, and
  a dead queue run's lock no longer blocks `peer queue`.
- `peer close` removes the peer's `coauthor-<id>.model` file, and `peer open`
  removes the ones that closed peers of older versions left behind.
- Workers are no longer refused or asked for `go test` on a `./x/claude` path, a
  local `DOCKER_HOST`, `rg` on a `.duolog` attachment, or their own temp files.
- On Windows a telemetry prune no longer fails when another duolog creates the
  prune stamp at the same moment, and a failed prune now names its cause.
- A Claude peer in your own project no longer stops at its folder-trust and new
  MCP server prompts; trust and servers follow what you already decided.

### Security

- A path out of the work root after `cd`, `..` after a symlink, a dangling link,
  `x=$(cat /x)` or a `grep` next to `$P` or `*` needs the person again.

## 1.5.1

### Added

- `agents.codex.skills_dir` moves Codex's project skills. 1.5.0 refuses it, so
  a repository that sets it needs this release.
- `install --check` (`--hooks-only` for CI) and `doctor` find hooks and skills
  an older duolog installed; the lead's session start names the fix.
- A worker reads the files outside the project that a task in `.duolog/`
  lists under Read as absolute paths, folders or globs; denies still apply.

### Fixed

- The worker gate no longer refuses a report as a start of `claude` or `codex`
  when a here-document or quoted text only names them.

### Security

- `wc`, `du`, `sort` and `find` with `files0-from` or a start of it, which read
  the files a list names, now ask, as does an unknown long option of `wc`/`du`.
- `shasum -c` and `--check` of `sha256sum`, `md5sum`, `b2sum`, `cksum` and the
  like read every file their list names, so they now ask instead of reading.

## 1.5.0

### Added

- `worker_modes` sets a worker's native mode per role and family, never above
  the lead's. At plan, a worker runs MCP tools only with a `read_only` rule.
- `paid_runs.<agent>.allow` runs workers and peers paid out of quota, up to
  `daily_cap_usd` a day; `usable_share` caps a workspace account's window use.
- `limits.max_total_sessions` and `limits.sessions_by_tier` cap live sessions
  per account; a delegation waits `limits.session_wait_seconds` for a slot.
- `agents.<name>.enabled: false` switches an agent off: it gets no task, and
  duolog builds no worker for it and reads none of its quota.
- `duolog quota` and `doctor` show billing, spend, sessions and `held:` panes;
  a timed-out quota read retries once; `peer release <id>` frees a closed one.
- `routing.balance: pace` starts a new task on the role's `fallback` while its
  `use` agent runs ahead of its window, never onto a paid route.
- `peer list --long`, `show`, `view` (Rex Remote Control) and `duolog-peers`
  show peers; leads hear of stalled peers; bypass/auto skips person-only asks.
- The lead sees its load (`duolog peers:`), hears when a peer ends a turn or
  exits, and can queue peers that open once a session is free (`peer queue`).

### Changed

- Codex workers get no network and no extra writable roots, even without
  `worker_modes`; give a role `network: true` in `worker_modes` to restore it.
- Free workers, peers and calibration drop per-token API keys even if listed in
  `env_passthrough`, so calibration starts; `duolog doctor` shows the fix.
- The prices in `models` now drive admission of paid runs. They leave out fast
  mode and cloud surcharges, so spend is a lower bound.
- Project config and `.duolog/config.local.yaml`, tracked or not, may only
  tighten: higher `models` prices, fewer `limits`. Raise `limits` at user level.

### Fixed

- After a Rex restart, `duolog peer list` shows the lead's peers as `gone`, and
  `peer close` frees their records and session slots.
- Telemetry's prune stamp keeps one name at all times, so a racing process no
  longer finds the store unsafe, which could last on Windows.

### Security

- Codex peers ignore `network_access`/`writable_roots` from the project; workers
  cannot start `claude`/`codex` via any path, `.cmd`/`.ps1`, runner or `env`.

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
