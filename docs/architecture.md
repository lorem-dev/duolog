# How the duolog code is split into layers

This page describes the tiers of the duolog code, which tier may import which,
and the test that checks the rules. Read it before you add a package or move
code from one package to another. The layers test, `TestLayers` in
`layers_test.go`, checks every import on all six release targets: darwin,
linux and windows, each on amd64 and arm64.

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

## The order of the app packages

An app package may import only the app packages that come earlier in this
list, lowest first:

```text
launch, lead, quota, learn, paidruns, gate, delegate, profile, peer,
calibrate, doctor, sync, roster, hook, install, remember, skilltest
```

The order keeps the app packages free of import cycles and gives every
import one direction. It matches the imports of today's code: `hook` imports
`sync` and `roster`, `doctor` imports `peer`, and `calibrate` imports
`profile`. No app package has to break the order when it moves.

## Where today's packages sit

Most packages have not moved yet. The `legacyTiers` table in `layers_test.go`
gives each one the tier of the part that stays at its path after the
migration, so the rules apply now. For example, `internal/task` is core,
`internal/gate` is the app `gate`, `internal/runner` is kit and
`internal/config/schemagen` is wiring.

Some code has moved already. The core packages `internal/core/mode`,
`internal/core/pathutil`, `internal/core/record`, `internal/core/route`,
`internal/core/screen`, `internal/core/usage` and `internal/core/learn` hold
what used to sit in `internal/mode`, `internal/pathutil`, `internal/router`,
`internal/peer`, `internal/usage` and `internal/learning`. The model resolver
is `internal/adapter/models`, and the code that edits the project config for
learning is `internal/app/learn`. A few old paths stay as forwarding packages
with aliases, so that the security boundary and the files near their size
limits keep their imports. Import the new path in new code.

When a package moves under a tier path, delete its row in the same change. The
test fails when a row names a package that `go list` does not report. Put a
new package under a tier path; do not add a row for it.

The test reads `legacyTiers` before the rule for names that end in `test`.
That is why `internal/skilltest` is the app `skilltest` and not a test
package.

## How the layers test reports a broken rule

The test names each import with a key, `from -> to`. A duolog package is
written relative to the module, and the module root is `.`. Any other package
is written as its import path. The key of a test edge has a space and `(test)`
after the importing package:

```text
internal/app/delegate -> os/exec
internal/adapter/config (test) -> internal/gate
```

`knownViolations` lists the imports that break a rule today, each with the
change that removes it. The test fails in three cases:

| Case | What to do |
|---|---|
| An import breaks a rule and its key is not listed | Fix the import. In a move, rename its key |
| A listed key happens on no target | Delete it and lower `maxKnownViolations` |
| `maxKnownViolations` is not the length of `knownViolations` | Set it to the length |

The list may only shrink. A key that exists on some targets only, such as
`internal/paidruns -> golang.org/x/sys/windows`, counts as found when one
target reports it. The ceiling must equal the length, because a ceiling left
high lets new violations in later.

A move may rename a key when the same import gets a new path at one end. For
example, `internal/app/delegate -> internal/models` becomes
`internal/app/delegate -> internal/adapter/models`. The count does not rise.
List each renamed key as `old -> new` in the pull request.

`boundaryExceptions` lists the imports of the security boundary that stay on
purpose, apart from `knownViolations`. It is empty until the gate moves to
`internal/app/gate`. The gate and the paid-runs account gate then keep a few
adapter imports, because replacing them with ports would change security code.

Run the test alone with:

```bash
go test -run TestLayers .
```

## How to prove a move changed nothing

A move changes no logic. It only moves code, changes import paths and adds
forwarding aliases. Three checks prove it.

**The test list stays the same.** Before the first change, list the tests of
the old packages. After the change, list the tests of the new and the
remaining packages, then compare. Put both lists in the pull request.

```bash
SCRATCH=$(mktemp -d)
go test -list . ./internal/models ./internal/router | grep -Ev '^(ok|\?|FAIL)[[:space:]]' | sort > "$SCRATCH/tests-before.txt"
go test -list . ./internal/core/route ./internal/adapter/models ./internal/models ./internal/router | grep -Ev '^(ok|\?|FAIL)[[:space:]]' | sort > "$SCRATCH/tests-after.txt"
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
