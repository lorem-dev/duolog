#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = ["click>=8.1"]
# ///
"""Check or set the duolog version, and keep CHANGES.md in its declared format.

check exits 0 when every place agrees and CHANGES.md keeps its limits, 1 when not,
and 2 on a wrong call. A release tag sets the version of a released binary, so check
--tag compares the repository with the tag before anything is built. notes prints
one version's section, which the release workflow uses as the release body.
"""

import json
import re
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import TypedDict

import click

NUMBER = r"(?:0|[1-9][0-9]*)"
PRERELEASE_PART = rf"(?:{NUMBER}|[0-9]*[A-Za-z-][0-9A-Za-z-]*)"
SEMVER_TEXT = rf"{NUMBER}\.{NUMBER}\.{NUMBER}(?:-{PRERELEASE_PART}(?:\.{PRERELEASE_PART})*)?"
SEMVER = re.compile(SEMVER_TEXT)
VERSION = r"([0-9A-Za-z.+-]+)"

CHANGES = "CHANGES.md"
DEVELOPMENT = "## Development"
VERSION_HEADING = re.compile(rf"## ({SEMVER_TEXT})")
SUBSECTIONS = ("### Added", "### Changed", "### Fixed", "### Removed", "### Security")
MAX_BULLETS_PER_VERSION = 15
MAX_BULLET_CHARS = 160
MAX_BULLET_LINES = 2
MAX_LINE_CHARS = 80


@dataclass(frozen=True)
class Place:
    """One file and a pattern whose single group is the version."""

    path: str
    pattern: str


PLACES: tuple[Place, ...] = (
    Place("cmd/duolog/main.go", rf'var version = "{VERSION}"'),
    Place("plugin/claude/.claude-plugin/plugin.json", rf'"version": "{VERSION}"'),
    Place("plugin/codex/plugin.json", rf'"version": "{VERSION}"'),
    Place("README.md", rf"--version v{VERSION}"),
    Place("README.md", rf"DUOLOG_VERSION = 'v{VERSION}'"),
    Place("docs/getting-started.md", rf"--version v{VERSION}"),
    Place("docs/getting-started.md", rf"DUOLOG_VERSION = 'v{VERSION}'"),
)


class Stated(TypedDict):
    """What one place states."""

    path: str
    pattern: str
    found: list[str]


def read_text(path: Path) -> str:
    """Read bytes as text, so that a Windows checkout keeps its line endings."""
    return path.read_bytes().decode("utf-8")


def found_versions(root: Path, place: Place) -> list[str]:
    """Return each version the place states. A missing file states none."""
    try:
        return re.findall(place.pattern, read_text(root / place.path))
    except FileNotFoundError:
        return []


def survey(root: Path) -> list[Stated]:
    """Return what every place states."""
    return [{"path": place.path, "pattern": place.pattern, "found": found_versions(root, place)} for place in PLACES]


def problems(places: list[Stated], expected: str) -> list[str]:
    """Name each place that is empty or differs from the expected version."""
    wrong: list[str] = []
    for place in places:
        if not place["found"]:
            wrong.append(f"{place['path']}: states no version for {place['pattern']}")
        wrong.extend(f"{place['path']}: {value}, expected {expected}" for value in place["found"] if value != expected)
    return wrong


def without_rules(text: str) -> list[tuple[int, str]]:
    """Return numbered lines outside the rules comment, whose examples are not entries."""
    lines: list[tuple[int, str]] = []
    in_comment = False
    for number, line in enumerate(text.replace("\r\n", "\n").split("\n"), 1):
        if in_comment or line.startswith("<!--"):
            in_comment = "-->" not in line
            continue
        lines.append((number, line))
    return lines


