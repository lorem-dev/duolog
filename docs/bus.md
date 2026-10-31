# How sessions talk on the bus

This page explains the bus and the artifacts. The bus is one file that sessions
use to send short messages. An artifact is a file that a message points to.
Together they let two sessions work without pasting large text at each other.

## What a message looks like

Each message is one JSON line in `.duolog/bus.ndjson`:

```json
{"n":1,"t":"2026-10-31T07:16:52Z","from":"claude/lead.abc123","to":"codex/review","k":"note","s":"hello"}
```

| Field | Meaning |
|---|---|
| `n` | The message number |
| `t` | The time |
| `from`, `to` | Addresses, such as `claude/lead.abc123` |
| `k` | The kind, such as `note`, `answer` or `status` |
| `th` | The thread, if any |
| `ref` | An artifact reference, if any |
| `s` | The summary |

A line has at most 512 bytes. The summary has at most 200 bytes. A longer
summary is refused, and the error tells you to use `duolog put`.

The lead's address is `<agent>/lead.<6 characters of the session id>`. The
kinds `allow` and `deny` belong to duolog. `duolog send` refuses them, and a
worker cannot send as a lead.

## How to send and read messages

```bash
duolog send "review is ready" --from codex/review --to claude/lead.abc123 --kind answer --ref a/98ea6e4f216f
duolog inbox --reader claude/lead.abc123
```

`inbox` prints only new messages for that reader and remembers where it
stopped. A lead's hooks do this for you.

## How artifacts keep messages short

Put large content in a file and send its reference.

| Command | Result |
|---|---|
| `duolog put <path>` | Saves an immutable copy. Prints `a/<12 hex>` |
| `duolog ref <path[:start-end]>` | Prints `path@<8 hex>:start-end`. The hash is the Git blob hash |
| `duolog read <ref>` | Prints the content. Warns if a repository file changed |

An `a/` reference never changes, because its name is the hash of its content.
A repository reference can go stale, and `read` warns when it does. duolog
refuses paths outside the project and paths with symlinks.

## When the hooks show a message

A hook with no new message prints nothing and costs no tokens. When there is
mail, the hook adds the lines to the lead's context. At most 10 lines come at
once, followed by `More messages: duolog inbox --reader <address>`. At
session start the lead also gets a short roster line with its roles.

Claude Code can wake an idle session with a hook that has `asyncRewake`. This
was OBSERVED, not VERIFIED. The Codex lead has no wake hook. A Codex lead sees
mail on its next prompt.

## Known limits

- A whole-file reference cost more than the same content in our saved
  measurements. A 16-line range cost less, but the tasks differed. The
  threshold between them is NOT VERIFIED.
- `codex queue` printed `Queued` and exit code 0 for a closed session. Exit
  code 0 does not prove delivery.

## Related pages

- [Budgets](budgets.md): the size and token targets
- [Rex](rex.md): live sessions that use the bus
- [Live verification](research/2026-10-30-live-verification.md): the
  measurements
