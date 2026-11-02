---
name: duolog
description: >-
  Delegate a bounded task to a second Claude or Codex session with duolog and
  read its short answer. Use for self-contained work or a second opinion.
allowed-tools: Bash(duolog version), Bash(duolog doctor), Bash(duolog roster), Bash(duolog route *), Bash(duolog caps), Bash(duolog caps *), Bash(duolog stats), Bash(duolog inbox *), Bash(duolog peer status *), Bash(duolog peer wait *)
---

# duolog

## Before you start

1. Run `duolog version`. If not found, tell the user and offer the OS
   command below. Run it only if they agree.

   macOS/Linux:

   ```sh
   curl -fsSL https://github.com/lorem-dev/duolog/releases/download/v1.2.0/install.sh | sh -s -- --version v1.2.0 && duolog install --scope project
   ```

   Windows PowerShell:

   ```powershell
   $env:DUOLOG_VERSION = 'v1.2.0'; irm https://github.com/lorem-dev/duolog/releases/download/v1.2.0/install.ps1 | iex
   ```

   On Windows, add the printed directory to PATH, then run
   `duolog install --scope project`.
2. Run `duolog doctor` once per session. If the rules are off, tell the
   user what it names (`/hooks`) before delegating.

## Hand over

Do steps that the rules or duolog settle without explaining them; report
results.
Collect small changes into one pull request per batch unless the user asks
otherwise.

1. Give whole features to `planner`: read, implement, check, list review hunks.
   Do small fixes yourself.
2. Write `.duolog/task.md` with sections Goal, Read, Write, Limits, Check.
   Give known files or ranges; list docs and changelog under Write for workers.
3. Run `duolog delegate <role> --task .duolog/task.md` with a long timeout.
   Wait for notice; do not poll.
4. Read the summary and `git diff --stat`; open marked hunks only. Read
   `answer.json` only for `decisions=`. Skip full test suites.
5. Keep hard parts and final review, or use `review_final`.

Put worker rules in `.duolog/brief.md`. With several leads, pass
`--lead <id>` from SessionStart.

## When duolog says `run here`

- `run here: self`: do the task here, reading only the listed ranges.
- `run here: subagent model=M effort=E`: use the Agent tool with model M and
  effort E. Pass task text. Never start another duolog session.

## Permission requests

A foreground delegate blocks you, so an ask denies: the worker retries
plainer, then returns `done: false` with the command in `needs`. When free,
answer with `duolog allow <n>` or `duolog deny <n>`. If the user allows a
command permanently, run `duolog remember '<command>'`.

## Live sessions

Rex sessions: `duolog peer wait` returns for asks.

## Never

Never send `/model` or `/effort` to a Claude pane; they change your own
session.
