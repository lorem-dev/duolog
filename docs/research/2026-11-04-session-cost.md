# What a fresh agent session costs and whether a cheaper hand-over exists

This page records what a new agent session costs duolog, and whether native
subagents, resumed threads or messages between sessions are cheaper, and at
what quality. It merges our own measurements with two research reports. Models
are named by family only.

## What we found

A fresh session with duolog's worker settings is the cheapest hand-over that we
measured, and its quality was as good as the others.

- A default Claude session writes about 15 thousand tokens to the cache at each
  start. With the worker settings the start writes about 4 thousand, and a real
  read-only task cost about 0.05 USD, against 0.06 to 0.12 USD for a default
  session.
- A native Claude subagent cost about 2.7 times a worker session on the same
  task, with the same quality.
- A resumed Claude follow-up gave the same recall as a fresh session and cost
  about 1.8 times more.
- Codex reuses the provider's prompt cache across separate sessions, so a fresh
  Codex start is cheap in money. A resumed Codex turn was not cheaper either.
- On three edit tasks replayed from history, every form passed the hidden tests
  for what the task asked. A worker session cost 0.15 USD on average, a
  default session 0.23 and a native subagent 0.28.

## How we measured

The lead ran small read-only checks on 2026-11-04 on one machine, with the
person's normal user settings unless a row says otherwise. Token counts are the
numbers that each CLI reports. A "worker settings" row uses the flags that
duolog gives a `minimal` worker, such as `--strict-mcp-config`, a minimal
settings file and no slash commands.

| Round | Task | Agents | Runs per case |
|---|---|---|---|
| 1 | Reply with one word, in an empty folder | Claude (sonnet family), Codex (sol family, low effort) | 1 |
| 2 | A: find where the gate denies worker reads of duolog state and explain it in 5 lines. B: list the tests that cover that decision | Same, Codex at medium effort, read-only | 1 |
| 3 | Same tasks A and B, repeated | Same | Claude 3, Codex 2 |

Task B ran either as a follow-up in the same session (`--resume`, or
`exec resume` for Codex) or as a fresh session. In round 3, a blind judge from
the opus family graded each task A answer from 0 to 5 against five reference
facts, minus one for each wrong claim. We scored each task B list by recall of
16 reference tests. We did not score precision, because answers often named a
few more relevant tests.

Limits: the samples are small, on one repository and one kind of work (code
reading). The one-word round is a mechanics check, not a benchmark. Edit tasks
were not measured. Codex prints no price, so its rows show tokens only.

## What a session start costs

Round 1, one run each. Cache write and cache read are Claude's counts. Codex
counts cached input inside its input.

| Case | Fresh input | Cache write | Cache read | USD (CLI) |
|---|---|---|---|---|
| Claude, default new session | 2 | 16,830 | 11,382 | 0.069 |
| Claude, same again, new session | 2 | 14,603 | 15,266 | 0.060 |
| Claude, `--resume`, second short turn | 4 | 14,617 | 15,266 | +0.060 |
| Claude, worker settings | 2 | 3,901 | 17,022 | 0.017 |
| Claude, `--tools ""` | 2 | 5,077 | 649 | 0.020 |
| Claude, parent asks a native subagent | 6 | 38,676 | 41,441 | 0.129 |
| Codex, new session | 14,943 (12,288 cached) | 0 | - | - |
| Codex, same again | 15,489 (12,288 cached) | 0 | - | - |
| Codex, `exec resume`, second turn (total) | 30,970 (27,008 cached) | 0 | - | - |

- A default Claude session re-writes about 15 thousand tokens at each start,
  even right after an identical session. Something in the start context
  changes from session to session. A resumed turn paid the same write again.
- The worker settings cut the cache write to about a quarter and the price to
  about a quarter. MCP servers, skills and instruction files are most of the
  per-session overhead.
- The native subagent paid its own start of about 21 thousand tokens of cache
  write, and the parent paid a turn to start it.
- Codex served about 12.3 thousand of 15 thousand input tokens from the cache
  in every new session. The cache covered the fixed prefix, not the task.

