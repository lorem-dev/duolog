# How to run live peers with Rex

This page explains `duolog peer`. It opens a second agent session using the
Rex command line and drives it with short messages.
Use it when you want a live session. Use `duolog delegate` when a one-shot
worker is enough.

## What you need

- The Rex terminal app, and `rex` on the `PATH` of the session that runs
  duolog. Without it, duolog looks in `REX_BIN_DIR`, then on macOS inside
  the Rex app. Another program can also be called `rex`, such as a Perl
  deployment tool from a package manager. duolog skips a `rex` that is a
  script, and `duolog doctor` names it.
- A lead session with duolog installed. See
  [Getting started](getting-started.md).
- The lead registered by its `SessionStart` hook. The `--lead` flag or the
  `DUOLOG_LEAD` variable names it.

## How a live session works

| Step | Command | What happens |
|---|---|---|
| 1 | `duolog peer open <role>` | Opens a pane or tab with the agent of the role and waits until the session is ready or asks a question |
| 2 | `duolog peer answer <id> <option>` | Answers the question that the session shows, by label or number |
| 3 | `duolog peer send <id> <task>` | Sends one line that points to a task file |
| 4 | `duolog peer wait <id>` | Keeps waiting for the session. The default limit is 30 minutes |
| 5 | `duolog peer status <id>` | Prints the state in one line |
| 6 | `duolog peer close <id>` | Closes the pane. Changed files stay |

A peer belongs to its role and work root. Opening that role again in the same
worktree reuses the peer. Opening it in another worktree starts a new peer.
For peers, `max_live_sessions` applies per lead, agent and work root.

The first peer creates a separate Rex session for the project, named
`duolog | <project folder name>`. It stays detached: duolog does not show it
on a client or select it. The person's session, windows, panes and focus stay
as they were. Every peer gets a tab named `duolog | <pane label>`, such as
`duolog | codex-planner`. Leads and linked worktrees share that project
session. Commands use the session recorded for each peer.

If Rex has several duolog sessions for the same project folder name, their
names end in `(1)`, `(2)`, and so on, in creation order. Creating or closing a
project session updates these numbers. A single session has no number.
Renaming is best effort; a failed rename does not stop opening or closing a
peer. Peer tab names stay the same.

Closing a peer removes its session entry; repeating that close is harmless.
Closing the last peer closes the project session too. If that close fails,
duolog keeps its identity and another close retries the cleanup.

Each command prints one line, such as `<id> <state>: <question or note>`. The
lead decides questions that the rules do not settle.

`duolog peer wait` reports the state, not the result. `task finished` means the
session went idle. A working or spinner line keeps it busy even when its
input prompt is visible. Duolog opens Codex's queued question panel to read
the question and options. The lead or an auto-answer rule can then answer it.
The peer's answer stays in its pane, and in any files it
wrote. duolog reads the pane only to learn its state. When the lead needs the
answer as data, use `duolog delegate`, which returns it and records its spend.

## How to run several peers at once

Run independent tasks in parallel. Give each task its own linked worktree
under `.worktrees/`, and keep that directory git-ignored. Each task gets one
worker and its own peer in a separate Rex tab. Never put two workers in one
worktree. Run full test suites one at a time.

When work needs an open pull request, stack its branch on that pull request's
branch. Start the work while its dependency's CI runs.

Run each wait as its own background command: one for each peer and one for
each CI run. Do not chain them. New peer tabs stay in the detached project
session and keep the person's current window and pane focused.

## How duolog settles routine questions

Peers start with Codex's `tui.notifications=false` and `notify=[]`, or Claude's
`preferredNotifChannel: notifications_disabled`. These settings apply only
to the peer. The lead keeps its own notification settings.

`peer wait` sends questions to the lead on the bus at once. With reminders
enabled, it keeps watching until the question clears or a reminder is due.
After `rex.stuck_notify_minutes` (5 by default), it sends one OS notification
with the peer id and the question. Another wait does not repeat it. A new
question gets its own timer. Set the value to `0` to turn reminders off and
return questions at once. Missing OS notification tools are skipped silently.

`rex.auto_answers` lists rules with `match` text and an `answer` option label.
Duolog checks them in order and records each automatic answer. The first rule
whose text occurs in the question wins. If its option is missing, the lead
answers. A project list replaces the defaults; `auto_answers: []` turns it off.

The default matches `Hooks need review` and selects `Continue without trusting`.
The hooks stay off, and the peer keeps its sandbox. Validation refuses answers
starting with `Trust`, `Yes, and don't ask` or `Always`, ignoring case and outer
spaces. Duolog also checks the selected option before answering.

Codex command prompts go to the duolog permission gate with the peer's role.
The gate judges the command after leading `NAME=value` assignments. An allow
selects `Yes, proceed`; a deny selects `No`. Only an ask reaches the lead, with
the command shown. Text rules cannot override a command decision. The peer
keeps its own sandbox; this only answers its prompt.

A new session can also ask whether to trust the folder. For Claude, pressing
Enter alone quits. For Codex, the default is to trust. Duolog leaves that choice
to the lead and selects options by label rather than pressing a default.

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

## How to test the Rex flow by hand

The end-to-end tests cannot do this part, because it needs a person to answer
the trust question. Do it once for each release candidate.

With Claude as the lead:

- [ ] In Rex, run `duolog install --scope project`.
- [ ] Start a new Claude session in the project.
- [ ] Run `duolog peer open review_intermediate`. A Codex peer starts in the
  project's detached Rex session. Check that your window and pane stay focused.
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
