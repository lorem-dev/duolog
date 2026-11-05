# How to configure duolog

This page explains the config file: where duolog reads it, what each section
does, and how to check a role. The defaults are in
[internal/config/config.default.yaml](../internal/config/config.default.yaml).

## Where duolog reads the config

duolog merges four layers. A later layer changes only the fields it names. A
list, such as `policy.rules`, is one field, so a later list replaces the earlier
one. The default deny rules stay in every case.

| Order | File | Use |
|---|---|---|
| 1 | The embedded default | The rules that ship with duolog |
| 2 | `duolog/config.yaml` in your user config folder | Your rules for all projects |
| 3 | `.duolog/config.yaml` | The project rules. Commit this file |
| 4 | `.duolog/config.local.yaml` | Your private changes. Git ignores it |

The user config folder is the one that Go's `os.UserConfigDir` returns. On
macOS it is `~/Library/Application Support`. On Linux it is `~/.config`.

An unknown key is an error. This catches typing mistakes, but it also means a
config that works with a newer duolog may fail with an older one.

## How to check your config in the editor

The config has a JSON Schema. Put this line first in `.duolog/config.yaml`:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/lorem-dev/duolog/main/duolog-config.schema.json
```

The link works only after the public repository exists. Until then, run
`duolog config schema > schema.json` and point your editor at that file.

## How a role chooses an agent, model and effort

A role names a kind of work. It has these fields:

| Field | Meaning |
|---|---|
| `use` | The agent, model alias and effort that do the work |
| `fallback` | Where the work goes when the `use` agent is unavailable |
| `sandbox` | `read-only` or `workspace-write`. The most the worker may do to files |
| `context` | `minimal` drops the project instructions, the skill list and, for Claude, every `CLAUDE.md`; `project` keeps them |
| `session` | `fresh` starts a new run; `resume` continues a thread |

To see the result for one role, run:

```bash
duolog route review_final
duolog route review_final --json
```

To see every role and its model, run `duolog roster`. Add `--versions` to see
which Codex version each alias resolves to.

## Which role takes a whole feature

Give a whole feature to `planner`. It reads the code, implements, runs the
checks and lists the risks and the hunks to review. It runs on `sol` with
minimal context and keeps its thread, so a follow-up round reuses the
worker's cache. In the cost study, an Opus lead that handed a large feature to
`planner` and did not read the code itself cost about half as much as an Opus
lead that read the code first, at the same test results. See
[the cost study](research/2026-11-01-cost-study.md).

Keep a small fix in the lead. A handoff has a fixed cost: on a two-file bug,
one Codex session alone cost less than any setup with duolog.

## Which role finds the cause of a bug

Give `debug` a bug whose cause can be found by reading one repository. It runs
on `sol` at medium effort, read-only, and keeps its thread for follow-up
questions. In the cost study, `sol` found the cause of four hard bugs at every
effort, for a third to a sixteenth of what Opus cost. Give `debug_hard` a bug
that crosses several components or needs logs and other evidence from outside
the repository. It runs on Opus and may edit files.

Reviews between tasks go to `review_intermediate` on `sol`. In the cost study,
`terra` cost half as much but made a false finding in half of its review runs,
and `sol` made none.

## How to give every worker the same rules

Put the project rules that every worker needs in `.duolog/brief.md`: the test
command, the tools to use, the files not to touch. duolog puts this file first
in each worker's input. The same start in every task lets the provider serve it
from its prompt cache. The file must be a regular file of at most 32 KiB; a
link is ignored, because it could point at a secret. The brief counts against
`wire.inline_max_bytes`, so a long brief leaves less room for Read files. A
resumed thread already holds the brief, so duolog does not send it again.

Every worker is also told not to commit, push or ask the user, and to edit a
changelog or docs only if the task lists them under Write.

## Why the config holds only model aliases

The config uses `fable`, `opus`, `sonnet`, `haiku`, `astra`, `sol`, `terra`
and `luna`. Claude Code resolves its own aliases when it runs. For Codex,
duolog picks the newest visible `gpt-<version>-<family>` from
`codex debug models`. A full model name in a role pins that version on
purpose.

The models `fable` and `astra` are for manual use. duolog refuses them unless
you pass `--allow-manual-model`. The list is `limits.manual_only_models`.

## How the lead limits the worker

The lead's own settings are the ceiling:

- A worker never runs above the lead's effort. The scale is `low`, `medium`,
  `high`, `xhigh`, `max`, `ultra`. An unknown lead effort sets no ceiling.
- A worker never gets a wider permission mode than the lead. See
  [Permissions](permissions.md).
- A worker has `DUOLOG_DEPTH=1`, so it cannot delegate again.

`duolog` never passes `--dangerously-bypass-approvals-and-sandbox` or
`bypassPermissions` to an agent.

## What the other sections do

| Section | What it sets |
|---|---|
| `wire` | The bus line limits and the inline size |
| `skill_tests` | `ask` (default), `always` or `never` for cross-model skill tests. Save a local preference with `duolog skill test --remember`. See [Skill tests](skills.md) |
| `limits` | Live sessions and the size of a task the lead may do itself |
| `models` | The agent and the prices for each alias. Prices drive `duolog stats` |
| `sessions` | When a resumed thread closes |
| `policy` | The rules for worker actions. See [Permissions](permissions.md) |
| `capabilities` | What to do when the other agent lacks a skill, plugin or server |
| `sync` | How agents and skills are generated. See [Sync](sync.md) |
| `env_passthrough` | The only variables a worker inherits |
| `rex` | Timeouts and capture settings for live sessions. See [Rex](rex.md) |
| `rex.stuck_notify_minutes` | Minutes before `peer wait` sends one reminder for an unanswered question. Default: `5`; `0` disables reminders |
| `rex.auto_answers` | Ordered `{match, answer}` rules for safe peer questions. Defaults to answering `Hooks need review` with `Continue without trusting`. A project list replaces the defaults; `[]` disables it. Trust and persistent approval grants are refused |
| `coauthors` | Enable worker credit and set commit co-author templates. See [Getting started](getting-started.md#how-to-credit-the-workers-in-commits) |

## What workers inherit

The default environment includes PATH, HOME and USER, plus `CODEX_HOME` and
`CLAUDE_CONFIG_DIR`. It leaves out `ANTHROPIC_API_KEY` and `OPENAI_API_KEY`:
a worker or peer that gets one is billed per token, even when you run on a
subscription. Windows also keeps SYSTEMROOT, USERPROFILE, APPDATA,
LOCALAPPDATA, TEMP, TMP, PATHEXT, COMSPEC and PSMODULEPATH. Without
PSMODULEPATH, each PowerShell cmdlet that loads a module takes 18 to 30
seconds. An explicit `env_passthrough` replaces these defaults.
The worker always gets `DUOLOG_DEPTH=1`, its `DUOLOG_LEAD` and `DUOLOG_ROLE`.
Codex trust, hooks and capabilities read `CODEX_HOME` when set. Claude hooks,
permissions and capabilities read `CLAUDE_CONFIG_DIR` when set. Home lookups
use the OS home directory, including USERPROFILE on Windows.

A Codex worker runs its commands with `allow_login_shell=false`. A login shell
reruns your profile, which can reset PATH and lose tools such as `node` from
nvm. The worker keeps the PATH that duolog passes.

A Claude worker starts with auto memory off. Claude keeps that memory per
project path, so a worker's notes would reach the lead and every later session
of the project. It also starts with no MCP servers, because the user's servers
added 7.4 thousand tokens to every call in the cost study. A task that names an
`mcp:` or `plugin:` need keeps them, unless every MCP need is bridged. A
`minimal` Claude worker skips every `CLAUDE.md`, the one in your home folder
too: Claude loads that file as project instructions when the project is inside
the home folder. A `minimal` worker of either agent keeps its skills only when
the task names a `skill:` or `plugin:` need.

Claude workers and peers start with fast mode off: their `--settings` JSON
holds `"fastMode": false`. Fast mode draws on usage credits even inside a
subscription window.

### If you use an API key on purpose

While `ANTHROPIC_API_KEY` or `OPENAI_API_KEY` is set in duolog's environment
and not listed in `env_passthrough`, the first `delegate` or `peer open` of
each lead session adds one line, and `duolog doctor` warns every time:

```text
claude workers no longer get ANTHROPIC_API_KEY by default. To let them use it, list it in env_passthrough explicitly.
```

When the key was the agent's only login, the login check fails. duolog does not
start a browser login, which would switch your billing, whatever
`capabilities.on_auth_failure` says. The task goes to the lead with the same
line, or with `fail` the delegation fails with it.

To let workers use the key, list it in an explicit `env_passthrough`. The list
replaces the defaults, so keep them all:

```yaml
env_passthrough: [PATH, HOME, USER, CODEX_HOME, CLAUDE_CONFIG_DIR, ANTHROPIC_API_KEY]
```

On Windows, also keep the Windows names above. Then every worker and peer of
that agent is billed per token for each run, and duolog does not cap that
spend. The line and the warning stop once the key is listed.

## How a worker borrows the lead's MCP server

A task names the MCP servers it relies on with `duolog delegate --needs
mcp:<name>`. By default, when the worker's agent lacks that server, duolog does
not start the worker. It tells the lead to do that part first
(`on_missing.mcp: lead_first`). With `bridge`, duolog instead hands the lead's
server to the worker for that one run:

```yaml
capabilities:
  on_missing: {mcp: bridge}
  mcp_tools: {tracker: [get_issue, search]}   # optional
