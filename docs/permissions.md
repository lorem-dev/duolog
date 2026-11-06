# How duolog decides what a worker may do

This page explains how a worker asks for permission and how duolog answers.
The `duolog` binary decides, not a model, so a normal decision costs no
tokens.

## The order of the decision

duolog checks in this order and stops at the first answer:

1. The deny rules.
2. The limit of the role (`sandbox`).
3. The lead's mode.

A worker's mode and role cannot go beyond the lead's limits. The lead's mode,
the role's limit and the deny rules all narrow what the worker can do. None
of them widens it. The gate checks what the worker asks for; see
[What the gate cannot see](#what-the-gate-cannot-see).

## What the lead's mode means

| Mode | Meaning |
|---|---|
| `plan` | The worker may only read. A write role is refused. A write, or a command not proven to be a read, is denied and never asked, so `duolog allow` cannot run it |
| `ask` | The default. Ordinary reads are allowed; the policy decides the rest |
| `edits` | The worker may edit files in the project |
| `full` | Same flags as `edits`. The policy rules still apply |

See your lead's mode with `duolog mode`. Give a lower mode with
`duolog mode plan`. The command can only lower the mode.

Native names map to these modes. For Claude, `default` is `ask`, `acceptEdits`
and `auto` are `edits`, and `bypassPermissions` is `full`. For Codex,
`read-only` is `plan`. `workspace-write` is `edits` with approval policy
`on-request`, `never` or `on-failure`, because Codex itself edits the project
there without asking. `danger-full-access` is `full` with `never` and `edits`
with the other two. Approval policy `untrusted` makes both `ask`. An unknown
value counts as `ask`.

## A worker's native mode

Each worker also starts in a native mode of its agent: a Claude
`--permission-mode` or a Codex sandbox. By default `duolog delegate` passes
the flag that matches the lead's mode, never above `acceptEdits` for Claude or
`workspace-write` for Codex. The `worker_modes` section of the config
([Configuration](configuration.md#which-layers-may-change-worker-modes))
changes it for one role or one model family. Without the section nothing
changes.

A Claude worker or peer never starts in Claude's own plan mode. Plan mode
asks a person before it lets the agent leave it, and a worker or peer has no
person to answer, so it stops and waits. A worker or peer at level `plan`
starts in Claude's `default` mode instead: a read-only role, a lead in
`plan`, `duolog mode plan`, or an entry that asks `plan`. Its level stays
`plan`. A Codex worker keeps its `read-only` sandbox, which the OS enforces.
The lead keeps its own mode; duolog does not change it.

Claude's plan mode refused every tool that is not read-only before it ran.
In `default`, duolog's gate is all that does, so for such a Claude worker or
peer:

- The gate denies every write and every command that is not a named
  read-only command. It allows only a peer's own report. At level `plan`
  it denies before it asks: a write outside the project and a command it
  cannot parse, such as `env X=1 touch f`, are denied, not asked. A line it
  cannot parse whose every command is a known read, such as
  `rg -n "a\b" internal`, is asked, unless it holds a `>`, a backtick or a
  `(` anywhere: a substitution, a subshell, an extended glob or a zsh glob
  qualifier such as `*(e:'cmd':)`, which runs `cmd` as the shell expands it.
- The gate denies an MCP tool unless an allow rule marks it `read_only` or
  it is one of the default CodeGraph read tools. It denies every tool it
  cannot judge as a read or a write, such as `EnterPlanMode`, `CronCreate`
  or `SendMessage`. Above `plan` and outside a read-only role, these ask.
  A denied tool cannot be allowed with `duolog allow`.
- The agent starts without Claude's built-in tools that are not read-only
  and that the gate cannot judge, a second wall behind the gate. A worker
  also starts without `Write` and `Edit`; a peer keeps them for its report.
- `duolog delegate` and `duolog peer open` start it only after the gate
  checks below, under "Why a looser mode needs proof that the gate runs",
  pass in its own environment. Otherwise nothing starts.

A peer opened, or a worker started, under a lead in `plan` gets
`DUOLOG_MODE=plan`, so its level stays `plan` when the lead later moves to a
looser mode: it still cannot read the web or write. A peer or worker in a
read-only role under a looser lead needs none: its role keeps it read-only.

An entry names a native value, such as `acceptEdits` or `read-only`. duolog
resolves it for each worker:

1. A role entry and a family entry both count; the lower one wins. Only a
   role entry can ask for more than duolog's default; a family entry alone
   gives at most `acceptEdits` or `workspace-write`, and one that asks more
   changes nothing. A read-only role ignores family entries.
2. The result never goes above the lead's mode, including a lower mode set
   with `duolog mode`. A read-only role stays read-only.
3. Claude `auto` needs a lead that is in `auto` at level `edits`, or at
   `full`. A lead in `acceptEdits` gives the worker `acceptEdits`.
   `bypassPermissions` needs a lead at `full`.
4. When the entry is above the lead, the worker gets duolog's default flag
   for the lead's mode instead.

When an entry applies, the worker's gate level follows the same result.
`duolog delegate` sets `DUOLOG_MODE` for the worker, and the gate takes the
lower of that and the lead's mode. A missing or forged value can only lower
the level, never raise it. So an entry that lowers a family, such as
`luna: {codex: read-only}`, binds the gate as well as the flag. A role entry
for a read-only role sets the gate to `plan`, which denies web reads; a
family entry does not. Without an
entry delegate sets no `DUOLOG_MODE`, and the gate keeps the lead's mode as
before, except under a lead in `plan`, which pins `plan` as above.

Project files `.duolog/config.yaml` and `.duolog/config.local.yaml` may only
lower an entry, and a project layer that changes a role drops what its entry
asks above the default. See the configuration page for what is ignored.

### Why a looser mode needs proof that the gate runs

In `default` and `acceptEdits`, the native mode is a second lock: if the gate
does not run, a Claude tool call still meets a prompt that nobody answers in
`claude -p`, and is denied. In `auto` and `bypassPermissions` that second lock
is gone, and a missing gate lets the tool run. Codex `workspace-write` keeps
its OS sandbox, but network and extra writable roots widen it.

A Claude worker or peer at level `plan` or in a read-only role starts in
`default`, but a `permissions.allow` entry in the settings it inherits, such
as `Write` or `Bash(*)`, lets that tool run without a prompt. Claude's plan
mode no longer stops it, so only the gate does.

So before a worker starts in a looser mode, with Codex network or extra
roots, or as such a Claude worker, `duolog delegate` checks that the gate
can run for that agent here. `duolog peer open` checks the same for such a
Claude peer:

- Claude: the duolog gate hooks are in a settings file, or the duolog plugin
  is enabled and installed for the user or for this project, and
  `disableAllHooks` is off. For a worker or peer at level `plan` or in a
  read-only role, each `PreToolUse` hook that names the gate must also be
  exactly the command `duolog install` writes,
  `duolog gate --agent claude --event PreToolUse`, with no comment or other
  shell text, and its program must be the `duolog` on the worker's PATH or
  an absolute path to that same file. In duolog's own checkout the dev hook
  `exec "$CLAUDE_PROJECT_DIR"/scripts/dev/bin/duolog gate --agent claude
  --event PreToolUse` counts too, exactly so, when that file is the dev
  launcher. The hook must not be `async` or `asyncRewake`, and must have no
  `timeout` below the 150 seconds that install writes: Claude does not wait
  for an async hook, and runs the tool when a hook times out. Claude's
  managed settings count too: when they set `allowManagedHooksOnly` or
  `disableAllHooks`, only a gate in the managed settings themselves counts.
  Otherwise the message names the hook and its file and says to run
  `duolog install`.
- Codex: the folder is trusted, and an approved gate hook runs duolog's own
  gate command. Codex skips a hook whose command changed after approval, so
  an approval of another command does not count. When `duolog install`
  changes the gate command, the approval Codex held then does not count
  either; approve the hook again in `/hooks`.
- The `duolog` binary is on the worker's PATH.
- No writable root overlaps a protected folder, checked again at launch.

If a check fails, delegate refuses the run. No worker starts and the other
agent does not run in its place. The message reads
`gate preflight failed for <agent> <native> (<source>): <reason>; nothing
ran`. Fix the reason, or remove the entry. For a Claude worker or peer at
level `plan` it reads `gate preflight failed for claude at level plan
(native default; only the duolog gate keeps it read-only): <reason>;
nothing ran`, and `peer open` opens no pane. Fix the reason. Any other run
with no looser entry is not checked this way and behaves as before.

`duolog route <role>` shows the result, such as
`codex sol medium mode=edits native=workspace-write (role implement)`. Its
JSON has a `permission` object with the level, the native value, the source
and what lowered it. Without a lead, route shows the request and
`ceiling: no lead`. `duolog doctor` lists each entry, says when it applies or
that it never does, and warns when the gate cannot be shown to run for an
entry that needs it. The delegate result line names the native mode when an
entry applies. `duolog decisions` and the usage records name it for every
run.

### What a Codex entry can add

A role entry for Codex may set `network: true` and `writable_roots`. They
apply only to that role's Codex route, only on `workspace-write` at level
`edits` or `full`, and never from a family entry or a project file. They
apply whether or not the entry names a `codex` value. duolog refuses a root
that is a volume root, the home folder or one of its parents, or that holds
or lies in credentials or agent and duolog settings. It compares the paths
after it resolves symlinks, and without case where the volume ignores case;
[Configuration](configuration.md#which-layers-may-change-worker-modes) lists
the folders. `danger-full-access` is not offered.

Every Codex `workspace-write` worker gets `network_access` and
`writable_roots` on its command line, `false` and `[]` unless an entry
allows more. Codex's own config files then cannot widen a worker. Whether
`codex exec` honours these keys is NOT VERIFIED.

Peers (`duolog peer`) do not use `worker_modes`; they keep the default flags.
A Codex `workspace-write` peer gets the same `false` and `[]`, so a cloned
repository's `.codex/config.toml` cannot widen it either.

## The default rules

The rules are in the `policy` section of the config. A rule has an action
(`read`, `write`, `exec`, `net` or `mcp`), a glob, a decision and optional
roles. An `mcp` allow rule may also say `read_only: true`; see "How a
worker's MCP tool calls are checked" below.

| Action | Match | Decision |
|---|---|---|
| read | everything | allow |
| read | `.env`, `*.pem`, `*.key`, `secrets/` | deny |
| read | duolog state in `.duolog`, except Markdown, the configs, `learned.yaml` and skill test plans | deny |
| write | `.duolog` folders and `.git` files or folders, at any depth; a command that cannot write, such as `echo` without a redirect or a listing, is not judged by this rule. The words of a `for` or `select` header are, when any command of the line can write, and so is a redirect into such a path after any command, loop or group | deny |
| write | `.claude/settings*.json`, `.claude/hooks/**` and the `.codex` folder | deny |
| write | everything, for the write roles | allow |
| exec | `go test`, `make build`, `npm run test`, `uv run pytest` and the like, for write roles | allow |
| exec | Check tools of each ecosystem, for write roles: `node_modules/.bin/eslint`, `.venv/bin/ruff`, `python -m pytest`, `go vet`, `cargo clippy`, `cmake --build`, offline `gradle` and `mvn`, `dotnet test --no-restore` and the like | allow |
| exec | Read commands: `cat`, `head`, `tail`, `ls`, `wc`, `rg`, `grep`, `find`, `sed -n 'N,Mp'`, `jq`, `sort`, `stat`, `tree` and others | allow |
| exec | `git status`, `git diff`, `git log`, `git show` | allow |
| exec | `git push`, `git reset --hard`, `git clean`, `git rebase` | deny |
| exec | `rm -rf *` | deny |
| exec | `claude` and `codex`, with or without arguments | deny |
| mcp | Nine named CodeGraph tools that read the code index, for every role | allow |
| net | everything | ask |

A read-only role can execute only commands named by a read-only allow rule,
such as `git status`, `git diff`, `cat` or `rg`. Build and test rules apply to
write roles. This limit holds in `full` too.

The default rules do not allow `go run`, `uv run` or `npm run deploy`, because
they can start any program. These commands ask. A test or build still runs the
project's own code, so a write role is only as safe as the code it can edit.

A wildcard rule for read commands grants reads only, for every role. `find`
and `rg` pass with known read options, so `find . -delete` or an unknown `rg`
option does not. Paths named by a read command follow the read rules, so
`cat .env` is denied and `cat /outside/notes.txt` asks. A shell glob in a
path asks because it can expand onto denied files. Recursive `grep` and `rg`
with `--hidden`, `-u` or `--no-ignore` ask unless their roots reach duolog
state. The state checks below deny those recursive commands. Plain `rg` and
the `Grep` tool can still search a denied file that is neither hidden nor
git-ignored:
keep such files out of the tree or ignore them. `git diff` and `git log` with
no path can also show a denied file that Git tracks, so do not commit one.

A `--glob` option of `rg`, or the glob of the `Grep` tool, asks when it
matches the name of a read deny, such as `.env`. The check is not complete:
`rg -g '*.go'` can still read a denied hidden file such as `.env.go`.

Quotes that only group literal text read as that text, so
`grep -n "TODO" a.go` and `git log --format="%h"` are plain reads. Read
commands can also quote literal patterns and paths with spaces, such as
`grep "a b" README.md`. Other commands with quoted spaces ask. Quoted text with
a glob, a brace, a tilde or a backslash still asks, and so does an empty
quoted word or `$` and a backtick inside double quotes. The shell would expand
such text, so the gate cannot read the words it passes on. A read-only role
may be denied instead of asked. A brace anywhere in a command asks for the
same reason. Quoted `rg` glob options follow the glob check above.

`sed` reads only as `sed -n 'N,Mp' file`. Other `sed` scripts ask. The gate
judges the files of the `r`, `R`, `w` and `W` commands and the `s///w` flag,
with or without a blank before the name. So `sed '1r/etc/passwd' x` needs the
person, and `sed 'w.git/config' x` is denied. The `e` command, the `s///e` flag
and a script the gate cannot read need the person, because the gate cannot
name what they reach. BSD `sed`, as on macOS, takes the word after a lone
`-i` as the backup suffix, and GNU `sed` takes it as the script. The gate
judges both readings, so `sed -i x w.git/config y` is denied. A backup
suffix also writes each input with the suffix added, and a suffix with a `/`
or a `*` needs the person, as in `sed '-i../*' -e s/a/b/ go.mod`. Each reading
follows its own `sed`: BSD `sed` ends a bracket expression at its own `]`, so
in `s/[/]/x/w /tmp/out` the first `/` belongs to the class, and the gate
judges `/tmp/out`. A script that both `sed`s can run must parse both ways,
or it needs the person.

`grep`, `jq`, `sort`, `uniq` and `file` pass only with options that change
the output, so `jq -f`, `sort -o`, `uniq - out`, `file -m magic` and ugrep's
`grep --filter` ask. A `jq` filter
that names `env` or a `$` variable asks, because it can print the worker's
API keys. The context options `-A`, `-B`, `-C`, `--after-context`,
`--before-context` and `--context` of `rg` and `grep` read with a number, as in
`rg -n -F X -A45 dir`; `rg --pre` still asks. `diff` and `yq` are not read
commands, because `diff -r` prints denied files and `yq` can write them.

A deny also holds inside any other command. `uv run git push` is denied like
`git push`, and `sed -n p .env` is denied like `cat .env`. A directory change
before another command, a shell glob, a program path with `..` and `git -c`
ask in every mode, because the gate reads the command text and cannot see what
they do. A path can match a deny
by accident: `go test ./secrets/...` is denied by the `secrets/` rule.

A shell glob that can leave the work root needs the person. A segment that
starts with a dot and a glob character, such as `.?`, `.*` or `.[!x]`, can
match `..` in `dash` and in `bash` before 5.2, so `cat .?/peer/go.mod` needs
the person. Quotes elsewhere in a word do not change this: in
`cat .?"/peer/go.mod"` the `?` still expands. The gate also expands every
other glob, with each bracket
expression such as `[!x]` or `[[:alpha:]]` matching any one character, so a
match through a symlink that leads out needs the person too.
The expansion of one command line may look at 10,000 names and take half a
second. A glob that needs more, as one through a symlink loop can, needs the
person.

An option value in the same word as a short option counts as a path when it
starts with `/`, `~` or `.`, after an optional `@`, or holds a `/` or a `\`
anywhere. A drive counts when a slash or a backslash follows it, as in
`-oC:\out`, and on Windows also alone, as in `-oD:out`. So
`sort -o/tmp/out x`, `make -C../peer` and
`sort -o_/../../out x` need the person, and `file -m.env x` is denied. A value
that is text for a known program stays text, such as the separator of
`cut -d/` or the pattern of `grep -e/api/`.

An assignment in front of a program can make it read a file. Git reads
`GIT_CONFIG_GLOBAL` and `GIT_DIR`, and `bash` reads `BASH_ENV`. The read rules
judge the value of such a variable, and of any variable whose name ends in
`_FILE` or `_CONFIG`. So `GIT_CONFIG_GLOBAL=.duolog/x git log` is denied. Any
assignment whose value, or a piece of it between colons, leaves the work root
needs the person, as `PATH=../peer/bin:$PATH make` does. The assignments of
`export`, `declare`, `typeset`, `readonly` and `local`, also after `builtin`,
count the same, and so does an assignment that stands alone, which a later
`export` hands on. So `export GIT_DIR=../peer/repo; git log` and
`GIT_DIR=../peer/repo; export GIT_DIR; git log` need the person.

Workers cannot start `claude` or `codex` themselves (rule `agent launch`).
The gate denies them by any program path or Windows launcher, behind
prefixes such as `env`, `timeout` or `sudo`, and through package runners
such as `npx`, `pnpm dlx` or `deno run npm:`, a tarball of either CLI
included. This check reads the command text, so a script or interpreter that
starts an agent, such as `python3 -c "import os; os.system('claude')"`, is
not caught.

Secrets include nested `.env` and `.env.*` files. Paths outside the working
tree ask in every mode, unless an explicit outside-path rule applies. A broad
`**` allow does not cover them. On macOS and Windows, path rules ignore case.
One exception: a Claude worker may read the large tool outputs Claude saved
for its own session, under `projects/<project>/<its session id>/tool-results`
in Claude's config folder. Another session's folder and every other path under
`~/.claude` still ask, and denies still apply.

A deny wins over an allow. When no rule matches, the decision is
`on_unmatched`, which is `ask` by default. The default denies always apply: a
project that sets its own `rules` replaces the allow rules, not the denies.

## Workers cannot read duolog state

The gate denies reads of duolog state before any policy allow. This protects
other workers' commands, answers and session data. The rule is `duolog state`.
It applies to `.duolog` folders at any depth, including outside the work root.

A worker may list the `.duolog` folder itself. Inside it, these files stay
readable:

- Markdown files at any depth, including task files and briefs.
- `config.yaml`, `config.local.yaml`, `learned.yaml` and
  `skill-tests/*.plan.yaml` at the paths shown.

All other reads under `.duolog` are denied. These include logs, session files,
cursors, the journal, `routing.learned.yaml` and task output captures.
The rule covers read tools and paths named by read commands such as `cat`.

Recursive `grep` and unrestricted `rg` searches are denied when their roots
contain the project's state folder. Searches rooted inside any `.duolog`
folder are denied too, including plain `rg token .duolog`.
Plain `rg token .` stays allowed because it skips hidden and ignored state.
A recursive search with unknown roots is denied when its working folder
contains state. Other recursive searches still ask.

Claude workers and peers also receive a `sandbox.filesystem.denyRead` list.
It names the shared project's logs, session file, cursors, journal and routing
data. It blocks reads by child programs only when the person's Claude sandbox
is already on. duolog never writes `sandbox.enabled` or turns a sandbox on.

## Workers cannot reach the telemetry and balance folders

The gate denies every worker tool request and command path that it can see
reaching the [telemetry](telemetry.md) folder or the balance folder beside
it. The rule is
`private usage store`. It comes before policy, modes and questions, so no rule
or mode opens the folders, and the gate asks no one.

The deny covers read and write tools, paths that commands name, and recursive
searches whose roots contain either folder. The gate compares resolved paths,
so a symbolic link, a junction, `/private/var` against `/var`, a change of
case or a Windows short name does not get around it. A path that does not
exist yet is placed by its closest existing folder.

The gate also denies `duolog telemetry` and `duolog balance` to a worker, by
any program path, in a pipeline or behind a wrapper such as `sh -c`. Global
flags before the subcommand, such as `--lead L` or `--lead=L`, do not hide
it. The commands refuse a worker too.

Tests or a build that read the folder without naming it are not stopped; see
[What the gate cannot see](#what-the-gate-cannot-see). Claude workers whose
sandbox is already on also get both folders in `sandbox.filesystem.denyRead`.
Codex workers and native Windows have the gate only.

When a lead model runs `duolog telemetry` or reads the folder, a Claude lead
asks you, and a Codex lead is told to have you run it in your terminal. A
Claude lead in `bypassPermissions` or `auto` is not asked; see the
[section on such a lead](#a-lead-in-bypass-or-auto-decides-by-itself).

## One ask per tool call

Claude runs two gate hooks for a tool call, PreToolUse and PermissionRequest.
Both reach the same question, but the lead is asked once. The first hook sends
the ask; the second waits for it and takes its answer, keyed by the session and
the tool call. Only a reply from the lead is shared, and only inside the ask's
wait. A deny stays a deny, and an ask that timed out, went unanswered or whose
record is missing or expired is never an allow: the hook asks for itself, or
denies when its own wait ends. The person-only flag is set on the one ask.

## Reads and writes outside the work root need the person

An outside-path ask is marked `p: true` on the bus. The peer's pending-ask
line says `needs the person`. This covers read tools, writes and exec asks
that name outside paths, such as `cat /outside/notes.txt` or
`git -C /other log`. A word that starts with `~` counts as outside: `~` and
`~/x` name the home folder, and `~name` the home folder of another login, so
`tar czf home.tgz ~` needs the person. An explicit outside-path rule still
applies as written; a broad `**` allow does not approve outside paths.

The lead's own hook guards shell commands that may run `duolog allow N`.
For a Claude lead, it asks Claude Code to prompt the person. For a Codex lead,
it denies the command. The hint names the owning lead and the project folder.
The person runs `duolog allow N --lead ID` there in their own terminal.
The lead may still deny the request. Web asks and asks inside the project
stay with the lead.

The guard also checks wrapped and compound commands. If it cannot read a
named request, it asks the person or gives the Codex terminal hint. Without a
known request number, it guards only while a person-only ask is pending, or
when it cannot read the bus. A pending ask has not expired or been answered.

The hook also guards `duolog remember` when the rule names a path outside the
project. A Claude lead asks the person. A Codex lead is denied, and the
person saves such a rule in their own terminal.

### A lead in bypass or auto decides by itself

When you run the Claude lead in `bypassPermissions` or `auto`, you let the
lead model decide, and none of the lead's guards asks you: not for
`duolog allow N` on a person-only ask, not for `duolog remember` with an
outside rule, not for `duolog peer show`, and not for telemetry or balance
data. `duolog allow` itself still refuses a person-only ask from the lead,
as below, so that ask waits for you. A command that may allow an ask sent
to another lead asks you, with `--lead` or without, since `--lead` names a
lead and does not prove who runs the command. In `default`, `acceptEdits`,
`plan` or any other mode, every guard asks you as above.

`duolog allow` and `duolog deny` also check who runs them, whatever the
hook saw. They look for the lead among their own parent processes: a lead
of this project found there may answer only its own asks, and `--lead`
must then agree with it. A command that runs in a Claude or Codex session
but finds no lead of this project above it, such as another project's lead
that changed folder, is refused, also from a script or under a name the
hook cannot read. So is a command with another Claude or Codex process
between it and the lead, such as a worker's. The person's own terminal has
no agent above it and answers with `--lead` as before. A lead whose process
duolog has not recorded runs `duolog lead register` in its own session.
A person-only ask is answered only there: with any Claude or Codex process
or a lead above them, `duolog allow` and `duolog deny` refuse it, since a
Claude subagent runs in its lead's process and no walk tells the two apart.

This check is best effort, like the guards above. It follows the live
parent processes and Claude's `CLAUDECODE` mark, and the lead, its workers
and your terminal all run as the same user. A process that detaches from
its parent, with `nohup`, `setsid`, `tmux new -d` or a Rex pane, and drops
`CLAUDECODE`, looks like your own terminal and is not stopped. No check
within one user can tell the two apart.

The lead decides by itself only a plain call that stays in the hook's own
project, whose bus the guard reads. A guarded call asks you when it may run
duolog in another project: a `cd`, `pushd` or other change of folder or
environment, also after `builtin`, `noglob` or `nocorrect`, a wrapper such
as `env` or `sh -c` around duolog, a subshell, a
`-C`, `--project` or state-folder flag, or anything duolog cannot read for
sure, such as `duolog allow "$N"`, a backslash or an open quote. A word in
single quotes, or in double quotes without `$`, a backtick or a backslash,
is read as the literal word the shell passes on: the rule in
`duolog remember 'Bash(go test *)'` goes ahead, and a quoted `'cd'` or
`'--project'` still counts as a `cd` or a flag. An allow or deny then names
only ask numbers and `--lead`.

duolog reads the mode from the lead's own hook input, which Claude Code
writes, and from the mode it recorded for that registered lead from earlier
hook input. Both must say `bypassPermissions` or `auto`; a lead that has
just left bypass is asked again. A Claude subagent shares its lead's
session id, but its hook input carries an `agent_id` and may carry the mode
of its own definition. duolog records the lead's mode only from input
without an `agent_id`, and a call with one is always asked, so a subagent
set to `bypassPermissions` cannot turn the guards off for a lead in
`default`. No environment variable or file can turn
this on. A worker or a peer cannot use it: its session is not a registered
lead, and its hook takes the worker path. A Codex lead keeps the deny in
every mode, `danger-full-access` included, because duolog reads its mode
from a transcript file and not from the hook input.

## What the gate cannot see

The gate sees what an agent asks for, not what a program it runs reads.
Project code run by a write role, such as `go test`, can read files without
naming them in the command. The gate alone cannot stop those reads. This
holds for duolog state, secrets and the telemetry folder alike.
Claude's deny list adds protection only where the sandbox is already on.

duolog does not sandbox the lead itself. A lead that edits the bus or other
state files directly can bypass the hook's approval guard: a line written
straight into `.duolog/bus.ndjson` is not checked. The hook's guard cannot
see `duolog allow N` inside a script file, a Makefile target or a renamed
binary; `duolog allow` itself then refuses an ask that the lead running it
does not own. It is a best-effort check on the lead model, not a sandbox.

## How a worker's MCP tool calls are checked

The defaults allow CodeGraph's read tools for every role: `codegraph_search`,
`codegraph_context`, `codegraph_explore`, `codegraph_node`,
`codegraph_callers`, `codegraph_callees`, `codegraph_impact`, `codegraph_files`
and `codegraph_status`. Each has its own exact `codegraph/<tool>` rule. These
tools only read the code index. Exact rules also match Codex's MCP names;
a wildcard rule would not match these tool names. Other CodeGraph tools ask.

An MCP tool call is an `mcp` action. Its target is `<server>/<tool>`, such as
`tracker/get_issue`. Both agents send the hook the name
`mcp__<server>__<tool>`. Claude also sends the server name as configured, so
an allow must name that server exactly: a rule for `team-api` does not allow
`team_api`. Codex writes `-` in a server name as `_` and sends nothing more, so
for Codex `-` and `_` count as one character, and a name with `__` inside the
server or the tool matches only a rule without `*`. A deny compares the full
folded name, `mcp__<server>__<tool>`, so a server named `my__wiki` cannot slip
past a deny of `mcp__my__wiki__delete`. A 1.1.0 rule such as
`{action: unknown, match: "mcp__tracker__*"}` still covers those tools.

duolog cannot tell whether a tool reads or writes. So an MCP tool runs only
when a rule names it. No mode or role allows one by itself, and `full` mode
does not turn the question into an allow. At level `plan` and in a read-only
role, the rule must also mark the tool `read_only: true`, as in
`{action: mcp, match: "tracker/get_*", decision: allow, read_only: true}`;
the default CodeGraph read tools need no mark. Any other MCP tool is denied
there, not asked. `read_only` belongs on an `mcp` allow rule only; on any
other rule it makes the policy invalid, and the gate denies everything.
Elsewhere, an unlisted tool asks the lead, and
a lead that waits for its foreground `delegate` denies it at once. The worker
then stops with `mcp:<server>/<tool>` in `needs`. A deny in the lead's own
Claude settings, such as `mcp__tracker__create_issue` or `mcp__tracker`,
holds for workers too.

Three things add allow rules. None replaces the default rules:

| Where | Applies to |
|---|---|
| `capabilities.mcp_tools: {tracker: [get_issue]}` | Every role. The same list limits the tools the server offers, bridged or the worker's own |
| `duolog remember mcp:tracker/get_issue` | The writing roles, or the roles named with `--role`. It saves the rule in `.duolog/learned.yaml` |
| An `mcp` rule in `policy.rules`, such as `{action: mcp, match: "tracker/get_*", decision: allow, roles: [implement]}` | What the rule says. This list replaces the default rules, so copy them first |

List read tools only, unless a worker should change things in that service.
`duolog remember --native` also adds `mcp__<server>__<tool>`, or
`mcp__<server>` for `<server>/*`, to `.claude/settings.local.json`; a glob in
the server name has no Claude form. Codex sets
MCP tool approval per server in its own `config.toml`, so duolog writes no
Codex rule.

Claude loads a deferred MCP tool with its `ToolSearch` tool first. The gate
allows `ToolSearch`, because it only loads a tool definition; the call of the
tool is checked on its own.

We checked this live with the gate on (OBSERVED, Codex 0.165.0 and Claude
Code 2.1.316). A Codex worker sent `mcp__<server>__<tool>` for an HTTP server
to the gate, and a Claude worker sent the same form with the server name in
`mcp_server.name`. Listed tools ran; an unlisted tool was denied with
the line above.

## How a worker asks the lead

For an `ask` decision, duolog sends a numbered request to the lead on the bus
and waits up to `ask_timeout_seconds` (120 by default). The wait is capped at
120 seconds, below every shipped gate hook's timeout of 150 seconds. Doctor
warns about larger settings. Timeout and cancellation always deny.

A lead waiting for its foreground `delegate` cannot answer a request. In that
case the gate denies at once and names the command. The worker retries once
with a plainer form, then stops with `done: false` and the command in `needs`.
The lead decides, adjusts the task or permitted policy/mode, and re-runs. Deny
rules still apply. When the lead is free, normal numbered asks work as below.
`peer wait` returns at once for a pending gate ask of its own peer. An ask
names its peer, and an ask from a worker outside a peer pane stops no
`peer wait`. An ask expires when its asker's wait has passed, so a request
that timed out no longer counts as pending. The worker gets a deny, so it
never waits forever. The lead gets one message on the bus, such as
`ask 11960 from peer-x expired and was denied: exec make lint`. It names the
request, the peer or `worker <role>`, and the action. An ask that the worker
cancelled sends no such message. A worker that tries to ask
the user, with Codex `request_user_input` or Claude `AskUserQuestion`, is
denied, because only the lead talks to the user.

For ordinary asks, the lead answers with:

```bash
duolog allow 3
duolog deny 3
```

Outside-path asks need the person's approval as described above. A lead may
deny any ask.

A worker cannot run these commands. See what duolog decided and why with
`duolog decisions`. Long request summaries keep their head and tail, mark
the cut, and point to this command for the full action.

## How to allow a command for good

When the user approves a command that workers will need again, the lead runs:

```bash
duolog remember '.venv/bin/ruff check app/a.py'
```

duolog saves an allow rule in `.duolog/learned.yaml` for every write role, or
for the roles named with `--role`. The rule keeps the program and its plain
subcommand words and lets the rest vary: this one allows
`.venv/bin/ruff check *`. A program with no subcommand, a program that runs
any code from its arguments, such as `python3.12` or `awk`, and a `run` or
`exec` subcommand, such as `uv run`, are saved exactly as approved. Give your
own glob with `--match`.

The command refuses a rule that cannot make the command allowed: a deny still
wins, and shell syntax that the gate cannot read always asks. A worker cannot
run it. The file stays out of Git, like the rest of `.duolog/`.

With `--native`, duolog also allows the command for the lead:
`Bash(prefix:*)` in `.claude/settings.local.json` and a `prefix_rule` in
`.codex/rules/duolog.rules`. Codex runs an allowed prefix outside its
sandbox, which is more than the worker rule grants, so `--native` is off by
default. A Codex rule matches a prefix, so an exact command gets no Codex
rule. Give the options of `remember` before the command. We checked the Codex
rule with `codex execpolicy check`; a live Codex session that reads it is not
verified.

## Where the checks run

The gate is a hook. The command is `duolog gate --agent <agent> --event
<event>`, for `PreToolUse` and `PermissionRequest`. Claude runs it directly
with `command` and `args`, without a shell. A missing binary does not block
a tool in the measured `claude -p` run. It also cannot offer an install.

Both delegate and peer put the directory of the running duolog executable
first in the worker's PATH, even when the parent's PATH lacks it, so the
worker's hooks find that same binary. Keep that binary in place for the
whole session. The measured Claude run did not fail closed when the
executable was missing, and PATH order cannot change that. Codex keeps its
POSIX and Windows guards. Interactive Claude display of a missing executable
has not been measured.

## A Codex lead that starts a Claude worker

A Codex lead runs `duolog delegate` through its shell tool. Two things matter,
and both were measured on 2026-10-31 with Codex 0.165.0 (see the
[live verification](research/2026-10-30-live-verification.md)):

1. Codex runs a login shell and resets `PATH`. A bare `duolog` gave
   "command not found". Give the lead the full path to the binary.
2. In the `workspace-write` sandbox, `duolog` runs and starts `claude -p`, but
   Claude cannot read its login and answers "Not logged in". Under macOS the
   login lives in the Keychain, which the sandbox blocks. `codex sandbox claude
   auth status` shows `loggedIn: false` there and `true` with
   `sandbox_mode="danger-full-access"`. Adding `~/.claude` to the writable
   roots did not help. So in this macOS setup, `duolog delegate` needs
   permission to run outside the Codex sandbox, so that Claude can read its
   login. Approve that command through the lead's normal approval flow,
   rather than running the whole lead with `danger-full-access`. This check
   does not establish the behavior on other OSes. A token passed in the
   environment may also work; we did not test it.

A Codex worker started by a Claude lead needs none of this. Claude starts it
outside any Codex sandbox.

A Codex worker has the same problem in the other direction. In the cost study,
`duolog delegate` run inside a Codex lead's sandbox stopped with
`failed to initialize in-process app-server client: Operation not permitted`.
It worked once the lead ran the command outside the sandbox (OBSERVED). The
lead's shell can also find an older `duolog` first on PATH, so check
`duolog version`.

## Known limits

These come from the research page and are OBSERVED, not VERIFIED:

- `PermissionRequest` did not fire under `codex exec`.
- `codex exec` skipped a hook that was not trusted, with no warning.
  `duolog delegate` refuses a Codex write role if it cannot see trust, and it
  warns for a read-only role.
- A `PreToolUse` deny still blocked a tool under `bypassPermissions` in
  Claude.
- Whether hooks fire for tools inside a Codex `spawn_agent` child is NOT
  VERIFIED.
- A looser worker mode (Claude `auto` or `bypassPermissions`) removes the
  native second lock, so a gate that does not run lets a tool through. That is
  why delegate refuses such a run when the gate preflight fails. Whether
  `codex exec` honours the sandbox keys behind Codex network and writable
  roots is NOT VERIFIED.
- One reported Windows 11 check covered install, hooks, workers and gate
  denies in Windows PowerShell 5.1. Real agents in PowerShell 7 and on
  Windows arm64 remain unchecked.

## How PowerShell commands are checked

The PowerShell tool counts as an exec action. Only a plain named command can
pass without review. Expressions, quotes, variables, pipelines, script blocks
and command chains ask, even in full mode. A read-only role can run only named
read commands allowed by policy, such as `Get-Content`, `Get-ChildItem` and
`git status`. `Remove-Item -Recurse` and `git push` match deny rules first.
Windows drive letters, backslashes and case are normalized before path rules.

## Related pages

- [Configuration](configuration.md): the `policy` section
- [Telemetry](telemetry.md): what the private usage stores hold
- [Rex](rex.md): permission questions in a live session
- [Live verification](research/2026-10-30-live-verification.md): hook results
