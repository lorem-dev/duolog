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

## How to register a lead that its hooks missed

The project's hooks do not run for a session that started in another folder.
They also miss a session resumed here after a restart. Then `duolog peer open`
says `no live lead`. Ask that session to run this command in its own shell:

```sh
duolog lead register [plan|ask|edits|full]
```

A linked worktree shares the lead of its repository's main checkout, a
submodule's included: run the command in either. A lead of another repository
is never found. `--lead <id>` names the state folder it searched when it finds
no live lead.

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
For peers, `max_live_sessions` applies per lead, agent and work root.
`duolog peer send` refuses a task file that is not under the peer's own work
root, so a task cannot run in the wrong worktree. A relative path is read from
the worktree you run the command in.

The first peer creates a separate Rex session for the project, named
`duolog | <project folder name>`. It stays detached: duolog does not show it
on a client or select it. The person's session, windows, panes and focus stay
as they were. duolog never moves focus, except `duolog peer view`, which you
ask for. Every peer gets a tab named `duolog | <pane label>`, such as
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

## After a Rex restart

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

Every peer may write one file under `.duolog`: its report at
`.duolog/tasks/<peer-id>/report.md` in the checkout it works in. In a linked
worktree that is the worktree's own `.duolog`, not the main checkout's. The
gate allows that write for that peer only, even in plan mode or a read-only
role; every other path under `.duolog` and every other peer's report stay
denied. The peer's prompt hook tells it to put its final answer there, short,
with the result first. The task line `peer send` types also names that exact
path (`Write the answer to .duolog/tasks/<peer-id>/report.md.`), so a peer
learns it whatever its agent or role, even if its hook does not run. The line
wraps onto a second row of an 80-column pane.
A peer may print its own id with `echo "$DUOLOG_PEER"` or `printenv
DUOLOG_PEER` without an ask; no other variable and no longer line is covered.
`peer send` removes the report of the task before, so a report is always the
current task's. `peer wait` and `peer status` add `report <path>` to their line
once the file exists, then print the report's first lines, at most 15 lines
and 1500 characters, with `[report cut]` when the rest is left out. A task
that ended without a report ends the wait line with `no report written`; the
bus keeps its cut `peer-idle` line, which adds `; report <path>` once the file
exists. The report must be a regular file inside the peer's own checkout: a
link, or a path behind one, that leaves it is ignored. A Codex peer in a
read-only role can still refuse the write before the gate sees it, because
its `-s read-only` sandbox allows no writes.

