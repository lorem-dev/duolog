---
name: duolog
description: >-
  Delegate bounded tasks to Claude or Codex with duolog for
  short answers or second opinions.
allowed-tools: Bash(duolog version), Bash(duolog doctor), Bash(duolog roster), Bash(duolog route *), Bash(duolog caps), Bash(duolog caps *), Bash(duolog stats), Bash(duolog inbox *), Bash(duolog peer status *), Bash(duolog peer wait *)
---

# duolog

## Before you start

1. Run `duolog version`. If not found, tell the user and offer the command
   below; run it only if they agree.

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
2. Run `duolog doctor` once per session. If rules are off, tell the user
   what it names (`/hooks`) before delegating.

## Hand over

Do settled steps without explaining them; report results. Batch small
changes into one pull request unless the user asks otherwise.

1. Give a whole feature to `planner`; it reads, implements, checks and lists
   hunks to review. Do small fixes yourself.
2. Write `.duolog/task.md`: Goal, Read, Write, Limits, Check.
   Give known files or ranges; list edited docs under Write.
3. Run `duolog delegate <role> --task .duolog/task.md` with a long timeout.
   Wait for notice; do not poll.
4. Read summary and `git diff --stat`; open marked hunks only. Read
   `answer.json` only for `decisions=`. Skip full test suites.
5. Keep hard parts and final review, or use `review_final`.

Put worker rules in `.duolog/brief.md`. With several leads, pass
`--lead <id>` from SessionStart. Learning mode: run `duolog task start` first.
Subagents of your own vendor use your tools, not duolog.

## When duolog says `run here`

- `run here: self`: do it here, reading only the listed ranges.
- `run here: subagent model=M effort=E`: use the Agent tool with model M, effort
  E and the task text. Never start another duolog session.

## Permission requests

A foreground delegate denies asks; the worker retries, then lists the command
in `needs`. Later run `duolog allow <n>` or `duolog deny <n>`, and
`duolog remember '<command>'` if the user allows it for good.

## Live sessions

Live sessions need Rex. `duolog peer wait` returns for a question; answer it
with `duolog peer answer <id> <option>`. Run each peer wait alone in the
background.

Never send `/model` or `/effort` to a Claude pane; they change your own
session.
