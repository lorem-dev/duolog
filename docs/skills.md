# How to test a skill across models

The lead uses `duolog skill test` to grade models' responses to concrete
situations after a skill change. Workers cannot run it or change its
preference.

## Write a plan before running models

Save a plan at `.duolog/skill-tests/<skill>.plan.yaml`, where `<skill>` is the
skill's folder name. Use `--plan <file>` to choose another path. When the plan
is missing, the command prints its path and stops without running models.

```yaml
situations:
  - id: peer-question
    text: A peer asks a question about an approved edit. How do you answer?
    check:
      contains: [duolog peer answer]
      absent: [duolog allow]
      starts: duolog
      regex: 'peer answer\s+peer-'
```

Each situation has a unique `id`, concrete `text` and a `check`. Every given
part of a check must pass. All comparisons ignore case. `contains` requires
every listed string; `absent` forbids every listed string. `starts` checks
the start after trimming space. `regex` uses Go regular expressions.
An empty answer, missing id or invalid JSON fails the check.

Use `agents: [claude]` or `agents: [codex]` on a situation to test only that
agent. An omitted or empty list applies to both; `[claude, codex]` also tests
both. Other agent names are errors. Runs receive and grade only their matching
situations. Skipped cells show `..` and do not count toward the score or cost
estimate. A target with no matching situations makes no model call.

Draw situations from the rules the change touches and from earlier failures.
Show the plan to the person before testing. When an expected answer was
wrong, fix the plan. Change the skill for failures the person agrees are real.

## Choose models and approve the cost

```bash
duolog skill test plugin/claude/skills/duolog/SKILL.md
duolog skill test path/to/SKILL.md --models claude/haiku/low,codex/sol/high --yes
```

The default uses each available agent's cheapest and strongest configured
model at low and high effort. Availability uses the router's provider state
and installed commands. The price endpoints select the model tiers. Models in
`limits.manual_only_models` (and `fable` and `astra`) are skipped, so the
strongest tier is the strongest model that is not manual-only. `--models`
chooses explicit agent/model/effort triples instead; it names a manual-only
model only together with `--allow-manual-model`, as `duolog delegate` does.
Codex aliases resolve to the newest visible version, as for delegation.

The command prints the run count and a cost estimate before asking to start.
The estimate uses configured prices, prompt bytes divided by four, and 200
output tokens per situation. Reasoning and provider overhead can raise the
actual cost. `--yes` approves this run without another question.

Runs use a temporary directory, no MCP servers and no tools. Claude gets an
empty tool list. Codex uses its read-only sandbox with shell and patch tools
disabled. Each run returns one JSON object keyed by situation id. The final
table shows `ok`, `--` or `..`, a score per run and costs from recorded usage.

Each run is admitted like a delegated worker and takes one of the agent's
sessions while it runs. A skill test runs only on a subscription: when the
agent's free worker would be paid, the run is refused before any model call,
and the error names the signal, even when `paid_runs` allows the agent.
See [paid runs](configuration.md#how-paid-runs-are-allowed-and-capped).

Raw answers overwrite `.duolog/skill-tests/<skill>/<run>.json` on the next
run of that target. The files include failed checks and process errors. When
a run fails, its stdout and stderr are copied to
`.duolog/skill-tests/<skill>/<run>.log` and the error line points there; a
later run of that target removes an old log.
Usage also goes to `.duolog/usage.ndjson`, as for delegation, and appears in
`duolog stats`. In linked worktrees, usage and provider availability share
the main checkout's state. Plans, preferences and answers use the current
checkout. Costs use the prices in config, not a provider invoice.

## Remember whether to test changes

The bundled `duolog-skill-check` follows your guidelines and any installed
skill-writing/review skill. It shows a plan and estimated cost, then asks
once per change: test, skip, always here or never here.

```bash
duolog skill test --remember always
duolog skill test --remember never
duolog skill test --remember ask
```

These commands set `skill_tests` in `.duolog/config.local.yaml` and preserve
other settings. The default is `ask`. With `always`, runs need no new
confirmation. With `never`, the command skips tests, even with `--yes`.
This repository sets `always` in `.duolog/config.yaml`.

`duolog install` installs this skill beside the duolog skill for both agents,
at user scope. The plugins include it too. Uninstall removes it. See
[Getting started](getting-started.md#where-duolog-keeps-its-skills).

## Which other skills are bundled

Install and plugins supply `duolog-calibrate` and `duolog-peers`.
Calibration runs only when requested, independently of `skill_tests`; see
[Calibration](calibration.md).

`duolog-peers` runs `runs --long` and, only when you ask to see a run,
`watch --tab`; for a peer, this is `peer view`. Claude needs no further
confirmation for that request. The skill names `runs --snapshot` as the same
list in one JSON line. It refuses `runs tail` as it refuses a bare `watch`:
both print a worker's stream, which stays out of the lead's context. In
Claude Code, `/duolog-pane` opens the pane without a model turn. See
[Rex](rex.md#how-to-watch-a-delegate-run) and
[the Claude pane](rex.md#how-to-see-peers-and-runs-in-the-claude-pane).

## The update skill updates duolog when you ask

`duolog-update` updates the binary, this agent's plugin and existing installs.
It leaves permission prompts and changed Codex hook approvals to you.
See [How to update duolog](getting-started.md#how-to-update-duolog).

## Related pages

- [Getting started](getting-started.md): install duolog and delegate a task
- [Configuration](configuration.md): layers, models and prices
- [Sync](sync.md): keep your own skills in both agents
- [Calibration](calibration.md): what the `duolog-calibrate` skill runs
- [Live peers with Rex](rex.md): what the `duolog-peers` skill runs