A Claude peer never starts in Claude's own plan mode, even in a read-only
role or under a lead in `plan`. Plan mode shows its own `Exit plan mode?`
prompt and waits for a person, while `peer status` says `ready`. The peer
starts in Claude's `default` mode instead, and the gate keeps it read-only:
it denies every write but its report, and every tool it cannot judge. So
`duolog peer open` first checks that the gate runs for the pane, and opens
nothing when it does not. A peer opened under a lead in `plan` stays at
`plan` when the lead moves to a looser mode. See
[Permissions](permissions.md#a-workers-native-mode).

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

Before handing off work, the lead records its starting checkout, branch and
existing edits. Each peer can work in its own worktree. That isolation does
not change where the person expects the result.

Unless the person requests another branch or a pull request, the lead brings
the peer's task changes into the starting checkout on its original branch.
This can be a linked worktree; it need not be the repository's main checkout.
The lead reviews the task changes, then merges, cherry-picks or applies their
diff as appropriate. Changes stay uncommitted unless a commit was requested
or required. Existing staged, unstaged and untracked work must survive.

The lead verifies the integrated result in that checkout before reporting
completion. If integration conflicts, it preserves the person's work and
reports the blocker. Its final answer gives the result's path, branch and
commit status. A peer going idle alone does not mean the result is delivered.

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

### The peer-idle and peer-exit events

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

The `Stop` hook runs only where duolog's hooks run in the peer. An older
install gets it when you update the plugin or run `duolog install` again.
A Codex peer runs it
only after you approve the hooks in `/hooks`; the auto-answer
`Continue without trusting` leaves them off.

## How the lead sees how many peers it can run

A lead that sizes its peers by habit leaves sessions unused. So duolog adds
one line to the lead's context. It shows the lead's live peers, the session
limit of each agent and the 5-hour window:

```text
duolog peers: claude 3 running (1 idle) of 12; codex 0 of 3; 5h 41%
```

- `3 running (1 idle) of 12` counts the lead's own live peers. An idle peer
  finished its turn with nothing queued, and it still holds a session. The
  line names idle peers as `idle: peer-ab12cd, ...`. The limit is the one
  that `duolog quota` prints as `<agent> sessions N of M`, which counts the
  sessions of all leads. duolog counts them once, from the same account state.
- The line reads no quota itself. A hook has no quota read of its own, so the
  limit is that of the session tier that admission recorded last, and the
  window is the one that the latest admission of the agent's current account
  measured, if that was at most 30 minutes ago. That admission may be a task
  still running, a task that has ended, or one that was refused. An agent
  with no session limit is left out. So is a window that duolog does not
  know.
- `queue 2`, after the agents, counts the items of the lead's ready queue
  that wait to open. While items wait, the line gives no advice: the hooks
  start a queue run themselves when a session is free.
- When an agent has at least half of its sessions free, the window is known
  and under 70% and the lead's ready queue is empty, the line ends with
  `- <n> <agent> sessions free: start independent work or stack it on open
  pull request branches (duolog peer queue add)`.

The `UserPromptSubmit` hook adds the line to every prompt. The `PostToolUse`
hook adds it only to a bus delivery, and at most once in 5 minutes unless a
number changed. Only a registered lead gets it. Workers and peers never do.
If duolog cannot work the line out within 2 seconds, it prints nothing and
the hook goes on.
`duolog peer list` prints the same line as its last line, except with
`--json`.

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

## How to queue peers that open when a session is free

The ready queue holds the peers that the lead wants to run next. duolog opens
them by itself when a session is free, so the number of peers does not depend
on how many the lead remembers to open.

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

`peer show` refuses when a model may read its output: a Claude or Codex
marker variable, output that is not a terminal, a worker, or a Claude or
Codex process above it. The lead hook also stops a lead's tool call that
names `duolog peer show`, behind any wrapper: Claude asks you, Codex denies
it. A Claude lead that runs in `bypassPermissions` or `auto` is not asked;
`peer show`'s own refusal still applies. These refusals guard against
accidents. They are not a barrier: no check can prove that a terminal is
yours, and a model that ignored them would have to work around them on
purpose.

`peer show` and `peer view` need `REX_SESSION`, like the other peer commands.
`peer view` moves one client: `--client`, or else the only client that shows
your session. It refuses when it cannot tell. Peers live in their own Rex
session, so `peer view` asks Rex.app to switch to it. Rex.app does that only
with Remote Control on, in its Settings under Rex Server; without it,
`peer view` stops and says so. Afterwards you look at the peer's pane, and
what you type goes to the peer. To return, use Previous Session in Rex, or
pick your session in the sidebar.

The `duolog-peers` skill runs `peer list --long` and `peer view` for the
lead. Claude runs `peer view` without asking, but only when you ask to see
a peer. The skill costs one model turn. For a path with no turn, type the
command yourself: in Claude Code with the `!` prefix, such as
`! duolog peer list --long`. That this starts no turn is not checked yet.
For Codex, no turn-free path is confirmed; do not rely on one. From a Codex
lead, run these commands outside the sandbox.

## How to drive peers with the MCP tool

A Claude lead drives peers with the `peer` tool of the
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
name tries `.worktrees/<name>` first. Relative paths start at the server's
working folder. A folder that is not a checkout of this repository, such as
a subfolder of the project, is refused. Without `worktree`, the peer opens in
the server's checkout. For example:

```json
{"action":"open","role":"implement","worktree":"feature"}
```

`send` writes the task in the target peer's registered checkout, even when
the lead runs elsewhere. Its `task_path` is relative to that checkout.

A `wait` returns after `timeout_minutes` (10 by default and at most), so the
lead calls it again and is never blocked for good. It returns the peer's
`state`, its `question` if one is open, the `open_asks` with their numbers, and
the `report` field: the first lines of the peer's report file.

`answer_ask` answers a gate ask by its number. It has the rights of
`duolog allow` and `duolog deny`. For an ask that only you may answer, the
lead's guard asks you, and a lead in `bypassPermissions` or `auto` answers its
own asks. Workers and peers never get these tools.

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

A peer pane's startup folder-trust question (`Yes, I trust this folder` in
Claude, `Trust and continue` in Codex) is answered by a built-in rule, not by
`rex.auto_answers`, and recorded like other automatic answers. It applies only
when the screen names the pane's folder and no folder outside the lead's
project, and the pane's folder is the lead's own root (the folder above the
`.duolog/` state folder), a folder below it or a linked worktree that the lead's
repository lists in `git worktree list` (not a stale `prunable` entry, and its
own git directory must be the lead's), and the agent already trusts the
lead's root, not the pane's new checkout. A folder that only claims in its own
`.git` to be a worktree does not count, and a lead root inside an outer
repository does not make that repository or its worktrees the lead's. A long
warning block can push the heading off the capture, so duolog reads a trust
question again from up to 200 lines, and sends nothing if that capture no
longer shows it. A folder path that wraps onto a second line is joined before
it is compared. A marker word counts only at the start of a line near the
options, and a command prompt counts only when its command is right above the
menu, so earlier output that quotes a marker or an old prompt does not stop
the gate. Output that starts a line with a marker within 30 lines above a
question leaves that question to the lead. Any other folder, or a config that
cannot be read, leaves the question to the lead, and no `rex.auto_answers` rule
answers a trust question, whether or not duolog reads it in full: a question
with a trust label or the text of a trust screen is the lead's. Hook
trust and persistent approvals are never granted. For Claude, the pane also
gets `enabledMcpjsonServers` and `disabledMcpjsonServers` from what the lead
decided for its root, so the new MCP servers dialog never shows: every
`.mcp.json` server the lead has not enabled is disabled, and so are all of them
when the settings cannot be read. As in Claude, approval is by server name: a
branch that changes the command of a server the lead enabled keeps it enabled.
`duolog doctor` warns when the project's `.mcp.json`, or a parent folder's, has
servers that Claude has neither enabled nor disabled for the folder, because a
Claude peer opened by hand there stops at the "new MCP servers found" dialog.
When `peer open` or `peer wait` stops at a startup screen it cannot answer, its
line starts with the screen's name, `folder trust` or `new MCP servers`, when
duolog recognises it.

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
- One-shot delegation from a Codex lead worked outside its sandbox in the
  cost study (OBSERVED). That does not verify live peers; follow the manual
  check below for `duolog peer`.
- Codex does not fire `SessionEnd` when its pane closes (OBSERVED), so a
  closed Codex pane can stay in the registry. `duolog doctor` warns about a
  pane whose lead has ended.
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
