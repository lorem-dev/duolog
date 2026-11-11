# How to run live peers with Rex

`duolog peer` drives a live second agent session through the Rex CLI. Use
`delegate` for a one-shot worker.

## What you need

- The Rex terminal app, and `rex` on the `PATH` of the session that runs
  duolog. Without it, duolog looks in `REX_BIN_DIR`, then on macOS inside
  the Rex app. Another program can also be called `rex`, such as a Perl
  deployment tool from a package manager. duolog skips a `rex` that is a
  script, and `duolog doctor` names it.
- A lead session with duolog installed. See
  [Getting started](getting-started.md).
- The lead registered by its `SessionStart` hook, or by its first prompt.
  The `--lead` flag or the `DUOLOG_LEAD` variable names it.

Claude Code can start a session process ahead of time, a bg spare, before
anyone uses it. Its `SessionStart` registers no lead: the session you
start in it runs under another ID, and your first prompt registers that one.
Claude Code runs one session per process, so a new lead in a process, as
after `/clear`, ends the lead that was there before. Codex runs every thread
in one process, so a new Codex lead ends no other.

## How to register a lead that its hooks missed

The project's hooks do not run for a session that started in another folder.
They also miss a session resumed here after a restart. Then `duolog peer open`
says `no live lead`. Ask that session to run this command in its own shell:

```sh
duolog lead register [plan|ask|edits|full]
```

A linked worktree shares the lead of its repository's main checkout.
A configured duolog submodule also finds its superproject's lead.
A lead of an unrelated repository is never found. If no live lead is found
for a submodule, the error names its work state and the shared lead home.
Old work-local lead files are ignored; close submodule peers before upgrading.

duolog reads the session ID that the agent sets in its shell. Claude sets
`CLAUDE_CODE_SESSION_ID`, and Codex sets `CODEX_THREAD_ID`. If the ID is
missing, the command refuses. duolog records the agent process above the
shell, as the hook does.

The mode you give can only be at or below the mode that duolog observes in the
session's transcript. Without that observation, the mode is at most `ask`.
Without a mode, duolog uses the observed one, or `ask`. A worker cannot
register a lead.

## How a live session works

| Step | Command | What happens |
|---|---|---|
| 1 | `duolog peer open <role>` | Opens a pane or tab with the agent of the role and waits until the session is ready or asks a question |
| 2 | `duolog peer answer <id> <option> [--reason <text>]` | Answers the question that the session shows, by label or number |
| 3 | `duolog peer send <id> <task>` | Sends one short line that points to a task file, by its path from the pane's folder |
| 4 | `duolog peer wait <id>` | Keeps waiting for the session. The default limit is 30 minutes |
| 5 | `duolog peer status <id>` | Prints the state in one line |
| 6 | `duolog peer close <id>` | Closes the pane. Changed files stay. A pane that is already gone is not closed again; the line says so |
| 7 | `duolog peer list [--long]` | Lists your peers. Plain output is `<id> <state>`; `--long` adds role, agent, task, last activity and tab. The last line is the load line. It presses no key |
| 8 | `duolog peer show <id> [--lines N]` | Prints the last lines of the peer's screen (40 by default, 1 to 200). It refuses inside an agent |
| 9 | `duolog peer view <id> [--client C]` | Switches your Rex client to the peer's tab and prints one line |

When `peer open` stops after the pane started but before it recorded the
pane, the pane's account hold stays. `duolog quota` and `duolog doctor` show
it as a `held:` line. Close the pane in Rex, then run
`duolog peer release <id>`.

A peer belongs to its role and work root. Opening that role again in the same
worktree reuses the peer. Opening it in another worktree starts a new peer.
`duolog peer open <role> --new` opens another peer of the role even when one
is live in that worktree, so two independent tasks of one role run side by
side. It keeps the session limits: with `max_live_sessions` reached it is
refused as any open is.
For peers, `max_live_sessions` applies per lead, agent and checkout group.
Each checkout of the lead home's project has its own group. Submodule
worktrees share the group of their top superproject checkout. A peer whose
work root was deleted does not count against the cap.
`duolog peer send` refuses a task file that is not under the peer's own work
root, so a task cannot run in the wrong worktree. A relative path is read from
the worktree you run the command in.

Peers use a detached Rex session, `duolog | <project folder name>`, shared
by leads and linked worktrees. Each tab is `duolog | <pane label>`, such as
`duolog | codex-planner`. Only `peer view` and `watch --tab` move your focus.

Duplicate project-session names get `(1)`, `(2)` and later numbers in
creation order. Creation and closure update them; renaming is best effort.
Commands use the session recorded for the peer. Closing removes its entry;
closing again is harmless. The last peer closes the project session. A
failed session close keeps its identity so another close can retry.

Each command prints `<id> <state>: <question or note>`. Questions the rules
do not settle go to the lead.

## How to recover after a Rex restart

A Rex server restart ends every Rex session and pane, and the lead's agent
comes back in a new Rex session. The peers' records stay in
`.duolog/sessions.json`, with their account session slots. `duolog peer list`
(plain, `--long` and `--json`) still shows the peers of your lead, and marks
each peer whose pane Rex no longer knows as `gone`. Only `list` reports
`gone`; `status` and `wait` report such a pane as `closed`. A peer opened from
another Rex session is still your lead's peer. Peers of another lead stay
hidden.

