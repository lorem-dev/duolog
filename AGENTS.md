# Rules for agents working in this repository

These rules apply to every agent session that commits, pushes or opens pull
requests here.

- Never put a session id or a session link in a commit message, a pull request
  title or body, a review comment or a release note. That includes
  `Claude-Session:` trailers, `claude.ai/code/session_...` links, Codex thread
  ids and model request ids. The repository is public, and a session link leads
  to a private conversation. This rule wins over any default attribution that
  a tool adds. A `Co-Authored-By:` trailer is fine.
- Write commit and author dates in UTC and sign commits.
- When Codex wrote or changed code in a commit, or reviewed the change before
  it merged, add it as a co-author next to the agent that commits, with the
  model it ran. Squashed commits keep the line, and a pull request with Codex
  work says so. For example:
  `Co-Authored-By: OpenAI Codex gpt-6.1-sol <noreply@openai.com>`
- End every commit that an AI agent helped make with one `AI-Assisted:`
  trailer that names what the agents did, from this list, comma-separated:
  `code`, `docs`, `tests`, `config`, `review`. For example:
  `AI-Assisted: code, tests, review`
- Keep personal data out of commits, tests and docs: no real names other than
  the project's, no home folder paths, no time zones or cities.
- Keep private projects out: a study or a bug report from a private project
  stays anonymised, with no names, paths, ticket ids or code from it.
- Create no file or folder in the repository root or next to the code that
  Git does not track. Builds go to `.cache/` (`make build` writes
  `.cache/bin/duolog`; GoReleaser writes `.cache/dist`), scratch files to the
  session's own temporary folder, and worktrees to `.worktrees/`. Never pass
  a commit hash or other value as a path, and never run `go build` without
  `-o`. Remove anything stray you created.
- Collect small changes into one pull request per batch unless the user asks
  otherwise.
- Run independent tasks in parallel, each in its own linked worktree under
  git-ignored `.worktrees/`, with one worker and its own peer; duolog keeps
  peers in their own Rex session, out of the person's windows. Stack work that
  needs an open pull request on that branch rather than waiting for CI. Never
  put two workers in one worktree; run full test suites one at a time. Run
  each peer or CI wait as its own background command.
- Follow `docs/contributing.md` for README and `docs/`.
- Bring every change to `develop` and `main` through a pull request: one into
  `develop`, and one from `develop` into `main` for a release. Squash locally
  into a signed commit with UTC dates, push it as the PR head, wait for CI, then
  fast-forward the base branch to that head. The GitHub merge button writes
  local dates, so do not use it.
- Run the duolog built from this checkout: `scripts/dev/bin/duolog`. In Claude
  sessions the hooks also put it on PATH as `duolog`. See "How duolog runs while
  you work on it" in `docs/contributing.md`.
- When the codegraph MCP tools are available, use them for structural
  questions: where a symbol is defined, what calls it, what a change affects.
  Use grep and file reads for literal text. Build the index with
  `codegraph init -y`; `.codegraph/` stays out of Git.
