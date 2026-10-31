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
   curl -fsSL https://github.com/lorem-dev/duolog/releases/latest/download/install.sh | sh && duolog install --scope project
   ```

   Windows PowerShell:

   ```powershell
   irm https://github.com/lorem-dev/duolog/releases/latest/download/install.ps1 | iex
   ```

   On Windows, add the printed directory to PATH, then run
   `duolog install --scope project`.
2. Run `duolog doctor` once per session. If it says the rules are off, tell
   the user what it names, such as `/hooks` for Codex, and follow its steps
   before delegating.

## Hand over

1. Write `task.md` with sections Goal, Read, Write, Limits, Check, in order.
2. List Read files by line range. Denied reads stay references; they are not
   inlined.
3. Run `duolog delegate <role> --task task.md`. Use a long timeout.
4. Read the summary and needed file ranges. Long answers point to `answer.json`.

With several leads, pass `--lead <id>` from SessionStart.

## When duolog says `run here`

- `run here: self`: do the task here, reading only the listed ranges.
- `run here: subagent model=M effort=E`: use the Agent tool with model M and
  effort E. Give the task text. Do not start another duolog session.

## Permission requests

Foreground delegate blocks its lead. An ask then denies immediately: the worker
must stop with `done: false` and the decision in `needs`. Decide, adjust the
task or permitted policy/mode, then re-run. A deny rule remains binding.

When the lead is free, answer numbered requests with `duolog allow <n>` or
`duolog deny <n>`. Read full commands with `duolog decisions`.

## Live sessions

On Windows use `duolog delegate`. Live sessions need Rex on macOS/Linux.
`duolog peer wait` returns for a gate ask. Put long content in an artifact.

## Never

Never send `/model` or `/effort` to a Claude pane. They change your own session
settings. Set model and effort through duolog instead. Approve Codex hooks in
`/hooks` before write work.
