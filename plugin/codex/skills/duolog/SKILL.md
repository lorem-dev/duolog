---
name: duolog
description: Delegate a bounded task to a second session (Claude or Codex) with duolog and read back a short answer. Use when a task is large, mechanical, or needs a second opinion.
---

# duolog

Delegate when a task is self-contained and its result fits in a few lines. Do small edits yourself.

## Hand over

1. Write `task.md` with exactly these sections, in order: Goal, Read, Write, Limits, Check.
2. In Read, list files with line ranges, never whole files. Workers read only those ranges.
3. Run `duolog delegate <role> --task task.md`.
4. The answer is one summary line plus a file list. Open the files you need, by range.

## Running duolog from Codex

Codex runs a login shell that resets `PATH`, so a bare `duolog` can fail with "command not found". Run the binary by its full path (`command -v duolog` in your own terminal prints it). To start a Claude worker (`claude -p`), the Codex session must run with `--sandbox danger-full-access`: in `workspace-write`, `claude -p` starts but cannot read its login and says "Not logged in". A Codex worker needs no extra permission.

## When duolog says `run here`

- `run here: self`: do the task yourself in this session. Read only the listed ranges.
- `run here: subagent model=M effort=E`: call `spawn_agent` with `model` M, `reasoning_effort` E and `fork_turns "none"`, and give the task text as the message. Then `wait_agent`. Do not start another duolog session.

## Permission requests

A worker that needs approval sends you a numbered request. Answer with `duolog allow <n>` or `duolog deny <n>`. Check `duolog decisions` for the reasons.

## Live sessions

For a running second session, use `duolog peer ...` (send, wait, answer). Do not paste long text: put it in an artifact and send the reference.

## Never

Never send `/model` or `/effort` to a Claude pane. They change that session's own settings. Pass model and effort through duolog instead.

Hooks need approval: if duolog hooks do not fire, review them in `/hooks`.
