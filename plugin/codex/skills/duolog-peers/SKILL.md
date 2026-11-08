---
name: duolog-peers
description: Use only in a project that has .duolog/config.yaml. List the running duolog peers or switch the person's Rex view to one, when the person asks which peers run or to see one.
metadata:
  duolog-version: "1.5.2"
---

Stop unless this project has `.duolog/config.yaml`.

# See duolog peers

Use when the person asks which peers run, or to see or switch to one.

- Which peers run: run `duolog peer list --long` and pass on its lines.
- Show or switch to a peer: run `duolog peer view <id>` only when the
  person asks; pass on its one line. It moves their Rex focus. Tell them
  to look and not to type in that tab.
- Never run `duolog peer show`, and never work around its refusal: it is
  for the person's own terminal, so that no model reads a peer's screen.
  The refusal guards against accidents. It is not a barrier: getting past
  it would take a workaround on purpose.

This skill costs one model turn. Without one: in Claude Code, type
`! duolog peer list --long` or `! duolog peer view <id>` (not checked yet);
in Codex no turn-free path is confirmed. From Codex, run these commands
outside the sandbox.
