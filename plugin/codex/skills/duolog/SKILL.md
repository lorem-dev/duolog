---
name: duolog
description: >-
  Delegate bounded tasks to Claude or Codex with duolog for
  short answers or second opinions.
---

# duolog

## Before you start

1. Run `duolog version`. If not found, tell the user and offer the command
   below; run it only if they agree.

   macOS/Linux:

   ```sh
   curl -fsSL https://github.com/lorem-dev/duolog/releases/download/v1.3.0/install.sh | sh -s -- --version v1.3.0 && duolog install --scope project
   ```

   Windows PowerShell:

   ```powershell
   $env:DUOLOG_VERSION = 'v1.3.0'; irm https://github.com/lorem-dev/duolog/releases/download/v1.3.0/install.ps1 | iex
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
   List file ranges and edited docs under Write.
3. Run `duolog delegate <role> --task .duolog/task.md` in the background;
   wait for its notice.
4. Read summary, `git diff --stat`, marked hunks and `answer.json`'s
   `decisions=` only; skip full suites.
5. Keep hard parts; finish with `review_final`.

Put worker rules in `.duolog/brief.md`. With several leads, pass
`--lead <id>` from SessionStart. Learning mode: run `duolog task start` first.
Same-vendor subagents use your tools, not duolog.

## Running from Codex

Run `duolog delegate` outside the sandbox, or workers cannot start. For an old
`duolog version`, use the full path.

## When duolog says `run here`

- `run here: self`: read only the listed ranges.
- `run here: subagent model=M effort=E`: call `spawn_agent` with model M,
  reasoning_effort E and fork_turns "none", then `wait_agent`. Pass task
  text. Never start another duolog session.

## Permission requests

For `"p":true` bus asks, tell the person to answer; rewrite the task if needed,
but never answer yourself.

Foreground delegates deny asks; workers retry, then list commands in `needs`.
For ordinary asks use `duolog allow <n>` or `duolog deny <n>`, or
`duolog remember '<command>'` after the user's approval.

## Live sessions

Live sessions need Rex. Answer `duolog peer wait` with
`duolog peer answer <id> <option>`. Run each peer or CI wait as its own
background command. Run independent tasks in parallel, one worker per
`.worktrees/` worktree; stack dependent work on the open PR branch.

Never send `/model` or `/effort` to a Claude pane; they change its session.
