# How to start using duolog

Install duolog, set up a project and send a first task. On macOS or Linux:

```sh
curl -fsSL https://github.com/lorem-dev/duolog/releases/latest/download/install.sh | sh
```

The script verifies SHA-256 checksums and installs to `~/.local/bin`. Run it
again to update. It warns if that directory is missing from `PATH` and never
edits your shell profile. Add the printed install directory to PATH if
needed, then open a terminal in your project and run:

```sh
duolog install --scope project
```

When install adds or changes the duolog MCP server or the user skills, restart
Claude Code and Codex sessions that are already running, as they load both only
at start; install prints a line to say so, and a lead started earlier is told
once at its next prompt.

The Linux binaries are static, so any libc works. The installer also runs
with busybox `sh` and `wget`. Claude Code and Codex have their own system
requirements.

Pin a release or choose another directory:

```sh
curl -fsSL https://github.com/lorem-dev/duolog/releases/latest/download/install.sh | sh -s -- --version v1.8.2
curl -fsSL https://github.com/lorem-dev/duolog/releases/latest/download/install.sh | DUOLOG_INSTALL_DIR="$HOME/bin" sh
```

An installer from a release page (`releases/download/v<version>/`) installs
that release; one from `releases/latest/download` installs the newest.
`--version` or `DUOLOG_VERSION` overrides both, and `--dir` sets the folder.
`--via-go` prints the source install command. Use the PowerShell installer
on Windows.

On Windows, use Windows PowerShell 5.1 or PowerShell 7 (`pwsh`):

```powershell
irm https://github.com/lorem-dev/duolog/releases/latest/download/install.ps1 | iex
```

The installer adds its folder to your user PATH (in `HKCU`, never the machine
PATH) when it is missing, and says so. Set `DUOLOG_NO_PATH=1` before running it
to skip that and add the folder yourself. Then open another terminal and run
`duolog install --scope project`. The default folder is
`$env:LOCALAPPDATA\Programs\duolog`. Both installers select amd64 or arm64
and verify SHA-256 before installing. An invalid download leaves an existing
binary unchanged.

To pin a Windows release or choose a directory:

```powershell
$env:DUOLOG_VERSION = 'v1.8.2'
$env:DUOLOG_INSTALL_DIR = "$env:LOCALAPPDATA\Programs\duolog"
irm https://github.com/lorem-dev/duolog/releases/latest/download/install.ps1 | iex
```

Project setup adds one block, between `<!-- duolog:begin -->` and
`<!-- duolog:end -->` lines, near the top of `AGENTS.md` and the Claude
instruction file. If the project has `.duolog/config.yaml` but a new machine
has no duolog on `PATH`, the block asks the agent to offer the install command.
The agent must get your agreement before running it. The block also tells the
agent to hand work to duolog's workers and to start its own subagent only when
duolog says `run here`. User setup does not add the block.

Each install rewrites only the text between the markers and migrates the
older one-line offer. Uninstall removes the block or old offer. A file with
only one of the two markers stops install and uninstall with an error that
names it; nothing changes.

## How to install with an agent

