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

   Windows: add the printed directory to PATH; run
   `duolog install --scope project`.
2. Run `duolog doctor` once per session. If rules are off, tell the user
   what it names (`/hooks`) before delegating.

## Hand over

Do settled steps without explaining them; report results. Batch small
changes into one pull request unless the user asks otherwise.

1. Give whole features to `planner`; do small fixes yourself.
2. Write `.duolog/task.md`: Goal, Read, Write, Limits, Check.
   Give known files or ranges; list edited docs under Write.
3. Run `duolog delegate <role> --task .duolog/task.md` in the background;
   wait for its notice.
4. Read summary, `git diff --stat` and marked hunks only. Read
   `answer.json` for `decisions=` only. Skip full test suites.
5. Keep hard parts; use `review_final` for final review.

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

Live sessions need Rex. Answer a `duolog peer wait` question with
`duolog peer answer <id> <option>`. Run each peer or CI wait as its own
background command. Run independent tasks in parallel, one worker per
`.worktrees/` worktree; stack dependent work on the open PR branch.

Never send `/model` or `/effort` to a Claude pane; they change your
session.
