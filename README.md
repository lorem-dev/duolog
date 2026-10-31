# duolog

duolog makes Claude Code and Codex work as a pair in one project. You choose
which agent and model does each kind of work. The two sessions then talk through
a cheap file bus, so they do not paste large context at each other.

**Status.** The code exists and its unit tests pass. The first release is
unpublished: there is no download yet, so you build the binary from source with
`make build`. We did not run duolog on Windows. Codex as the lead is not
proven. Read [the known limits](#what-is-not-proven-yet) before you rely on it.

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
- Windows has not been run. Treat it as unsupported until its CI matrix and
  one real run pass; rules may not apply there.
- Codex as the lead is not proven. In our checks its shell could not find
  `rex`, and we have not shown that it can start `claude -p`.
- Most agent behavior behind the design is OBSERVED in a few runs, not
  VERIFIED. The [live verification][lv] lists each answer with its label.
- The cost targets are not measured yet. See [Budgets](docs/budgets.md).

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
