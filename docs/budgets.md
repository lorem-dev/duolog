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
| An ordinary `gate` call | 0 tokens, under 50 ms | not measured yet |

Code enforces the first target: the bus refuses a longer line. The other
three need a live session, so they stay "not measured yet" until someone runs
them.

No live run gave a number for these four targets, so they stay as they are.
The live run of 2026-10-31 did measure the cost of a handoff (`gpt-6-luna`, low
effort, estimated USD per run):

| What | Measured |
|---|---|
| Whole file inline (2000 lines) | 0.00137 USD, against 0.00238 for the saved whole-file reference |
| 16 lines inline | 0.00049 USD, against 0.00089 for the saved range reference |
| Prompt cache of a small delegate run | 11520 of 14952 input tokens cached |

## How to measure

The live tests print the usage of each run from `.duolog/usage.ndjson`.
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
form with the saved whole-file and range rows.

## Related pages

- [Bus](bus.md): the message limits
- [Live verification](research/2026-10-30-live-verification.md): the saved
  measurements
