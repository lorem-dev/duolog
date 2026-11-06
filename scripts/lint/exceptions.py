#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = ["pyyaml>=6"]
# ///
"""Keep the size exceptions in .golangci.yml honest.

The exceptions are exclusion rules between the BEGIN and END size exceptions
markers in .golangci.yml, one line per function (funlen, gocognit, cyclop), per
nested `if` (nestif) and per file (revive file-length-limit). Each line carries
the current size in its text, so a function that grows stops matching and fails
make lint-go, and one that shrinks fails make lint-exceptions until its line is
updated or removed.

check   runs golangci-lint for linux, darwin and windows without the exceptions
        and exits 1 when a listed exception no longer fires (fixed: remove it, or
        run update) or when a finding is not listed.
update  rewrites the block from the same runs. It refuses to add a finding or to
        raise a number, so the list can only shrink. --init writes it from
        scratch, once.
"""

import argparse
import json
import os
import re
import subprocess
import sys
import tempfile
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[2]
CONFIG = ROOT / ".golangci.yml"
BEGIN = "# BEGIN size exceptions"
END = "# END size exceptions"
OSES = ("linux", "darwin", "windows")
SIZE_LINTERS = {"cyclop", "funlen", "gocognit", "nestif", "revive"}
INDENT = "      "

CYCLOP = re.compile(r"for function (\S+) is (\d+), max")
CYCLOP_AVERAGE = re.compile(r"the average complexity for the package (\S+) is ")
FUNLEN = re.compile(r"Function '(.+?)' (is too long|has too many statements) \((\d+) > (\d+)\)")
GOCOGNIT = re.compile(r"cognitive complexity (\d+) of func `(.+?)` is high")
NESTIF = re.compile(r"`(.+)` has complex nested blocks \(complexity: (\d+)\)")
FILELEN = re.compile(r"file length is (\d+) lines")
NUMBER = re.compile(r"(?<![\w.])\d+(?![\w])")


def bare(name: str) -> str:
    """The method or function name without its receiver."""
    return name.rsplit(".", 1)[-1]


def tool_path() -> str:
    env = {k: v for k, v in os.environ.items() if k not in ("GOOS", "GOARCH")}
    out = subprocess.run(
        ["go", "tool", "-modfile=tools/go.mod", "-n", "golangci-lint"],
        cwd=ROOT, env=env, capture_output=True, text=True, check=True,
    )
    return out.stdout.strip()


def split_config() -> tuple[list[str], list[str], list[str]]:
    lines = CONFIG.read_text().splitlines()
    try:
        begin = next(i for i, l in enumerate(lines) if BEGIN in l)
        end = next(i for i, l in enumerate(lines) if END in l)
    except StopIteration:
        sys.exit(f"{CONFIG.name}: the {BEGIN!r} and {END!r} markers are missing")
    return lines[: begin + 1], lines[begin + 1 : end], lines[end:]


def run_lint(config: Path, goos: str, tool: str) -> list[dict]:
    with tempfile.TemporaryDirectory() as tmp:
        out = Path(tmp) / "issues.json"
        env = dict(os.environ, GOOS=goos)
        proc = subprocess.run(
            [tool, "run", "-c", str(config), "--output.json.path", str(out), "--output.text.path", "/dev/null"],
            cwd=ROOT, env=env, capture_output=True, text=True,
        )
        if proc.returncode > 1 or not out.exists():
            sys.exit(f"golangci-lint failed for GOOS={goos}:\n{proc.stdout}{proc.stderr}")
        issues = json.loads(out.read_text()).get("Issues") or []
    return [i for i in issues if i["FromLinter"] in SIZE_LINTERS]


def collect() -> list[dict]:
    """Every size finding without the exceptions, on all three systems."""
    head, _, tail = split_config()
    tool = tool_path()
    with tempfile.TemporaryDirectory() as tmp:
        stripped = Path(tmp) / "golangci.yml"
        stripped.write_text("\n".join(head + tail) + "\n")
        found: dict[tuple, dict] = {}
        for goos in OSES:
            for issue in run_lint(stripped, goos, tool):
                key = (issue["FromLinter"], issue["Pos"]["Filename"], issue["Text"])
                found[key] = {
                    "linter": issue["FromLinter"],
                    "file": issue["Pos"]["Filename"],
                    "text": issue["Text"],
                }
    return list(found.values())