`duolog peer close <id>` on a `gone` peer frees its record and its account
session slot, asks Rex to close nothing, and prints
`<id> closed; its pane was already gone`.

`duolog peer send` checks the typed task line before it presses Enter. It
presses Enter only when the input holds exactly the line, even when the line
wraps over several rows. Otherwise it clears the input with ctrl+c and types
the line again, three tries in all. If every try fails, it leaves the task
unsubmitted and reports an error. An empty input gets no ctrl+c, because
there it starts the agent's exit.

An input that shows only the agent's suggestion counts as empty, so
`duolog peer send` types over it. duolog tells a suggestion from typed text
by its look: Claude draws suggestions dim or grey, in the semicolon or the
colon form of an SGR colour. Claude's `Try "..."`
placeholder also counts, even when it wraps or is cut at the pane's edge.
duolog reads the input line with `rex capture --format vt`, because Rex's
default format drops the styles; without them, duolog takes the text for
typed text and refuses to send.

A peer reads inside the checkout it works in. Put its task file and the
files the task attaches in that checkout's `.duolog` folder as Markdown. In a
linked worktree that is the worktree's own `.duolog`. Peers get no Read list,
so a read of the main checkout asks the person.

A peer writes its answer to `.duolog/tasks/<peer-id>/report.md` in its own
checkout. Its prompt hook and the typed task line name that path.

| Rule | Effect |
|---|---|
| Report exception | Only that peer may write its report, even in plan mode or a read-only role. Other state writes stay denied |
| Codex read-only sandbox | May refuse the report write before the gate sees it |
| Own id | `echo "$DUOLOG_PEER"` or `printenv DUOLOG_PEER` needs no ask; no other variable or longer line is covered |
| New task | `peer send` removes the previous report |
| Status and wait | Print `report <path>` and at most 15 lines/1500 characters, then `[report cut]` if needed; an ended task without it says `no report written` |
| Bus | The bounded `peer-idle` line adds the report path once it exists |
| File check | Must be a regular file in the peer checkout; links that lead out are ignored |

