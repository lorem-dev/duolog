#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///
"""Measure the input tokens of three ways to hand a review to Codex.

Run: uv run scripts/bench/handoff_bench.py <empty-temp-dir>
Forms: "content" (file in the prompt), "ref" (task.md plus a path), "ref-range" (task.md plus 16 lines),
"resume" (a second turn in a session that already reviewed the file).
It uses gpt-6-luna at effort low, 3 repeats, and data from `codex exec --json`.
"""
import csv
import json
import pathlib
import subprocess
import sys
from typing import Any, cast

MODEL = "gpt-6-luna"
REPEATS = 3
QUESTION = "Review this file for bugs. Answer in at most 3 lines."
BASE = ["codex", "exec", "--json", "--skip-git-repo-check", "--ignore-user-config",
        "-m", MODEL, "-c", "model_reasoning_effort=low",
        "-c", "project_doc_max_bytes=0", "-s", "read-only"]


def source_file(lines: int = 2000) -> str:
    out = ["package probe", ""]
    n = 0
    while len(out) < lines:
        n += 1
        bug = " + 1" if n == 137 else ""
        out += [f"// Sum{n} adds the values in xs.", f"func Sum{n}(xs []int) int {{",
                "\ttotal := 0", "\tfor _, x := range xs {", "\t\ttotal += x",
                "\t}", f"\treturn total{bug}", "}", ""]
    return "\n".join(out[:lines]) + "\n"


def run(args: list[str], cwd: pathlib.Path) -> tuple[int, str | None, dict[str, Any], int]:
    p = subprocess.run(args, check=False, cwd=cwd, stdin=subprocess.DEVNULL, capture_output=True,
                       text=True, timeout=300)
    thread: str | None = None
    usage: dict[str, Any] | None = None
    commands = 0
    for line in p.stdout.splitlines():
        try:
            e = json.loads(line)
        except ValueError:
            continue
        if e.get("type") == "thread.started":
            thread = e["thread_id"]
        if e.get("type") == "turn.completed":
            usage = e["usage"]
        if e.get("type") == "item.completed" and e["item"].get("type") == "command_execution":
            commands += 1
    return p.returncode, thread, usage or {}, commands


def main(root: str) -> None:
    root_path = pathlib.Path(root)
    text = source_file()
    (root_path / "target.go").write_text(text)
    (root_path / "task.md").write_text(
        "# Goal\n" + QUESTION + "\n\n# Read\ntarget.go\n\n# Write\nNothing.\n\n"
        "# Limits\nDo not change files.\n\n# Check\nName the function and line.\n")
    (root_path / "task-range.md").write_text(
        "# Goal\nReview for bugs. Answer in at most 3 lines.\n\n"
        "# Read\ntarget.go lines 1225-1240 only. Use sed -n. Read nothing else.\n\n"
        "# Write\nNothing.\n\n# Limits\nDo not change files.\n\n"
        "# Check\nName the function and line.\n")
    rows: list[dict[str, Any]] = []
    threads: list[str | None] = []

    def record(
        form: str, rep: int, res: tuple[int, str | None, dict[str, Any], int]
    ) -> None:
        rc, thread, u, commands = res
        threads.append(thread)
        rows.append({"form": form, "rep": rep, "rc": rc, "thread": thread, "commands": commands,
                     "input": u.get("input_tokens"), "cached": u.get("cached_input_tokens"),
                     "output": u.get("output_tokens")})
        print(rows[-1], flush=True)

    for rep in range(1, REPEATS + 1):
        record("empty", rep, run(BASE + ["Reply with the word ok."], root_path))
        record("content", rep, run(BASE + [QUESTION + "\n\n```go\n" + text + "```\n"], root_path))
        record("ref", rep, run(BASE + ["Do the task in task.md."], root_path))
        record("ref-range", rep, run(BASE + ["Do the task in task-range.md."], root_path))
        first = run(BASE + ["Do the task in task.md."], root_path)
        record("resume-first", rep, first)
        again = ["codex", "exec", "resume", "--json", "--skip-git-repo-check",
                 "--ignore-user-config", "-m", MODEL, "-c", "model_reasoning_effort=low",
                 "-c", "project_doc_max_bytes=0", cast(str, first[1]),
                 "The file did not change. Review it again and answer in at most 3 lines."]
        record("resume-second", rep, run(again, root_path))
    with open(root_path / "results.csv", "w", newline="") as f:
        w = csv.DictWriter(f, fieldnames=list(rows[0]))
        w.writeheader()
        w.writerows(rows)


if __name__ == "__main__":
    main(sys.argv[1])
