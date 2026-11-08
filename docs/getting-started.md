# Getting started with duolog

This page shows how to install duolog, set up a project, check the setup and
send a first task. Install the binary on macOS or Linux:

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

The Linux binaries are static, so they need no libc of a given version. CI
runs the amd64 binary on Alpine 3.10 and 3.20, Ubuntu 14.04 and 16.04,
Debian 9 and CentOS 7, and the arm64 binary on Alpine 3.20 and Ubuntu 16.04.
The installer also works with busybox `sh` and `wget`, so Alpine needs no curl
or bash; CI runs it on Alpine 3.20 and Ubuntu 14.04. Claude Code and Codex
have their own system requirements.

Pin a release or choose another directory:

```sh
curl -fsSL https://github.com/lorem-dev/duolog/releases/latest/download/install.sh | sh -s -- --version v1.5.2
curl -fsSL https://github.com/lorem-dev/duolog/releases/latest/download/install.sh | DUOLOG_INSTALL_DIR="$HOME/bin" sh
```

From the release after 1.4.0 on, an installer downloaded from a release page,
such as `releases/download/v<version>/install.sh` or `install.ps1`, installs
that release by default; the `releases/latest/download` links install the
newest one.
`DUOLOG_VERSION` and `--version` always win. `DUOLOG_VERSION` also pins a
POSIX release; `--dir` also sets its directory.
Use `--via-go` to print the source install command. The POSIX script supports
macOS and Linux; use the PowerShell installer on Windows.

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
$env:DUOLOG_VERSION = 'v1.5.2'
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

Each install replaces everything between the markers with the current text, so
a newer duolog updates it; your text outside the markers is never touched. An
install also turns the one-line offer of an earlier duolog into the block, in
the same place. Uninstall removes the block and that old line and keeps your
other instructions. If a file has a begin marker without an end marker, or the
reverse, install and uninstall stop with an error that names the file and
change nothing.

## What works on each OS