def changes_problems(text: str) -> list[str]:
    """Name every break of the format and limits that CHANGES.md declares."""
    wrong: list[str] = []
    if not text.isascii():
        wrong.append(f"{CHANGES}: holds characters that are not ASCII")
    section = ""
    seen: list[str] = []
    bullets = 0
    bullet: list[str] = []
    bullet_at = 0

    def finish_bullet() -> None:
        nonlocal bullet
        if not bullet:
            return
        joined = " ".join(part.strip() for part in bullet)
        if len(joined) > MAX_BULLET_CHARS:
            wrong.append(f"{CHANGES}:{bullet_at}: bullet has {len(joined)} characters, the limit is {MAX_BULLET_CHARS}")
        if len(bullet) > MAX_BULLET_LINES:
            wrong.append(f"{CHANGES}:{bullet_at}: bullet has {len(bullet)} lines, the limit is {MAX_BULLET_LINES}")
        bullet = []

    def finish_section() -> None:
        finish_bullet()
        if section and section != DEVELOPMENT and bullets == 0:
            wrong.append(f"{CHANGES}: {section} is empty")
        if bullets > MAX_BULLETS_PER_VERSION:
            wrong.append(f"{CHANGES}: {section or 'a section'} has {bullets} bullets, the limit is {MAX_BULLETS_PER_VERSION}")

    for number, line in without_rules(text):
        if len(line) > MAX_LINE_CHARS:
            wrong.append(f"{CHANGES}:{number}: line has {len(line)} characters, the limit is {MAX_LINE_CHARS}")
        if line.startswith("# ") or not line.strip():
            finish_bullet()
        elif line.startswith("## "):
            finish_section()
            section, bullets = line, 0
            if line != DEVELOPMENT and not VERSION_HEADING.fullmatch(line):
                wrong.append(f"{CHANGES}:{number}: heading must be {DEVELOPMENT} or ## 1.2.3")
            if line in seen:
                wrong.append(f"{CHANGES}:{number}: {line} appears twice")
            if not seen and line != DEVELOPMENT:
                wrong.append(f"{CHANGES}:{number}: {DEVELOPMENT} must be the first section")
            seen.append(line)
        elif line.startswith("#"):
            finish_bullet()
            if line not in SUBSECTIONS:
                wrong.append(f"{CHANGES}:{number}: use only {', '.join(SUBSECTIONS)}")
        elif line.startswith("- "):
            finish_bullet()
            bullets += 1
            bullet, bullet_at = [line[2:]], number
        elif line.startswith("  ") and bullet and not line.lstrip().startswith(("- ", "* ", "```")):
            bullet.append(line)
        else:
            finish_bullet()
            wrong.append(f"{CHANGES}:{number}: only one level of bullets belongs in a section")
    finish_section()
    if DEVELOPMENT not in seen:
        wrong.append(f"{CHANGES}: has no {DEVELOPMENT} section")
    return wrong


def section_body(text: str, version: str) -> str:
    """Return the entries under a version heading, or an empty string."""
    lines = text.replace("\r\n", "\n").split("\n")
    for start, line in enumerate(lines):
        match = VERSION_HEADING.fullmatch(line)
        if match and match[1] == version:
            end = next((i for i in range(start + 1, len(lines)) if lines[i].startswith("## ")), len(lines))
            return "\n".join(lines[start + 1 : end]).strip()
    return ""


def release_body(section: str) -> str:
    """Join each wrapped bullet into one line: GitHub shows a line break in a release."""
    return re.sub(r"\n  +", " ", section)


def promoted(text: str, version: str) -> str:
    """Turn the Development entries into a version section under a fresh Development."""
    newline = "\r\n" if "\r\n" in text else "\n"
    heading = re.compile(rf"^{re.escape(DEVELOPMENT)}[ \t]*\r?$", re.MULTILINE)
    return heading.sub(f"{DEVELOPMENT}{newline}{newline}## {version}", text, count=1)


def development_body(text: str) -> str:
    """Return the entries under the Development heading."""
    lines = text.replace("\r\n", "\n").split("\n")
    if DEVELOPMENT not in lines:
        return ""
    start = lines.index(DEVELOPMENT)
    end = next((i for i in range(start + 1, len(lines)) if lines[i].startswith("## ")), len(lines))
    return "\n".join(lines[start + 1 : end]).strip()


@click.group()
@click.option(
    "--root",
    type=click.Path(exists=True, file_okay=False, path_type=Path),
    default=Path(__file__).resolve().parents[2],
    help="The repository root. The default is the repository that holds this script.",
)
@click.pass_context
def main(ctx: click.Context, root: Path) -> None:
    """Check or set the duolog version."""
    ctx.obj = root


