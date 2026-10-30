# Practices and models on 2026-10-30

This page gives the evidence behind `defaults/config.default.yaml`. Each section
says how far you can trust its data, because many benchmark numbers come from
vendor charts or from one blog.

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

A cheap review by another model saves money, but it does not find more bugs. So
the final review stays on Opus, and an intermediate review uses at least
`gpt-6.1-terra`.

- A controlled study, "When Does a Second Model Help?", tested this. A strong
  reviewer from another model was not significantly better than the same model
  in a new session. The two found partly different errors (41.2% overlap). A
  light reviewer from another model was no better than the same model.
- A Greptile post from July 2026 says models find more serious bugs in code that
  another model wrote. We could not check its sample size.

Both reviewers did better in a new session, so reviews default to `fresh`. A
`fresh` review also cannot see how the author reasoned.

## What other projects already do

We found no project that combines two-way messages, one config for roles, and
permission decisions without a model. That gap is the reason for duolog. Our
search was not complete.

| Project | What it is | What we take | What we avoid |
|---|---|---|---|
| `openai/codex-plugin-cc` | Codex commands inside Claude Code, using the Codex app server | A thin wrapper on the agent's own CLI. Background jobs with a session id | A review on the Stop hook. Its README warns that this uses up limits quickly |
| `cc-plugin-codex` (a fork of the `sendbird` project) | The other direction: a new `claude -p` for each call | Jobs that belong to a parent session. No hooks in nested sessions. A summary in place of a very large diff | A review on every Ctrl+C |
| `mcp_agent_mail` | Mail for agents: Git, SQLite and an HTTP MCP server | Short references, reading only unread messages, a history you can audit | A server that must always run. A file bus needs no process |
| The `platform` repository (D-160, `docs/agents/`) | Rules and a file format for two repositories | The `task.md` and `answer.json` format, the model table, the move to another agent when a quota ends, the effort limit, the measurements | Nothing. duolog automates these rules |
| Articles about hand-overs | General advice | References in place of content. A snapshot of a file that can change | The idea that a reference always costs less. We must measure this |

## What a Codex call costs before it reads the task

An empty Codex call uses about 14,800 input tokens. A large `AGENTS.md` adds
about 8,400 tokens to each call, and one setting removes them. The `platform`
session measured this with codex-cli 0.165, `gpt-6-luna` and effort `low`, on a
task of one word.

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

## Sources

- [Claude pricing](https://platform.claude.com/docs/en/about-claude/pricing)
- [OpenAI API pricing](https://developers.openai.com/api/docs/pricing)
- [Codex pricing](https://learn.chatgpt.com/docs/pricing)
- [Codex plugins](https://developers.openai.com/codex/plugins/build)
- [Codex hooks](https://learn.chatgpt.com/docs/hooks)
- [Codex app server](https://learn.chatgpt.com/docs/app-server)
- [Claude Code plugins](https://code.claude.com/docs/en/plugins/reference)
- [Claude Code plugin install](https://code.claude.com/docs/en/plugins/install)
- [Claude Code hooks](https://code.claude.com/docs/en/hooks)
- [Claude Code channels](https://code.claude.com/docs/en/channels)
- [When Does a Second Model Help?](https://www.opentrain.ai/papers/when-does-a-second-model-help-cross-model-review-in-llm-verification--arxiv-2610.01471/)
- [Greptile on model inversion](https://greptile.com/blog/model-inversion)
- [Opus 5.5 and GPT-6.1 Sol, CodingFleet](https://codingfleet.com/blog/claude-opus-5-5-vs-gpt-6-1-sol/)
- [GPT-6.1 Sol benchmarks, Vellum](https://www.vellum.ai/blog/gpt-6-1-sol-benchmarks-explained)
- [GPT-6.1 Sol for coding, MyClaw](https://myclaw.ai/blog/gpt-6-1-sol-for-coding)
- [GPT-6.1 Sol and Opus 5.5, DataCamp](https://www.datacamp.com/blog/gpt-6-1-sol-vs-opus-5-5)
- [GPT-6 Sol and Luna, Kingy AI](https://kingy.ai/blog/gpt-6-sol-luna-specs-benchmarks-pricing-comparison/)
- [openai/codex-plugin-cc](https://github.com/openai/codex-plugin-cc)
- [cc-plugin-codex](https://github.com/kelven1217/cc-plugin-codex)
- [mcp_agent_mail](https://github.com/Dicklesworthstone/mcp_agent_mail)
- [Multi-agent hand-overs](https://dev.to/gabrielanhaia/multi-agent-handoffs-the-protocol-that-stops-context-loss-3ea1)
- [Handoff Debt](https://arxiv.org/pdf/2606.02875)

## Related pages

- [The default rules](../../defaults/config.default.yaml): the config this
  evidence supports