| OS | Install command | Known limits |
|---|---|---|
| macOS | `curl -fsSL https://github.com/lorem-dev/duolog/releases/latest/download/install.sh` piped to `sh` | Rex is needed only for live sessions |
| Linux | `curl -fsSL https://github.com/lorem-dev/duolog/releases/latest/download/install.sh` piped to `sh` | Rex is needed only for live sessions |
| Windows | `irm https://github.com/lorem-dev/duolog/releases/latest/download/install.ps1` piped to `iex` | No Rex or live sessions; use `duolog delegate`. Real agents checked once, see [Known limits](#known-limits) |

All other single-line `duolog` commands shown here work in POSIX shells and
PowerShell. duolog is not on a package registry; use the installers or Go.

## What you need before you start

| Need | Why |
|---|---|
| Go 1.26.10 | Only to build from source |
| Git | duolog stores its state in `.duolog/` at the root of the main worktree |
| Claude Code, Codex, or both | duolog starts them; it does not replace them |
| Rex | Only for live second sessions. See [Rex](rex.md) |

Without Rex, `duolog delegate` still works. It runs a one-shot worker and
reads the answer from a file.

## How to build the binary

Run this in the repository root:

```bash
go build ./cmd/duolog
```

This writes `./duolog` on macOS and Linux, or `.\duolog.exe` on Windows. Put
its directory on PATH. The Claude hooks use no shell guard, and `claude -p` did
not block a tool when the binary was missing. The Codex hooks skip a missing
binary for the lead and block a worker gate. See [Permissions](permissions.md).

## How to install the hooks and the skill

Run this in your project:

```bash
duolog install --scope project
```

The command writes these files and prints each path:

| File | Content |
|---|---|
| `.claude/settings.json` | Claude Code hooks, merged with yours |
| `.codex/hooks.json` | Codex hooks, merged with yours |
| `~/.claude/skills/duolog/SKILL.md` | The skill that teaches the lead to delegate, at user scope |
| `~/.claude/skills/duolog-skill-check/` | The skill that offers [skill tests](skills.md) after a change |
| `~/.claude/skills/duolog-calibrate/` | The skill that runs [calibration](calibration.md) when you ask |
| `~/.claude/skills/duolog-peers/` | The skill that lists [peers](rex.md) and switches your Rex view to one |
| `~/.agents/skills/duolog/`, `~/.agents/skills/duolog-skill-check/`, `~/.agents/skills/duolog-calibrate/` and `~/.agents/skills/duolog-peers/` | The same four skills for Codex |
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
project needs no copy of its own. Each skill's description starts with
"Use only in a project that has .duolog/config.yaml", and its first line
tells the agent to stop elsewhere. In other projects only the skill's
one-line listing is in the agent's context. Where the Claude plugin is
enabled, the plugin supplies Claude's skills and install writes none.

Each `SKILL.md` names the duolog version that wrote it, as
`duolog-version` under `metadata` in its frontmatter. `duolog install`
replaces a duolog skill whose files differ from the binary's, such as one
of another version, when every file in it is one that some duolog release
wrote, unchanged. A lead's session start in a duolog project replaces such
a skill when an older duolog wrote it. So a new binary updates the skills
with no step of yours. Neither touches any other skill folder of the same
name:

- a skill you changed, or added a file to, even one that names a version;
- a folder that duolog did not write: its `SKILL.md` names no version and
  its files match no duolog release;
- a folder that is a link, or holds a link at any depth.

Install prints a note for each, and `duolog install --check` and
`duolog doctor` print a line for it. To get duolog's skill back, keep your
changes elsewhere, remove the folder and run `duolog install`.

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
`duolog uninstall` removes only the hook entries and the skill files that
duolog added. A skill file you changed or added stays, with a note. It also
removes the marked block. An instruction file is deleted only if duolog
created it and it is now empty.

Install records the `permissions.allow` entries that it adds, and
uninstall removes only those. An entry you had before install stays. A
project keeps the record in `.duolog/claude-allow-entries.json`; a user
install keeps it in `duolog-allow-entries.json` beside the user
`settings.json`.

If `mcpServers.duolog` in `~/.claude.json` runs something other than
`duolog mcp`, it is yours: install and uninstall leave it and print a
note, and `duolog install --check` and `duolog doctor` print a line for it.
Rename it and run `duolog install` to get the duolog tools.

Uninstall at either scope removes the duolog skills at user scope, so every
project loses them, and it says so: `duolog skills removed for all projects;
other duolog projects get them back with duolog install`. This is the rule
for the duolog MCP server entry too. Knowing whether another project still
uses duolog would mean searching your folders.

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

Some lines need you, not `duolog install`, and do not change the exit code.
Such a line names a file to remove from a duolog skill folder, a duolog
skill you changed, a skill folder of duolog's name that duolog did not
write, or a project copy that you changed. It can also say that duolog
hooks in `.claude/settings.json` run twice beside the plugin, that two
agents share one skills folder, or that `mcpServers.duolog` runs something
else.

A repository can commit the hooks. A clone on a machine that never ran
`duolog install` has the hooks and no user skills, which is out of date. A
CI job for such a repository checks the hooks only:

```bash
duolog install --check --hooks-only
```

Codex needs two approvals that duolog cannot give. Open `/hooks` in Codex and
press `t` to trust the hooks. Trust the project folder too. Without them,
Codex skips the hooks and prints no warning.

You give these approvals once, in the project folder. A linked worktree of
the project, such as one under `.worktrees/`, is a new folder to Codex.
Before `delegate` or `peer open` runs Codex in one, duolog copies the
project's folder trust and its approvals of duolog's own hooks to that
worktree. See
[Permissions](permissions.md#how-a-linked-worktree-gets-codex-trust).

Sign in to the agents you will use before you send a task. Workers do not
get `ANTHROPIC_API_KEY` or `OPENAI_API_KEY`, and Claude workers start with
fast mode off; both are new in 1.4.0. If an agent signs in only with a key,
workers fail its login check. To let paid runs use the key, under a daily
cap, see [Configuration](configuration.md#if-you-use-an-api-key-on-purpose).
Local telemetry is on by default; run `duolog telemetry off` to stop it. See
[Telemetry](telemetry.md).

## How the lead uses the duolog MCP server

A Claude lead gets `duolog mcp`, an MCP server with four tools. Each tool
calls the same code as the CLI command it stands for, so it can do no more
than the lead's shell can. Codex leads keep the skill and the CLI.

| Tool | What it does |
|---|---|
| `delegate` | Hands a task to a worker from typed fields; returns the answer, or `run_here` as data |
| `peer` | Drives a live [Rex peer](rex.md#how-to-drive-peers-with-the-mcp-tool): open, send, wait, answer, status, close, list |
| `answer_ask` | Allows or denies a worker's or a peer's permission ask by its number |
| `status` | Lists peers, open asks, delegations, load and quota |

Install adds the server as one entry at Claude's user scope, from either
`--scope`. It also adds `mcp__duolog__delegate`, `mcp__duolog__peer` and
`mcp__duolog__status` to `permissions.allow` of the install scope, so the first
call does not ask. It never adds `mcp__duolog__answer_ask`: the lead's guard or
you decide that call, as for `duolog allow`. The marked block in `CLAUDE.md`
tells the lead to use the tools and the CLI only where a tool is missing.

Only the lead gets the tools. Outside a folder with `.duolog/config.yaml`, and
in a worker or a peer, the server lists no tools, so they cost nothing there.

Keep deferred tool loading on, which is Claude Code's default. Claude then
loads a tool's schema only when it needs it. We measured about 12 tokens per
tool per turn, and about 155 tokens once per tool when its schema loads. With
`ENABLE_TOOL_SEARCH=false`, Claude adds about 17,000 tokens to every turn.
`duolog doctor` says so when the setting is off.

`duolog uninstall` removes the allow entries at its scope. It always removes
the server entry, and says `duolog MCP server removed for all projects; other
duolog projects get it back with duolog install`. An entry you changed stays,
with a note. To remove the server by hand, delete `mcpServers.duolog` from
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

The command prints one line for each finding. It checks the agent binaries and
versions, whether hooks are installed, Codex trust, Claude trust, Rex, and the
instruction block (current, outdated or missing; for information only).
`duolog install --check` counts a missing or outdated block as out of date.
The Claude line reads only the trust flag
of the project folder from Claude's user state. If the folder is not trusted,
Claude ignores the project's `permissions.allow` entries; run Claude Code in
the folder once and accept the trust dialog. If the state cannot be read, the
line says `unknown` and is not a failure. It makes no model call unless you
add `--probe`.
A config file that does not load comes first, as an `error:` line with the
file and the line. Doctor stops there, because every other check needs the
config.
Follow the steps it names. For a project that already uses duolog, install
the binary, use the project's skill, and run doctor.

## How to send your first task

A task is a Markdown file with five sections in this order: Goal, Read,
Write, Limits and Check. Each section is a heading, usually `## Goal`; `# Goal`
and `### Goal` work too. Save it inside the project. `.duolog/task.md` is a
good place, because Git ignores `.duolog/`.

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

A line that starts with `run here:` means duolog did not start a worker. The
role points to the lead's own agent and model, or it needs little context and
the lead's model costs no more. So the lead does the task. See
[Configuration](configuration.md).

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

Run `duolog quota` to see Claude and Codex quota without a model call. It
runs `claude -p /usage` and `codex app-server`, which read the limits:

```bash
duolog quota
duolog quota --json
```

Each agent shows its plan, used percentage and reset time for each window.
Claude also shows extra usage. Codex also shows credits and per-model limits
when available. The command does not spend credits to reset limits. It asks
each agent's CLI for quota and does not parse the agent's auth files itself.

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

## Known limits

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
- Most agent behavior in the research page is OBSERVED in a few runs, not
  VERIFIED. Read
  [the live verification](research/2026-10-30-live-verification.md) before you
  rely on a hook to protect a worker.
- The cost study ran each setup once per task, on five tasks of one private
  project. Repeats are needed before its numbers are averages. See
  [Budgets](budgets.md) and
  [the cost study](research/2026-11-01-cost-study.md).

## Related pages

- [Configuration](configuration.md): roles, models and limits
- [Permissions](permissions.md): how duolog decides what a worker may do
- [Sync](sync.md): keep skills and agents in step
- [Bus](bus.md): messages and artifacts
- [Rex](rex.md): live second sessions
- [Telemetry](telemetry.md): the local history and how to turn it off
- [Calibration](calibration.md): a starting order from past work, on request
