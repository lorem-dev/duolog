# How the duolog code is split into layers

Read this before adding or moving a package. `TestLayers` in `layers_test.go`
checks imports on darwin, linux and windows, each on amd64 and arm64.

## How the code is split into tiers

Every package belongs to one tier, and its path decides which. A package that
has no tier fails the layers test, so a new folder cannot slip past the rules.

| Tier | Path | What it holds |
|---|---|---|
| core | `internal/core/...` | Facts, types and decisions, with no I/O |
| app | `internal/app/<name>/...` | Use cases. Each app package owns its ports |
| kit | `internal/adapter/...`, except the agent packages | I/O that is the same for every agent: files, processes, the bus, the store |
| agent | `internal/adapter/claude`, `internal/adapter/codex` | The code of one agent |
| wiring | `internal/wiring/...` | The agent registry. It builds the app packages from the adapters |
| cmd | the module root, `cmd/...`, `scripts/...` | Commands, flags and output |
| test | `internal/testbin/...`, `internal/wiring/wiringtest`, and a package whose last path element ends in `test`, such as `internal/app/delegate/delegatetest` | Test helpers and in-memory fakes |

A port is a small interface that an app package declares and an adapter
implements. The app package then decides what to do, and the adapter does the
I/O. A test can replace the adapter with a fake.

## Which tier may import which

The test checks each import against these rules in this order, and reports the
first rule that the import breaks.

| Rule | An import breaks it when |
|---|---|
| `unknown-tier` | The importing package, or the duolog package it imports, has no tier |
| `test-only` | A package outside the test tier imports a test package from its production code |
| `core-imports-outer` | A core package imports a duolog package that is not core |
| `app-imports-adapter` | An app package imports a kit or agent package |
| `app-imports-outer` | An app package imports wiring or cmd |
| `app-order` | An app package imports an app package that is not earlier in the app order |
| `kit-imports-outer` | A kit package imports app, agent, wiring or cmd |
| `agent-imports-outer` | An agent package imports app, another agent, wiring or cmd |
| `wiring-imports-cmd` | Wiring imports cmd |
| `test-imports-cmd` | A test package imports cmd |
| `core-stdlib` | A core package imports `os`, `net`, `syscall` or a package below them, `plugin` or `unsafe` |
| `app-stdlib` | An app package imports `os/exec`, `net`, `syscall` or a package below them |
| `core-module` | A core package imports a module other than `github.com/bmatcuk/doublestar/v4`. Only `internal/core/config` may also import `go.yaml.in/yaml/v3` |
| `app-module` | An app package imports a module other than doublestar and yaml |

Core has no I/O, so its tests need no files, processes or network. An app
package may read and write the task and state folders with `os`, but it starts
no process and opens no connection. That work goes behind a port, in an
adapter.

An import that only the tests of a package have is a test edge. A test edge
follows the rules for duolog packages. It may also import any test package,
standard package or module, because tests start processes and read test data.

The test leaves out imports of `internal/telemetry`, and later of
`internal/adapter/telemetry`. `TestTelemetryDependencies` and
`TestTelemetryImporters` check those with stricter rules.

## Which app packages may depend on each other

An app package may import only the app packages that come earlier in this
list, lowest first:

```text
launch, lead, learn, paidruns, quota, gate, delegate, profile, peer,
calibrate, doctor, sync, roster, load, hook, install, remember, skilltest
```

The order prevents import cycles. For example, `hook` imports `sync`,
`roster` and `load`; `doctor` imports `peer`; `calibrate` imports `profile`.

## Where today's packages sit

Legacy kit packages are listed in [`legacyTiers`](../layers_test.go). Delete
a row when its package moves; stale rows fail. New packages belong under
tier paths. `legacyTiers` takes precedence over the `test` suffix rule.

## How the layers test reports a broken rule

The test names each import with a key, `from -> to`. A duolog package is
written relative to the module, and the module root is `.`. Any other package
is written as its import path. The key of a test edge has a space and `(test)`
after the importing package:

```text
internal/app/delegate -> os/exec
internal/core/learn (test) -> internal/adapter/config
```

The test fails in two cases:

