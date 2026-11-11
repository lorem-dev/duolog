# What the compact window cost in November 2026

How the size at which Claude compacts the lead session changes the cost,
time and quality of a long piece of work. The result set the default
`workers.compact_ladder` to 300k tokens for Pro, 500k for Max and 700k for
Ultra.

We ran 6 sessions from 2026-11-10 to 2026-11-11, on one macOS machine, with
Claude Code 2.1.319 and duolog 1.6.0. One Opus lead took a chain of 7 linked
tasks on the duolog code and gave each one to duolog workers. Each window ran
twice; two runs broke and are left out. Every number is OBSERVED: one or two
runs, not a statistic.

## What the study found

| Finding | What we saw |
|---|---|
| Each compaction costs money | A 150k window cost 26 to 31% more than a run with no compaction; a 250k window cost 8% more |
| Quality does not depend on the window | Every lead answered both memory questions after compaction, and the hidden tests failed on the same steps |
| Time does not depend on the window | Clean runs took 85 to 89 minutes with any window |
| Claude compacts before the window | With a 150k window the lead compacted at about 117k tokens; with 250k at about 217k |
| A long idle lead compacts by itself | After about 55 minutes with no turn, Claude compacted a 212k context under a 500k window and read it again without the cache |
| 500k and 700k need a longer chain | On 7 steps the lead context never went above 267k tokens |

The cost comes from reading the code again: after a compaction the lead reads
code it saw before, takes more turns and hands out more tasks. We raised the
Pro window from 250k to 300k, so that a long chain compacts once less often.

## What each run cost

| Run | Cost | Time | Lead turns | Tasks handed out | Compactions | Peak context | Code read | Person stepped in | Memory | Hidden tests failed |
|---|---|---|---|---|---|---|---|---|---|---|
| W150-r1 | 14.89 USD | 122 min | 200 | 19 | 6 | 117.6k | 402k chars | 5 | 2/2 | 5 |
| W150-r2 | 15.56 USD | 85 min | 173 | 19 | 6 | 116.9k | 352k chars | 0 | 2/2 | 3 |
| W250-r2 | 12.76 USD | 89 min | 151 | 18 | 1 | 210.6k | 265k chars | 0 | 2/2 | 4 |
| W500-r1 | 11.85 USD | 89 min | 135 | 13 | 0 | 266.8k | 235k chars | 0 | 2/2 | 4 |

The window is the value of `CLAUDE_CODE_AUTO_COMPACT_WINDOW`, checked before
each run. A compaction left 67k to 81k tokens. W150-r1 took longer because the
person stepped in five times on the first step.

## Which runs we left out

| Run | What went wrong |
|---|---|
| W250-r1 | The lead session was lost after step 5 and resumed about 20 hours later with a cold cache; its workers ran on Opus from step 2 |
| W500-r2 | The next step came 55 minutes late; Claude compacted the cold context at 212k, and the run cost 23.02 USD, twice the clean one |

## What this means for duolog

| Setting | Why |
|---|---|
| `compact_ladder` starts at 300k, not lower | A smaller window compacts more often and pays for each compaction |
| `fresh_session_hint` stays at 200k | A fresh session costs less than coming back to a large, cold one |
| A long idle lead is expensive with any window | Claude compacts a cold context by itself |

## What the study does not show

- One or two runs per window. The spread between repeats (14.89 and 15.56
  USD) is smaller than the gap between windows, but it is not a statistic.
- One task chain, on one code base, with an older duolog.
- 500k and 700k were not compared. That needs a chain of about 14 steps.

## Related pages

- [Configuration](../configuration.md#what-workers-inherit): the compact
  window and the ladder
- [Hand-over study](2026-11-10-hand-over-study.md): what each way of handing
  work over cost
- [Budgets](../budgets.md): cost targets and what we measured
