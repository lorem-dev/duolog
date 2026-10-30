# duolog

duolog makes Claude Code and Codex work as a pair in one project. You choose
which agent and model does each kind of work. The two sessions then talk through
a cheap file bus, so they do not paste large context at each other.

**The project has no code yet.** The commands below show the planned interface.

## What duolog will do

| Need | What duolog does |
|---|---|
| Use the right model for each job | Sends each role to the agent, model and effort in your config |
| Spend few tokens between sessions | Sends one-line messages that point to files |
| Avoid extra sessions | Reuses a live session and runs independent tasks once |
| Control the second session | The binary allows or denies its actions, so a normal decision costs no tokens |
| Keep one source for agents and skills | Generates one agent's files from the other's, in the direction you choose |
| Work in the Rex terminal | Installs an MCP server, so one agent can open the other beside it |

## How you will use it

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
[defaults/config.default.yaml](defaults/config.default.yaml), and a project can
change any of them.

## How your editor checks the config

The config has a JSON Schema, so your editor can suggest fields and find
mistakes. Put this line first in `.duolog/config.yaml`:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/lorem-dev/duolog/main/duolog-config.schema.json
```

The link points to `main`, so you do not need to change it after an upgrade.
Each release will also include the schema, if you want a fixed version.

duolog will generate this file from its Go types, and
`duolog config schema` will print it. The link works only after the public
repository exists.

## Related pages

- [Practices and models](docs/research/2026-10-30-practices-and-models.md): the
  evidence behind the default rules
- [How to write the documentation](docs/contributing.md): the writing rules
