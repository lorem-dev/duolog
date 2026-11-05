# How duolog keeps local telemetry

duolog keeps a small local history of its own work: tokens, cost and outcome
per run, quota readings, routing choices and permission questions. The
history is for you. It stays on this machine, nothing uploads it, and it holds
numbers and fixed names, never text. It is on by default. This page says what
duolog keeps, where, for how long, how to turn it off or delete it, and what
it does not protect against.

## How to turn it off

Run `duolog telemetry off` in your terminal. It sets `telemetry.enabled:
false` in your user config, and duolog stops writing for every project. Turn
it on again with `duolog telemetry on`.

| Switch | Where | What stops |
|---|---|---|
| `duolog telemetry off` | Your user config | Every write, in every project |
| `DUOLOG_TELEMETRY=off` | The environment | Every write. This value cannot turn telemetry on |
| `telemetry.enabled: false` | `.duolog/config.yaml` or `.duolog/config.local.yaml` | Run, permission and peer events of this project only |

A project can turn off only its own events. Quota, routing and reset events
belong to your account, so a project switch does not stop them.

Turning telemetry off does not stop pruning. duolog keeps removing expired
data until it is gone. To delete it now, use `duolog telemetry purge`.

The first session start that collects prints one line, once for each user:
"Telemetry is local only; turn it off with duolog telemetry off."

## What duolog keeps

Each record is one line of JSON with a version, a time in UTC seconds and a
type. Every text field holds a fixed name from a short list, or `other`.

| Type | What it holds | Where |
|---|---|---|
| `run` | Role, task class, agent, model family, effort, where it ran (worker, peer, the lead itself), attempt number, input, cached and output tokens, cost, seconds, outcome, error kind, permission asks and denials | The project |
| `gate` | One record per permission question: the kind of action, the kind of rule that asked, the answer, who answered and how long the worker waited. An allowed action writes nothing | The project |
| `peer` | Open, send or close of a live peer, the agent, seconds and outcome | The project |
| `quota` | One reading of an agent's limit windows: the plan name, each window's length, use in basis points, minutes until reset and family. A reading that did not change is not written again | Your account |
| `route` | The role, the candidates, the chosen target, the target it replaced and the rule that chose | Your account |
| `reset` | An agent and the reason its quota history starts again | Your account |

Records name model families, such as `opus` or `sol`, never a model version.
Every failed attempt is a run too, whether learning mode is on or not.
Calibration runs write no run events.

duolog never keeps prompts, answers, task text, notes, diffs, file contents,
file names or paths, commands, permission targets, URLs, branches, commit ids,
environment values, sign-in data, email addresses, account or organisation
ids, vendor session or request ids, host names, user names, time zones or
error messages. A field that would hold any of these holds `other`. A line
longer than 1 KiB is refused.

duolog does not copy or move the existing `.duolog` logs into telemetry.

## Where the data lives

Telemetry has one folder for each user. No setting or variable moves it.

