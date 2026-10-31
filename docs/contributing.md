# How to write the documentation

This page gives the rules for `README.md` and every page in `docs/`. Most
readers are not native English speakers, so clear writing is a requirement.

These rules come from the writing style of the `platform` repository. We left
out its rules for the documentation site and for diagrams, because this project
has no site.

## Rules for sentences

**Use English and ASCII only.** Do not use curly quotes, em dashes or ellipsis
characters. A reader can then search for any text and type it on any keyboard.

**Write short sentences.** Put one idea in each sentence. Split a sentence that
has more than about 25 words.

**Use common words.** Write *use*, not *utilise*. Write *start*, not *initiate*.
Write *about*, not *regarding*.

**Use the active voice.** Write *the hook runs the command*, not *the command is
run by the hook*. The active voice shows who does the work.

**Do not use idioms.** A phrase such as *low-hanging fruit* does not translate.
Say what you mean.

**Use one word for one thing.** A reader who sees a new word looks for a new
thing. The [approved words](#use-these-words) are below.

**Explain a term the first time it appears on a page.** Readers often arrive
from a search, not from the first page.

**Put the answer first.** Start a section with the conclusion, then explain why.

**Prefer a table to a list of paragraphs** when you compare things.

**Say why, not only what.** The next person may remove a rule that has no
reason.

## Rules for pages

Every page starts with one short paragraph that says what the page is about.

Headings are sentences, not labels. `How a worker asks for permission` is better
than `Permissions`.

Lines have at most 80 characters. Tables, fenced code blocks and the badge links
in `README.md` are exempt, because you cannot wrap them without breaking them.

Every page ends with a section named `Related pages`, unless it has none.

## Use these words

| Word | What it means |
|---|---|
| Agent | Claude Code or Codex |
| Session | One running conversation with an agent |
| Lead | The session you work in. It starts and controls the others |
| Worker | A session that the lead started |
| Role | A kind of work, such as `review_final` |
| Route | The agent, model and effort that do a role |
| Bus | The file that sessions use to send short messages to each other |
| Message | One line on the bus |
| Artifact | A file that sessions share. Messages point to it |
| Task | A work request in a `task.md` file |
| Sync | Generating one agent's skill and agent files from the other's |

## How to check a page before you commit

Two checks run in CI. Run them with one command:

```bash
make docs-lint
```

| Check | What it finds |
|---|---|
| `pymarkdownlnt`, with the rules in `.pymarkdown.yaml` | Heading style, list style, trailing spaces, tabs, bare URLs |
| `docs_test.go` | Characters that are not ASCII, long lines, a page with no first paragraph |

## How branches work

The repository uses three kinds of branch, so that `main` always holds what was
released.

| Branch | What it is for |
|---|---|
| `main` | Releases. Direct commits stop after the first release |
| `develop` | Integration. Feature branches merge here |
| `feature/<name>` | One piece of work, branched from `develop` |

Before a release, `develop` merges to `main` through a pull request. After that,
a workflow opens a pull request that merges `main` back into `develop`, so the
two do not drift apart.

## What does not belong in the documentation

- Status and meeting notes. They go out of date.
- Plans and design notes. These go in `docs/superpowers/`, which git ignores.
- Code that goes out of date. Link to the source file instead.

Python scripts carry a uv header and type annotations. Run them as
`uv run <script>`, on every OS, rather than executing a shebang. PowerShell
uses `$env:NAME = 'value'` instead of `export NAME=value`. It uses a backtick
for a continued command instead of a POSIX backslash; prefer one-line commands.

On Windows, use `go build ./cmd/duolog` instead of `make build`. For the
individual checks, run `go vet ./...`, `go test ./...`,
`go test . -run '^TestSchemaFileIsCurrent$'`,
`go run ./scripts/thirdparty -check` and `go run ./scripts/render-install`.
The CI workflow shows the PowerShell form of docs lint; it lists Markdown
files with `Get-ChildItem` before calling `uvx` and `go test -run TestDocs .`.
The test jobs cover Linux amd64/arm64, macOS and Windows amd64/arm64. The
Windows arm64 job runs without the race detector, which Go does not support.
The cross-build job compiles all six release targets. These checks do not run
real agents.

## How to write the changelog

`CHANGES.md` tells a person whether to upgrade, and the GitHub release shows the
section of that version. Add an entry to `## Development` in the change that
causes it. The limits are hard, because a changelog is scanned, not read:

| Limit | Value |
|---|---|
| Bullets in one version section | 15 |
| Characters in one bullet | 160 |
| Lines in one bullet | 2 |
| Characters in one line | 80 |

Use one level of bullets, and only the headings `Added`, `Changed`, `Fixed`,
`Removed` and `Security`. A fix for a bug that no release had needs no entry.
The comment at the top of the file has the full rules, and CI fails when one
breaks.

## How to change the version

Use `scripts/version/version.py`, because the version appears in several files.
The `bump` command sets all of them and moves the `## Development` entries under
the new version. The `check` command fails CI when a file differs. A tag such as
`v1.2.3` must match, or the release workflow stops.

```bash
uv run scripts/version/version.py bump 1.2.3
uv run scripts/version/version.py check --tag v1.2.3
```

The repository has `bump-version`, `check-version` and `check-changes` skills
that run these commands. Edit a skill in `.claude/skills/`, then run
`go run ./cmd/duolog sync`. That command generates the copy in `.agents/skills/`
that Codex reads, and CI fails if the copy is out of date.

## How to add a dependency

Prefer the Go standard library. Explain why each new module is needed in the
pull request. Run `make third-party` and commit the updated license table.
Each new module must pass the license check in `make third-party-check`.
