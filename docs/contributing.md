# How to write the documentation

Write docs in plain English for readers who may not be native speakers.

## How to write sentences

| Rule | Reason or example |
|---|---|
| Use English and ASCII | Readers can search and type the text on any keyboard |
| Keep one idea per sentence; split after about 25 words | Short sentences are easier to read |
| Use common words | use, start, about |
| Use active voice | The hook runs the command |
| Avoid idioms | Say what you mean |
| Use one term per thing | A new word suggests a different thing |
| Define terms on first use per page | Readers may arrive from search |
| Put the answer first | Explain why afterwards |
| Compare things in tables | Avoid parallel paragraphs |
| Explain rules | Readers need the reason before removing one |

## How to structure pages

Every page starts with one short paragraph that says what the page is about.

Headings are sentences, not labels. `How a worker asks for permission` is better
than `Permissions`.

Lines have at most 80 characters. Tables, fenced code blocks and the badge links
in `README.md` are exempt, because you cannot wrap them without breaking them.

Every page ends with a section named `Related pages`, unless it has none.

## Use these words

Use the terms in [Glossary](glossary.md). A message is one bus line; an
artifact is a shared file referenced by a message. Sync generates one
agent's skill and agent files from the other's.

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

## How to run checks on Windows

Python scripts carry a uv header and type annotations. Run them as
`uv run <script>` on every OS. PowerShell uses `$env:NAME = 'value'` for
variables and a backtick for a continued command; prefer one-line commands.

On Windows, use `go build -o .cache/bin/duolog.exe ./cmd/duolog` instead of
`make build`. Put `.cache/bin` on PATH. For the
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
that run these commands. Edit a skill in `.claude/skills/`. In a fresh clone,
first delete its generated skill folders under `.agents/skills/`, as CI does.
Keep the installed `duolog` and `duolog-skill-check` folders. The sync lock
is local, so sync otherwise refuses the committed copies. Then run
`go run ./cmd/duolog sync --from claude` to generate the copies Codex reads.
CI fails if a generated copy is out of date.

## How to add a dependency

Prefer the Go standard library. Explain why each new module is needed in the
pull request. Run `make third-party` and commit the updated license table.
Each new module must pass the license check in `make third-party-check`.

## How duolog runs while you work on it

In this repository, Claude Code and Codex run the duolog that you build from
the work tree, not a release. Nothing outside the repository changes.

- `.claude/settings.json` and `.codex/hooks.json` call `scripts/dev/bin/duolog`.
  On Windows, Codex calls `scripts/dev/bin/duolog.ps1`, from the repository
  root.
- The script builds `.cache/dev/duolog` only when it is missing, then runs it.
  The build stays pinned while the lead and workers edit the source.
- If that build fails and the work tree has no earlier build, for example in a
  worktree whose code does not compile during a merge with conflicts, the
  script does not stop the session. It runs the build of the main checkout of
  the same repository (`.cache/dev/duolog` there) if it exists, else an
  installed `duolog` on `PATH`, and says on stderr which one it used. The
  installed one is never a `scripts/dev/bin` launcher, this one or another
  checkout's. The script passes the launchers that already failed in
  `DUOLOG_DEV_TRIED`, and a launcher listed there runs no fallback again, so
  two broken checkouts cannot hand the run to each other. Only when nothing
  usable is left does it fail, with exit code 2 for a worker so that the
  submit stays blocked, and 1 otherwise.
- After verifying a batch, the lead runs `scripts/dev/rebuild`, or
  `scripts/dev/rebuild.ps1` on Windows. It runs vet and tests before replacing
  the build. A failed check or build leaves the earlier build in place. It
  closes the lead's idle peers; working peers and peers with questions stay
  open.
- At the start of a session it copies the skills from `plugin/`. For Claude it
  also puts `scripts/dev/bin` first on the session's PATH, so that `duolog` in
  a command runs this build. A Codex session runs `scripts/dev/bin/duolog`.
- The duolog plugin is off in this repository, so that no gate runs twice.

Codex runs the hooks only after you approve them in `/hooks` and trust the
project. `.duolog/config.yaml` passes the Go settings to workers.
`.duolog/learned.yaml` lets workers run the checks above. `.codex/rules/` and
`.claude/settings.json` let the lead run them without a question.

## Related pages

- [Glossary](glossary.md): terms used in the docs
- [Architecture](architecture.md): package and import rules
