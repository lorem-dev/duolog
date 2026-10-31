---
name: duolog
description: >-
  Delegate a bounded task to a second Claude or Codex session with duolog and
  read its short answer. Use for self-contained work or a second opinion.
---

# duolog

Delegate self-contained work. Do small edits yourself. Commands also work in
PowerShell. Real agents on Windows have not been run.

## Hand over

1. Write `task.md` with sections Goal, Read, Write, Limits, Check, in order.
2. List Read files by line range. Denied reads stay references; they are not
   inlined.
3. Run `duolog delegate <role> --task task.md`. Use a long shell-tool timeout;
   split tasks that exceed the host's limit.
4. Read the summary and needed file ranges. Long answers point to `answer.json`.

The lead resolves automatically. With several leads, pass `--lead <id>` from the
SessionStart line. Missing leads refuse delegation.

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

On Windows use `duolog delegate`; live sessions need Rex on macOS/Linux.
Use `duolog peer` send, wait and answer. `peer wait` returns when a gate ask
arrives. Put long content in an artifact.

## Never

Never send `/model` or `/effort` to a Claude pane. They change your own session
settings. Set model and effort through duolog instead. Approve Codex hooks in
`/hooks` before write work.