Use the one-line install in [README](../README.md#how-to-install). The agent
asks whether to set up this project: its Git root, or the main checkout for
a linked worktree. Either answer installs the binary and the agent's plugin;
yes also runs `duolog install --scope project` and `duolog doctor`. It stops
at the first failed command and leaves permission prompts to you. A
release's install prompt installs that release.

## How to update duolog

Ask your agent to update duolog: the `duolog-update` skill does every step.

By hand: rerun the installer above with `DUOLOG_VERSION=latest` and check
`duolog version`, update your agent's plugin with the marketplace commands
below, run `duolog install --scope project` in each project that has
`.duolog/config.yaml` (and `--scope user` if you installed for all
projects), approve changed Codex hooks in `/hooks`, run `duolog doctor`, and
restart both agents.

## Which install methods are available

duolog is not on a package registry; use the installers or Go. Single-line
`duolog` commands work in POSIX shells and PowerShell.

## What you need before you start

| Need | Why |
|---|---|
| Go 1.26.11 | Only to build from source |
| Git | duolog stores its state in `.duolog/` at the root of the main worktree |
| Claude Code, Codex, or both | duolog starts them; it does not replace them |
| Rex | Only for live second sessions. See [Rex](rex.md) |

## How to build the binary

Run this in the repository root:

```bash
go build -o .cache/bin/duolog ./cmd/duolog
```

On Windows, use `-o .cache/bin/duolog.exe`. Put `.cache/bin` on PATH.
Current Claude gate hooks set `onFailure: block`; install duolog before
using committed hooks. The older missing-binary probe lacked this
protection. Codex hooks skip a missing binary for the lead and block a
worker gate. See [Permissions](permissions.md#where-the-checks-run).

## How to install the hooks and the skill

`duolog install --scope project` writes these files and prints each path:

| File | Content |
|---|---|
| `.claude/settings.json` | Claude Code hooks, merged with yours |
| `.codex/hooks.json` | Codex hooks, merged with yours |
| `~/.claude/skills/duolog/SKILL.md` | The skill that teaches the lead to delegate, at user scope |
| `~/.claude/skills/duolog-skill-check/` | The skill that offers [skill tests](skills.md) after a change |
| `~/.claude/skills/duolog-calibrate/` | The skill that runs [calibration](calibration.md) when you ask |
| `~/.claude/skills/duolog-peers/` | The skill that lists [peers](rex.md) and switches your Rex view to one |
| `~/.claude/skills/duolog-update/` | The skill that updates duolog when you ask |
| `~/.agents/skills/duolog/`, `~/.agents/skills/duolog-skill-check/`, `~/.agents/skills/duolog-calibrate/`, `~/.agents/skills/duolog-peers/` and `~/.agents/skills/duolog-update/` | The same five skills for Codex |
| `~/.claude.json` | The `mcpServers.duolog` entry, at user scope (`.claude.json` under `CLAUDE_CONFIG_DIR` if set) |
| `.claude/settings.json` | Also three `permissions.allow` entries for the duolog MCP tools |
| `.duolog/config.yaml` | A starter config, only if none exists |
| `AGENTS.md` | A marked block: offer to install on a machine without duolog, and work through duolog |
| `.claude/CLAUDE.md` or `CLAUDE.md` | The same block, with one more line about the MCP tools; uses the nested file if it exists |

Use `--agent claude` or `--agent codex` to install for one agent. Use
`--scope user` to install in your home folder for all projects. SessionStart
then creates `.duolog/` in a new project and registers its lead.

User scope honors `CODEX_HOME` and `CLAUDE_CONFIG_DIR`. If they are unset,
Go's native home lookup supplies `.codex` and `.claude` under your home.

### Where duolog keeps its skills

Every install, at either scope, writes the duolog skills at user scope:
under `~/.claude/skills` for Claude, or `skills` under `CLAUDE_CONFIG_DIR`,
and under `~/.agents/skills` for Codex. One copy serves every project, so a
project needs no copy of its own. Except for `duolog-update`, which also works
outside a duolog project, each skill's description starts with
"Use only in a project that has .duolog/config.yaml", and its first line
tells the agent to stop elsewhere. In other projects only the skill's
one-line listing is in the agent's context. Where the Claude plugin is
enabled, the plugin supplies Claude's skills and install writes none.

`SKILL.md` records its version under `metadata.duolog-version`. Install
replaces a skill whose files all match a duolog release; a lead's session
start updates an older such skill. Both leave alone, with a note, a skill
you changed or added a file to, a folder duolog did not write, and a folder
that is or holds a link. `install --check` and `doctor` report these. To
restore a skill, keep your changes elsewhere, remove the folder and run
`duolog install`.

Older versions wrote the skills into the project, under `.claude/skills`
and `.agents/skills` or the folder that `agents.codex.skills_dir` names.
`duolog install --scope project` removes such a copy when every file in it
is one that some duolog release wrote, unchanged. A copy with a file you
changed or added stays, and install prints a note. Remove it when you no
longer need it.

Codex hooks store a POSIX command and `commandWindows` together. Claude
hooks use `command: "duolog"` with `args` and no shell on every OS, including
the plugin. Both hook files can be committed and shared across OSes. No Git
Bash is needed for Claude hooks. PowerShell tool calls use the same gate as
Bash tool calls.

An enabled Claude plugin supplies the hooks and skill on every OS. The
installer skips those copies and removes older native hooks to avoid running
each gate twice. Codex still needs `duolog install`: its marketplace plugin
supplies the skill, but does not install hooks. The next section has the
marketplace commands.

A marketplace plugin's skill checks `duolog version` and offers the binary
install if it is missing. In a project someone else set up, the marked block
in `AGENTS.md` and `CLAUDE.md` offers it when `.duolog/config.yaml` exists;
the skill also checks. Both require agreement before running the install.
If the binary exists but the rules are off, the skill runs `duolog doctor`
once per session and reports what it names, such as `/hooks` for Codex.
A hook cannot offer the install when its binary is missing.

If a file already exists and changes, duolog first copies it. A project file
goes to the same path under `.duolog/backup/`, which Git ignores, so
`git add -A` does not pick it up. A file in your home folder goes to
`<file>.duolog.bak`. A duolog skill is replaced with no backup.
Uninstall at either scope removes user-scope skills and `mcpServers.duolog`
for every project, and says so. It removes only the allow entries recorded
at its own scope. Changed skill files and server entries stay, with a note.
Other projects can restore them with `duolog install`.
It also removes the marked instruction block; an instruction file is
deleted only if duolog created it and it is now empty. Install records
allow entries in `.duolog/claude-allow-entries.json` for project scope or
`duolog-allow-entries.json` beside user `settings.json` for user scope.

If `mcpServers.duolog` in `~/.claude.json` runs something other than
`duolog mcp`, it is yours: install and uninstall leave it and print a
note, and `duolog install --check` and `duolog doctor` print a line for it.
Rename it and run `duolog install` to get the duolog tools.

A new duolog binary does not change the hooks that an older one installed.
`duolog doctor` names each install that is out of date, with its files and
the command that fixes it. The lead's session start says so too. To check
without writing anything, run:

```bash
duolog install --check
```

It takes the same `--scope` and `--agent` flags. Use it in a Git hook or a
CI job. Only duolog's own hooks and skill folders count; your other hooks,
settings and skills do not.

| Exit code | Meaning |
|---|---|
| 0 | Current, or not installed: a folder with no duolog hooks or skills |
| 1 | Out of date, so `duolog install` would change it, or it could not be checked |

A missing duolog skill at user scope, or one that another duolog version
wrote, makes the install out of date, at either scope. An unchanged copy in
the project does too, since install removes it.

Lines that need you, such as a changed duolog skill, a foreign
`mcpServers.duolog`, shared skills folders or hooks that run twice beside
the plugin, do not change the exit code.

A repository can commit the hooks. A clone on a machine that never ran
`duolog install` has the hooks and no user skills, which is out of date. A
CI job for such a repository checks the hooks only:

```bash
duolog install --check --hooks-only
```

Codex needs project trust and hook approval: start Codex in the project and
press `t` in `/hooks`. Without both, hooks are silently skipped. duolog can
copy this approval to linked worktrees; see
[Permissions](permissions.md#how-a-linked-worktree-gets-codex-trust).

Sign in to the agents before sending a task. Free workers get no API key,
and free Claude workers start with fast mode off. Key-only logins need
[paid-run opt-in](configuration.md#if-you-use-an-api-key-on-purpose). See
[Telemetry](telemetry.md) for the local history.

## How the lead uses the duolog MCP server

A Claude lead gets `duolog mcp`, an MCP server with four tools. Each tool
calls the same code as the CLI command it stands for, so it can do no more
than the lead's shell can. Codex leads keep the skill and the CLI.

| Tool | What it does |
|---|---|
| `delegate` | Hands a task to a worker from typed fields and waits; returns the whole result, or `run_here` as data |
| `peer` | Drives a live [Rex peer](rex.md#how-to-drive-peers-with-the-mcp-tool): open, send, wait, answer, status, close, list |
| `answer_ask` | Allows or denies a worker's or a peer's permission ask by its number |
| `status` | Lists peers, open asks, delegations, load and quota |

A hand-over costs the lead one turn. `delegate` waits by default (`wait`
defaults to true) and returns the result in its response: `state`, `done`,
`summary`, `decisions`, `needs`, the `files` the worker reports changed, a
`diffstat` of those files against `HEAD` (plus the files new to Git), and the
`answer_path` of the result record. The lead needs no background start, no
notice and no output file. A worker's asks behave as for a foreground
`duolog delegate`: the gate denies them and the worker lists the commands in
`needs`. `wait` false starts the run and returns at once.

A waiting call defaults to, and is capped at, 10 minutes; the MCP client's
own timeout may be shorter. A timed-out call leaves the run running. To
wait again, call `delegate` with `action: result` and `task_path` set to the
returned id. If the client lost the reply, find the id with `status`, or
resubmit the same running task: duolog returns its existing run instead of
starting another. For CLI delegation, use a background command and wait
for its notice. CLI `peer wait` defaults to 30 minutes; the MCP `peer` wait
has the same 10-minute default and cap as MCP `delegate`.

Install adds the server as one entry at Claude's user scope, from either
`--scope`. It also adds `mcp__duolog__delegate`, `mcp__duolog__peer` and
`mcp__duolog__status` to `permissions.allow` of the install scope, so the first
call does not ask. It never adds `mcp__duolog__answer_ask`: the lead's guard or
you decide that call, as for `duolog allow`. The marked block in `CLAUDE.md`
tells the lead to use the tools, one waiting `delegate` call per hand-over, and
the CLI only where a tool is missing.

Only the lead gets the tools. Outside a folder with `.duolog/config.yaml`, and
in a worker or a peer, the server lists no tools, so they cost nothing there.

Keep deferred tool loading on, which is Claude Code's default. Claude then
loads a tool's schema only when it needs it. We measured about 12 tokens per
tool per turn, and about 155 tokens once per tool when its schema loads. With
`ENABLE_TOOL_SEARCH=false`, Claude adds about 17,000 tokens to every turn.
`duolog doctor` says so when the setting is off.

To remove the server by hand, delete `mcpServers.duolog` from
`~/.claude.json`. `duolog install --check` and `duolog doctor` report a
missing or different entry and missing allow entries.

## How to install the plugins from the duolog marketplace

A marketplace is a Git repository that lists plugins. This repository is its
own marketplace, named `duolog`. It is not in the official plugin
directories yet, so each agent needs the address once. Install the binary
first, because the plugins call it.

In Claude Code, run these commands in a terminal:

```sh
claude plugin marketplace add lorem-dev/duolog
claude plugin install duolog@duolog
```

Or run the same steps inside a session:

```text
/plugin marketplace add lorem-dev/duolog
/plugin install duolog@duolog
```

In Codex, run these commands in a terminal:

```sh
codex plugin marketplace add lorem-dev/duolog
codex plugin add duolog@duolog
```

Start a new session after the install, so that the agent loads the plugin. In
`duolog@duolog`, the first word is the plugin and the second is the
marketplace.

The address can take other forms. Claude Code tries `lorem-dev/duolog` over
SSH first and then HTTPS, so the HTTPS form only skips the failed first try.

| Address | When to use it |
|---|---|
| `lorem-dev/duolog` | The normal case |
| `https://github.com/lorem-dev/duolog.git` | You want HTTPS from the start |
| `./path/to/duolog` | You test a local copy of this repository |

Use these commands to update or remove the plugin:

| Task | Claude Code | Codex |
|---|---|---|
| See what is installed | `claude plugin list` | `codex plugin list` |
| Get the new marketplace list | `claude plugin marketplace update duolog` | `codex plugin marketplace upgrade duolog` |
| Update the plugin | `claude plugin update duolog@duolog` | `codex plugin add duolog@duolog` |
| Remove the plugin | `claude plugin uninstall duolog@duolog` | `codex plugin remove duolog@duolog` |
| Remove the marketplace | `claude plugin marketplace remove duolog` | `codex plugin marketplace remove duolog` |

To share the Claude Code plugin with a project, add `--scope project` to both
Claude Code commands. They write the marketplace and the plugin to
`.claude/settings.json`. Commit that file, so that other people get the same
setup.

## How to check that everything works

```bash
duolog doctor
```

Doctor lists binaries and versions, hooks, Codex and Claude trust, Rex and
the instruction block. A config error comes first, names its file and line,
and stops the checks. The Claude trust line reads the project flag from
user state; an unreadable flag is `unknown`, not a failure. An untrusted
folder ignores project `permissions.allow`: start Claude there and accept
the dialog. `install --check` counts a missing or outdated instruction
block as out of date; doctor reports it for information. Doctor makes no
model call unless you pass `--probe` or Claude's plan has no quota windows
in `/usage`, when it may call Haiku once per route.

## How to send your first task

A task is a Markdown file with five required sections: Goal, Read,
Write, Limits and Check, with optional Run between Limits and Check.
Each section is a heading, usually `## Goal`; `# Goal` and `### Goal` work too.
Save it inside the project. `.duolog/task.md` is a good place, because Git
ignores `.duolog/`.

```markdown
## Goal
Review the function in the listed lines for bugs.

## Read
- internal/example/sum.go:10-40

## Write
Nothing.

## Limits
Do not change files. Answer in at most 3 lines.

## Check
Name the function and the line.
```

Run lists required generators, formatters and code writers that are not checks.
Use the same command syntax as Check. For a write role, preflight judges both
sections before the worker starts, unless you pass `--skip-preflight`.

Write each Check command as one backtick span on its own line, or in a fenced
block with one command per line (a `#` comment line is skipped, and a line
that ends in `\` goes on in the next). Before a worker
starts, duolog judges them with the gate and stops when one would ask or be
denied; see
[Permissions](permissions.md#how-run-and-check-preflight-works).

List files with line ranges in Read. duolog puts those lines in the worker's
standard input, up to `wire.inline_max_bytes`, so the worker does not spend
calls on reading them. In a task file under `.duolog/`, an absolute path,
folder or glob in Read, or one that starts with `~`, lets the worker read
those files outside the project; see
[Permissions](permissions.md#a-worker-reads-what-its-task-lists).

Put files that a task attaches, such as a contract or notes, in the work
root's `.duolog` folder as Markdown, for example `.duolog/s5/contracts.md`.
A worker reads inside its own work root. In a linked worktree that is the
worktree, so a read of the main checkout asks the person unless the task
lists the absolute path under Read.

Send the task to a role:

```bash
duolog delegate review_intermediate --task .duolog/task.md
```

The command finds its lead from `--lead`, then `DUOLOG_LEAD`, then a registered
ancestor, then the only live lead in the project. With none or several, it
refuses. The SessionStart line gives the ID for `--lead` when needed. `route`,
`mode`, `roster` and `peer` use the same choice.

Use a long shell-tool timeout for delegate; split tasks beyond the host's
limit. An ask denies at once while the lead waits on delegate. The worker
returns `done: false` with `needs`. Decide, adjust the task or permitted
policy/mode, and re-run. See [Permissions](permissions.md).

In linked worktrees, tasks, edits and sync use the current checkout. Relative
task lookup checks the current directory first, then the checkout root.
Sessions and other `.duolog/` state remain in the main checkout.

The command prints one line:

```text
done=true summary=<one line> files=<comma separated paths>
```

The worker must write `answer.json`. duolog takes the result only from that
file. An exit code of 0 from the agent does not mean success.

The line can also carry two fields:

| Field | What it means |
|---|---|
| `decisions=<n>` | The worker made n choices that the task left open, such as a library or a name. They are listed under `decisions` in `answer.json` |
| `outside=<paths>` | Files that changed during the run but are not under any path that Write lists. A change the lead made meanwhile shows up too |

`outside` needs Git, a role that can write and a Write section that lists
paths.

The line aims at 512 bytes. When it is longer, duolog cuts the summary first
and then the `files` list, which then ends in `...`. It never cuts `needs`,
`decisions` or a warning such as `outside=` or `rules are off`, so a long
warning can make the line longer than 512 bytes. `answer.json` keeps the
whole summary and list.

A `run here:` line starts no worker; follow the
[routing result](configuration.md#how-a-fallback-returns-work-to-the-lead).

Exit code 3 means the task is waiting on a capability that the lead must
provide first. Exit code 1 is a failure.

## How to see what the work cost

```bash
duolog stats
```

The command reads `.duolog/usage.ndjson` and prints tokens and an estimated
cost for each role, agent and model. The prices come from the config. They are
estimates, not a bill.

`INPUT` is all input tokens. `CACHED` is the part read from the prompt cache,
priced at the `cached` rate. `CACHE WRITE` is the part that Claude wrote to its
prompt cache. A write to the five-minute cache costs 1.25 times the `input`
rate, and a write to the one-hour cache costs 2 times that rate. Claude Code
workers usually write to the one-hour cache. Codex reports no cache writes.
Records from duolog 1.4.0 and older have no cache writes, and their cost stays
as it was.

## How to check the remaining subscription quota

Run `duolog quota` to see Claude and Codex quota; Codex uses `codex app-server`.
Claude windows come from `/usage` or, when it shows none on a confirmed
subscription, one short Haiku call without saving a session.
The output names this probe as the source, and both reads use the same cache:

```bash
duolog quota
duolog quota --json
```

Each agent shows its plan, used percentage and reset time for each window.
Claude also shows extra usage. Codex also shows credits and per-model limits
when available. The command does not spend credits to reset limits. It asks
each agent's CLI for quota and does not parse the agent's auth files itself.

A Codex seat of an enterprise or workspace plan can have a per-person spend
limit instead of the five-hour and weekly windows. duolog shows it as the
`individual` window: the share of the limit used and its reset time, such as
`individual 11.48% used, resets in 3d 14h`. See
[Configuration](configuration.md#how-paid-runs-are-allowed-and-capped) for
how runs use it.

Each read has a 15-second timeout. If an agent is missing, signed out or fails
to answer, its block says `unavailable` and gives the reason. The other agent
still prints. Exit code 0 means at least one read worked; 1 means neither did.
The `--json` output is one object with `claude` and `codex` entries.

A successful read clears a stale unavailable mark when every reported window
has quota left, has a future reset time and is not rejected. The output says
`cleared unavailable mark`, or sets `cleared_unavailable` in JSON.

## What duolog keeps in .duolog

Each delegation gets a folder under `.duolog/tasks/` with the task copy, the
answer and the worker log. `duolog put` saves snapshots under
`.duolog/artifacts/`. When a lead session starts, duolog removes the folders
and snapshots that nothing changed for 7 days. You do not need to run a
command for this. The logs, such as `usage.ndjson`, stay.

## How to credit the workers in commits

Run `duolog coauthors` to print credit lines for workers that changed files
since the current commit. Use `--since <rev>` to start from another commit.
duolog records each worker's agent and resolved model when it changes files.

To add the lines during each commit, save this hook in
`.git/hooks/prepare-commit-msg`:

```sh
#!/bin/sh
exec duolog coauthors --hook "$1"
```

On macOS and Linux, run `chmod +x .git/hooks/prepare-commit-msg` to make it
executable. Git for Windows runs the hook with its own `sh`.
The hook skips lines already present and never blocks a commit.
It does not change a commit message inside a worker.

Set `coauthors.enabled: false` in `.duolog/config.yaml` to stop recording
worker credit and adding lines. Use this when the repository forbids them.
The `coauthors.claude` and `coauthors.codex` templates accept `{model}` for
the model ID and `{name}` for a display name such as `Claude Opus 5.5`.
An empty template adds no line for that agent.

## What remains unverified

- One reported Windows 11 check covered install, hooks, workers and gate
  denies in Windows PowerShell 5.1. Real agents in PowerShell 7 and on
  Windows arm64 remain unchecked.
- Windows console interruption and descendant cancellation still lack native
  verification. POSIX signal tests do not prove those Windows paths.
- Live sessions require Rex on macOS or Linux. Windows uses
  `duolog delegate`.
- Codex delegation needed an outside-sandbox approval in the measured setup;
  see [Permissions](permissions.md#a-codex-lead-that-starts-a-claude-worker).
  Its shell could not find `rex` in our checks, so live peers from a Codex
  lead remain unverified. See [Rex](rex.md).

## Related pages

- [Configuration](configuration.md): roles, models and limits
- [Permissions](permissions.md): how duolog decides what a worker may do
- [Sync](sync.md): keep skills and agents in step
- [Bus](bus.md): messages and artifacts
- [Rex](rex.md): live second sessions
- [Telemetry](telemetry.md): the local history and how to turn it off
- [Calibration](calibration.md): a starting order from past work, on request
- [Glossary](glossary.md): lead, worker, planner, peer and other words