## What a real task costs, and the quality

Rounds 2 and 3 use Claude with `--setting-sources user` and read tools allowed.
USD is the mean of three runs, with the range in brackets.

| Case | Task A score (runs) | Task A USD | Task B recall of 16 (runs) | Task B USD |
|---|---|---|---|---|
| Claude, default new session | 3.33 (3, 3, 4) | 0.064 (0.042-0.105) | - | - |
| Claude, worker settings | 4.00 (4, 4, 4) | 0.048 (0.043-0.051) | fresh: 15, 15, 15 | 0.047 (0.041-0.059) |
| Claude, follow-up with `--resume` | - | - | 16, 16, 14 | 0.083 (0.066-0.097) |
| Claude, parent plus native subagent | 4.00 (4, 4, 4) | 0.131 (0.094-0.185) | - | - |
| Codex, new session | 3.50 (5, 2) | 78-104k input, about 85% cached | fresh: 16, 15 | 103-112k input, 79-81% cached |
| Codex, follow-up with `exec resume` | - | - | 15, 15 | 50-98k added input |

- Worker settings matched the native subagent on quality (4.00) at about 37% of
  its cost. They also beat a default session on both quality and cost, with
  less spread.
- A resumed follow-up gave the same recall as a fresh worker. It cost about 1.8
  times more. In round 2 the single sample (0.114 against 0.121 USD) pointed
  the other way, and the repeat did not confirm it. One sample is not enough.
- The resumed session needed fewer turns (4 against 15 in round 2), because it
  knew the code. The saving went into reading the longer history again.
- In round 2 the native subagent's parent reported that it had not checked the
  code itself. We did not record this in round 3, where all three scores were
  4.00.
- Codex quality varied more between runs (5 and 2 on task A). Two runs are too
  few to compare it with Claude.

## What an edit task costs, and the quality

Three edit tasks were redone from the develop base, each taken from a real fix
of the same day. The fix's tests were hidden from the agent and added after the
run. "Core" counts the hidden tests for behaviour that the task text asked for.

| Form | Runs | Core hidden tests | USD per run, mean (range) | Time per run |
| --- | --- | --- | --- | --- |
| Claude, worker settings | 9 | all passed in every run | 0.153 (0.121-0.189) | 43-166 s |
| Claude, default session | 6 | all passed in every run | 0.233 (0.197-0.266) | 51-133 s |
| Claude, native subagent | 6 | all passed in every run | 0.276 (0.243-0.300) | 59-143 s |
| Codex, new session | 9 | all passed in every run | 214k-662k input, 85-97% cached | 59-163 s |

- The forms differed only on behaviour the task text did not mention. An
  escaped `\>|` case passed in 2 of 3 Codex runs and in none of 7 Claude runs.
  A weekly reset with a date failed for every form.
- One stated behaviour failed for every form in the same way, which points at
  the hidden test rather than the agents.
- In task 1, Codex added a test with the same name as a hidden one, so the
  package did not build once the hidden tests were added. With the agent's
  test file reset, the hidden tests passed.
- For calibration this means two rules: a task text names every behaviour the
  hidden tests check, and candidate tests are kept apart from hidden tests.

## What the public sources say

Neither report could read a public page. The gate denied web requests in both
runs. So every claim about a vendor product in the reports is unconfirmed. The
list below is what the reports expect, with the page to check. We did not read
these pages either.