| Case | What to do |
|---|---|
| An import breaks a rule and its key is in neither `boundaryExceptions` nor `allowedEdges` | Fix the import, most often with a port |
| A key of `boundaryExceptions` or `allowedEdges` happens on no target | Delete the key |

A key counts as found when one target reports it, so a key that exists on
some targets only is not stale. The example is
`internal/app/paidruns -> golang.org/x/sys/windows`.

A move may rename a key when the same import gets a new path at one end. For
example, `internal/app/gate -> internal/store` becomes
`internal/app/gate -> internal/adapter/store` when the store moves under
`internal/adapter`. List each renamed key as `old -> new` in the pull request.

Run the test alone with:

```bash
go test -run TestLayers .
```

## Which imports are boundary exceptions or allowed edges

No import breaks a rule without a reason in `layers_test.go`. Each such import
is a boundary exception or an allowed edge.

**Boundary exceptions.** `boundaryExceptions` lists the imports of the
security boundary that stay on purpose. The gate and the paid-runs account
gate keep a few adapter imports there, because replacing them with ports
would change security code. A key's `from` must be `internal/core/policy`,
`internal/app/gate`, `internal/core/paidrules`, `internal/app/paidruns` or
`internal/adapter/paidstore`. Each reason starts with
`security boundary: moves only;` and a space. `internal/core/policy` may
import `os` and nothing else. The boundary ports (not yet scheduled) will
replace these imports.

**Allowed edges.** A port buys nothing when the code behind it is the same
for every agent, has no planned second backend and needs no test seam at a
process, clock or random boundary. That is the port rule of the design. An
app package may import such a kit directly. `allowedEdges` lists these
imports, each with its reason. A key is valid only when its `from` is
`internal/app/<name>`, its `to` is one of the targets below, and its reason
is not empty. `invalidAllowedEdges` checks this.

| Target | Reason |
|---|---|
| `internal/store` | State folder handle, renames and locks. The same for every agent, with no second backend and no seam |
| `internal/journal` | Append-only journal in the state folder. Same reason |
| `internal/bus` | Message files in the state folder. Same reason |
| `internal/coauthor` | Co-author records in the state folder. The templates per agent come from config |
| `internal/project` | `git rev-parse` of the project and state roots, and git helpers. Git is the project's tool, not an agent's |
| `internal/adapter/session` | Lead and peer registry in the state folder. Its agent parts sit behind the Lead port in `internal/app/lead` |
| `internal/adapter/config` | Loads the config files. The same for every agent: agent facts reach validation through descriptors. No second backend and no seam |
| `plugin` | Embedded skill names, read-only data |

So the app tier may load the config files. `internal/rex` is not on this
list. It gets the `Pane` port, because a second pane backend is planned.

## Which code is kept for the security boundary

The boundary files call some code of other packages. That code keeps its
package, name, signature and behaviour until the boundary ports (not yet
scheduled). It
also holds both agents' arms, so a new agent adds its arm there too.

| Package | Kept | Called from |
|---|---|---|
| `internal/workerenv` | `Build`, `SettingsLayers`, `ArgvLayers`, `ReadLayer`, `IsWorker`: in effect the whole package | `internal/app/paidruns`, `cmd/duolog/gate.go` |
| `internal/quota` | `Reader` with `ReadProcess`, and `IdentityAccount` and `IdentityOrg`: in effect the whole package | `internal/app/paidruns` |
| `internal/trusthome` | `Claude`, `HomeIn`, `ErrNoHome` and the helpers they call | `internal/app/gate`, `internal/app/paidruns`, `cmd/duolog/gate.go` |
| `internal/trust` | `CodexHome` and the helpers it calls | `internal/app/gate` |
| `internal/adapter/session` | `ValidSessionID`, `CurrentLead`, `Open`, `Address`, `CodexMode`, `ReadLeadState`, `Registry`, and the types `Lead` and `LeadObservation` | `internal/app/gate`, `cmd/duolog/gate.go`, `cmd/duolog/leadguard.go` |
| `internal/journal`, `internal/store`, `internal/bus` | All of them | `internal/app/paidruns`, `internal/app/gate` |

## Where the code may name an agent

