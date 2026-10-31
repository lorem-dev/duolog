#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///
"""Check saved evidence only; do not start agents or collect measurements."""

import csv
import json
from collections import defaultdict
from pathlib import Path
from statistics import mean
from typing import Any

FIXTURES = Path(__file__).resolve().parents[1] / "fixtures"


def load_json(relative: str) -> Any:
    return json.loads((FIXTURES / relative).read_text())


def main() -> None:
    starts = sorted((FIXTURES / "hooks").glob("*-sessionstart-*.json"))
    assert len(starts) == 4
    for path in starts:
        record = json.loads(path.read_text())
        assert record["hook_event_name"] == "SessionStart"
        assert record["session_id"]
        if path.name == "claude-sessionstart-print.json":
            assert "model" not in record
        else:
            assert record["model"]
    print("E1: all four saved SessionStart samples name a session id")
    print("E2: all name a model except the claude -p sample")

    prompts = sorted((FIXTURES / "hooks").glob("*-userpromptsubmit*.json"))
    assert prompts
    for path in starts + prompts:
        record = json.loads(path.read_text())
        assert "effort" not in record
    print("E3: no saved SessionStart or UserPromptSubmit sample names effort")

    init = load_json("results/claude-stream-init.trimmed.json")
    plugins = [item["name"] for item in init["plugins_sample"]]
    assert plugins == ["plannotator", "superpowers", "figma"]
    servers = {
        item["name"]: item["status"] for item in init["mcp_servers_sample"]
    }
    assert servers == {
        "codegraph": "connected",
        "figma": "needs-auth",
        "unreal-editor": "failed",
    }
    print("M3 plugins listed: " + ", ".join(plugins))
    print("M3 server statuses: " + json.dumps(servers, sort_keys=True))
    print("M3: discovery only; this sample contains no execution result")

    groups: defaultdict[str, list[dict[str, str]]] = defaultdict(list)
    with (FIXTURES / "results/handoff-bench.csv").open(newline="") as source:
        for row in csv.DictReader(source):
            groups[row["form"]].append(row)
    forms = ("empty", "content", "ref", "ref-range",
             "resume-first", "resume-second")
    assert set(groups) == set(forms)
    costs = {}
    print("Form | Mean input | Mean cached | Mean output | Mean USD")
    for form in forms:
        rows = groups[form]
        assert sorted(int(row["rep"]) for row in rows) == [1, 2, 3]
        inputs = mean(int(row["input"]) for row in rows)
        cached = mean(int(row["cached"]) for row in rows)
        outputs = mean(int(row["output"]) for row in rows)
        # USD per million: input 0.10, cached input 0.01, output 0.50.
        cost = ((inputs - cached) * 0.10 + cached * 0.01
                + outputs * 0.50) / 1_000_000
        costs[form] = cost
        print(f"{form} | {int(inputs):,} | {int(cached):,} | "
              f"{int(outputs):,} | {cost:.5f}")
    assert costs["ref"] > costs["content"]
    assert costs["ref-range"] < costs["content"]
    assert costs["resume-second"] > costs["content"]
    print("B1: whole-file reference cost more than content")
    print("B2: directed range task cost less; tasks differ")
    print("B3: second resume turn cost more than new content")
    print("Comparable review quality and thresholds: NOT VERIFIED")


if __name__ == "__main__":
    main()
