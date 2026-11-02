---
name: duolog
description: >-
  Delegate a bounded task to a second Claude or Codex session with duolog and
  read its short answer. Use for self-contained work or a second opinion.
---

# duolog

## Before you start

1. Run `duolog version`. If it is not found, tell the user and offer the
   command for this OS below. Run it only if they agree.

   macOS/Linux:

   ```sh
   curl -fsSL https://github.com/lorem-dev/duolog/releases/download/v1.0.0/install.sh | sh -s -- --version v1.0.0 && duolog install --scope project
   ```

   Windows PowerShell:

   ```powershell
   $env:DUOLOG_VERSION = 'v1.0.0'; irm https://github.com/lorem-dev/duolog/releases/download/v1.0.0/install.ps1 | iex
   ```

   On Windows, add the printed directory to PATH, then run
   `duolog install --scope project`.
2. Run `duolog doctor` once per session. If it says the rules are off, tell
   the user what it names, such as `/hooks`, before delegating.

## Hand over

1. Give a whole feature to `planner`: it reads, implements, checks and lists
   hunks to review. Do a small fix here yourself.
2. Write `.duolog/task.md` with sections Goal, Read, Write, Limits, Check.
   Give files or line ranges you know. Put a changelog or docs under Write if
   the worker edits them.
3. Run `duolog delegate <role> --task .duolog/task.md` with a long timeout.
   Wait for its notice; do not poll.
4. Read the summary and `git diff --stat`; open only marked hunks. Skip
   `answer.json` and full test suites.
5. Keep hard parts and the final review, or use `review_final`.

`.duolog/brief.md` holds rules for every worker. With several leads, pass
`--lead <id>` from SessionStart.

## Running from Codex

Run `duolog delegate` outside the sandbox, or workers cannot start. An old
`duolog version` means PATH found an older copy; use the full path.

## When duolog says `run here`

- `run here: self`: do the task here, reading only the listed ranges.
- `run here: subagent model=M effort=E`: call `spawn_agent` with model M,
  reasoning_effort E and fork_turns "none", then `wait_agent`. Give the task
  text. Do not start another duolog session.

## Permission requests

A foreground delegate blocks you, so an ask denies: the worker retries
plainer, then returns `done: false` with the command in `needs`. When free,
answer with `duolog allow <n>` or `duolog deny <n>`. If the user allows a
command for good, run `duolog remember '<command>'`.

## Live sessions

Live sessions need Rex; `duolog peer wait` returns for an ask.

## Never

Never send `/model` or `/effort` to a Claude pane; they change its settings.
