# Getting started with duolog

Install the binary with one command, then set up this project:

```sh
curl -fsSL https://github.com/lorem-dev/duolog/releases/latest/download/install.sh | sh
duolog install --scope project
```

The download link works after the first release. The script verifies SHA-256
checksums and installs to `~/.local/bin`. Run it again to update. It warns if
that directory is missing from `PATH` and never edits your shell profile.

Pin a release or choose another directory:

```sh
curl -fsSL https://github.com/lorem-dev/duolog/releases/latest/download/install.sh | sh -s -- --version v0.1.0
curl -fsSL https://github.com/lorem-dev/duolog/releases/latest/download/install.sh | DUOLOG_INSTALL_DIR="$HOME/bin" sh
```

`DUOLOG_VERSION` also pins a release; `--dir` also sets the directory. Use
`--via-go` to print the source install command. The script supports Linux and
macOS on amd64 and arm64. On other platforms, including Windows, it prints
`go install github.com/lorem-dev/duolog/cmd/duolog@<version>` and exits with
an error.

Project setup adds one marked line near the top of `AGENTS.md` and the Claude
instruction file. If the project has `.duolog/config.yaml` but a new machine
has no duolog on `PATH`, that line asks the agent to offer the install command.
The agent must get your agreement before running it. User setup does not add
this line. Uninstall removes the line and keeps your other instructions.

## What you need before you start

| Need | Why |
|---|---|
| Go 1.25.13 | Only to build from source |
| Git | duolog stores its state in `.duolog/` at the root of the main worktree |
| Claude Code, Codex, or both | duolog starts them; it does not replace them |
| Rex | Only for live second sessions. See [Rex](rex.md) |

Without Rex, `duolog delegate` still works. It runs a one-shot worker and
reads the answer from a file.

## How to build the binary

Run this in the repository root:

```bash
make build
```

This writes `./duolog`. Put it on your `PATH`. The hooks call `duolog` by
name, so a binary that is not on `PATH` does nothing.

## How to install the hooks and the skill

Run this in your project:

```bash
duolog install --scope project
```

The command writes these files and prints each path:

| File | Content |
|---|---|
| `.claude/settings.json` | Claude Code hooks, merged with yours |
| `.claude/skills/duolog/SKILL.md` | The skill that teaches the lead to delegate |
| `.codex/hooks.json` | Codex hooks, merged with yours |
| `.agents/skills/duolog/SKILL.md` | The same skill for Codex |
| `.duolog/config.yaml` | A starter config, only if none exists |
| `AGENTS.md` | One line offering install on a machine without duolog |
| `.claude/CLAUDE.md` or `CLAUDE.md` | The same line; uses the nested file if it exists |

Use `--agent claude` or `--agent codex` to install for one agent. Use
`--scope user` to install in your home folder for all projects. SessionStart
then creates `.duolog/` in a new project and registers its lead.

If a file already exists and changes, duolog first copies it to
`<file>.duolog.bak`. `duolog uninstall` removes only the hook entries and the
skill file that duolog added. It also removes the marked install offer. An
instruction file is deleted only if duolog created it and it is now empty.

Codex needs two approvals that duolog cannot give. Open `/hooks` in Codex and
press `t` to trust the hooks. Trust the project folder too. Without them,
Codex skips the hooks and prints no warning.

## How to check that everything works

```bash
duolog doctor
```

The command prints one line for each finding. It checks the agent binaries and
versions, whether hooks are installed, Codex trust, Rex, and the install offer
(for information only). It makes no model call unless you add `--probe`.

## How to send your first task

A task is a Markdown file with five sections in this order: Goal, Read,
Write, Limits and Check. Each section is a second-level heading.

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
calls on reading them.

Send the task to a role:

```bash
duolog delegate review_intermediate --task task.md
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

A line that starts with `run here:` means duolog did not start a worker. The
role points to the lead's own agent and model, so the lead does the task. See
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

## Known limits

- The first release is unpublished. There is no download.
- Windows has not been run. Its CI matrix needs a green run before merge.
- Codex as the lead is not proven. See [Rex](rex.md).
- Most agent behavior in the research page is OBSERVED, not VERIFIED. Read
  [the live verification](research/2026-10-30-live-verification.md) before you
  rely on a hook to protect a worker.

## Related pages

- [Configuration](configuration.md): roles, models and limits
- [Permissions](permissions.md): how duolog decides what a worker may do
- [Sync](sync.md): keep skills and agents in step
- [Bus](bus.md): messages and artifacts
- [Rex](rex.md): live second sessions
