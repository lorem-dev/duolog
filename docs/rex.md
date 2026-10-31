# How to run a second session beside you with Rex

This page explains `duolog peer`. It opens a second agent session in a pane
next to yours, using the Rex command line, and drives it with short messages.
Use it when you want a live session. Use `duolog delegate` when a one-shot
worker is enough.

## What you need

- The Rex terminal app, and `rex` on the `PATH` of the session that runs
  duolog.
- A lead session with duolog installed. See
  [Getting started](getting-started.md).
- The lead registered by its `SessionStart` hook. The `--lead` flag or the
  `DUOLOG_LEAD` variable names it.

## How a live session works

| Step | Command | What happens |
|---|---|---|
| 1 | `duolog peer open <role>` | Opens a pane with the agent of the role and waits until the session is ready or asks a question |
| 2 | `duolog peer answer <id> <option>` | Answers the question that the session shows, by label or number |
| 3 | `duolog peer send <id> <task>` | Sends one line that points to a task file |
| 4 | `duolog peer wait <id>` | Keeps waiting for the session. The default limit is 30 minutes |
| 5 | `duolog peer status <id>` | Prints the state in one line |
| 6 | `duolog peer close <id>` | Closes the pane. Changed files stay |

Each command prints one line, such as `<id> <state>: <question or note>`. The
lead reads the question and decides. duolog never answers a question for the
lead.

`duolog peer wait` reports the state, not the result. `task finished` means the
session went idle. The peer's answer stays in its pane, and in any files it
wrote. duolog reads the pane only to learn its state. When the lead needs the
answer as data, use `duolog delegate`, which returns it and records its spend.

## Why the lead must answer every question

A new session can ask whether to trust the folder. For Claude, pressing Enter
alone quits. For Codex, the default is to trust. A wrong default would either
close the session or widen its access, so duolog leaves the choice to the
lead.

## What a worker may never receive

Never send `/model` or `/effort` to a Claude pane. They write to your user
settings, even when you start Claude with project-only settings. duolog
passes the model and effort at startup instead.

## Known limits

These come from the
[live verification](research/2026-10-30-live-verification.md).

- Rex worked from Claude in our runs (OBSERVED). From Codex, the shell could
  not find `rex`, and one sandboxed run could not reach its socket
  (OBSERVED). So Codex as the lead is not proven for `duolog peer`.
- Codex as the lead running `claude -p` through `duolog delegate` is not
  proven either. Run the end-to-end test to find out.
- Codex does not fire `SessionEnd` when its pane closes (OBSERVED), so a
  closed Codex pane can stay in the registry. `duolog doctor` warns about a
  pane whose lead has ended.
- Rex has no known Windows build. Live sessions are unavailable on Windows.
  `duolog peer` prints this limit; use `duolog delegate` instead.
- Real agents on Windows have not been run.

## How to test the Rex flow by hand

The end-to-end tests cannot do this part, because it needs a person to answer
the trust question. Do it once for each release candidate.

With Claude as the lead:

- [ ] In Rex, run `duolog install --scope project`.
- [ ] Start a new Claude session in the project.
- [ ] Run `duolog peer open review_intermediate`. A second Codex opens beside
  the lead.
- [ ] If the line shows a question, answer it with `duolog peer answer`,
  giving the session id and the option.
- [ ] Write a task file and run `duolog peer send <id> task.md`.
- [ ] Run `duolog peer wait <id>`. It prints `task finished` when the session
  goes idle, and the answer is in the pane.
- [ ] Check that the lead's context got one line, not the whole answer.
- [ ] Run `duolog peer close <id>`.

With Codex as the lead, repeat every step with the roles swapped. Open a Claude
peer with a role whose `use` agent is `claude`. Write down any permission that
Codex needs to start `rex`. If one is missing, report it to the author.

To run the automatic part, set `DUOLOG_E2E_REX=1` inside a Rex pane and run
`go test -tags=e2e ./test/e2e/ -run TestCodexLeadOpensPeer -v`. The test is
skipped otherwise.

## Related pages

- [Permissions](permissions.md): how duolog decides for a worker
- [Bus](bus.md): the messages a live session sends
- [Live verification](research/2026-10-30-live-verification.md): the Rex
  results
