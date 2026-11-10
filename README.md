<p align="center">
  <img src="/assets/icon.svg" width="64" alt="duolog icon">
</p>

<h1 align="center">duolog</h1>

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

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)"
      srcset="/assets/how-duolog-works-dark.webp">
    <img src="/assets/how-duolog-works-light.webp"
      alt="The lead hands work to a worker, the planner or a live peer">
  </picture>
</p>

<p align="center">
The <a href="docs/glossary.md#gate">gate</a> checks every worker command
against the project rules. The <a href="docs/glossary.md#bus">bus</a> in
<code>.duolog/</code> carries messages, asks and results.
</p>

<p align="center">
In our November 2026 study, a lead that gave a whole feature to the
<a href="docs/glossary.md#planner">planner</a> on Codex cost 38-45% less
than one Opus session, with the same test results, on two tasks. Models and
prices change. See the
<a href="docs/research/2026-11-10-hand-over-study.md">study</a>.
</p>

---

## How to install the binary

On macOS and Linux:

```sh
curl -fsSL https://github.com/lorem-dev/duolog/releases/latest/download/install.sh | sh
```

On Windows, use Windows PowerShell 5.1 or PowerShell 7:

```powershell
irm https://github.com/lorem-dev/duolog/releases/latest/download/install.ps1 | iex
```

To build from source instead, use Go:

```sh
go install github.com/lorem-dev/duolog/cmd/duolog@latest
```

Run the same command again to update. Open a new terminal afterwards. For the
install folders, PATH handling, `DUOLOG_NO_PATH`, per-release installers and
pinning a version, see [Getting started](docs/getting-started.md).

## How to install the plugins

The plugins call the binary, so install it first. In Claude Code, run:

```sh
claude plugin marketplace add lorem-dev/duolog
claude plugin install duolog@duolog
```

In Codex, run:

```sh
codex plugin marketplace add lorem-dev/duolog
codex plugin add duolog@duolog
```

## How to install the hooks and approve them

Install hooks for one project or for all your projects:

```sh
duolog install --scope project
duolog install --scope user
```

Codex needs this step, then one approval in `/hooks` (press `t`) and a trusted
project folder. Claude needs it only if its plugin is not enabled. Then start
a new session in your project. That session is the lead.

## How to check the setup

```sh
duolog doctor
```

Follow the steps that doctor names. Local telemetry is on by default; turn it
off with `duolog telemetry off`.

## How to send the first task

[Getting started](docs/getting-started.md#how-to-send-your-first-task) shows
the task format. Write a task file, such as `.duolog/task.md`, and send it to
a role from the lead session:

```sh
duolog delegate review_intermediate --task .duolog/task.md
```

## License

duolog uses the [Apache License 2.0](LICENSE).

## Related pages

- [Getting started](docs/getting-started.md): install, set up and send a task
- [Glossary](docs/glossary.md): lead, worker, planner, peer and other words
- [Hand-over study](docs/research/2026-11-10-hand-over-study.md): what each
  way of handing work over cost
- [Configuration](docs/configuration.md): roles, models, defaults and limits
- [Permissions](docs/permissions.md): how duolog decides for a worker
- [Rex](docs/rex.md): a live second session
- [Budgets](docs/budgets.md): cost targets and what we measured
- [Telemetry](docs/telemetry.md): the local history and how to turn it off
- [Contributing](docs/contributing.md): the writing rules
