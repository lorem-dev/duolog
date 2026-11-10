# What duolog is allowed to cost

This page lists the cost targets for duolog and the value that we measured for
each. The targets come from the design. A miss is reported to the author, and
the target stays as it is.

## The targets

| What | Target | Measured |
|---|---|---|
| A message on the bus | At most 512 bytes | not measured yet |
| A hook with no new message | 0 tokens | not measured yet |
| The `SessionStart` line | At most 80 tokens | not measured yet |
| An ordinary `gate` call | 0 tokens, under 50 ms | 0 tokens; 20 ms median on two macOS arm64 machines; 0.26 to 0.7 s through the Codex hook guard on Windows 11 |

Code enforces the first target: the bus refuses a longer line. Two more need
a live session, so they stay "not measured yet" until someone runs them. The
gate meets its target on macOS and misses it on Windows, where the shell guard
starts more processes.

The live run of 2026-10-31 did measure the cost of a handoff (`gpt-6-luna`, low
effort, estimated USD per run):

| What | Measured |
|---|---|
| Whole file inline (2000 lines) | 0.00137 USD, against 0.00238 for the saved whole-file reference |
| 16 lines inline | 0.00049 USD, against 0.00089 for the saved range reference |
| Prompt cache of a small delegate run | 11520 of 14952 input tokens cached |

## How to measure

The live tests print the usage of each run from `.duolog/usage.ndjson`.
Estimated USD prices Claude cache writes at 1.25 times the input rate for the
five-minute cache and 2 times for the one-hour cache. See [how to see what
the work cost](getting-started.md#how-to-see-what-the-work-cost).
Run them with real agents:

```bash
go test -tags=e2e ./test/e2e/ -v
```

The tests use `luna` and `haiku` at low effort to keep the cost small. They
start real agents and spend tokens, so run them on purpose. Tests that change
the Codex folder trust need `DUOLOG_E2E_TRUST=1`. See
[the end-to-end tests](../test/e2e/helpers_test.go).

`TestInlineCostsLessThanReference` repeats the handoff measurement from the
research page through `duolog delegate`. It compares the cost of the inline
form with the saved whole-file and range rows. Both sides are priced as if
nothing was cached, because Codex decides per call whether a prompt comes from
its cache, and that halved or doubled the cached price between runs.

The cost of whole sessions, with and without duolog, is on
[the cost study](research/2026-11-01-cost-study.md) page. It also found that
effort above `high` raised no model's quality, while Opus at `max` cost six
times Opus at `high`, so no default role asks for `xhigh` or `max`.

## Related pages

- [Bus](bus.md): the message limits
- [Live verification](research/2026-10-30-live-verification.md): the saved
  measurements
- [Cost study](research/2026-11-01-cost-study.md): sessions with and without
  duolog
- [Hand-over study](research/2026-11-10-hand-over-study.md): lead, subagent,
  worker, planner and peer compared
