---
name: duolog-peers
description: Use only in a project that has .duolog/config.yaml. List the running duolog peers and delegate runs, or switch the person's Rex view to one, when the person asks which run or to see one.
metadata:
  duolog-version: "1.7.0"
---

Stop unless this project has `.duolog/config.yaml`.

# See duolog peers

Use when the person asks which peers or delegate runs run, or to see one.

- Which peers or delegate runs run: run `duolog runs --long`, one table
  with a `peer` or `delegate` column; pass on its lines.
- Show a peer or a delegate run: run `duolog watch --tab <id>` only when
  the person asks; pass on its one line. For a peer it is
  `duolog peer view <id>`, which moves their Rex focus. Tell them to look
  and not to type in that tab.
- Never run `duolog watch` or `duolog peer show` without `--tab`, and never
  work around their refusal: they are for the person's own terminal, to keep
  a peer's screen and a worker's stream out of your context.
  The refusal guards against accidents. It is not a barrier: getting past
  it would take a workaround on purpose.

This skill costs one model turn. Without one: in Claude Code, type
`! duolog runs --long` or `! duolog watch --tab <id>` (not checked yet);
in Codex no turn-free path is confirmed. From Codex, run these commands
outside the sandbox.
