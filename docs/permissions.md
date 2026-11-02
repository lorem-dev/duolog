# How duolog decides what a worker may do

This page explains how a worker asks for permission and how duolog answers.
The `duolog` binary decides, not a model, so a normal decision costs no
tokens.

## The order of the decision

duolog checks in this order and stops at the first answer:

1. The deny rules.
2. The limit of the role (`sandbox`).
3. The lead's mode.

A worker never gets more than the lead has. The lead's mode, the role's limit
and the deny rules all narrow what the worker can do. None of them widens it.

## What the lead's mode means

| Mode | Meaning |
|---|---|
| `plan` | The worker may only read. A write role is refused |
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

## The default rules

The rules are in the `policy` section of the config. A rule has an action
(`read`, `write`, `exec` or `net`), a glob, a decision and optional roles.

| Action | Match | Decision |
|---|---|---|
| read | everything | allow |
| read | `.env`, `*.pem`, `*.key`, `secrets/` | deny |
| write | `.duolog` and `.git` folders, at any depth | deny |
| write | `.claude/settings*.json`, `.claude/hooks/**` and the `.codex` folder | deny |
| write | everything, for the write roles | allow |
| exec | `go test`, `make build`, `npm run test`, `uv run pytest` and the like, for write roles | allow |
| exec | Check tools of each ecosystem, for write roles: `node_modules/.bin/eslint`, `.venv/bin/ruff`, `python -m pytest`, `go vet`, `cargo clippy`, `cmake --build`, offline `gradle` and `mvn`, `dotnet test --no-restore` and the like | allow |
| exec | Read commands: `cat`, `head`, `tail`, `ls`, `wc`, `rg`, `grep`, `find`, `sed -n 'N,Mp'`, `jq`, `sort`, `stat`, `tree` and others | allow |
| exec | `git status`, `git diff`, `git log`, `git show` | allow |
| exec | `git push`, `git reset --hard`, `git clean`, `git rebase` | deny |
| exec | `rm -rf *` | deny |
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
`cat .env` is denied and `cat ~/.ssh/id_rsa` asks. A shell glob in a path, a
recursive `grep` and `rg` with `--hidden`, `-u` or `--no-ignore` ask, because
they can reach denied files without naming them. Plain `rg` and the `Grep`
tool can still search a denied file that is neither hidden nor git-ignored:
keep such files out of the tree or ignore them. `git diff` and `git log` with
no path can also show a denied file that Git tracks, so do not commit one.

Quotes that only group one word read as that word, so `grep -n "TODO" a.go`
and `git log --format="%h"` are plain reads. Quoted text with a space, a glob,
a brace, a tilde or a backslash still asks, and so does an empty quoted word
or `$` and a backtick inside double quotes. The shell would split or expand
such text, so the gate cannot read the words it passes on. A brace anywhere in
a command asks for the same reason.

`sed` reads only as `sed -n 'N,Mp' file`. `grep`, `jq`, `sort`, `uniq` and
`file` pass only with options that change the output, so `jq -f`, `sort -o`,
`uniq - out`, `file -m.env` and ugrep's `grep --filter` ask. A `jq` filter
that names `env` or a `$` variable asks, because it can print the worker's
API keys. `diff` and `yq` are not read commands, because `diff -r` prints
denied files and `yq` can write them.

A deny also holds inside any other command. `uv run git push` is denied like
`git push`, and `sed -n p .env` is denied like `cat .env`. A directory change
before another command, a shell glob, a program path with `..` and `git -c`
ask in every mode, because the gate reads the command text and cannot see what
they do. A path can match a deny
by accident: `go test ./secrets/...` is denied by the `secrets/` rule.

Secrets include nested `.env` and `.env.*` files. Paths outside the working
tree ask in every mode, unless an explicit outside-path rule applies. A broad
`**` allow does not cover them. On macOS and Windows, path rules ignore case.

A deny wins over an allow. When no rule matches, the decision is
`on_unmatched`, which is `ask` by default. The default denies always apply: a
project that sets its own `rules` replaces the allow rules, not the denies.

## How a worker's MCP tool calls are checked

The gate does not know what an MCP tool does, so a call such as
`mcp__jira__get_issue` is an `unknown` action and asks the lead. A lead that
waits for its foreground `delegate` cannot answer, so the call is denied. That
holds for a worker's own servers and for servers bridged from the lead (see
[Configuration](configuration.md#how-a-worker-borrows-the-leads-mcp-server)).
`capabilities.mcp_tools` limits which tools a bridged server offers; it does
not allow them in the gate.

To let workers call a tool without a question, add a rule with the action
`unknown` and the tool name the gate saw, which `duolog decisions` shows:

```yaml
policy:
  rules:
    - {action: unknown, match: "mcp__jira__get_issue", decision: allow}
```

A `rules` list in your config replaces the default allow rules, so copy them
from `internal/config/config.default.yaml` first. The default denies stay in
any case. We saw a bridged server's tool run in a Codex and in a Claude worker
in a folder where the gate was off. Whether Codex sends MCP calls to the gate
is NOT VERIFIED.

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
`peer wait` returns at once for a pending gate ask. A worker that tries to ask
the user, with Codex `request_user_input` or Claude `AskUserQuestion`, is
denied, because only the lead talks to the user.

The lead answers with:

```bash
duolog allow 3
duolog deny 3
```

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

Worker safety comes from launch: both delegate and peer put the directory of
the running duolog executable first in the worker's PATH, even when the
parent's PATH lacks it. The worker therefore finds that same binary for its
gate. The requirement that a worker fails closed if the binary is absent
holds through this launch guarantee, rather than a Claude shell guard.
Codex keeps its POSIX and Windows guards. Interactive Claude display of a
missing executable has not been measured.

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
   roots did not help. So the lead must run with `--sandbox danger-full-access`
   to delegate to Claude. A token passed in the environment may also work; we
   did not test it.

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
- Real agents ran on Windows 11 once, in Windows PowerShell 5.1: both agents
  ran the hooks and the gate denied. PowerShell 7 and arm64 are unchecked.

## How PowerShell commands are checked

The PowerShell tool counts as an exec action. Only a plain named command can
pass without review. Expressions, quotes, variables, pipelines, script blocks
and command chains ask, even in full mode. A read-only role can run only named
read commands allowed by policy, such as `Get-Content`, `Get-ChildItem` and
`git status`. `Remove-Item -Recurse` and `git push` match deny rules first.
Windows drive letters, backslashes and case are normalized before path rules.

## Related pages

- [Configuration](configuration.md): the `policy` section
- [Rex](rex.md): permission questions in a live session
- [Live verification](research/2026-10-30-live-verification.md): hook results