`TestAgentNames` in `agent_names_test.go` parses every Go file that is not a
test. It fails on a string literal equal to an agent's name outside the
places that `agentNameHomes` lists, each with its reason:

- the agent's own adapter, `internal/adapter/claude` or
  `internal/adapter/codex`
- `internal/core/mode`, which keeps the mode tables of each agent
- the security boundary
- the code kept for the boundary: `internal/workerenv`, `internal/quota`,
  and the files of `internal/adapter/session` that hold a kept function

Other code takes the name from the agent's descriptor, `agent.Descriptor`.
Comments may name an agent, and the test skips the test-tier folders whose
name ends in `test`, such as `internal/app/delegate/delegatetest`. The test
also fails when a place in `agentNameHomes` names no agent any more.

The files that still name an agent today are in `pendingAgentNames`. The list
is closed and may only shrink, under the ceiling `maxPendingAgentNames`, and
names move to the agent descriptors with the boundary ports (not yet
scheduled).

## How to add an agent

1. Make the adapter, `internal/adapter/<name>`. It exports `Facts`, an
   `agent.Descriptor`, and one type for each port that `wiring.Agent` holds.
2. Run the contract test of each port against the adapter. The contract
   tests are in the fake packages, such as
   `internal/app/delegate/delegatetest`.
3. Add the agent to `wiring.Agents()`. A port left nil fails closed: with no
   `Trust`, write roles are refused; with no `Quota`, the paid pass is
   refused; with no `Pane`, `peer open` refuses the agent.
4. Add the adapter's path to the agent tier in `tierOf` of `layers_test.go`
   and to the tier table above.
5. Add the agent to `BuiltinAgents` in `internal/adapter/config`, and to the
   mode tables in `internal/core/mode`.
6. Add its arms to the code kept for the boundary: `internal/workerenv` and
   `internal/quota`, and the lead state and trust if it can be a lead. These
   arms move to the adapter with the boundary ports (not yet scheduled).
7. Run `make schema-write`, then `make schema`. The agent maps of the config,
   such as `paid_runs`,
   `coauthors` and `worker_modes`, take the new name from the descriptor.

## How to prove a move changed nothing

A move changes no logic. It only moves code and changes import paths. Three
checks prove it.

**The test list stays the same.** Before the first change, list the tests of
the old packages. After the change, list the tests of the new and the
remaining packages, then compare. Put both lists in the pull request.

```bash
SCRATCH=$(mktemp -d)
go test -list . ./internal/rex | grep -Ev '^(ok|\?|FAIL)[[:space:]]' | sort > "$SCRATCH/tests-before.txt"
go test -list . ./internal/adapter/rex | grep -Ev '^(ok|\?|FAIL)[[:space:]]' | sort > "$SCRATCH/tests-after.txt"
diff "$SCRATCH/tests-before.txt" "$SCRATCH/tests-after.txt"
```

`diff` prints nothing when the lists match.

**The goldens stay the same.** A golden test compares output with a file in
`testdata/`. The goldens pin the result line of `duolog delegate`, the worker,
peer and lead processes, and the hook output and hook files. A move leaves
every golden byte for byte the same. The `-update` flag rewrites the files:

```bash
go test ./internal/app/delegate -run TestResultLineGolden -update
```

Use `-update` only in a change that means to alter the output, and read every
golden it changes. A golden test refuses output that holds a value of one
machine, such as a temporary folder, a home folder, a timestamp or a carriage
return, because such a golden fails on the next machine.

**The lint exceptions follow the files.** When a file listed in the size
exceptions of `.golangci.yml` moves, change only the `path:` of its lines, by
hand, and keep the text and the numbers. When a function got smaller, run
`make lint-exceptions-update`; it only lowers numbers. When a moved file falls
under a rule that names paths, such as the gosec `G30[126]` path list, add the
new path to that rule. When cyclop reports a new `package average` finding
for a new package, stop and say so in the pull request; do not add an
exception for it. `make lint-exceptions` checks the result.

## Related pages

- [How to write the documentation](contributing.md): the writing rules and the
  checks to run before a commit
- [Permissions](permissions.md): the gate, which moves only and keeps its code
