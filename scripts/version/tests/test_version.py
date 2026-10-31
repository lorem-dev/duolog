# /// script
# requires-python = ">=3.12"
# dependencies = ["click>=8.1", "pytest>=8"]
# ///
"""Tests for scripts/version/version.py, on a small copy of the versioned files."""

import json
import sys
from pathlib import Path

import pytest
from click.testing import CliRunner

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import version as tool

CHANGES = """# Changes

<!-- ## Not a heading - and a very long line of rules that the limits do not apply to, because it is a comment -->

## Development

### Added

- `duolog new` does a new thing.

## 1.0.0

### Added

- `duolog first` exists.
"""


@pytest.fixture
def root(tmp_path: Path) -> Path:
    files = {
        "cmd/duolog/main.go": 'var version = "1.0.0"\n',
        "plugin/claude/.claude-plugin/plugin.json": '{"version": "1.0.0"}\n',
        "plugin/codex/plugin.json": '{"version": "1.0.0"}\n',
        "README.md": "--version v1.0.0\r\n$env:DUOLOG_VERSION = 'v1.0.0'\r\n",
        "docs/getting-started.md": "--version v1.0.0\n$env:DUOLOG_VERSION = 'v1.0.0'\n",
        "CHANGES.md": CHANGES,
    }
    for name, text in files.items():
        path = tmp_path / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(text.encode())
    return tmp_path


def run(root: Path, *args: str) -> tuple[int, str]:
    result = CliRunner().invoke(tool.main, ["--root", str(root), *args])
    return result.exit_code, result.output


def test_consistent_files_pass(root: Path) -> None:
    code, output = run(root, "check", "--format", "json")
    report = json.loads(output)
    assert code == 0 and report["consistent"] and report["version"] == "1.0.0"


def test_a_different_place_fails_and_is_named(root: Path) -> None:
    (root / "plugin/codex/plugin.json").write_text('{"version": "0.9.0"}\n')
    code, output = run(root, "check")
    assert code == 1 and "plugin/codex/plugin.json: 0.9.0, expected 1.0.0" in output


def test_a_place_with_no_version_fails(root: Path) -> None:
    (root / "docs/getting-started.md").write_text("no pin here\n")
    code, output = run(root, "check")
    assert code == 1 and "docs/getting-started.md: states no version" in output


def test_a_missing_file_fails(root: Path) -> None:
    (root / "plugin/codex/plugin.json").unlink()
    assert run(root, "check")[0] == 1


def test_tag_must_match(root: Path) -> None:
    assert run(root, "check", "--tag", "v1.0.0")[0] == 0
    code, output = run(root, "check", "--tag", "v1.0.1")
    assert code == 1 and "1.0.0, expected 1.0.1" in output


@pytest.mark.parametrize("tag", ["1.0.0", "v1.0", "vX.Y.Z", "v01.0.0"])
def test_a_wrong_tag_is_a_wrong_call(root: Path, tag: str) -> None:
    assert run(root, "check", "--tag", tag)[0] == 2


def test_bump_changes_every_place_and_keeps_line_endings(root: Path) -> None:
    code, output = run(root, "bump", "1.2.3-rc.1")
    assert code == 0 and len(json.loads(output)["changed"]) == 6
    assert run(root, "check", "--tag", "v1.2.3-rc.1")[0] == 0
    assert (root / "README.md").read_bytes() == b"--version v1.2.3-rc.1\r\n$env:DUOLOG_VERSION = 'v1.2.3-rc.1'\r\n"


def test_bump_to_the_same_version_with_no_new_entries_changes_nothing(root: Path) -> None:
    text = (root / "CHANGES.md").read_text().replace("### Added\n\n- `duolog new` does a new thing.\n\n## 1.0.0", "## 1.0.0")
    (root / "CHANGES.md").write_text(text)
    code, output = run(root, "bump", "1.0.0")
    assert code == 0 and json.loads(output)["changed"] == []


def test_bump_moves_development_entries_under_the_version(root: Path) -> None:
    assert run(root, "bump", "1.1.0")[0] == 0
    text = (root / "CHANGES.md").read_text()
    assert "## Development\n\n## 1.1.0\n\n### Added\n\n- `duolog new` does a new thing." in text
    assert run(root, "notes", "v1.1.0")[1] == "### Added\n\n- `duolog new` does a new thing.\n"
    assert run(root, "notes", "v1.0.0")[1] == "### Added\n\n- `duolog first` exists.\n"


