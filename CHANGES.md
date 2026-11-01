# Changes

<!--
  RULES FOR THIS FILE. scripts/version/version.py enforces the limits, and CI
  fails when one is broken.

  Audience: someone deciding whether to upgrade. This is not a commit log.

  LIMITS, all hard:
    15 bullets in one version section, across its subsections.
    160 characters in one bullet.
    2 lines in one bullet, with 80 characters in a line.
    One level of bullets. No code blocks, tables or paragraphs inside a section.

  Over a limit, merge related entries into one that names the feature, or cut
  what does not affect a user. A long entry belongs in the docs: link to them.

  What goes in:
    - A feature, option or command that a user can now use.
    - A change in behaviour that would otherwise surprise them.
    - A fix for something that was broken in a released version.
    - A removal, or anything that needs action on upgrade.

  What stays out:
    - A fix for a bug that no released version had.
    - Refactoring, tests, CI and dependency bumps with no visible effect.
    - The reasoning. One clause of why is fine when it changes what a reader does.

  Form:
    - One entry per user-visible thing. Present tense, active voice.
    - Name the command or option in backticks, so that a reader can search for it.
    - ASCII only.

  Sections: `## Development` collects unreleased entries and is always first. A
  version heading is `## 1.2.3`, with no date. Inside a section, use only
  `### Added`, `### Changed`, `### Fixed`, `### Removed` and `### Security`.

  `version.py bump` turns Development into a version section, and `version.py
  notes` extracts that section as the body of the GitHub release, so the heading
  format matters.
-->

## Development

### Changed

- The Claude skill runs report commands, such as `duolog doctor`, unprompted.
- The skills install the version they shipped with, not the latest release.

### Fixed

- `duolog sync` waits up to 15 seconds for another run, not 2, on a slow disk.
- A hook no longer ignores input that starts with a byte order mark.

## 1.0.0

### Added

- Roles send each kind of work to an agent, model and effort that you set in
  `.duolog/config.yaml`.
- `duolog delegate` runs a task on a worker and returns one short line.
- `duolog peer` opens a second agent in a Rex pane and reports its state.
- A permission gate decides worker actions from project rules, with no model
  call.
- Modes `plan`, `ask`, `edits` and `full`. A worker's effort never exceeds the
  lead's.
- At a provider limit, duolog starts no new session and prints `run here:`.
- `duolog sync` generates agent and skill files for the other agent.
- Plugins for Claude Code and Codex from a custom marketplace, and
  `duolog install`.
- `duolog doctor`, `roster`, `caps` and `stats` report setup, routes, gaps and
  cost.
- One-line installers for macOS, Linux and Windows that verify SHA-256.