| Claim | Status | Page to check |
|---|---|---|
| A Claude cache entry lives five minutes or one hour, and a read refreshes it | Unconfirmed | [Prompt caching](https://platform.claude.com/docs/en/build-with-claude/prompt-caching) |
| The Claude cache key is the exact prefix of tools, system prompt and messages | Unconfirmed | Same page |
| A one-hour Claude cache write costs twice the base input price | Matches one saved run to the last digit, but unconfirmed in the docs | [Claude pricing](https://platform.claude.com/docs/en/about-claude/pricing) |
| OpenAI caches common prefixes from 1,024 tokens in steps of 128, for minutes of idle time | Steps seen in our fixtures; the rest unconfirmed | [OpenAI prompt caching](https://developers.openai.com/api/docs/guides/prompt-caching) |
| A Claude subagent starts with a fresh context, with its own model and tools | Unconfirmed | [Claude Code subagents](https://code.claude.com/docs/en/sub-agents) |
| Hooks fire for a subagent's tool calls with a usable identity | Unconfirmed; Codex marked not verified in our earlier tests | [Claude Code hooks](https://code.claude.com/docs/en/hooks) |
| Codex subagents inherit the parent's history by default and can use their own model and effort | Seen in our earlier tests; docs unconfirmed | [Codex multi-agent](https://developers.openai.com/codex/multi-agent) |
| Resume restores a conversation but does not make its tokens free | Unconfirmed | [Claude Code CLI reference](https://code.claude.com/docs/en/cli-reference) |

What the repository itself shows, without a public page:

- A call carries about 15 to 16.5 thousand input tokens before the task, for
  both agents.
- Codex lost its cache with `--output-schema` in an earlier probe. duolog no
  longer passes that flag.
- Every model call in a turn sends the whole context again. In an earlier
  bench a resumed Codex thread cost more than a new session with the content
  inline.
- `duolog stats` prices a Claude cache write as plain input, so it
  under-reports Claude workers (37% on one saved run).

## Where the reports and the measurements disagree

The measurements win wherever they exist.

| Topic | Reports | Measurements | Which wins |
|---|---|---|---|
| A native subagent | One report: at best a little cheaper than a fresh session. The other: undecided | About 2.7 times a worker session, same quality | Measurements |
| A resumed thread | One report: cheaper when the follow-up needs the material again, within 30 minutes | 1.8 times a fresh worker session, same recall | Measurements, for code reading |
| A live peer | One report: a warm cache makes it cheaper for long work | Not measured | Open |
| A native subagent on the cheapest model | One report: a win under a Claude lead | Only the sonnet family was measured | Open |
| Cross-session cache | Hypothesis, to verify | Claude: shared prefix only. Codex: about 80% of input cached | Measurements |
| Cold start of a Claude session | 16.4 thousand tokens per call, with no MCP servers | A default session wrote and read about 28 thousand tokens, the worker settings about 21 thousand | Measurements |

Both reports agree on three points that the measurements do not contradict: a
native subagent does not avoid the cold start, it loses the role sandbox, the
gate per role, routing and the usage records, and it needs the vendor pages to
be verified first.

## What this means for duolog

Keep a fresh session with the worker settings for delegation.

- Fresh worker sessions stay the default. They cost least, kept the quality of
  the other forms, and keep the sandbox, the gate, routing and the records.
- Do not switch to native subagents for cost. We measured no case where one is
  cheaper. Consider one only for a read-only search under the lead's own
  vendor when the other vendor is at its limit, which duolog already does.
  Before any wider use, check that the lead's hooks fire for the child's tool
  calls.
- A resumed thread is worth it only when a follow-up must read again much of
  what the first run read, and the answer takes one or two calls. We found no
  such case yet. Keep the idle close below the cache lifetime.
- Keep the worker settings narrow. MCP servers, skills and instruction files
  are most of the start cost, so a role that needs them pays for them.

## What to measure next

1. More edit tasks, larger ones, from more than one repository.
2. More runs per case, at least five, because one task can vary up to 30 times
   in tokens between runs.
3. The Codex quality spread, with five or more runs on task A.
4. A live peer and a native Codex subagent. Neither was measured.
5. A resume after the cache lifetime, to see what a late resume pays.
6. Cache writes in `usage.ndjson` and `duolog stats`, so that the records show
   the real cost of a Claude worker.

## Related pages

- [Cost study on 2026-11-01](2026-11-01-cost-study.md): the cost of duolog
  against single sessions
- [Practices and models](2026-10-30-practices-and-models.md): the evidence
  behind the default models
- [Live verification](2026-10-30-live-verification.md): the earlier hand-over
  bench
- [Budgets](../budgets.md): the cost targets
- [Configuration](../configuration.md): worker settings and session reuse