def parse_tag(tag: str) -> str:
    """Return the version of a release tag, or stop on a wrong call."""
    if not (tag.startswith("v") and SEMVER.fullmatch(tag[1:])):
        raise click.BadParameter(f"{tag!r} is not a tag such as v1.2.3", param_hint="TAG")
    return tag[1:]


@main.command()
@click.option("--tag", help="A release tag such as v1.2.3. Every place must match it.")
@click.option("--format", "output", type=click.Choice(["text", "json"]), default="text", help="The report format.")
@click.pass_obj
def check(root: Path, tag: str | None, output: str) -> None:
    """Report whether every place states the same version and CHANGES.md keeps its limits."""
    expected_tag = parse_tag(tag) if tag is not None else None
    places = survey(root)
    expected = expected_tag or next((value for place in places for value in place["found"]), "")
    wrong = problems(places, expected)
    try:
        changes = read_text(root / CHANGES)
    except FileNotFoundError:
        wrong.append(f"{CHANGES}: the file is missing")
    else:
        wrong.extend(changes_problems(changes))
        if not section_body(changes, expected):
            wrong.append(f"{CHANGES}: has no entries for version {expected}")
    if output == "json":
        click.echo(json.dumps({"version": expected, "consistent": not wrong, "problems": wrong, "places": places}))
    else:
        for line in wrong:
            click.echo(line)
        click.echo(f"version {expected}: {'consistent' if not wrong else 'NOT consistent'}")
    sys.exit(1 if wrong else 0)


@main.command()
@click.argument("tag")
@click.option("-o", "--output", type=click.Path(dir_okay=False, path_type=Path), help="Write here instead of printing.")
@click.pass_obj
def notes(root: Path, tag: str, output: Path | None) -> None:
    """Print the CHANGES.md section of TAG, such as v1.2.3, for the release body."""
    version = parse_tag(tag)
    body = section_body(read_text(root / CHANGES), version)
    if not body:
        click.echo(f"{CHANGES}: has no entries for version {version}", err=True)
        sys.exit(1)
    body = release_body(body)
    if output is None:
        click.echo(body)
    else:
        output.parent.mkdir(parents=True, exist_ok=True)
        output.write_text(body + "\n", encoding="ascii")


@main.command()
@click.argument("version")
@click.pass_obj
def bump(root: Path, version: str) -> None:
    """Write VERSION, such as 1.2.3, to every place, and move Development entries under it.

    Prints the changed files as JSON. A wrong call or a missing place changes no file.
    """
    if not SEMVER.fullmatch(version):
        raise click.BadParameter(f"{version!r} is not a version such as 1.2.3", param_hint="VERSION")
    missing = [f"{p['path']}: states no version" for p in survey(root) if not p["found"]]
    changes_path = root / CHANGES
    changes = read_text(changes_path) if changes_path.exists() else ""
    if not changes:
        missing.append(f"{CHANGES}: the file is missing")
    written = section_body(changes, version)
    if changes and not written and not development_body(changes):
        missing.append(f"{CHANGES}: the {DEVELOPMENT} section is empty, so this version has no notes")
    if written and development_body(changes):
        missing.append(f"{CHANGES}: {version} has a section, and {DEVELOPMENT} also has entries")
    new_changes = changes if written else promoted(changes, version)
    missing.extend(changes_problems(new_changes) if changes else [])
    if missing:
        click.echo(json.dumps({"changed": [], "problems": missing}))
        sys.exit(1)
    changed: set[str] = set()
    for place in PLACES:
        path = root / place.path
        before = read_text(path)
        after = re.sub(place.pattern, lambda m: m[0].replace(m[1], version), before)
        if after != before:
            path.write_bytes(after.encode("utf-8"))
            changed.add(place.path)
    if new_changes != changes:
        changes_path.write_bytes(new_changes.encode("utf-8"))
        changed.add(CHANGES)
    click.echo(json.dumps({"version": version, "changed": sorted(changed)}))


if __name__ == "__main__":
    main()