def test_bump_refuses_a_version_with_no_entries(root: Path) -> None:
    text = (root / "CHANGES.md").read_text().replace("### Added\n\n- `duolog new` does a new thing.\n\n## 1.0.0", "## 1.0.0")
    (root / "CHANGES.md").write_text(text)
    before = (root / "cmd/duolog/main.go").read_text()
    code, output = run(root, "bump", "1.1.0")
    assert code == 1 and "section is empty" in output
    assert (root / "cmd/duolog/main.go").read_text() == before


def test_notes_for_an_unknown_version_fail(root: Path) -> None:
    assert run(root, "notes", "v9.9.9")[0] == 1
    assert run(root, "notes", "9.9.9")[0] == 2


def test_notes_write_the_release_body_to_a_file(root: Path) -> None:
    out = root / ".release" / "notes.md"
    assert run(root, "notes", "v1.0.0", "-o", str(out))[0] == 0
    assert out.read_text() == "### Added\n\n- `duolog first` exists.\n"


def test_notes_join_a_wrapped_bullet_into_one_line(root: Path) -> None:
    text = (root / "CHANGES.md").read_text().replace("- `duolog first` exists.", "- `duolog first` exists\n  on two lines.\n- Another.")
    (root / "CHANGES.md").write_text(text)
    assert run(root, "notes", "v1.0.0")[1] == "### Added\n\n- `duolog first` exists on two lines.\n- Another.\n"


def development_with(*lines: str) -> str:
    entries = "\n".join(lines)
    return f"# Changes\n\n## Development\n\n### Added\n\n{entries}\n\n## 1.0.0\n\n### Added\n\n- `x` exists.\n"


@pytest.mark.parametrize(
    ("text", "message"),
    [
        (development_with(*[f"- Entry {i}." for i in range(16)]), "16 bullets, the limit is 15"),
        (development_with("- " + "word " * 15 + "\n  " + "word " * 15 + "\n  " + "word " * 15), "bullet has 3 lines"),
        (development_with("- " + "word " * 15 + "\n  " + "word " * 15 + "\n  " + "wo"), "the limit is 2"),
        (development_with("- " + "w" * 90), "line has 92 characters"),
        (development_with("- " + "word " * 15 + "\n  " + "wordy " * 13 + "xxxxxxxxxxxxxxxxxxxxxxxxx"), "the limit is 160"),
        (development_with("- Entry.", "  - A nested entry."), "one level of bullets"),
        (development_with("- Entry.", "", "A paragraph."), "one level of bullets"),
        (development_with("- Entry.", "", "### Fixes", "", "- Other."), "use only"),
        (development_with("- Caf\u00e9."), "not ASCII"),
        ("# Changes\n\n## 1.0.0\n\n- `x` exists.\n", "must be the first section"),
        ("# Changes\n\n## Development\n\n## 1.0.0 -- 2026-10-31\n\n- `x` exists.\n", "heading must be"),
        ("# Changes\n\n## Development\n\n## 1.0.0\n", "is empty"),
    ],
)
def test_check_enforces_the_declared_format(root: Path, text: str, message: str) -> None:
    (root / "CHANGES.md").write_bytes(text.encode())
    code, output = run(root, "check")
    assert code == 1 and message in output


def test_the_rules_comment_states_the_enforced_limits() -> None:
    rules = (Path(__file__).resolve().parents[3] / "CHANGES.md").read_text()
    assert f"{tool.MAX_BULLETS_PER_VERSION} bullets in one version section" in rules
    assert f"{tool.MAX_BULLET_CHARS} characters in one bullet" in rules
    assert f"{tool.MAX_BULLET_LINES} lines in one bullet, with {tool.MAX_LINE_CHARS} characters" in rules


def test_a_version_with_no_entries_fails_check(root: Path) -> None:
    code, output = run(root, "check", "--tag", "v2.0.0")
    assert code == 1 and "no entries for version 2.0.0" in output


@pytest.mark.parametrize("version", ["v1.0.0", "1.0", "latest", "1.0.0 ", "1.0.0-..", "1.0.0-", "1.0.0-rc.01"])
def test_bump_refuses_a_wrong_version_and_changes_nothing(root: Path, version: str) -> None:
    before = (root / "cmd/duolog/main.go").read_text()
    assert run(root, "bump", version)[0] == 2
    assert (root / "cmd/duolog/main.go").read_text() == before


def test_bump_changes_nothing_when_a_place_is_missing(root: Path) -> None:
    (root / "docs/getting-started.md").write_text("no pin here\n")
    before = (root / "cmd/duolog/main.go").read_text()
    assert run(root, "bump", "2.0.0")[0] == 1
    assert (root / "cmd/duolog/main.go").read_text() == before
