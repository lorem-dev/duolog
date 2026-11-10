# Which evidence informed the initial defaults

Evidence for [the default
config](../../internal/core/config/config.default.yaml),
including the limits of vendor charts and single-blog sources.

## What the models cost

These prices are in USD per 1 million tokens. They come from the vendor pages.

| Model | Input | Cached input | Output | Codex credits: input, cached, output |
|---|---|---|---|---|
| Claude Fable 5.1 | 10.00 | 0.25 | 50.00 | |
| Claude Opus 5.5 | 4.00 | 0.20 | 20.00 | |
| Claude Sonnet 5.5 | 2.00 | 0.10 | 10.00 | |
| Claude Haiku 5.5, prompt up to 100K tokens | 0.10 | 0.01 | 0.50 | |
| Claude Haiku 5.5, prompt over 100K tokens | 0.50 | 0.05 | 2.50 | |
| gpt-6-astra | 10.00 | 1.00 | 50.00 | 250, 25, 1250 |
| gpt-6.1-sol | 2.00 | 0.10 | 10.00 | 50, 2.5, 250 |
| gpt-6.1-terra | 1.00 | 0.05 | 5.00 | 25, 1.25, 125 |
| gpt-6-luna | 0.10 | 0.01 | 0.50 | 2.5, 0.25, 12.5 |

The credit numbers come from a generated summary of the Codex pricing page, not
from its exact text. Check them before you depend on them.

## How the models compare on quality

Opus 5.5 is a little stronger on hard tasks, and it costs much more per task.
So the default rules use Opus for planning and for the final review, and
`gpt-6.1-sol` for implementation.

| Test | Result | How far to trust it |
|---|---|---|
| DeepSWE v1.1, `gpt-6.1-sol` | 75.2% at `high`, lower at `xhigh` and `max` | OpenAI's report, through a blog |
| DeepSWE, Sonnet 5.5 | 71.0% | One source, a different run |
| DeepSWE, `gpt-6-luna` | 66.6%, $0.22 per task, at `max` | One blog |
| Terminal-Bench 4.0 | Sol 56.1% for $1.82, Opus 5.5 59.6% for $13.11 | One blog |
| Terminal-Bench Science 0.1 | Sol 57.0% for $5.47, Opus 5.5 63.3% for $23.21 | OpenAI's report, through a blog |

We found no coding comparison for Haiku 5.5 or for `gpt-6.1-terra`. Their places
in the rules come from their price and their description.

## What a second model adds to a review

The cited controlled study found no significant advantage for a strong
cross-model reviewer over the same model in a new session; errors overlapped
by 41.2%. A light cross-model reviewer did no better. Greptile reported more
serious cross-model findings, but its sample size was unavailable. Both
reviewers improved in new sessions, motivating fresh reviews. The initial
rules kept the final review on Opus and intermediate reviews on at least
terra; [current role
defaults](../configuration.md#which-models-the-default-roles-use)
include later cost-study evidence.

## What other projects already do

Our incomplete search found no project combining two-way messaging, one
role config and permission decisions without a model; that motivated duolog.

| Project | What it is | What we take | What we avoid |
|---|---|---|---|
| `openai/codex-plugin-cc` | Codex commands inside Claude Code, using the Codex app server | A thin wrapper on the agent's own CLI. Background jobs with a session id | A review on the Stop hook. Its README warns that this uses up limits quickly |
| `cc-plugin-codex` (a fork of the `sendbird` project) | The other direction: a new `claude -p` for each call | Jobs that belong to a parent session. No hooks in nested sessions. A summary in place of a very large diff | A review on every Ctrl+C |
| `mcp_agent_mail` | Mail for agents: Git, SQLite and an HTTP MCP server | Short references, reading only unread messages, a history you can audit | A server that must always run. A file bus needs no process |
| An anonymised multi-repository workflow | Rules and a file format for two repositories | The task and answer format, model table, quota fallback, effort limit and measurements | Nothing; duolog automates these rules |
| Articles about hand-overs | General advice | References in place of content. A snapshot of a file that can change | The idea that a reference always costs less. We must measure this |

## What a Codex call costs before it reads the task

A one-word probe with codex-cli 0.165, gpt-6-luna and low effort measured
about 14,800 input tokens before the task; large AGENTS.md files added
about 8,400.

| Where it ran | Input tokens | Of those, cached |
|---|---|---|
| An empty folder | 14,812 | 11,520 |
| A repository root, `AGENTS.md` of 54 KB | 23,081 | 13,696 |
| A repository root, `AGENTS.md` of 71 KB | 23,246 | 11,520 |
| The same root, with `project_doc_max_bytes=0` | 14,728 | 11,520 |

The same session found these problems:

- `codex exec` hangs if stdin is not empty or closed.
- A retired model in `~/.codex/config.toml` stops every run, so always pass
  `-m`.
- `-c` cannot set project trust.
- Under `codex exec`, `spawn_agent` offers no custom agent names.
- Rex has no `mcp` command.
- `rex capture` with no limit prints the whole scrollback.

## Which sources the study used

| Source |
|---|
| [Claude pricing](https://platform.claude.com/docs/en/about-claude/pricing) |
| [OpenAI API pricing](https://developers.openai.com/api/docs/pricing) |
| [Codex pricing](https://learn.chatgpt.com/docs/pricing) |
| [Codex plugins](https://developers.openai.com/codex/plugins/build) |
| [Codex hooks](https://learn.chatgpt.com/docs/hooks) |
| [Codex app server](https://learn.chatgpt.com/docs/app-server) |
| [Claude Code plugins](https://code.claude.com/docs/en/plugins/reference) |
| [Claude Code plugin install](https://code.claude.com/docs/en/plugins/install) |
| [Claude Code hooks](https://code.claude.com/docs/en/hooks) |
| [Claude Code channels](https://code.claude.com/docs/en/channels) |
| [When Does a Second Model Help?](https://www.opentrain.ai/papers/when-does-a-second-model-help-cross-model-review-in-llm-verification--arxiv-2610.01471/) |
| [Greptile on model inversion](https://greptile.com/blog/model-inversion) |
| [Opus 5.5 and GPT-6.1 Sol, CodingFleet](https://codingfleet.com/blog/claude-opus-5-5-vs-gpt-6-1-sol/) |
| [GPT-6.1 Sol benchmarks, Vellum](https://www.vellum.ai/blog/gpt-6-1-sol-benchmarks-explained) |
| [GPT-6.1 Sol for coding, MyClaw](https://myclaw.ai/blog/gpt-6-1-sol-for-coding) |
| [GPT-6.1 Sol and Opus 5.5, DataCamp](https://www.datacamp.com/blog/gpt-6-1-sol-vs-opus-5-5) |
| [GPT-6 Sol and Luna, Kingy AI](https://kingy.ai/blog/gpt-6-sol-luna-specs-benchmarks-pricing-comparison/) |
| [openai/codex-plugin-cc](https://github.com/openai/codex-plugin-cc) |
| [cc-plugin-codex](https://github.com/kelven1217/cc-plugin-codex) |
| [mcp_agent_mail](https://github.com/Dicklesworthstone/mcp_agent_mail) |
| [Multi-agent hand-overs](https://dev.to/gabrielanhaia/multi-agent-handoffs-the-protocol-that-stops-context-loss-3ea1) |
| [Handoff Debt](https://arxiv.org/pdf/2606.02875) |

## Related pages

- [The default rules](../../internal/core/config/config.default.yaml): the
  config this evidence supports