```

Bridging is off by default because it gives the server's credentials to the
other vendor's agent. Turn it on only for servers you would also configure in
that agent yourself.

A worker never installs a plugin. When Claude Code in a peer pane offers one,
such as its LSP plugin recommendation, duolog selects `No, not now` and never
`Yes, install`, `Never for this plugin` or `Disable all ...`, because those
change your own settings. With `on_missing.plugin: lead_first`, the default,
the lead gets one `note` on the bus per peer and plugin. It names the plugin,
its marketplace and what triggered the offer, with the command to install it
yourself, such as `claude plugin install gopls-lsp@claude-plugins-official`.
With any other value the offer is declined and the lead is not told. A
`rex.auto_answers` rule cannot answer these offers. An offer with no option
that declines for now goes to the lead as a question.

duolog reads the server from the lead's agent config and translates it:

| Lead | Where duolog reads it | Worker gets it as |
|---|---|---|
| Claude | `~/.claude.json` (user and local scope) and the project's `.mcp.json` | `-c mcp_servers.<name>.<field>=...` options of `codex exec` |
| Codex | `config.toml` in `CODEX_HOME`, and the project's `.codex/config.toml` when Codex trusts the folder | A temporary file for `--mcp-config` |

Only the servers the task names are bridged, never all of them. Claude's order
applies: local scope, then project, then user. A `.mcp.json` server counts only
after you approved it in Claude, and the project's own `.claude/settings.json`
and `settings.local.json` approve nothing until you trusted the folder. Codex
merges the project layer over the user layer, key by key.

Every env and header value of a bridged server moves into the worker's
environment under a new name, such as `DUOLOG_MCP_TRACKER_1`. The translated
config names the variable: `env_http_headers` for Codex and
`${DUOLOG_MCP_TRACKER_1}` for Claude. A value that the lead's config reads
from a variable, such as `${TRACKER_TOKEN}` or `bearer_token_env_var`, is read
from the environment of `duolog delegate`. The Claude file is written with
mode 0600 in the task folder and removed after the run. On Windows the mode
only sets the read-only flag, and the file keeps the access rules of the task
folder. A url and literal stdio args pass as written,
so a token inside a url path reaches the worker as it is. A Codex worker hides
the new variables from its shell commands with `shell_environment_policy`: as
`filters`, or added to the `exclude` list when the worker's policy uses the
older form, since Codex refuses both forms together.

A Claude worker's Bash tool can read the variables. Claude has two ways to
hide variables, and neither fits:

- `CLAUDE_CODE_SUBPROCESS_ENV_SCRUB=1` removes variables whose name or value
  looks like a credential from Bash, hooks and stdio servers. In our check it
  also emptied those variables in the `${VAR}` headers of an HTTP server, so
  the server listed no tools. A name that does not look like a credential
  still expands, but Bash sees it too.
- `sandbox.credentials` removes variables from sandboxed Bash commands only.
  It needs Claude's sandbox, which also limits the network of every command,
  and a command that Claude retries outside the sandbox keeps them.

With the gate on, commands that print the environment, such as `env`,
`printenv` or `echo $VAR`, ask the lead. In `full` mode the gate allows what it
would ask, so do not bridge a server to a Claude worker in `full` mode unless
you accept that the worker can read its token.

A Codex stdio server gets only a short default environment, so its variables
pass under their own names with `env_vars`, and the worker's Codex gets them
too. So a server is not bridged when it sets a name the worker already has with
another value, a name the agents or the system read, such as
`OPENAI_BASE_URL`, `CODEX_HOME`, `PATH` or a `*_PROXY`, or a `DUOLOG_` name.
On Windows these names compare without case.

`mcp_tools` limits what a server offers, bridged or the worker's own, and the
same list allows those tools in the gate. Codex gets `enabled_tools`, so it
sees only those tools. Claude gets `--allowedTools mcp__<server>__<tool>`; it
still sees the other tools, and the gate asks the lead before each of them.
Without a list, the whole server is bridged, and every tool asks. See
[Permissions](permissions.md#how-a-workers-mcp-tool-calls-are-checked).

A Claude worker whose only MCP needs are bridged starts with those servers and
no others (`--strict-mcp-config`). When the task also needs one of the
worker's own servers, the bridged servers are added to its own. Then its
other stdio servers start with the bridged variables in their environment too.
A Codex worker always keeps its own servers, and the bridged ones are added. A
server of the same name in the worker's Codex config, even a disabled one,
stops the bridge, because the options would merge into it.

Some servers cannot be bridged. The task then goes to the lead, as with
`lead_first`, with one line that says why:

- A server that signs in with OAuth, such as Codex `auth`, `oauth` or `scopes`
  and Claude `oauth`. A remote server with no credentials in config counts as
  OAuth too, because a public server and an OAuth server look the same.
- A server whose headers come from a helper command.
- Claude connectors and plugin servers, which no config file lists.
- SSE and unknown transports. A Codex server with a `cwd` other than the
  project, because Claude cannot set one.
- A url with a query or user info, a url that reads a variable, and stdio args
  that read a variable, for a Codex worker. Codex takes these as text, so the
  value would be in argv.
- A variable that the lead's environment does not have.

`duolog caps` marks each server `bridgeable` or says why it is not, and
`duolog doctor` names the servers each agent can lend.

We checked both directions with Codex 0.165.0 and Claude Code 2.1.316
(OBSERVED). A Codex worker called a tool of an HTTP server with a token header
from the Claude lead's `~/.claude.json`. A Claude worker called a tool of an
HTTP server with a token header from the Codex lead's `config.toml`. The token
was in no file under `.duolog/` and in no argument of the worker.

## What happens when a worker cannot sign in

Before a worker starts, duolog checks two logins. A failed login would
otherwise end the run only after the lead waited for it.

| What | How duolog checks it, with no model call |
|---|---|
| The worker's agent | `codex login status` or `claude auth status --json` |
| An MCP server with a token in config | One MCP `initialize` request with the headers the worker gets. Only HTTP 401, 403 or an error that names the token count |
| An MCP server that signs in with OAuth | `codex mcp list --json` or `claude mcp get <name>` |

A passed check is kept for five minutes in `.duolog/cache/auth.json`, which
holds only the time. A stdio server is not checked, because it would have to
start. A server that checks its token only when a tool runs passes too; one
server in our check accepted `initialize` with a wrong token. The
request does not follow a redirect, which would take the headers to another
url. A `.mcp.json` server you did not approve, or one you turned off, gets no
check at all. The worker is
told to stop with `auth:mcp:<server>` in `needs` when a server refuses it, and
duolog reads a refused agent login from the worker's errors. Both get the same
treatment as a failed check. In our check, a Codex worker given a wrong token
this way stopped, and the lead got the line that names the config file.

`capabilities.on_auth_failure` decides what happens next:

| Value | What happens |
|---|---|
| `login_then_lead` | The default. duolog runs the agent's own login once: `codex login`, `claude auth login`, `codex mcp login <name>` or `claude mcp login <name>`. If it works, the worker starts. Otherwise the task goes to the lead |
| `lead` | The task goes to the lead at once, with the login command to show the person |
| `fail` | `duolog delegate` fails |

The login waits up to `login_timeout_seconds` (180 by default) for the person
to finish it in the browser. When it needs a terminal that duolog does not
have, the result line has `needs=auth:<agent>` or `needs=auth:mcp:<name>` and
the command for the person, such as `! codex mcp login tracker`.

When the person closes the login, or it times out or fails, the task goes to
the lead, as with `on_missing.mcp: lead_first`. The line names the agent or the
server and the reason, never the token. After a closed or failed login, duolog
does not ask for it again until the lead's session ends; after a timeout it
may, since the person can finish it from a terminal. Only a server the task
named with `--needs` gets a login. The login command is quoted for a shell. A
server with a token in config has nothing to log in to: the line names the
config entry that holds the token. duolog runs at most one login for each
delegation.

## What a Codex worker's sandbox blocks

A write role runs Codex in `workspace-write`. The cost study saw these limits
with `codex exec` (OBSERVED, Codex 0.165.0 on macOS):

| What fails | Why | What to do |
|---|---|---|
| Tests that use a local database or Redis | The sandbox blocks the network, even localhost | Set `network_access = true` under `[sandbox_workspace_write]` in the project's `.codex/config.toml` |
| `uv run` | Its cache lock is outside the project | Run `.venv/bin/python -m pytest` |
| `npx` | Its cache in `~/.npm` is outside the project | Run `node_modules/.bin/<tool>` |
| `git status` with Git LFS | LFS writes to `.git/lfs/tmp` | Name the check in the task instead |

Put the working forms in `.duolog/brief.md`, so that every worker uses them.

## How learning mode chooses a target for each kind of work

Learning mode gives each delegation a task class, and the class picks the
target from a ladder of agent, model and effort entries. It is off by default,
and with it off nothing changes. See [Learning mode](learning.md) for the task
workflow and journal. The `learning` keys and their defaults:

| Key | Default | What it does |
|---|---|---|
| `enabled` | `false` | Turns learning mode on |
| `threshold` | `2.5` | Cost-first: the cheapest entry whose quality minus the margin reaches this |
| `quality_margin` | `0.25` | Quality-first: a cheaper entry this close to the best one wins |
| `prior_runs` | `2` | How many judged runs a prior quality is worth |
| `uncertainty` | `0.5` | The margin is this divided by the square root of prior runs plus judged weight |
| `profile_below_runs` | `3` | Task finish offers to profile an entry with less judged weight |
| `judges` | opus, then sol, both `high` | The first judge whose agent neither side used |
| `classify` | twelve rules | Ordered rules from task features to a class; the first match wins |
| `classes` | ten classes | Roles, ladders, priors and the judge rubric of each class |

A layer replaces `classify` as a whole list, as YAML lists do, so a project
that adds one rule restates the others. `classes` merge per class and field, so
a project can add a class or change one ladder and keep the rest.

## Related pages

- [Getting started](getting-started.md): install and first task
- [Permissions](permissions.md): policy rules and modes
- [Learning mode](learning.md): task classes, the journal and learned orders
- [Practices and models](research/2026-10-30-practices-and-models.md): the
  evidence behind the defaults
- [Cost study](research/2026-11-01-cost-study.md): what duolog cost against
  one agent, and why 1.1.0 changed the gate and the roles