def entries(issues: list[dict]) -> list[dict]:
    """Group the findings: one entry per function, per nested if, per file."""
    groups: dict[tuple, dict] = {}

    def group(kind, file, name, comment_key):
        g = groups.setdefault((kind, file, name), {"file": file, "name": name, "kind": kind, "linters": set(), "alts": set(), "sizes": {}})
        return g

    for i in issues:
        linter, file, text = i["linter"], i["file"], i["text"]
        if linter == "cyclop":
            if m := CYCLOP.search(text):
                name = bare(m[1])
                g = group("func", file, name, None)
                g["alts"].add(rf"function (?:\(\*?\w+\)\.)?{re.escape(name)} is {m[2]},")
                g["sizes"]["cyclomatic"] = max(g["sizes"].get("cyclomatic", 0), int(m[2]))
            elif m := CYCLOP_AVERAGE.search(text):
                directory = str(Path(file).parent)
                g = group("average", directory, m[1], None)
                g["alts"].add(rf"the average complexity for the package {re.escape(m[1])} is ")
                g["sizes"]["package average"] = 0
            else:
                sys.exit(f"unknown cyclop message: {text}")
        elif linter == "funlen":
            m = FUNLEN.search(text) or sys.exit(f"unknown funlen message: {text}")
            name = bare(m[1])
            g = group("func", file, name, None)
            g["alts"].add(rf"Function .{re.escape(name)}. {m[2]} \({m[3]} > ")
            what = "lines" if m[2] == "is too long" else "statements"
            g["sizes"][what] = max(g["sizes"].get(what, 0), int(m[3]))
        elif linter == "gocognit":
            m = GOCOGNIT.search(text) or sys.exit(f"unknown gocognit message: {text}")
            name = bare(m[2])
            g = group("func", file, name, None)
            g["alts"].add(rf"complexity {m[1]} of func .(?:\(\*?\w+\)\.)?{re.escape(name)}. is")
            g["sizes"]["cognitive"] = max(g["sizes"].get("cognitive", 0), int(m[1]))
        elif linter == "nestif":
            m = NESTIF.search(text) or sys.exit(f"unknown nestif message: {text}")
            g = group("nestif", file, m[1], None)
            g["alts"].add(rf".{re.escape(m[1])}. has complex nested blocks \(complexity: {m[2]}\)")
            g["sizes"]["nesting"] = max(g["sizes"].get("nesting", 0), int(m[2]))
        elif linter == "revive":
            m = FILELEN.search(text) or sys.exit(f"unknown revive message: {text}")
            g = group("file", file, "", None)
            g["alts"].add(rf"file length is {m[1]} lines")
            g["sizes"]["code lines"] = int(m[1])
        g["linters"].add(linter)
    out = []
    for g in groups.values():
        g["alts"] = sorted(g["alts"])
        g["linters"] = sorted(g["linters"])
        out.append(g)
    return sorted(out, key=lambda g: (g["file"], g["kind"], g["name"]))


def quote(s: str) -> str:
    return "'" + s.replace("'", "''") + "'"


def render(entry: dict) -> str:
    if entry["kind"] == "average":
        path = "^" + re.escape(entry["file"]) + "/[^/]+$"
    else:
        path = "^" + re.escape(entry["file"]) + "$"
    text = "|".join(entry["alts"])
    sizes = ", ".join(f"{k} {v}" for k, v in entry["sizes"].items() if v)
    label = entry["name"] or entry["file"]
    if entry["kind"] == "nestif":
        label = "if " + entry["name"]
    comment = f"{label}: {sizes}" if sizes else label
    linters = ", ".join(entry["linters"])
    return f"{INDENT}- {{path: {quote(path)}, linters: [{linters}], text: {quote(text)}}} # {comment}"


def parse_block(lines: list[str]) -> list[dict]:
    body = "\n".join(l for l in lines if l.strip())
    return yaml.safe_load(body) or [] if body else []


def alts_of(text: str) -> list[str]:
    return re.split(r"(?<!\\)\|", text)


def fires(rule: dict, linter: str, issues: list[dict]) -> bool:
    path, text = re.compile(rule["path"]), re.compile(rule["text"])
    return any(i["linter"] == linter and path.search(i["file"]) and text.search(i["text"]) for i in issues)


def check(issues: list[dict], rules: list[dict]) -> list[str]:
    problems = []
    for rule in rules:
        for linter in rule["linters"]:
            if not fires(rule, linter, issues):
                problems.append(f"stale: {rule['path']} {linter} {rule['text'][:60]}: no longer fires; run make lint-exceptions-update")
    for i in issues:
        listed = any(
            i["linter"] in r["linters"] and re.search(r["path"], i["file"]) and re.search(r["text"], i["text"])
            for r in rules
        )
        if not listed:
            problems.append(f"not listed: {i['file']} {i['linter']}: {i['text']}")
    return problems


def only_shrinks(new: dict, old_rules: list[dict]) -> str | None:
    """None when the new entry is covered by an old rule with numbers not lower."""
    path = "^" + re.escape(new["file"]) + ("/[^/]+$" if new["kind"] == "average" else "$")
    old_alts: dict[str, list[int]] = {}
    for r in old_rules:
        if r["path"] == path and set(new["linters"]) <= set(r["linters"]) | {"cyclop", "funlen", "gocognit", "nestif", "revive"}:
            for alt in alts_of(r["text"]):
                old_alts[NUMBER.sub("N", alt)] = [int(n) for n in NUMBER.findall(alt)]
    for alt in new["alts"]:
        key = NUMBER.sub("N", alt)
        if key not in old_alts:
            return f"new finding {new['file']} {new['name']}: {alt}"
        if any(n > o for n, o in zip((int(x) for x in NUMBER.findall(alt)), old_alts[key])):
            return f"{new['file']} {new['name']} grew: {alt}"
    return None


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("mode", choices=["check", "update"])
    ap.add_argument("--init", action="store_true", help="with update: write the list without the shrink check")
    args = ap.parse_args()

    head, block, tail = split_config()
    rules = parse_block(block)
    issues = collect()

    if args.mode == "check":
        problems = check(issues, rules)
        for p in problems:
            print(p)
        print(f"{len(rules)} exceptions, {len(issues)} findings, {len(problems)} problems")
        return 1 if problems else 0

    new = entries(issues)
    if not args.init:
        grown = [p for e in new if (p := only_shrinks(e, rules))]
        if grown:
            print("refusing to update: the list may only shrink\n" + "\n".join(grown))
            return 1
    lines = head + [render(e) for e in new] + tail
    CONFIG.write_text("\n".join(lines) + "\n")
    print(f"wrote {len(new)} exceptions for {len(issues)} findings")
    return 0


if __name__ == "__main__":
    sys.exit(main())
