<h1 align="center">
  <img src="/assets/icon.png" width="24" alt="duolog icon">
  duolog
</h1>

<p align="center">
  <a href="https://github.com/lorem-dev/duolog/releases/latest"><img src="https://img.shields.io/github/v/release/lorem-dev/duolog?label=download" alt="Download"></a>
  <a href="./docs/getting-started.md"><img src="https://img.shields.io/badge/docs-read-blue" alt="Documentation"></a>
  <a href="./LICENSE"><img src="https://img.shields.io/github/license/lorem-dev/duolog" alt="License"></a>
  <a href="https://github.com/lorem-dev/duolog/actions/workflows/ci.yml"><img src="https://github.com/lorem-dev/duolog/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
</p>

<p align="center">
duolog makes Claude Code and Codex work as a pair in one project. You choose
which agent and model does each kind of work. The sessions talk through a
small file bus, so they do not paste large context at each other.
</p>

---

## Install

Install the binary first. On macOS and Linux:

```sh
curl -fsSL https://github.com/lorem-dev/duolog/releases/latest/download/install.sh | sh
```

On Windows, use Windows PowerShell 5.1 or PowerShell 7:

```powershell
irm https://github.com/lorem-dev/duolog/releases/latest/download/install.ps1 | iex
```

The binary links work after the first release. Until then, use Go:

```sh
go install github.com/lorem-dev/duolog/cmd/duolog@latest
```

Both installers verify SHA-256 and select amd64 or arm64. Run the same command
again to update. The default folder is `~/.local/bin` on macOS/Linux and
`$env:LOCALAPPDATA\Programs\duolog` on Windows. Add the printed directory to
PATH and open a new terminal if needed. Neither installer edits PATH.

Pin a version with `--version`, or choose a directory with `--dir`:

```sh
curl -fsSL https://github.com/lorem-dev/duolog/releases/latest/download/install.sh | sh -s -- --version v0.1.0
curl -fsSL https://github.com/lorem-dev/duolog/releases/latest/download/install.sh | sh -s -- --dir "$HOME/bin"
```

The POSIX installer also accepts `DUOLOG_VERSION` and `DUOLOG_INSTALL_DIR`.
On Windows, set those variables before running the installer:

```powershell
$env:DUOLOG_VERSION = 'v0.1.0'
$env:DUOLOG_INSTALL_DIR = "$env:LOCALAPPDATA\Programs\duolog"
irm https://github.com/lorem-dev/duolog/releases/latest/download/install.ps1 | iex
```

### How to install the plugins from the duolog marketplace

Add this repository as a custom marketplace, then install the plugin from it.
A marketplace is a Git repository that lists plugins. This repository is its
own marketplace, named `duolog`. It is not in the official plugin directories
yet, so each agent needs the address once. Install the binary first, because
the plugins call it.

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
`duolog@duolog`, the first word is the plugin and the second is the marketplace.

The address can take other forms. Claude Code clones `lorem-dev/duolog` over
SSH, so use the HTTPS form if you have no SSH key for GitHub.

| Address | When to use it |
|---|---|
| `lorem-dev/duolog` | The normal case |
| `https://github.com/lorem-dev/duolog.git` | You have no SSH key for GitHub |
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

The enabled Claude plugin supplies its hooks and skill on every OS. Claude does
not need a separate hook install. If you run `duolog install`, it uses the
enabled plugin and removes older duplicate native hooks.

### How to install the hooks

Install hooks for one project or for all your projects:

```sh
duolog install --scope project
duolog install --scope user
```

Codex needs this step because marketplace plugins do not install its hooks.
It then needs one approval in `/hooks` (press `t`) and a trusted project
folder. Claude needs this step if its plugin is not enabled. Both scopes
install the skills too; project scope adds a starter config and an install
offer in `AGENTS.md` and `CLAUDE.md`.

Run `duolog doctor` to check the setup. For a project that already uses duolog,
install the binary, use the project's skill, and follow doctor's named steps.

A marketplace plugin's skill first checks `duolog version` and offers the
binary install if it is missing. In a project someone else set up, the marked
instruction line offers it when `.duolog/config.yaml` exists; the skill also
checks. Both ask for agreement before running the install. If the binary is
present but hooks are not approved, the skill runs `duolog doctor` once per
session and reports what it names, such as `/hooks` for Codex. A hook cannot
offer the install when the binary is missing.

Real agents on Windows have not been run. Native Windows CI checks are
configured; hosted results for this change are pending. Codex as the lead
has limits; read [what is not proven yet](#what-is-not-proven-yet).

## What duolog does

| Need | What duolog does |
|---|---|
| Use the right model for each job | Sends each role to the agent, model and effort in your config |
| Spend few tokens between sessions | Sends one-line messages that point to files |
| Avoid extra sessions | Reuses a live session and runs independent tasks once |
| Control the second session | The binary allows or denies its actions, so a normal decision costs no tokens |
| Keep one source for agents and skills | Generates one agent's files from the other's, in the direction you choose |
| Work in the Rex terminal | Lets one agent open the other beside it, with the Rex command line |
| Stay on the newest models | Keeps only model aliases in the config and finds the newest version when it runs |
| Handle a plugin that only one agent has | Tells the second session what to do, or asks the lead to do that part |
| Keep working when a provider's limit ends | Moves the work to the lead's provider, with no new session |

## How you use it

Install duolog for one project or for all your projects:

```bash
duolog install --scope project
```

Choose which agent holds the source files. Hooks then keep the other agent's
files up to date when you change a skill or an agent:

```bash
duolog sync --from claude
```

Send a task to a role:

```bash
duolog delegate review_intermediate --task task.md
```

## The default rules

Planning and the final review use Opus, because a mistake costs the most there.
Routine work uses cheaper Codex models. The rules are in
[internal/config/config.default.yaml](internal/config/config.default.yaml).
A project can change any of them.

## How your editor checks the config

The config has a JSON Schema, so your editor can suggest fields and find
mistakes. Put this line first in `.duolog/config.yaml`:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/lorem-dev/duolog/main/duolog-config.schema.json
```

The link points to `main`, so you do not need to change it after an upgrade.
Each release will also include the schema, if you want a fixed version.

duolog generates this file from its Go types, and `duolog config schema`
prints it. The link works only after the public
repository exists.

## What is not proven yet

- The first release is unpublished. Nothing is on a package registry.
- Real agents on Windows have not been run. Hosted CI must pass before merge;
  it executes generated hooks and installers without starting real agents.
- Live sessions require Rex on macOS or Linux. Windows uses `duolog delegate`.
- Codex as the lead is not proven. In our checks its shell could not find
  `rex`, and we have not shown that it can start `claude -p`.
- Most agent behavior behind the design is OBSERVED in a few runs, not
  VERIFIED. The [live verification][lv] lists each answer with its label.
- The cost targets are not measured yet. See [Budgets](docs/budgets.md).

## License

duolog uses the [Apache License 2.0](LICENSE).

## Related pages

- [Getting started](docs/getting-started.md): build, install and send a task
- [Configuration](docs/configuration.md): roles, models and limits
- [Sync](docs/sync.md): generate one agent's files from the other's
- [Bus](docs/bus.md): messages and artifacts
- [Permissions](docs/permissions.md): how duolog decides for a worker
- [Rex](docs/rex.md): a live second session
- [Budgets](docs/budgets.md): cost targets and what we measured
- [Practices and models](docs/research/2026-10-30-practices-and-models.md): the
  evidence behind the default rules
- [How to write the documentation](docs/contributing.md): the writing rules

[lv]: docs/research/2026-10-30-live-verification.md
