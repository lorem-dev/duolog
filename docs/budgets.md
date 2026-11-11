# What duolog is allowed to cost

These cost targets stay fixed when a measurement misses; report misses to
the author.

## Which cost targets apply

| What | Target | Measured |
|---|---|---|
| A message on the bus | At most 512 bytes | not measured yet |
| A hook with no new message | 0 tokens | not measured yet |
| The `SessionStart` line | At most 80 tokens | not measured yet |
| An ordinary `gate` call | 0 tokens, under 50 ms | 0 tokens; 20 ms median on two macOS arm64 machines; 0.26 to 0.7 s through the Codex hook guard on Windows 11 |

The bus enforces its size limit. The token targets need live measurements.

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

Live tests spend tokens using luna and haiku at low effort. Tests changing
Codex trust also require `DUOLOG_E2E_TRUST=1`; see
[test helpers](../test/e2e/helpers_test.go).

`TestInlineCostsLessThanReference` repeats the study through delegate
against saved whole-file and range reference costs. It prices both sides
without caching because per-call cache changes halved or doubled the
cached price.

For whole-session costs and the evidence for default effort limits, see
[the cost study](research/2026-11-01-cost-study.md).

## Related pages

- [Bus](bus.md): the message limits
- [Live verification](research/2026-10-30-live-verification.md): the saved
  measurements
- [Cost study](research/2026-11-01-cost-study.md): sessions with and without
  duolog
- [Hand-over study](research/2026-11-10-hand-over-study.md): lead, subagent,
  worker, planner and peer compared
- [Compact window study](research/2026-11-11-compact-window-study.md): what
  compacting the lead at 150k, 250k and 500k tokens cost
