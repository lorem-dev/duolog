---
name: duolog
description: >-
  Use only in a project that has .duolog/config.yaml. Delegate bounded
  tasks to Claude or Codex for short answers or second opinions.
allowed-tools: Bash(duolog version), Bash(duolog doctor), Bash(duolog roster), Bash(duolog route *), Bash(duolog caps), Bash(duolog caps *), Bash(duolog stats), Bash(duolog inbox *), Bash(duolog peer status *), Bash(duolog peer wait *)
metadata:
  duolog-version: "1.6.0"
---

Stop unless this project has `.duolog/config.yaml`.

# duolog

## First

1. Run `duolog version`. If not found, tell the user and offer the command
   below; run it only if they agree.

   macOS/Linux:

   ```sh
   curl -fsSL https://github.com/lorem-dev/duolog/releases/download/v1.6.0/install.sh | sh -s -- --version v1.6.0 && duolog install --scope project
   ```

   PowerShell:

   ```powershell
   $env:DUOLOG_VERSION = 'v1.6.0'; irm https://github.com/lorem-dev/duolog/releases/download/v1.6.0/install.ps1 | iex
   ```

   Windows: add the printed directory to PATH; run
   `duolog install --scope project`.
2. Run `duolog doctor` once per session. If rules are off, tell the user
   what it names (`/hooks`) first.

## Hand over

Do settled steps without explaining them; report results.
If a PR is requested or required, batch small changes into one.

With the duolog MCP tools loaded, use `delegate`, `peer`, `answer_ask` and `status` instead of the commands below; they return `run_here` as data.
Use the CLI only if a tool is missing or fails.

1. Give whole features to `planner`; do small fixes yourself.
2. Before handoff, record the starting checkout, branch and existing edits.
   Write `.duolog/task.md`: Goal, Read, Write, Limits, Check.
   List file ranges and edited docs under Write.
3. Call `delegate`; its response is the result. If `running`, call `result`
   with the id as task_path (`status` lists runs).
   CLI: run `duolog delegate <role> --task .duolog/task.md` in the
   background; wait for its notice.
4. Read summary, `git diff --stat`, marked hunks and `answer.json`'s
   `decisions=` only; skip full suites.
   Batch nameable reads in parallel.
5. Keep hard parts; `review_cross` ends big batches, else `review_final`.

Worker rules: `.duolog/brief.md`. With several leads, pass `--lead <id>`. Learning mode: run `duolog task start` first.
Same-vendor subagents use your tools.

## On `run here`

- `run here: self`: read only the listed ranges.
- `run here: subagent model=M effort=E`: use the Agent tool with M, E and
  the task text, not duolog.

## Asks

For `"p":true` asks, tell the person; never answer yourself.
Foreground delegates deny asks; workers list commands in `needs`.
Use `duolog allow <n>`, `duolog deny <n>` or, with approval,
`duolog remember '<command>'`.

## Live sessions (Rex)

Answer `duolog peer wait` with `duolog peer answer <id> <option>`;
background each wait. Run independent tasks in parallel, one worker per
`.worktrees/` worktree; stack dependent work on that task's branch.

Before reporting done, review and bring the peer's task changes into the
starting checkout on its original branch, unless the person requested another
branch or PR. Keep changes
uncommitted unless a commit was requested or required; preserve existing
staged, unstaged and untracked work. If integration conflicts, report the
blocker instead of completion. Verify the integrated result there and tell
the person its path, branch and commit status.

Never send `/model` or `/effort` to a Claude pane.