Claude read-only peers start in `default`; the gate keeps them read-only
except for their report. Open refuses when gate preflight fails. A peer
opened under a lead in `plan` stays there after the lead's mode rises. See
[Permissions](permissions.md#how-a-workers-native-mode-is-chosen).

## What to do when a peer is interrupted

A peer is `interrupted` when its last line above the input is the agent's
interrupt notice. After a denied permission prompt or Esc, Claude prints
`Interrupted` and asks what it should do instead. Codex prints
`Conversation interrupted`. The agent then waits, and its task stays
unfinished. `duolog peer status`, `wait` and `list` report this state, so the
lead knows that the peer needs a follow-up.

You have two ways to give that follow-up:

- Deny with a reason: `duolog peer answer <id> No --reason "<one line>"`.
  duolog selects `No`, waits for the interrupted input and types the reason
  after the words `Reason:`. The agent reads it as its reply and goes on.
  The fixed start keeps a reason that begins with `!`, `#`, `$`, `@` or `/`
  from being read as a shell command or a mode switch. A reason with a line
  break or another control character is refused. If the pane is not
  interrupted, or its input holds text, duolog sends nothing and says so.
- Send a task: `duolog peer send <id> <task>` works on an interrupted peer
  as on a ready one.

`duolog peer wait` reports the state, not the result. `task finished` means the
session went idle. A working or spinner line keeps it busy even when its
input prompt is visible. Duolog opens Codex's queued question panel to read
the question and options. The lead or an auto-answer rule can then answer it.
The peer's answer stays in its pane, and in any files it
wrote. duolog reads the pane only to learn its state. When the lead needs the
answer as data, use `duolog delegate`, which returns it and records its spend.

## How to bring a peer's changes back

Record the starting checkout, branch and existing edits before handoff.
Unless the person requests another branch or PR, review and integrate the
peer's changes there by merge, cherry-pick or patch. Keep them uncommitted
unless a commit is requested or required, and preserve staged, unstaged
and untracked work.

Verify the integrated result before reporting completion. On conflicts,
preserve the person's work and report the blocker. Report the result's
path, branch and commit status; idle does not mean delivered.

## What the person is told between turns

When a worker finishes while the person looks elsewhere, the lead's hooks put
one line in the terminal. It is the hook's `systemMessage`, which Claude Code
and Codex show to the person and do not pass to the model, so it costs no
tokens and starts no turn. It appears with `SessionStart`, with
`UserPromptSubmit` and when the lead's `Stop` hook runs:

- `duolog: run r2 (review) failed`: a delegate run ended. Its state is `done`,
  `failed`, `needs input`, `run here` or `lost`.
- `duolog: peer p1 is idle`: a peer ended a turn, as its `Stop` hook
  recorded. The fast snapshot cannot see a working pane, so a running peer
  without a recorded turn end is not called idle.
- `duolog: run r1 ended`: a run left the snapshot's newest finished runs
  before the lead saw its end, so only its id is known.
- `duolog: 2 asks wait for an answer`: asks are open to the lead.

Each hook names what is new since the last line it showed the same lead, at
most three items and then `and N more`, in 200 bytes. A lead's first look shows
open asks only. The last line shown is recorded in
`<lead home>/notices/<lead>.json`. A worker, a peer, a subagent and a session
that is no registered lead show none. The line is best effort: a snapshot that
fails or takes more than 2 seconds shows nothing and leaves the hook's normal
output as it was. A Claude `Stop` line carries no context, so it does not keep
the turn going.

### How the person is called back

Some news needs the person's eyes when they have left the terminal. With
`lead.notify: true`, the default, duolog alerts them:

- When a Claude lead's `Stop` hook shows a line, it also sets the hook's
  `terminalSequence` to an OSC 9 notice with the same text. Claude Code
  writes it to the terminal in an interactive session, and a terminal that
  supports OSC 9 shows a notice. `SessionStart` and `UserPromptSubmit` lines
  have none, as the person is at the terminal then.
- A Codex hook has no terminal sequence. Set `tui.notifications = true` in
  Codex's `config.toml` for an alert when a Codex lead's turn ends;
  `duolog doctor` names the file while it is off.
- An ask that only you can answer shows a desktop notice once it is filed:
  `ask 7 waits for you: exec rm x`.
- A run started from the MCP `delegate` tool that ends failed, needing input
  or handed back shows a desktop notice: `run d-abc123 (review) failed`. A
  `done` run gets the line and the `Stop` alert only. A `duolog delegate` run
  from your own command shows none, as the command prints its outcome.

An alert never changes a hook's output, the gate's answer or a run's record:
any error gives no alert. `lead.notify: false` keeps the lines and drops the
alerts; only the user config and a local config of your own may set it, so a
shared project config cannot turn your alerts off. Rex has no way to post a
notice into a pane, so duolog does not send alerts through Rex.

On Windows the desktop notice may not show: the toast has no registered app
id, and a cold PowerShell may take longer than the 3 second limit. Nothing
else breaks; the hook, the gate and the run go on as before.

## What the lead is told when a peer stops

Keep each `duolog peer wait <id>` in its own background command with a
completion notification. Saying that you are waiting starts no wait.
After relaying gate ask N, restart it as
`duolog peer wait <id> --after-ask N`. It waits through that ask's answer or
expiry, then watches the peer again. It still reports new asks. It never
answers an ask itself, including one only the person may answer.

Claude's `Stop` hook blocks the lead's first stop when its peer is starting,
updating or asking without a live wait. It tells the lead to start one.
It also delivers unread mail, so a completion just before Stop gets noticed.
A live wait allows the lead to yield. A repeated stop, a worker, a subagent,
an unknown lead or a failed state check stays unblocked to avoid stop loops.

A peer's task stays unfinished until a `peer wait` sees the peer go idle
after it. With no wait running, the peer can sit `ready` while
`peer status` says `task <path> is unfinished`. So duolog tells the lead:

| Event | Message to the lead on the bus |
|---|---|
| The peer ends a turn, its task is unfinished and no `peer wait` watches it | `<peer-id> stopped; task <path> is unfinished` |
| A gate ask of the peer gets no answer in time | `ask <n> from <peer-id> expired and was denied: <action>` |
| The pane closes or its program ends | `<peer-id> closed` or `<peer-id> exited`, with the unfinished task |

The first message comes from the peer's `Stop` hook, once per sent task: a
peer that ends more turns on the same task sends no more, and a task sent
later is told again. A peer whose task is finished, or that a `peer wait`
watches, sends none: the wait reports the turn itself. The `Stop` hook and
an expiring ask never wait long for duolog's state: when another process
holds its lock, they drop the line and the peer stops or is denied on time.
A dropped stop line does not count as sent: the next turn that ends on the
same task sends it. Each message is one line, and the lead's hooks
deliver it with the next prompt or tool call. Answer with `peer status`,
`peer send` or `peer close`.

### When peer-idle and peer-exit events are sent

Two more messages tell the lead that a peer needs a look, whatever its task:

| Event | Message to the lead on the bus |
|---|---|
| The peer ends a turn | kind `peer-idle`: `<peer-id> idle in <folder>`, then the first line of its last message, up to 160 characters, when the hook knows it |
| `duolog peer list` sees the peer's process exited or gone | kind `peer-exit`: `<peer-id> exited in <folder>` |

`<folder>` is the peer's checkout relative to the project, `.` for the
project itself. The `Stop` hook sends one `peer-idle` per turn: it remembers
the transcript length, so a hook that runs again for the same turn sends
nothing. When it cannot read the transcript, it remembers the last message
with the number of turns the peer began since its last `peer send`, which
the peer's prompt hook counts, so two turns that end with the same message
still send two. The
lead's next prompt or tool hook sends `peer-exit`, once per peer; a peer you
closed with `peer close` sends none. Both show as one short line,
like other bus messages. When the lead's hooks deliver a `peer-idle`, they
start the lead's queue run (see
[How to queue peers](#how-to-queue-peers-that-open-when-a-session-is-free)).
The peer's own `Stop` hook runs as a worker and opens nothing.

The `Stop` hook runs only where duolog's hooks run in the peer. A Codex peer
runs it
only after you approve the hooks in `/hooks`; the auto-answer
`Continue without trusting` leaves them off.

## How the lead sees how many peers it can run

The lead's hooks show its peers, account session limits and five-hour
window without reading quota themselves:

```text
duolog peers: claude 3 running (1 idle) of 12; codex 0 of 3; 5h 41%
```

| Part | Meaning |
|---|---|
| `3 running (1 idle) of 12` | This lead's live peers; idle still holds a session. `idle: <ids>` names them. Limits count account sessions across all leads, once |
| Session limit | Last tier recorded by admission, as in `quota`. Omit an agent without one |
| Five-hour window | Latest admission read for the current account, at most 30 minutes old. The task may be running, ended or refused. Omit unknown windows |
| `queue 2` | This lead's items waiting to open. While nonempty, hooks start queue runs and print no advice |
| Free-session advice | With at least half the sessions free, a known window below 70% and no queued item, suggests independent or stacked work using `peer queue add` |
| Delivery | Every UserPromptSubmit; on PostToolUse only with bus mail, at most once in five minutes unless counts change. Registered leads only |
| Time budget | Two seconds; failure prints nothing |
| `peer list` | Same line at the end, except with `--json` |

## How the lead shows its workers in the status line

`duolog statusline` is a command for Claude Code's `statusLine` setting.
Claude Code runs it, pipes a JSON object on stdin and shows the first line
of its output under the prompt. duolog reads `session_id` and the project
folder from `workspace.project_dir`, else `workspace.current_dir`, else
`cwd`. It prints the lead's workers in one line, for example:

```text
duolog 2 peers 1 run 1 ask 3 queued
```

It counts live peers, running runs, open asks and queued items, and leaves
out each count that is zero. A project folder in a submodule shows the same
line as its superproject.

It prints nothing, writes nothing to stderr and exits 0 when:

- the input is not JSON, is over 1 MiB or has no `session_id`;
- the session is not a live lead of this project, or it has no workers;
- the process is a worker or a peer (`DUOLOG_DEPTH` or `DUOLOG_PEER`);
- it takes more than one second.

It reads the lead's state and creates none, and reads no peer screen. It
prints no color codes.

`duolog install` sets the `statusLine` entry (`"command": "duolog
statusline"`, `"refreshInterval": 10`) when no settings file has one, and
`duolog doctor` reports it. If you have your own status line, install leaves
it and says so; doctor shows that as an info line, not a warning. If you
remove the entry duolog wrote, install does not add it again until you run
`duolog install --status-line`. This holds at project scope only; at user
scope there is no such record, so install adds the line again each run. An
entry whose command is `duolog statusline` counts as duolog's. To keep your
own and add duolog's part, run `duolog statusline` from your script with the
same stdin, and print its line next to yours:

```sh
input=$(cat)
mine=$(printf '%s' "$input" | ~/my-statusline.sh)
ours=$(printf '%s' "$input" | duolog statusline)
printf '%s %s\n' "$mine" "$ours"
```

## How to see peers and runs in the Claude pane

The duolog plugin for Claude Code carries a mod: a small program that Claude
Code loads into its own interface. In a project with `.duolog/config.yaml`,
type `/duolog-pane` to open a pane named `duolog` beside the conversation.
A session started in a subfolder of the project has it too: the mod looks
for the file in each folder above, up to the filesystem root, and stops
below your home folder. The `duolog` skill keeps `/duolog`, so the pane has
a name of its own.

| Part | What it shows |
|---|---|
| Peers | Each live peer: id, role, agent and state, and a `view` button that runs `duolog peer view <id>` |
| Runs | Each delegate run: id, role, agent and model, and state, with a `watch` button that runs `duolog watch --tab <id>` and a `log` button |
| Log | After `log`, the last 200 lines of that run's log under the lists; `hide` closes it |
| Above the prompt | While the lead has workers, the status line's text, such as `duolog 1 peer 1 run`, as a button: a click opens the pane. A survey keeps that place |

A pane 80 columns wide or more shows Peers and Runs side by side. A narrower
pane shows one list, and two buttons switch between them. When a button's
command prints a line, Claude Code shows it as a short notice. An error shows
nothing.

The mod runs `duolog runs --snapshot --fast` every second while the pane is
open, and every 3 seconds while it is closed. After 30 seconds with no
workers it reads every 15 seconds, so a worker started from a terminal or
by another agent still shows, until your next prompt or the next tool call.
The log reads `duolog runs tail` every second while the pane is open. That
call sets `DUOLOG_VIEWER=claude-mod`, and duolog shows the log only when
Claude Code itself started duolog. The lead guard stops a model's command
that names
`runs tail`, and the parent check refuses a call with a shell between Claude
Code and duolog. These checks keep a model from reading a worker's stream by
accident. They are no barrier against a model that works around them, for
example by reading the log file. Claude Code installed through npm runs as
`node`. duolog knows it only when node's first argument is the Claude Code
script, or the npm link to it, with no node option before it. An npm install
of Claude Code may not be recognised on macOS, where it can show other
arguments; the pane then shows the refusal in its dim line.

When a call fails, the pane lists nothing for it and shows the first line
of the error, dim. A snapshot that carries an `error`, such as a lead that
is not live, shows it in the same dim line above the lists. A failed log
call ends that log: the pane does not try it again until you press `log`
again. The pane leaves out control characters and text direction marks of
every line it shows. A worker session that duolog started (`DUOLOG_DEPTH`
set) registers no command and runs nothing.

The mod API of Claude Code is early access. `duolog doctor` names the Claude
Code version the mod was tested with, in a project where Claude is a lead.
If a later Claude Code changes the API, or duolog answers with a snapshot
version other than 1, the pane lists nothing. The status line and the hook
notices do not need the mod and work without it, and a Claude Code without
the mod API does not load it.

## How to run several peers at once

Run independent tasks in parallel, each with one worker and peer in its own
git-ignored `.worktrees/` checkout and Rex tab. Run full test suites one at
a time. Stack dependent branches on their open PR branches while CI runs.
Give each peer or CI wait its own background command. Peer tabs stay in
the detached project session without changing the person's focus.

## How to queue peers that open when a session is free

The ready queue opens requested peers when a session becomes free.

| Command | What it does |
|---|---|
| `duolog peer queue add <role> --task <file> [--cwd <dir>] [--after <peer-id>]` | Adds an item. `--cwd`, the current folder by default, must be in the project or one of its linked worktrees. `--task` is a file path relative to `--cwd`, with no `..`, and must be a file in the checkout of `--cwd`. Symlinks are followed before both checks. It works only in a Rex pane, where the run can open the item |
| `duolog peer queue list` | Prints `<id> <role> <cwd> <task> <state>` for each item, in order. The state is `queued`, `opening`, `opened <peer-id>` or `failed: <reason>` |
| `duolog peer queue remove <id>` | Drops an item. An item that a run is opening stays while that run goes. Removing another item never holds up a run |
| `duolog peer queue run` | Opens the queued items in order, then prints the list |

A run opens each item in its `--cwd` as `peer open` does there, with the same
session limits, window headroom and paid-run rules, but only on the free
variant: a run never opens a paid peer, even where `paid_runs` allows one.
It always opens a new pane, even when a peer of the item's role is open in
that checkout, so a queued task never lands in another task's conversation.
It stops at the first item
that `peer open` refuses for now and leaves that item queued: admission, a
lead in plan mode with a role that writes, or no agent available to the
role. A later close or reset can let it open. A run checks the folder and the
task again before it opens an item. An item whose check, open or send fails
for another reason is marked `failed`, the lead gets
`queue: <id> failed: <reason>`, and the run goes on. Each opened peer gets its
task as `peer send` sends it, and the lead gets one bus line:
`queue: opened <peer-id> (<role>) in <cwd> for <task>`. An item with `--after`
waits until that peer is idle or closed; the items behind it do not wait.

duolog starts a run in a separate process when the lead has queued items:

- after `duolog peer close` closes a peer. Its line then ends with
  `; queue run started`.
- at each of the lead's prompts, while the load line shows a free session
  for the agent of the item a run would open first, or for any agent when
  that item's role names none. An item that waits `--after` a peer still
  working is passed over, as the run passes it.
- when the lead's hooks deliver a `peer-idle` message, so a peer that
  finished lets the next item start, with the same check.

The hooks also start a run when an item was left `opening` and no run is
going, whatever the load line says.

A lock in `.duolog` lets one run per lead go at a time, and a second run exits
at once. The lock records the run's process id; a lock whose process is gone
is dropped and does not block a new run. A run that ended in the middle, for
example when its process was killed, can leave an item `opening`. The next
run marks it `failed` and tells the lead, since its peer may be open.
`queue remove` drops it when no run is going. The items are in
`.duolog/peer-queue.ndjson`. Runs started this way
write to `.duolog/peer-queue-run.log`, which each run empties once it holds
the lock. On Windows the run has a hidden console of its own, so neither it
nor the git and rex it calls opens a window.

## How to see what a peer shows

None of these commands puts screen text into a model's context. In your
own terminal none costs tokens. Run by the lead or its skill, each costs a
model turn and the lines it prints.

| Command | Who runs it | Prints | Model cost |
|---|---|---|---|
| `duolog peer list --long` | You, or the lead | One table row per peer, no screen text | None in your terminal; one line per peer in a lead's context |
| `duolog peer show <id>` | You, in your own terminal | The last lines of the screen, cleaned and bounded | None; it refuses inside an agent, a worker or a pipe |
| `duolog peer view <id>` | You, or the lead when you ask | One line; your Rex client shows the peer's tab | None in your terminal; one line in a lead's context |

`peer show` refuses workers, non-terminal output, Claude/Codex marker
variables and Claude/Codex ancestor processes. The lead hook also guards
wrapped calls: Claude asks, except in `auto` or `bypassPermissions`; Codex
denies. These checks prevent accidents, but a deliberate workaround can
bypass them.

`peer show` and `peer view` need `REX_SESSION`, like the other peer commands.
`peer view` moves one client: `--client`, or else the only client that shows
your session. It refuses when it cannot tell. Peers live in their own Rex
session, so `peer view` asks Rex.app to switch to it. Rex.app does that only
with Remote Control on, in its Settings under Rex Server; without it,
`peer view` stops and says so. Afterwards you look at the peer's pane, and
what you type goes to the peer. To return, use Previous Session in Rex, or
pick your session in the sidebar.

`duolog runs` and `duolog watch` take a peer id as well as a delegate run's id
(see [How to watch a delegate run](#how-to-watch-a-delegate-run)), so one
interface shows all workers. The three peer commands above stay.

The `duolog-peers` skill runs `runs --long` and `watch --tab` for the lead,
and names `runs --snapshot` as the same list in one JSON line. Claude runs
`watch --tab` without asking, but only when you ask to see a peer or a run.
The skill refuses `runs tail` as it refuses a bare `watch`, and leaves
`duolog statusline` to Claude Code's status line. The skill costs one model
turn. For a path with no turn in Claude Code, type the command yourself with
the `!` prefix, such as `! duolog runs --long`, or type `/duolog-pane` to
open the [Claude pane](#how-to-see-peers-and-runs-in-the-claude-pane). From
a Codex lead, run these commands outside the sandbox.

## How to watch a delegate run

A worker that the lead starts with the `delegate` tool of the MCP server, or
with `duolog delegate`, writes only raw JSON to its log,
`.duolog/tasks/<task>/stdout.log`. Two commands show it to you in a readable
form. They need no Rex, except for `--tab`.

| Command | Who runs it | Prints |
|---|---|---|
| `duolog runs [--long\|--json]` | You, or the lead | The live peers and delegate runs of this project and of every work project that shares its lead home (a superproject and its duolog submodules): the running ones first, then the last five that ended. Plain output is `<id> <state>`; `--long` is a table with kind (`peer` or `delegate`), role, agent and model, age and the worktree's name; `--json` is the rows as JSON |
| `duolog runs --snapshot [--fast]` | You, the lead, or a status line | The lead's live peers, delegate runs, open asks and load as one versioned JSON line; see the table below |
| `duolog watch [id]` | You, in your own terminal | For a delegate run: the worker's messages and one short line per tool call, such as `> Bash go test ./...`. Tool output is left out; a log line over 1 MiB prints as one line such as `> (output of 3 MiB not shown)`. For a peer id: what `peer show` prints, with `--lines` |
| `duolog watch --tab [id]` | You, or the lead when you ask | One line; for a delegate run, your Rex client shows a tab that runs `duolog watch <id>` and closes 10 s after the run ends; for a peer id, what `peer view` does |
| `duolog runs tail <id> [--from N] [--cursor C] [--json]` | The Claude pane, or you in your own terminal | One page of a delegate run's log, in the lines `watch` prints; `--json` is one JSON line; see the table below |

A peer's state in `runs` is the one `peer list` gives; `--lead` names the lead
whose peers and delegate runs are listed (default `DUOLOG_LEAD`, then the lead
that runs the command). Without a lead, all runs are listed. Without Rex, or
when the peers cannot be read, `runs` lists the delegate runs and prints
`peers not listed: <reason>` on stderr. A peer has no recorded start time or
model, so its `started` is `null` and its `model` is empty.

`duolog runs --json` prints one list of rows with the same keys for peers and
delegate runs, for the same lead as the plain output:

| Key | Value |
|---|---|
| `id` | The peer or run id |
| `kind` | `peer` or `delegate` |
| `role` | The role |
| `agent`, `model` | The agent and model; `model` is empty when not known |
| `state` | A delegate run's state, or the peer state as in `peer list --json` |
| `started` | The start time (RFC 3339), or `null` |
| `worktree` | The absolute path of the work root |
| `task_dir` | The absolute path of the folder of the run's task and log, or empty |

`duolog runs --snapshot` prints one JSON object on one line, for a status
line or a pane. It covers this lead and every work project of its lead home
(its submodules too). Version 1 keeps its keys; a later version 1 only adds
keys. Every path is relative to the project root of the lead home, with `/`.
The load comes from cached numbers only: the snapshot never reads quota.
It cannot be used with `--long` or `--json`, and it creates no `.duolog`
folder.

With `--fast` the snapshot reads no pane screen, so it is cheap enough to
poll every few seconds. Each peer whose pane runs has the `state` `unknown`
and `working` false; an exited or gone pane keeps its state. An `unknown`
peer counts as live and not idle, so a fast snapshot lists no idle peers in
`load`. `--fast` needs `--snapshot`.

| Key | Value |
|---|---|
| `version` | `1` |
| `generated_at` | The time of the reading (RFC 3339, UTC) |
| `line` | One short line, such as `duolog 2 peers 1 run 1 ask`; empty when no peer is live, no run is running, and no ask or queued item waits |
| `lead` | `{"id", "agent"}`, or `null` when no lead resolves |
| `rex` | `{"available"}`: `false` when there is no Rex, so no peers are listed |
| `peers` | This lead's peers, with their state: `id`, `role`, `agent`, `model`, `state`, `working`, `task`, `tab`, `worktree`, and `last` (`kind`, `at`) or `null` |
| `runs` | The running delegate runs, then the last five that ended: a `done` run for 15 minutes after it ended, one in any other state for 2 hours. Fields: `id`, `role`, `agent`, `model`, `state`, `started`, `ended`, `worktree`, `task_dir`, `has_log`, `line` |
| `asks_open` | The number of asks open to the lead |
| `load` | `agents` (each `agent`, `running`, `idle`, `limit`, `live`), `queued`, and the 5-hour and weekly use in percent, `window_5h` and `weekly`, or `null` when not known |
| `error` | Left out, or the parts that could not be read, such as `peers: <reason>` |

With no lead home, no lead or no Rex, the object keeps the same keys:
`lead` is `null`, `rex.available` is `false`, and `peers` and `runs` are
empty lists. Unlike plain `runs`, the snapshot shows nothing when no lead
resolves, such as with several live leads and none named: it never shows
another lead's workers. A lead that you name with `--lead` or
`DUOLOG_LEAD` and that is not live gives `error`
`lead: <id> is not a live lead`.

duolog keeps the running records and the newest 50 that ended in each work
project; it removes older ones when a new run starts.

`duolog runs tail <id>` reads a delegate run's log in pages, for a pane that
asks for the next page every few seconds. One page has at most 200 lines and
reads at most 1 MiB of the log. A log line longer than 1 MiB prints as one
line, such as `> (output of 3 MiB not shown)`, as in `watch`. With
`--from -1`, the page starts in the last 64 KiB of the log, at a whole line.
For the next page, pass back the `offset` and `cursor` of the last page
unchanged. A line that the worker has not finished waits for the next page.
`--json` prints the page as one JSON line, and without `--from` it starts as
`--from -1` does. With neither `--json` nor `--from`, `tail` prints the last
200 lines of the last 64 KiB, up to the end of the log, and no offset; with
`--from` alone, it prints only the lines of that page.

| Key | Value |
|---|---|
| `offset` | The byte offset to pass as `--from` for the next page |
| `reset` | `true` when the page starts the log over from its start: a retry wrote a new log, the log got shorter, or the cursor could not be read |
| `lines` | The lines of this page, as `watch` prints them; an empty list when there is nothing new |
| `ended` | `true` when the run has ended and this page reached the end of its log |
| `state` | The run's state, as in `duolog runs` |
| `cursor` | An opaque text to pass as `--cursor` for the next page; empty while the run has no log |

Like `watch`, `tail` prints a worker's text only for you, and refuses a
model and a worker. The Claude pane runs it with `DUOLOG_VIEWER=claude-mod`,
and it passes only when Claude Code is its direct parent process, so the
variable alone does not pass a model's shell command. The lead hook guards a
command that runs `duolog runs tail` as it guards `peer show`, behind any
wrapper: Claude asks except in `auto` or `bypassPermissions`; Codex denies.
The pane's own call is no tool call of the lead, so the hook does not see it.

Without an id, `watch` follows the newest running run. It starts at the
beginning of the log, keeps reading while the log grows, reads both Claude's
stream JSON and Codex's JSON events, skips an event it does not know, and
ends with a line `ended: <state>` when the run ends. Ctrl-C stops it at any
time without touching the run.

`watch` keeps output out of the lead's context, but cannot stop a lead from
reading `stdout.log`. It uses the same caller checks as
[`peer show`](#how-to-see-what-a-peer-shows); workers may run neither
`watch` nor `runs`. The lead hook guards a command that runs `duolog watch`
without `--tab`, including assignments and wrappers. Merely mentioning it
in a search or commit message does not count. A `--tab` used as the value
of `--lead` or `--client` does not count either. Claude asks except in
`auto` or `bypassPermissions`; Codex denies. The command's own refusal
still applies.

`watch --tab` opens the tab in duolog's own Rex session, the one that holds
peer tabs, and moves your Rex client to it, as `peer view` does. The tab is
read-only in effect: the program reads no key, so what you type goes nowhere.
It needs `REX_SESSION` and a client to move (`--client`, or else the only
client that shows your session). To return, use Previous Session in Rex. The
tab ends with the run. Closing a project's last peer also closes a
`watch --tab` tab in the project session. Without Rex the command prints the
`duolog watch <id>` line for you to run in your own terminal instead.

## How to drive peers with the MCP tool

A Claude or Codex lead drives peers with the `peer` tool of the
[duolog MCP server](getting-started.md#how-the-lead-uses-the-duolog-mcp-server)
instead of the `duolog peer` commands. The `action` field picks the step:

| Action | What it does |
|---|---|
| `open` | Opens a peer for a role |
| `send` | Writes the task file from the `task` fields and sends it |
| `wait` | Waits for the peer, at most `timeout_minutes` |
| `answer` | Picks an option for a question in the peer's screen |
| `status`, `list` | Shows one peer, or all of them |
| `close` | Closes the peer |

For `open`, set `worktree` to a path or a name under `.worktrees/`. A plain
name tries `.worktrees/<name>` first, in the server's checkout and in each
checkout of a duolog submodule. A duolog submodule is a Git submodule with
its own `.duolog/config.yaml` whose superproject has duolog too, at any
depth, also inside a linked checkout of the superproject. When two
checkouts have the name, the tool refuses and names both; pass a path
instead. With no match, the name is a path. Relative paths start at the
server's working folder.

Session start names these submodules without loading their instructions.
The first peer or delegation into each submodule reminds the lead to read
its `AGENTS.md` and `CLAUDE.md`. Its roles and models come from its own config.

The tool accepts only the root of a checkout that Git lists: a checkout of
this repository, or of a duolog submodule that shares the lead. It refuses
a subfolder, a submodule that is not initialized or has no duolog config,
and an unrelated repository nested in a folder. Without `worktree`, the peer
opens in the server's checkout. For example:

```json
{"action":"open","role":"implement","worktree":"feature"}
```

`send` writes the task in the target peer's registered checkout, even when
the lead runs elsewhere. Its `task_path` is relative to that checkout.

A `wait` returns after `timeout_minutes` (10 by default and at most), so the
lead calls it again and is never blocked for good. It returns the peer's
`state`, its `question` if one is open, the `open_asks` with their numbers, and
the `report` field: the first lines of the peer's report file.

The `delegate` tool takes the same `worktree` field, with the same lookup
and checks. The worker then starts in that checkout, with its project's
config, and the task file and result record stay in that project. The
lead stays the one that runs the server, even when the checkout belongs to
a duolog submodule. For example:

```json
{"role":"implement","worktree":"module/.worktrees/feature","task":{"goal":"Fix the parser."}}
```

The lead's registry keeps the work project of each run, also after the run
and the lead end. So `action: result` with the run's id finds the result
from any checkout of the lead, even after its worktree was removed. A
relative `task_path` can name runs in more than one project; the call then
fails and names them, and you pass the id or `worktree` instead. In
`status`, each delegation row has its `work_root`, the checkout it ran in,
and its `answer_path` in that project. Peer rows of `status` and of
`peer list` also have `work_root`.

`answer_ask` has the rights of `duolog allow` and `duolog deny`. Only the
person's terminal may allow or deny a person-only ask. The lead and
`answer_ask` handle ordinary asks. Workers and peers get none of these tools.

## How duolog settles routine questions

Peers start with Codex's `tui.notifications=false` and `notify=[]`, or Claude's
`preferredNotifChannel: notifications_disabled`. These settings apply only
to the peer. The lead keeps its own notification settings.

`peer wait` sends questions to the lead on the bus at once. A pending duolog
gate ask from this peer makes `peer wait` return at once; answer it before
the asker's wait ends. Gate asks of other peers do not stop this wait. For a
question in the agent's own screen, with reminders enabled, it keeps
watching until the question clears or a reminder is due.
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
starting with `Trust`, `Yes, and don't ask` or `Always`, or containing
`trust this folder` or `without asking`, ignoring case and outer spaces.
Duolog also checks the selected option before answering.

A built-in rule handles startup folder trust, separately from
`rex.auto_answers`, and logs `peer.folder_trust`.

| Check | Requirement |
|---|---|
| Existing trust | The agent already trusts the lead's root, the folder above shared `.duolog/` |
| Eligible pane folder | Lead root, a folder below it, or a linked worktree listed by that repository's Git, with the same git directory and no stale prunable entry |
| Repository identity | A pane's `.git` claim alone is insufficient. A lead root nested in another repository grants nothing to the outer repository or its worktrees |
| Screen paths | Screen names the pane's folder and no folder outside the lead's project; wrapped paths are joined |
| Capture | Reread up to 200 lines if warnings hide the heading; send nothing if the trust screen disappears |
| Markers | Count only line-start markers near options and a command prompt directly above the menu; a marker within 30 lines above a question leaves it to the lead |
| Unknown or ineligible screen/config | Leave it to the lead. Text rules never answer trust, even when only a trust label or screen text is recognised |
| Other grants | Never grant hook trust or persistent approvals |

Claude peers inherit the root's enabled and disabled `.mcp.json` server
names. Unapproved servers are disabled, as are all when settings cannot be
read. Approval follows the name: changing an enabled server's command keeps
it enabled. Doctor warns about servers in the project or a parent that
Claude has neither enabled nor disabled; a manually opened peer can stop at
that dialog. An unhandled startup screen is named `folder trust` or
`new MCP servers` when recognised.

Codex command prompts go to the duolog permission gate with the peer's role.
The gate judges the command after leading `NAME=value` assignments. An allow
selects `Yes, proceed`; a deny selects `No`. Only an ask reaches the lead, with
the command shown. Text rules cannot override a command decision. The peer
keeps its own sandbox; this only answers its prompt.

duolog declines Claude's offers to install a plugin with `No, not now`, or
`Not now` or `No` when that is the option that declines only for now. Text
rules cannot override that. See
[Configuration](configuration.md#how-a-worker-borrows-the-leads-mcp-server)
for what the lead is told.

Claude's default exits; Codex's trusts. duolog selects by label and
automatically trusts eligible folders of an already trusted lead project,
as described above. Other trust questions go to the lead.

## What a worker may never receive

Never send `/model` or `/effort` to a Claude pane. They write to your user
settings, even when you start Claude with project-only settings. duolog
passes the model and effort at startup instead.

## What remains unverified

These come from the
[live verification](research/2026-10-30-live-verification.md).

- Rex worked from Claude in our runs (OBSERVED). From Codex, the shell could
  not find `rex`, and one sandboxed run could not reach its socket
  (OBSERVED). So Codex as the lead is not proven for `duolog peer`.
- One-shot delegation from a Codex lead worked outside its sandbox in the
  cost study (OBSERVED). That does not verify live peers; follow the manual
  check below for `duolog peer`.
- Codex does not fire `SessionEnd` when its pane closes (OBSERVED), so a
  closed Codex pane can stay in the registry. `duolog doctor` warns about a
  pane whose lead has ended. Where Codex runs the hook, it gives it 1 second
  by default; duolog's hook asks for 3, the most Codex allows.
- Rex has no known Windows build. Live sessions are unavailable on Windows.
  `duolog peer` prints this limit; use `duolog delegate` instead.
- A tab shown by `duolog peer view` is interactive. Your keys reach the peer,
  and duolog cannot filter them. Look; do not type.
- `scripts/dev/rebuild` closes every ready peer, so it may close one that you
  are viewing.
- How Rex switches the client's session and whether it resizes the pane are
  not checked in real Rex yet.

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
