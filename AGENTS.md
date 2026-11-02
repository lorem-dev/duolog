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
- Keep personal data out of commits, tests and docs: no real names other than
  the project's, no home folder paths, no time zones or cities.
- Keep private projects out: a study or a bug report from a private project
  stays anonymised, with no names, paths, ticket ids or code from it.
- Follow `docs/contributing.md` for README and `docs/`.