| OS | Folder |
|---|---|
| macOS | `~/Library/Application Support/duolog/telemetry.noindex/` |
| Linux | `$XDG_STATE_HOME/duolog/telemetry/`, or `~/.local/state/duolog/telemetry/` when `XDG_STATE_HOME` is unset or relative |
| Windows | `%LocalAppData%\duolog\telemetry\`, never the roaming profile |

WSL uses the Linux folder, so WSL and Windows keep separate histories.

Inside the folder, `v1/_account/` holds the account events. Each project has
its own `v1/<project-id>/`. The project id is the first 16 hex characters of
an HMAC-SHA256 of the main checkout's path, keyed by a random salt in the same
folder. Every linked worktree of a checkout shares one id, and so does every
spelling of the path through a symbolic link. A moved or newly cloned project
gets a new id, and its old history expires. duolog stores no table from ids
to paths.

duolog refuses a folder, and collects nothing, when the folder is inside a
Git work tree, in a cloud-synced folder (`Library/Mobile Documents`,
`Library/CloudStorage`, `Dropbox`, `OneDrive` or `Google Drive`), or on NFS
under Linux, where file locks are unreliable. `duolog doctor` then warns.

## How long data stays

duolog writes each record to the file of the UTC day it writes it. The file
of UTC day D is deleted at the first prune on or after D plus 30 days, at
00:00 UTC. Readers also skip any record older than 30 days, or dated more than
a day ahead, even while its file is still on disk.

`telemetry.retention_days` shortens the 30 days, from 1 to 30. Only your user
config sets it, so pruning does not depend on the project that runs it.

| Setting | Default | What it does |
|---|---|---|
| `enabled` | `true` | The user switch |
| `retention_days` | `30` | Days a record is kept, 1 to 30 |
| `prune_every_hours` | `6` | Hours between automatic prunes |
| `max_megabytes` | `16` | The size cap of the whole folder, account history included |

There is no scheduled job. duolog prunes while it works: at a lead's session
start, at a write when the last prune is older than `prune_every_hours`, and
in every `duolog telemetry` command. A prune also compresses each finished
day with gzip.

Two caps keep the folder small. 1 MB is 1,000,000 bytes:

- The whole folder holds at most `max_megabytes`. Above it, the oldest days go
  first, across all projects.
- A project writes at most 2 MB of plain records in one UTC day. duolog drops
  the records after that and counts them.

The dropped and failed counts in `status` and `doctor` are those of the
process that prints them; duolog stores no counter. A failed write never
fails a run.

A clock set back delays pruning and does nothing else. duolog keeps no clock
state.

## The commands

The commands are for you, in your terminal. A worker cannot run them.

| Command | What it does |
|---|---|
| `duolog telemetry status` | The switches, the folder, its size and files, the oldest day, this project's id, the last prune, the caps, the lost-event counts and the protection checks |
| `duolog telemetry show` | Runs per day, agent, role and outcome, the quota readings and routing moves. `--since` takes `1h` to `30d` (default `7d`), `--project` takes `here` (default) or `all`, `--json` prints JSON |
| `duolog telemetry prune` | Applies retention, compression and the size cap now |
| `duolog telemetry purge` | Deletes this project's history. `--all` deletes every project, the account history and the salt. `--yes` skips the question |
| `duolog telemetry on` and `off` | The user switch |

There is no export command and no option that writes the data elsewhere.

`purge` asks you to type `yes` in a terminal, unless you pass `--yes`.
`purge --all` keeps the small marker that says the first notice was shown,
so the notice does not repeat.

Purge deletes telemetry only. It never touches the `balance` folder beside
the telemetry folder, which is kept for balance routing's own state. Routing
never reads telemetry, so it works the same with telemetry off, failing, full
or purged.

## Who can read the data

Workers cannot read or write the folder. The gate denies any worker read,
write or command path that reaches the telemetry or balance folder, before
policy, modes and questions. It compares resolved paths, so a symbolic link,
`/private/var` against `/var`, a change of case or a Windows short name does
not get around it. It also denies `duolog telemetry` and `duolog balance` to a
worker, with or without global flags such as `--lead`.

A lead model is not the person, and what it reads leaves the machine with its
context. When a Claude lead runs a `duolog telemetry` command, or reads the
folder, you confirm the call. A Codex lead is told to have you run it in your
terminal. Besides the first notice, a session start says at most
`telemetry: unhealthy`. A `doctor` that a model runs, or whose output goes
to a pipe, says only `telemetry: on, healthy`, `off` or `unhealthy`: no path,
id, size or day. `doctor` in your own terminal shows the full line and the
warnings.

## What the protection does not cover

These limits are known and accepted:

- **The gate sees what an agent asks for, not what a program does.** A worker
  that runs project code, such as tests or a build, can read the folder
  without naming it. The records hold numbers and fixed names, so such a read
  shows usage figures, not text.
- **Codex's sandbox limits writes only.** For Codex workers the gate is the
  only check on reads.
- **Native Windows has the gate only.**
- **Claude's sandbox helps only when it is already on.** duolog then adds both
  folders to `sandbox.filesystem.denyRead`. duolog never turns a sandbox on.
  `duolog doctor` says `isolation: gate only` or `isolation: gate + sandbox`.
- **No encryption.** duolog would have to read a key without a prompt, so
  any program you run could read it too. Disk encryption, such as FileVault,
  LUKS or BitLocker, covers a stolen disk.
- **No guaranteed erasure.** Purge deletes the files. Snapshots, backups and
  SSD wear levelling can keep copies.
- **The no-network claim is for the telemetry code.** The telemetry package
  imports no network code, and a test checks this. The duolog binary as a
  whole has network code for MCP sign-in checks, but it sends no telemetry.

The folder is owner only: mode 0700 and files 0600 on macOS and Linux, and a
protected access list for you, SYSTEM and Administrators on Windows. A
folder that is too open is tightened and reported. A folder owned by another
user, or a symbolic link, stops collection.

Backup and search exclusions differ by platform. `duolog doctor` says which
ones it verified:

| OS | What duolog does | Verified |
|---|---|---|
| macOS | Sets the Time Machine exclusion attribute on the folder; the `.noindex` name keeps Spotlight out | duolog reads the attribute back. Tests cannot ask `tmutil`: they run in temporary folders, which Time Machine already skips |
| Linux | Writes `CACHEDIR.TAG`, which borg, restic and `tar --exclude-caches` honour | Deja Dup does not honour it |
| Windows | Nothing: Windows Search does not index AppData by default, and OneDrive folder backup does not cover it | Not verified; Microsoft does not document the OneDrive part |

## Related pages

- [Configuration](configuration.md): the `telemetry` section and the layers
- [Permissions](permissions.md): the gate and the private usage stores
- [Learning mode](learning.md): the journal, which telemetry does not replace
- [How to write the documentation](contributing.md): the writing rules
