# How duolog decides what a worker may do

The duolog gate decides worker permissions without a model call.

## How the gate orders its checks

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

## How a worker's native mode is chosen

Each worker also starts in a native mode of its agent: a Claude
`--permission-mode` or a Codex sandbox. By default `duolog delegate` passes
the flag that matches the lead's mode, never above `acceptEdits` for Claude or
`workspace-write` for Codex. The exception is a lead that allows Claude
`auto` (a Claude lead in `bypassPermissions` or `auto`, or any lead at level
`full`): its Claude workers and peers start in `auto`, never in
`bypassPermissions`. A read-only role or level `plan` keeps `default`, and
`duolog mode edits` under a lead in `bypassPermissions` gives `acceptEdits`
again. The `worker_modes` section of the config
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
  `timeout` below 150 seconds: Claude does not wait for an async hook, and
  runs the tool when a hook times out without `onFailure: "block"`. Claude's
  managed settings count too: when they set `allowManagedHooksOnly` or
  `disableAllHooks`, only a gate in the managed settings themselves counts.
  Otherwise the message names the hook and its file and says to run
  `duolog install`.
- Codex: the folder is trusted, and an approved gate hook runs duolog's own
  gate command. Codex skips a hook whose command changed after approval, so
  an approval of another command does not count. When `duolog install`
  changes the gate command or its timeout, the approval Codex held then does
  not count either; approve the hook again in `/hooks`. A linked worktree of the
  project gets the project's trust and approvals first; see
  [below](#how-a-linked-worktree-gets-codex-trust).
- The `duolog` binary is on the worker's PATH.
- No writable root overlaps a protected folder, checked again at launch.

If a check fails, delegate refuses the run. No worker starts and the other
agent does not run in its place. The message reads
`gate preflight failed for <agent> <native> (<source>): <reason>; nothing
ran`. Fix the reason, or remove the entry. For a Claude worker or peer at
level `plan` it reads `gate preflight failed for claude at level plan
(native default; only the duolog gate keeps it read-only): <reason>;
nothing ran`, and `peer open` opens no pane. Fix the reason. Other runs with
no looser entry skip this preflight.

`duolog route <role>` shows the result, such as
`codex sol medium mode=edits native=workspace-write (role implement)`. Its
JSON has a `permission` object with the level, the native value, the source
and what lowered it. Without a lead, route shows the request and
`ceiling: no lead`. `duolog doctor` lists each entry, says when it applies or
that it never does, and warns when the gate cannot be shown to run for an
entry that needs it. The delegate result line names the native mode when an
entry applies. `duolog decisions` and the usage records name it for every
run.

### How to check Codex trust before a worker starts

`duolog doctor` warns when an enabled Codex has no folder trust or approved
hooks. The warning names the folder. Run `codex` in that folder, accept the
trust question, and approve the hooks in `/hooks`.

A project-scope `duolog install` ends with the same note when Codex is
enabled for the project and either step is missing, even with
`--agent claude`. These checks only read trust and approvals. They do not
write them or change the gate's rules.

When Codex lacks trust or hook approval for a write role, duolog skips it
for that run and tries the next routing candidate. It fails only when no
candidate remains. The skip line names the worker's folder and the command
to fix its trust once. In a linked worktree, it also explains why duolog
could not carry over the source checkout's trust, when its checks can tell.
The source may lack folder trust or gate
approval. A stale source gate approval needs renewal in `/hooks` in the
source checkout. The `.codex` folders may differ; the message names the
first path. Hooks to copy may also be outside those that `duolog install`
writes.

### How a linked worktree gets Codex trust

duolog gives a linked worktree of the lead's checkout the Codex trust that
the checkout has. Codex keeps folder trust and hook approvals per folder in
`config.toml` of its home, `CODEX_HOME` or `~/.codex`. A new worktree is a
new folder to Codex, so without this you would open `/hooks` in each one.

Before `delegate` or `peer open` runs Codex in a worktree, duolog checks
these things. It does so whether the command runs in the lead's checkout or
inside the worktree; from inside, the main checkout is the source of trust.

- The worktree is a linked worktree of the lead's repository. duolog runs
  `git` without any `GIT_` variable, and `git worktree list` of the lead's
  checkout names the worktree, not as prunable or bare. The worktree's common
  Git folder is the lead's. A folder with a copied `.git` file does not
  count.
- Codex trusts the lead's checkout, or the main checkout, and has approved
  duolog's `PreToolUse` gate hook there.
- Each approved source hook and target hook is one that `duolog install`
  writes for the same event: the current guard, which runs bare `duolog`
  from PATH, or the dev launcher form, whose launcher must be the same in
  both checkouts. Older guards count for hooks other than the gate. Each of
  `command` and `commandWindows` must hold its own OS's form, since Codex
  runs only that one. A handler with `args`, which Codex does not pass, a
  program path such as `./tools/duolog`, extra shell syntax, and foreign
  hooks do not count. Their approvals stay untouched. An approval covers the
  target hook at its own place, or one that is the same at another place.
  An approval that install made stale is not copied.
- Before it writes trust, the worktree's `.codex/` folder holds the same as
  the `.codex/` folder of the checkout whose trust is copied, apart from
  `hooks.json`. Both have the same names, and each file is the same, byte for
  byte. A link or any other special file in either folder counts as a
  difference, and so does a `.codex` that is itself a link. A `.codex` that
  is absent in both is the same. Codex loads this folder only where it trusts
  the project, its `config.toml` can name any file in it, and another branch
  may change it. When the folders differ, duolog writes nothing and names the
  first path that differs.

Then duolog appends the missing tables to `config.toml`: the worktree's
`trust_level = "trusted"`, and an approval hash for each eligible target
hook. An identical hook keeps the source hash; a different form gets its
own hash. It changes nothing else in the file and keeps its comments and its
line endings. It never changes a sandbox or an approval policy. Before the
first change it saves `config.toml.duolog.bak`; an empty backup counts as
none. It replaces the file in one rename. It holds the lock file
`config.toml.duolog.lock` from reading the file to replacing it, so two
worktrees that start at once both get their tables. When Codex changes the
file meanwhile, duolog reads it and plans once more. The command prints one
`duolog:` line for each entry it wrote. A `# duolog:` comment above the
tables and a `# duolog: end` comment below them mark them as duolog's.

duolog compares the two `.codex/` folders again before each `delegate` or
`peer open` in a worktree that it gave trust. A worker, a checkout or a new
worktree at the same path can change that folder later. When the folders
differ, duolog takes its own tables for that worktree out of `config.toml`:
the trust and the copied approvals, under the same lock and backup rules.
It takes only the tables between its two comments that still hold what it
wrote, and stops at the first one that differs. A trust or approval that you
or Codex added later stays. It prints why, and Codex does not start there.
A delegation tries its next candidate; a peer pane does not open.
Codex runs in that worktree again once you have checked it and
trusted the folder yourself in `/hooks`. duolog never removes a trust that
it did not write.

duolog checks the folders again only when it uses that worktree. Until then
the trust stays in `config.toml`. If you run `codex` in the worktree
yourself after its `.codex/` folder changed, Codex still trusts it. The same
holds for a new folder made at the path of a removed worktree. Check
`.codex/` before you run Codex there by hand, or remove the worktree's
tables between the two `# duolog:` comments yourself.

If a check fails, duolog writes nothing. A delegation skips Codex for the
run and tries the next candidate. Its line names the worktree and explains
the failed trust or hook check when it can. Run
`codex` in that worktree, accept the trust question, and approve the hooks
in `/hooks`. duolog also writes nothing when you set another trust level
for the worktree. Hooks that are not `duolog install`'s own need
`/hooks` in each worktree. Claude needs no hook approval, so duolog writes
nothing for Claude.

### What a Codex entry can add

Role entries can give Codex `workspace-write` workers network access or
extra writable roots, within the lead's ceiling and after protected-path
checks. Families and project files cannot grant these extras. Every
`workspace-write` worker and peer gets explicit sandbox keys; peers ignore
`worker_modes` and get no extras. Whether Codex honours the keys is NOT
VERIFIED. See
[Configuration](configuration.md#which-layers-may-change-worker-modes) for
values, path restrictions and defaults.

## Which rules apply by default

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
| exec | Read commands: `cat`, `head`, `tail`, `ls`, `wc`, `rg`, `grep`, `find`, `sed -n 'N,Mp'`, `jq`, `sort`, `stat`, `tree`, `cmp A B` and others | allow |
| exec | `mkdir`, for write roles; a folder in a denied or outside path is still denied or asked | allow |
| exec | `git status`, `git diff`, `git log`, `git show`, and `git worktree list` | allow |
| exec | `git branch --show-current`, and `git rev-parse` alone or with `HEAD`, `--short HEAD`, `--abbrev-ref HEAD`, `--show-toplevel` or `--is-inside-work-tree`, exactly so | allow |
| exec | `git push`, `git reset --hard`, `git clean`, `git rebase` | deny |
| exec | `rm -rf *` | deny |
| exec | `claude` and `codex`, with or without arguments; a path operand of a program that never runs it, as in `go test -v ./claude ./pkg`, is not a start | deny |
| mcp | Nine named CodeGraph tools that read the code index, for every role | allow |
| net | everything | ask |

A read-only role can execute only commands named by a read-only allow rule,
such as `git status`, `git diff`, `cat` or `rg`. Build and test rules apply to
write roles. This limit holds in `full` too.

The default rules do not allow `go run`, `uv run <script>` or `npm run deploy`,
because they can start any program. These commands ask. A test or build still
runs the project's own code, so a write role is only as safe as the code it can
edit.

`uv run` is allowed for `pytest`, `ruff`, `ty`, `mypy`, `pyright` and
`basedpyright`, for write roles only, as `.venv/bin/` is. Any other `uv run`
command asks.

### How Run and Check preflight works

Before a worker starts in a write role, `duolog delegate` takes each command in
the task's `## Run` and `## Check` sections and judges them with the code the
gate uses for that role in that work root. A command is a line, or list item,
that is one backtick span, or a line inside a fenced block. In a fenced block a
line that starts with `#` is a comment and is skipped, and a line that ends in
`\` joins the next into one command. The MCP `delegate` tool writes each `run`
and `check` item as one backtick span. The judgment is made for the route that
starts, in that route's worker mode: a blocked first route is left for the
fallback, and the task is refused only when no route can run it. A replay and a
judge run are not judged. If any answer is `ask` or `deny`, no worker starts
and the command exits non-zero. The message names each blocked command with
its section and decision. For `ask` it shows `duolog remember '<command>'`,
which the person may run to approve it. The MCP `delegate` tool returns the
same list in the `blocked` field, with `remember` on each `ask`.

A preflight only reports. It allows nothing and remembers nothing. A Check line
that cannot be read as a command is not a block: it is named in the output, and
in `skipped_checks` over MCP, on a refusal and on every run that starts. A
read-only role runs no Check command, so it is not judged. Pass
`--skip-preflight`, or `skip_preflight` over MCP, to start the worker anyway.

`duolog doctor` and `duolog install --check` print the project's own test and
lint commands, from the survey `duolog calibrate` does, that the gate would not
allow for `implement`. They only print.

| Form | Example | Decision |
|---|---|---|
| Named read with known options | `cat f`, `cmp A B` (exactly two files, no options), `find .`, `rg x src` | Judge paths by read rules; unknown options, `find -delete` and shell path globs ask |
| Unquoted pattern glob | `grep .e* f` | Ask: expansion can add input files; deny if it reaches state |
| Recursive grep/rg with hidden or ignored files | `rg --hidden x src` | Ask unless a state root requires deny; plain rg can still read denied non-hidden, non-ignored files |
| Claude Grep | Search hidden files | Passes `--hidden`; can read a hidden file not ignored by Git. Keep secrets out of the tree or ignore them |
| Git read without paths | `git diff`, `git log` | Can show tracked denied files; do not commit secrets |
| Git line range or pathspec magic | `git log -L1,1:.env`, `':(top).env'`, `':/x'` | Judge the plain path, from the repository root for top magic |
| Git pathspec spelling a denied name | `'.en?'`, `'sec*/x'`, `'a.p*'` | Deny even quoted: Git expands it. `*` and `?` can match `/`, including `'secrets*'`, `'sec*x'`, `'sub?.env'` and `'.duolog?bus*'` |
| Git glob with enough literal text | `'main.p*'` | Spelling needs three characters, or dot plus an extension letter. Check the tree; ask when matches exist and none is denied |
| Short or other Git glob | `'.e*'`, `'se*'`, `'a*m'`, `'src/*'`, `'*.go'` | Deny if it spells and matches a denied file, or can name state; otherwise ask. History may hold files absent from the tree |
| Unquoted Git option glob | `git log -*` | Ask; deny in plan mode or a read-only role. Same for a word before `--` that can expand to an option (`[-]*`, `?L*`, `*`); `*.go` is a filename and asks |
| rg/Grep glob matching a deny name | `rg -g '.env' x` | Ask. Incomplete: `rg -g '*.go'` can read a denied hidden `.env.go` |
| Search glob option attached to flag | `grep --include=*.go x src` | Needs no quotes: expansion cannot add a different file set. Covers grep include/exclude/exclude-dir and rg g/glob/iglob |
| Search glob option as its own word | `rg -g *.go x` | Needs at most one local match; more matches add pattern/files and ask |
| Option naming input files | `--file=*.go` | Ask |
| Recursive grep with include/exclude options | `grep -rn x cmd --include=*.go` | Walk bounded roots and judge every file, ignoring filters. Denied files or symlinks ask; state denies. BSD grep can match paths, and expansion can change exclusions. Name subfolders; the project root usually contains state |
| Recursive grep without filters | `grep -rn x cmd` | Ask |

Quotes that only group literal text read as that text, so
`grep -n "TODO" a.go` and `git log --format="%h"` are plain reads. Read
commands can also quote literal patterns and paths with spaces, such as
`grep "a b" README.md`. Other commands with quoted spaces ask. Quoted text with
a glob, a brace, a tilde or a backslash still asks, and so does an empty
quoted word or `$` and a backtick inside double quotes. Only the tilde of a
Windows short name, between a letter and a digit, is plain. The shell would
expand such text, so the gate cannot read the words it passes on. A read-only
role may be denied instead of asked. A brace anywhere in a command asks for the
same reason. Quoted `rg` glob options follow the glob check above.

The quoted search pattern of `rg`, the first operand when no option gives the
pattern, is data, and gets the decision of the same `rg` with a plain pattern.
So it may hold a `*`, as in `rg -F 'func (r *Registry)' a.go`, and, in double
quotes, a backslash before one of `|`, `(`, `)`, `*`, `;` or `&`, as in
`rg "Root\(|Dir" a.go`. A `$` or a backtick in double quotes still asks. The
pattern of `-e`, a quoted path and any other word follow the rules above, and
so does a quoted pattern that names `.duolog`, `.env`, `.pem`, `.key` or
`secrets`.

| Command form | Gate decision |
|---|---|
| `sed -n 'N,Mp' file` | Read; other scripts ask |
| sed `r`, `R`, `w`, `W`, `s///w` | Judge file paths, with or without a blank before the name |
| sed `e`, `s///e`, an unreadable script, or sed/awk code from stdin | Person; the gate cannot determine the target |
| sed `-i`, bracket expressions and backup suffixes | Judge BSD and GNU interpretations; both must parse when both can run. Backups write each input path plus the suffix; a suffix with `/` or `*` needs the person |
| sed/awk scripts starting with `/`, and tr sets | Patterns, not outside paths; full mode confirms the ask |
| awk `getline <`, `print >`/`>>`, `-f`, `-E`, `--exec`, `-i`, `--include`, `@include` | Judge file paths |
| awk `-l`, `--load`, `@load` | Judge extension paths |
| awk code/extension names without `/` | Check each AWKPATH/AWKLIBPATH folder, including line assignments/exports; a system extension with no AWKLIBPATH needs the person because it can write |
| awk pipes, `\|&`, `system()`, ARGV, computed filenames or backslash escapes in filenames | Person |

These checks also inspect commands inside sudo, caffeinate, sh/bash `-c`
(including `bash -o pipefail -c`) and find `-exec`.

`find -execdir` and `-okdir` run their command in the folder of each match,
and a starting point is a match in the folder that holds it. So the gate
judges the command's relative paths, its plain words among them, from each
starting point and from the folder that holds it, not from the working
folder. A path that leaves the work root from any of them needs the person:
`find <work root> -execdir cat x \;` reads `x` next to the work root.
`find sub/deep -execdir cat ../x \;` stays inside and goes to the lead.

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
This also holds for a glob where a search takes its pattern, because the shell
can expand it into a pattern and files to read: `grep /x/pa* f` and
`git log -G /x/pa* x` need the person, and `grep '/x/pa*' f` stays a pattern.
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

A wrapper's options come before the program it starts. The gate skips the
value of each wrapper option that takes one, written apart, after `=` or in
a cluster of short options, as in `nice -n 5`, `timeout -s KILL 5`,
`stdbuf -o L`, `env -iC dir` and `bash -euo pipefail`, so it judges the
program after them. A value that names a file or a folder is judged too:
the file of `xargs -a` and `--arg-file` and the folders of BSD `env -P` by
the read rules, and the folder of `env -C` and `--chdir` as a path the
program may read and write. The program's relative paths and words count
from that folder as well as from the working folder, so `env -C ../peer make`
needs the person and `env -C .git sed -n wconfig x` is denied. `sudo` keeps
its own check.

`bash` and `sh` run the startup file of `--rcfile` or `--init-file` before
the command. The gate judges that file with the read rules, and reads the
`-c` script after it as any other. So `bash --rcfile ../peer/rc -i` needs the
person, `bash --rcfile .env -i` is denied, and a startup file read from
standard input, as `--rcfile /dev/stdin`, needs the person.

An assignment in front of a program can make it read a file. Git reads
`GIT_CONFIG_GLOBAL` and `GIT_DIR`, `bash` reads `BASH_ENV` and `sh` reads
`ENV`. The read rules
judge the value of such a variable, and of any variable whose name ends in
`_FILE` or `_CONFIG`. So `GIT_CONFIG_GLOBAL=.duolog/x git log` is denied. Any
assignment whose value, or a piece of it between colons, leaves the work root
needs the person, as `PATH=../peer/bin:$PATH make` does. The assignments of
`export`, `declare`, `typeset`, `readonly` and `local`, also after `builtin`,
count the same, and so does an assignment that stands alone, which a later
`export` hands on. So `export GIT_DIR=../peer/repo; git log` and
`GIT_DIR=../peer/repo; export GIT_DIR; git log` need the person.

`DOCKER_HOST` in front of a program, pointing at a Docker daemon on this
machine, is the one assignment the gate looks past. The value must be a
`unix://` socket at an absolute path with no `~`, `$`, glob, `.` or `..` in
it, or `tcp://` to `localhost`, `127.0.0.1` or `[::1]`, with or without a
port. The Docker client connects to that socket and does not read or write it
as a file, so the gate judges the command after the assignment as if it stood
alone. `DOCKER_HOST=unix:///<home>/.colima/default/docker.sock go test ./...`
is decided as `go test ./...`, and a `docker` command after it as `docker`
alone. A remote daemon, such as `tcp://build-host:2375` or `ssh://`, still
needs the person, and so does every other assignment, `PATH`, `LD_*`,
`DYLD_*`, `GIT_*`, `DUOLOG_*`, `CLAUDE_*` and `CODEX_*` among them. To hand
`DOCKER_HOST` from your own shell to every worker instead, see
[What workers inherit](configuration.md#what-workers-inherit).

A project may allow other assignments by name. An `exec` allow rule whose
match starts with the assignments, such as `UE_ROOT=* task ue:*`, allows
`UE_ROOT=/opt/engine task ue:build` in `edits` and `full` mode for the roles
it names. The line must be one command with plain values: no `$`, quotes,
redirect, `;` or `&&`. Every assignment must be one the rule names, so
`UE_ROOT=x LD_PRELOAD=y task ue:build` still needs the person. The command
after the assignments still meets every deny and the role's limits. Name
only variables that cannot make the program run other code.

The `agent launch` rule denies workers starting `claude` or `codex`,
including paths, Windows launchers, wrappers, package runners and CLI
tarballs. It reads command text; an interpreter or script that starts an
agent can escape this check.

| Text | Agent-launch check |
|---|---|
| Quoted data or a here-document read only by cat/tee/echo/printf | Starts no agent; other rules still judge writes and content |
| Substitution, pipe into a shell (even across a newline), or uncertain parsing | Read word by word |
| Comment, backslash outside single quotes or a quoted here-document, or `${` in double quotes | Uncertain; read word by word |
| `go test -run` or `-skip` value | Test name, not a command, unless it holds a substitution or the test uses `-exec`, `-toolexec` or `-vettool` |

Only agent-launch rules skip data. Every unquoted word still counts, and
other rules inspect the full line: a report containing `git push` can
still be denied.

The gate reads two common shell forms as the plain commands they run, so they
need no ask. It judges the line as written first, and a deny of it stands.

| Form | Read as |
|---|---|
| `$(go env GOROOT)/bin/go test ./...`, also quoted, and `.../bin/gofmt` | `go test ./...`, so the rules for `go` apply |
| `cat > /dev/null <<'EOF'`, `: <<'EOF'` or `true <<'EOF'` on its own line, with its body | Nothing: the line is left out |

The first form is the way around a version manager's `go` shim in this
repository. Only this exact substitution names a program, and only as the
first word of a command, not after a redirect such as `>|`. Any other one,
such as `$(go env GOPATH)/bin/x`, still asks, in full mode too. So does a
variable that holds the path, as in `GO=$(go env GOROOT)/bin/go; $GO vet`,
since a quoted name such as `export "G""O=git"` can set it again. The line
asks too when it names `GOROOT`, `GOENV`, `GOTOOLCHAIN` or `IFS` elsewhere,
since they change the folder that `go env GOROOT` prints or how the shell
splits it, or when it defines a function or uses `alias`, `hash`, `enable` or
`trap`.

A here-document that only `cat > /dev/null`, `:` or `true` reads runs nothing.
Agents write one as a note beside a read. The line keeps it, and asks as
before, when the body is unquoted and holds `$`, a backtick or a backslash,
which the shell expands. It also keeps it when a pipe or `&&` on the line
before continues into it. A line that defines a function or uses `alias`,
`hash`, `enable` or `trap` keeps it too, since these can make `cat` run
something else. `python3 - <<'EOF'` and `cat <<'EOF' | sh` run their body,
so they still ask.

`go env -w` and `go env -u` ask the person in every mode, full too. They
change the user's Go env file outside the work root, and every later `go`
run reads it, the lead's too. `go env GOPATH` and other reads follow the
read rules.

Secrets include nested `.env` and `.env.*`. Denies win over allows on every
host, ignoring case because Linux may use a case-insensitive volume. Read
and write paths with a segment shaped like a Windows 8.3 short name, such
as `DUOLOG~1`, are denied on every host; use the long name. Outside paths
need the person unless an explicit outside-path rule applies.

Workers may read these agent-owned paths with read tools and named read
commands. Denies still apply; none grants execution or writes.

| Path | Limits |
|---|---|
| Claude's `projects/<project>/<its session id>/tool-results` | Only the worker's own saved large tool outputs; another session asks |
| `~/.agent/skills`, `~/.agents/skills`, `~/.codex/skills`, `~/.claude/skills` | Includes plugin cache `plugins/cache/<marketplace>/<plugin>/<version>/skills`; `CODEX_HOME` and `CLAUDE_CONFIG_DIR` replace config roots |
| Recursive skill reads | Same checks as task Read entries; no link may lead out of a skill tree |
| Hidden, credential or config files inside skills | Still ask, including `.netrc`, `auth.json`, `.mcp.json`, `settings.json` and folders/globs holding one |
| Skill root linked to home or an agent config folder | Grants nothing; a link to another sufficiently narrow folder works |

A Claude worker's session temp folder (its scratchpad) counts as its own;
`$T` below stands for its path. Other sessions' folders need the person.

| Form | Decision |
|---|---|
| Read tool or named read command | Read its scratchpad |
| Redirect, mkdir/touch, cp, ls, cat/tail/grep, including a longer line | Follow the same mode as inside the work root |
| Read/list output | Must stay on screen or in the folder. `cat $T/x \| sh`, `cat $T/x > x.sh` and `ls $T \| sh` need the person, even across a continued pipe |
| Command/process substitution; multi-command line with here-document, exec, eval, source or dot | No scratchpad path exception: output can become code or unseen file content. A plain single command still counts |
| Execute or copy out; ls -R/-L; `..`; link out or dangling link | Person |
| cp options | Only -f, -i, -n, -p, -v, -L; other options can copy a link and need the person |
| Wrapper such as xargs | Person: it can add paths |
| New path and a command able to make a link names it or an ancestor | Person: ln, mv, tar, git or an unknown program could run before the write |

A deny wins over an allow. When no rule matches, the decision is
`on_unmatched`, which is `ask` by default. The default denies always apply: a
project that sets its own `rules` replaces the allow rules, not the denies.

## A worker reads what its task lists

A worker of `duolog delegate` may read a path outside the work root that its
task lists under Read: an absolute file, folder or glob, or one that starts
with `~`, which names the lead's home folder. A relative entry names a path
in the project. Only a task file under `.duolog/`, where the lead
writes tasks, counts. For a task file elsewhere, such as one that a cloned
repository ships, delegate ignores such entries and says so on stderr.
delegate hands the list to the worker's gate in `DUOLOG_READ`, which a
worker cannot set. The list allows the read tools and named read commands
such as `cat`, `wc` or `rg` on those paths, in every mode and role. It allows
no write, no other command and no path that a deny rule, the state rule or
the private store rule refuses.

delegate leaves out an entry that is too wide, by the rule for Codex writable
roots: a volume root, the home folder or a parent of it, or a path that holds
or lies in a folder of credentials or agent settings, such as `~/.ssh`. The
gate checks each entry again after it resolves its symlinks. Reads of such a
path still ask.

The gate resolves symlinks first, so a listed name that leads elsewhere still
asks. A path written with `..`, and a folder with a symlink below it that
leads out, are not covered either. A listed glob covers files, not folders,
and a shell glob in a command still asks. Peers do not get the list.

In linked worktrees, put Markdown attachments in that worktree's `.duolog/`,
or list their absolute main-checkout paths under Read. Relative entries
stay relative to the work root and cannot override state denies.

A search, a listing or a glob of a listed folder counts only when everything
below the search root is something a search may read. A symlink anywhere
below it does not count, since `rg -L` or `grep -R` follows it, even to a
`.env` beside the folder. Nor does a Windows junction, such as the hidden
`My Music` in `Documents`, or anything else that is neither a folder nor a
regular file, such as a socket or a device. Nor does a hidden name, such as
`.env` or `.duolog`, a name shaped like a Windows short name, or a path that
a read deny or the state rule refuses, such as `server.key`. The gate walks
up to 10,000 entries to find out. Otherwise the search asks, and the plain
files in the folder stay readable one by one.

A read command that reads the files a list names, such as
`wc --files0-from=list`, is no plain read and asks. GNU programs take any
unambiguous start of a long option, so `wc --files0=list` asks too, and so
does any long option of `wc` or `du` that the gate does not know. A checksum
check, such as `shasum -c list` or `sha256sum --check`, reads every file its
list names, from a file or from standard input, so it asks too.

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
The rule covers read tools and paths named by read commands such as `cat`,
also behind an assignment or a wrapper, as in `LC_ALL=C cat .duolog/bus.ndjson`
and `env grep .d*/* f`, behind a program path or with an option the gate does
not know. An input redirect counts: `grep S <.du*/bus.ndjson` is denied, and
`cat < .en?` too, as it spells a denied name.

A file inside a `.duolog` folder is readable only when it is a regular file.
No part of its path from the `.duolog` folder down may be a symlink, so it
cannot lead out of the folder. A symlink there is denied whatever its name,
even `notes.md` that leads to `bus.ndjson`. So are a folder, a socket or a
device, and a path written with `..` that stays inside the folder.

Recursive `grep` and unrestricted `rg` searches are denied when their roots
contain the project's state folder. Searches rooted inside any `.duolog`
folder are denied too, including plain `rg token .duolog`. A search of one
readable file, such as `rg -n token .duolog/brief.md` or Claude's Grep on
that file, is judged like `cat` and the Read tool. A glob, a folder or a
file that the state rule denies is still denied.
Plain `rg token .` stays allowed because it skips hidden and ignored state.
A recursive search with unknown roots is denied when its working folder
contains state. Other recursive searches still ask.

Claude workers and peers also receive a `sandbox.filesystem.denyRead` list.
It names the shared project's logs, session file, cursors, journal and routing
data. It blocks reads by child programs only when the person's Claude sandbox
is already on. duolog never writes `sandbox.enabled` or turns a sandbox on.

## Workers cannot reach the telemetry and balance folders

The `private usage store` rule denies worker paths reaching telemetry,
balance or account state (`duolog/accounts` in the user config folder,
including `quota-reads.json`) before policy, modes or questions. It covers
read/write tools, command operands and recursive roots. Paths resolve
links, junctions, macOS aliases, case and Windows short names; new paths use
the closest existing parent.

The gate and commands also deny worker `duolog telemetry`, including global
flags, pipelines and wrappers. The gate reserves the `balance` verb too;
there is no such CLI command.
[Telemetry](telemetry.md#who-can-read-the-data) describes lead controls,
sandbox protection and limits of checks on programs.

## How one tool call produces one ask

Claude runs two gate hooks for a tool call, PreToolUse and PermissionRequest.
Both reach the same question, but the lead is asked once. The first hook sends
the ask; the second waits for it and takes its answer, keyed by the session and
the tool call. Only a reply from the lead is shared, and only inside the ask's
wait. A hook input with no tool call id is keyed by the question instead,
which can name another call, so that reply is reused once and only within 120
seconds of the start of the ask, however long the wait is. A deny stays a
deny, and an ask that timed out, went unanswered or whose record is missing
or expired is never an allow: the hook asks for itself, or denies when its own
wait ends. The person-only flag is set on the one ask.

## Reads and writes outside the work root need the person

Outside-path asks have `p: true` and need the person in every mode. This
covers read/write tools, `~`, `~/x`, `~name`, Windows drive-rooted paths and
exec operands such as `git -C /other log`. On Windows, `/x` and `\x` start
at the current drive's root and need the person unless they resolve inside
the work root. An explicit outside-path rule can allow one; a broad `**`,
wildcard exec rule or `on_unmatched: allow` cannot. Read-command allows
survive only after their paths pass read rules. `full` confirms ordinary
policy asks, never outside-path asks.

Only a word that names a file makes a command the person's. A search
pattern does not, even with `..` or `/` in it. That covers the first operand
of `grep`, `egrep`, `fgrep`, `rg` and `git grep`, unless `-e` or `-f` gives
the patterns, and each `-e` or `--regexp` value. It also covers the text of
`rg -r` and the `-G` and `-S` values of `git log`, `git diff` and `git show`.
The sets of `tr` count as patterns, and so does an `awk` program without
`system`, `getline`, a redirect or a pipe. So `grep -n '\.\.' a.go` stays
with the lead, while `grep x ../peer/f`, `grep -f ../x a.go` and
`grep --exclude-from=/etc/x y a.go` need the person. An unknown option, or one
whose value may or may not be the next word, makes every word count as a path.
This holds only for a program the part runs itself: behind a wrapper such as
`xargs`, `parallel`, `env`, `timeout` or `nice`, every word counts as a path,
since `xargs` can add an `-e` that turns the pattern into a file to read. So
`xargs grep /x` and `xargs sed -n '/a/p'` need the person. Nor does it hold
when a word after the program holds a `$` outside single quotes, or a `*`,
`?`, `[` or `~` outside quotes, since the shell can expand it to an option:
`grep /x $P` and `grep /x *` need the person.
In these commands, and in `cat`, `head`, `tail`, `sort`, `uniq`, `wc`, `cut`,
`ls`, `go list` and a `sed` that only substitutes or prints, a quoted argument
is one word. A `<` or `>` inside its quotes redirects nothing. Other commands
are read word by word, so a path inside `sh -c 'cat ../x'` still needs the
person. A stream such as `/dev/null` is not a path outside the work root.
A `cd` may work or fail, so a relative path after it is judged both from the
folder the line starts in and from the folder `cd` leads to: in
`cd sub && cat link/a`, a symlink `sub/link` that leads out needs the person.
A write through a dangling symlink goes where the symlink points, so
`echo x > sub/dangling` needs the person when that place is outside. A `..`
after a symlink goes up from where the symlink leads, so with `sub/link`
leading out, `cat sub/link/../secret` and a read tool on that path need the
person. The script that `sh`, `bash` or `zsh` runs is a path too:
`bash /elsewhere/x.sh` needs the person.
A line with bash's `$'...'` or `$"..."` quoting, or a brace expansion outside
quotes such as `{..,.}` or `{a..c}`, needs the person in every mode, since
the gate does not expand them: `cat $'.env'` and `cat {..,.}/peer/f` do.
So does a path that starts with a variable, as `cat $HOME/.ssh/x` and
`cat $PWD/../x` do, and any word with `$IFS`, as `cat x$IFS.env` does. A
variable whose values the line shows stays with the lead: `for d in a b; do
cat $d/x; done` and `d=sub; cat $d/x`. `src/$X` stays with the lead too. The
`search` of a `jq` `import` or `include` is a folder that jq reads, so
`{search:"/x"}` needs the person, and so does a `search` value the gate cannot
read.

The lead's own hook guards shell commands that may run `duolog allow N`.
For a Claude lead, it asks Claude Code to prompt the person. For a Codex lead,
it denies the command. The hint names the owning lead and the project folder.
The person runs `duolog allow N --lead ID` there in their own terminal.
Only the person's terminal may allow or deny a person-only ask. The lead
and `answer_ask` handle ordinary asks, including web and in-project asks.

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
no agent above it and answers with `--lead` under the same rules. A lead whose
process
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

The same holds for writes. In `edits` mode a write role runs `go test`
without an ask, and its test or program code can write `.duolog`, a line in
`.duolog/bus.ndjson` included, such as an allow reply to its own ask. The
bus is therefore not a trust boundary against code that a worker runs.

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
past a deny of `mcp__my__wiki__delete`. A rule such as
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
and waits. Timeout and cancellation always deny. Set the waits under `policy`
in `.duolog/config.yaml`; doctor warns about a value above its cap:

| Key | Waits for | Default and cap |
| --- | --- | --- |
| `ask_timeout_seconds` | An ask the lead can answer | 120 seconds |
| `person_ask_timeout_seconds` | An ask only the person can answer | 600 seconds |

Every wait ends at least 30 seconds before the gate hook's own timeout, so the
gate answers with a deny before the agent kills it. `duolog install` writes a
timeout of 630 seconds. When a hook has a shorter one, as a hook from an
earlier install has, the gate shortens the wait to fit: 120 seconds under a
timeout of 150. Run `duolog install` to get the full wait; `duolog install
--check` reports the old value. Codex then needs the hook approved again in
`/hooks`, because its approval covers the timeout.

An upgrade that changes Codex hook text or timeouts needs new approval in
`/hooks`; duolog cannot approve hooks for you. The install note names the file,
each changed event and field with old and new values, and any new events.

An agent treats a hook it kills as a failed hook, and the tool call can go on:

- Claude Code blocks the call only when the hook sets `onFailure: "block"`,
  which `duolog install` writes on the gate hooks and which needs Claude Code
  2.1.295 or later. It also blocks every tool call when the `duolog` program
  cannot start, so a project that commits these hooks needs duolog on each
  contributor's `PATH`.
- Codex has no such setting. The gate's own earlier deny is the only guard.

The lead's hooks repeat open asks until answered or expired; see
[Bus](bus.md#when-the-hooks-show-a-message). A foreground delegate denies
asks immediately; the worker retries one plainer command, then returns
`done: false` with `needs`. The lead can adjust the task or permitted rules
and retry; denies still apply. Claude workers and peers are told to batch
read-only Bash commands with `;` and avoid `node -e` and `python -c`.

A free lead answers ordinary asks with `duolog allow N` or `duolog deny N`;
only the person answers `p: true` asks. `peer wait` stops for its own peer's
pending ask, not another worker's. Timeout denies and sends one expiry
status naming the request, peer or worker role, and action; cancellation
sends none. Worker user-question tools are denied: only the lead talks to
the person.

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
rule. The options of `remember` (`--match`, `--role`, `--native`) may come
before the command. They may also follow it when the command is one quoted
argument, as in `duolog remember 'make x' --match 'make *' --role planner`.
With several unquoted words every word is part of the command, including `--`
and `--native`, so `duolog remember git log -- docs` keeps the `--`.
We checked the Codex rule with `codex execpolicy check`; a live Codex session
that reads it is not verified.

## Where the checks run

The gate is a hook: `duolog gate --agent <agent> --event <event>`, for
`PreToolUse` and `PermissionRequest`. Claude runs it directly with `command`
and `args`, without a shell. Current gate hooks set `onFailure: block`;
install duolog before using committed hooks. The older missing-binary probe
lacked this protection; see the
[historical probe](research/2026-10-30-live-verification.md#missing-binary).

Delegate and peer put the running duolog executable's directory first on
the worker's PATH so hooks find the same binary. Keep it there for the
session. Codex keeps its POSIX and Windows guards.

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

## What remains unverified

These come from the research page and are OBSERVED, not VERIFIED:

- `PermissionRequest` did not fire under `codex exec`.
- `codex exec` skipped a hook that was not trusted, with no warning.
  `duolog delegate` skips Codex for a write role if it cannot see trust and
  tries the next candidate. It warns for a read-only role.
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
