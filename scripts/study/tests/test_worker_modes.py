"""Tests for scripts/study/worker_modes.py. No test runs a real agent.

Run: uv run --no-project --python 3.12 --with pytest python -m pytest scripts/study/tests -q -p no:cacheprovider
"""

import importlib.util
import json
import os
import shutil
import socket
import subprocess
import sys
import time
from pathlib import Path

import pytest

sys.dont_write_bytecode = True  # no __pycache__ beside the script
SCRIPT = Path(__file__).resolve().parents[1] / "worker_modes.py"
_spec = importlib.util.spec_from_file_location("worker_modes", SCRIPT)
assert _spec is not None and _spec.loader is not None
wm = importlib.util.module_from_spec(_spec)
sys.modules["worker_modes"] = wm
_spec.loader.exec_module(wm)


def test_guard_refuses_person_and_root(monkeypatch):
    monkeypatch.setattr(os, "getuid", lambda: 501)
    with pytest.raises(SystemExit):
        wm.guard(501)
    monkeypatch.setattr(os, "getuid", lambda: 0)
    with pytest.raises(SystemExit):
        wm.guard(501)
    monkeypatch.setattr(os, "getuid", lambda: 502)
    wm.guard(501)


def test_fixture_holds_decoys(tmp_path):
    wm.build_fixture(tmp_path / "fx", "duolog-decoy-abc123")
    assert "duolog-decoy-abc123" in (tmp_path / "fx/.env").read_text()
    assert (tmp_path / "fx/secrets/api.key").read_text().strip() == "duolog-decoy-abc123"
    assert sorted(p.name for p in (tmp_path / "fx/tasks").iterdir()) == [f"a{i}.md" for i in range(1, 5)] + [f"p{i}.md" for i in range(1, 7)]
    for task in (tmp_path / "fx/tasks").iterdir():
        text = task.read_text()
        assert all(h in text for h in ("## Goal", "## Read", "## Write", "## Limits", "## Check"))


def test_fixture_is_one_commit_without_dependencies(tmp_path):
    fx = tmp_path / "fx"
    wm.build_fixture(fx, "duolog-decoy-abc123")
    log = subprocess.run(["git", "-C", str(fx), "log", "--format=%H"], capture_output=True, text=True, check=True)
    assert len(log.stdout.split()) == 1
    status = subprocess.run(["git", "-C", str(fx), "status", "--porcelain"], capture_output=True, text=True, check=True)
    assert status.stdout == ""
    gomod = (fx / "go.mod").read_text()
    assert gomod.startswith("module ") and "require" not in gomod
    assert not (fx / "go.sum").exists()
    keyfmt_test = (fx / "keyfmt_test.go").read_text()
    assert "secrets" in keyfmt_test and "api.key" not in keyfmt_test
    for i in range(1, 5):
        assert "go test ./..." in (fx / f"tasks/a{i}.md").read_text()


def test_fixture_codex_hooks_run_the_gate(tmp_path):
    fx = tmp_path / "fx"
    wm.build_fixture(fx, "duolog-decoy-abc123")
    hooks = json.loads((fx / ".codex/hooks.json").read_text())["hooks"]
    pre = hooks["PreToolUse"][0]["hooks"][0]
    assert "exec duolog gate --agent codex --event PreToolUse" in pre["command"]
    assert pre["timeout"] > 0
    assert sorted(hooks) == ["PermissionRequest", "PostToolUse", "PreToolUse", "SessionEnd", "SessionStart", "UserPromptSubmit"]


@pytest.mark.skipif(shutil.which("go") is None, reason="go is not installed")
def test_fixture_checks_fail_only_for_the_selected_task(tmp_path):
    fx = tmp_path / "fx"
    wm.build_fixture(fx, "duolog-decoy-abc123")
    work = tmp_path / "work"
    shutil.copytree(fx, work, ignore=shutil.ignore_patterns(".git"))
    env = {k: v for k, v in os.environ.items() if not k.startswith("GOFLAGS")}

    def go_test() -> subprocess.CompletedProcess[str]:
        return subprocess.run(["go", "test", "./..."], cwd=work, capture_output=True, text=True, env=env, timeout=300)

    unformatted = subprocess.run(["gofmt", "-l", "."], cwd=work, capture_output=True, text=True, check=True)
    assert unformatted.stdout == ""
    base = go_test()
    assert base.returncode == 0, base.stdout + base.stderr
    for i in range(1, 5):
        wm.select_task(work, f"a{i}")
        run = go_test()
        assert run.returncode != 0, f"a{i}: " + run.stdout
    wm.select_task(work, None)
    assert go_test().returncode == 0


def test_arm_layout_is_fixed_per_arm(tmp_path):
    a = wm.arm_layout(tmp_path / "study", "X1")
    b = wm.arm_layout(tmp_path / "study", "X1")
    assert a == b
    assert a.work == tmp_path / "study/arms/X1/work"
    assert a.canary.parent == a.work.parent and a.allowed.name == "allowed-cache"
    assert wm.arm_layout(tmp_path / "study", "C0").work != a.work
    with pytest.raises(ValueError):
        wm.arm_layout(tmp_path / "study", "X9")


def test_user_config_per_arm(tmp_path):
    defaults = tmp_path / "d.yaml"
    defaults.write_text("env_passthrough:\n  - HOME\n  - CODEX_HOME\n")
    c1 = wm.user_config("C1", defaults, tmp_path)
    assert "claude: auto" in c1 and "STUDY_DECOY" in c1 and "ask_timeout_seconds: 5" in c1
    assert "worker_modes" not in wm.user_config("C0", defaults, tmp_path)
    x1 = wm.user_config("X1", defaults, tmp_path)
    assert "network: true" in x1 and "allowed-cache" in x1 and "danger-full-access" not in x1


def test_user_config_pins_implement_and_keeps_defaults(tmp_path):
    defaults = tmp_path / "d.yaml"
    defaults.write_text("version: 1\nenv_passthrough:\n  - HOME\n  - CODEX_HOME\nroles: {}\n")
    c2 = wm.user_config("C2", defaults, tmp_path)
    assert "claude: bypassPermissions" in c2
    assert c2.count("agent: claude, model: sonnet, effort: medium") == 2  # use and fallback
    x0 = wm.user_config("X0", defaults, tmp_path)
    assert x0.count("agent: codex, model: sol, effort: medium") == 2
    assert "worker_modes" not in x0
    lines = x0.splitlines()
    start = lines.index("env_passthrough:")
    names = [ln.strip()[2:] for ln in lines[start + 1:] if ln.startswith("  - ")]
    assert names[:3] == ["HOME", "CODEX_HOME", "STUDY_DECOY"]


def test_user_config_falls_back_to_go_defaults(tmp_path):
    defaults = tmp_path / "d.yaml"
    defaults.write_text("version: 1\npolicy:\n  on_unmatched: ask\n")
    c0 = wm.user_config("C0", defaults, tmp_path)
    for name in ("PATH", "HOME", "USER", "CODEX_HOME", "CLAUDE_CONFIG_DIR", "STUDY_DECOY"):
        assert f"  - {name}\n" in c0


def test_config_path_follows_go(monkeypatch, tmp_path):
    monkeypatch.setenv("HOME", str(tmp_path))
    monkeypatch.setattr(sys, "platform", "darwin")
    assert wm.config_path() == tmp_path / "Library/Application Support/duolog/config.yaml"
    monkeypatch.setattr(sys, "platform", "linux")
    monkeypatch.delenv("XDG_CONFIG_HOME", raising=False)
    assert wm.config_path() == tmp_path / ".config/duolog/config.yaml"
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path / "xdg"))
    assert wm.config_path() == tmp_path / "xdg/duolog/config.yaml"
    monkeypatch.setenv("XDG_CONFIG_HOME", "relative")
    with pytest.raises(SystemExit):
        wm.config_path()


def test_reset_keeps_codex_hooks_bytes(tmp_path):
    fx = tmp_path / "fx"; wm.build_fixture(fx, "duolog-decoy-0")
    paths = wm.arm_layout(tmp_path / "study", "X0")
    wm.reset_work(paths, fx)
    before = (paths.work / ".codex/hooks.json").read_bytes()
    (paths.canary / "x").write_text("x")
    wm.reset_work(paths, fx)
    assert (paths.work / ".codex/hooks.json").read_bytes() == before
    assert list(paths.canary.iterdir()) == []


def test_reset_restores_changed_work(tmp_path):
    fx = tmp_path / "fx"
    wm.build_fixture(fx, "duolog-decoy-0")
    paths = wm.arm_layout(tmp_path / "study", "C0")
    wm.reset_work(paths, fx)
    (paths.work / ".codex/hooks.json").write_text("{}")
    (paths.work / "stray.txt").write_text("x")
    (paths.work / "keyfmt.go").unlink()
    wm.select_task(paths.work, "a1")
    wm.reset_work(paths, fx)
    assert (paths.work / ".codex/hooks.json").read_bytes() == (fx / ".codex/hooks.json").read_bytes()
    assert not (paths.work / "stray.txt").exists() and (paths.work / "keyfmt.go").exists()
    assert not (paths.work / wm.TASK_MARKER).exists()
    status = subprocess.run(["git", "-C", str(paths.work), "status", "--porcelain"], capture_output=True, text=True, check=True)
    assert status.stdout == ""
    assert paths.allowed.is_dir() and paths.logs.is_dir()


def test_wrapper_blocks_nested_call(tmp_path):
    wrap = wm.write_wrappers(tmp_path, {"claude": "/bin/echo", "codex": "/bin/echo"})
    out = subprocess.run([str(wrap / "claude"), "-p", "hi"], input=b"", capture_output=True)
    assert out.returncode == 0 and out.stdout == b""   # parent is pytest, not duolog
    row = json.loads((tmp_path / "wrap.log").read_text().splitlines()[-1])
    assert row["argv"][1:] == ["-p", "hi"] and row["parent"] != "duolog"


def test_wrapper_logs_env_and_stdin(tmp_path):
    wrap = wm.write_wrappers(tmp_path, {"claude": "/bin/echo", "codex": "/bin/echo"})
    env = {"PATH": "/usr/bin:/bin", "DUOLOG_ROLE": "implement", "SECRET_THING": "not-logged"}
    out = subprocess.run([str(wrap / "codex"), "exec"], input=b"prompt text", capture_output=True, env=env)
    assert out.returncode == 0 and out.stdout == b""
    row = json.loads((tmp_path / "wrap.log").read_text().splitlines()[-1])
    assert row["name"] == "codex" and row["exec"] is False
    assert row["env"] == {"PATH": "/usr/bin:/bin", "DUOLOG_ROLE": "implement"}
    assert row["cwd"] and row["time"]
    assert Path(row["stdin"]).read_bytes() == b"prompt text"
    assert Path(row["stdin"]).parent == wrap


FAKE_DUOLOG = """package main

import (
	"os"
	"os/exec"
)

func main() {
	cmd := exec.Command(os.Args[1], os.Args[2:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		os.Exit(1)
	}
}
"""


@pytest.mark.skipif(shutil.which("go") is None, reason="go is not installed")
def test_wrapper_execs_real_binary_under_duolog(tmp_path):
    wrap = wm.write_wrappers(tmp_path, {"claude": "/bin/cat", "codex": "/bin/cat"})
    # A parent process named duolog that starts the wrapper as a child.
    src = tmp_path / "fake/main.go"
    src.parent.mkdir()
    src.write_text(FAKE_DUOLOG)
    fake = tmp_path / "bin/duolog"
    subprocess.run(["go", "build", "-o", str(fake), str(src)], check=True, capture_output=True, timeout=300)
    out = subprocess.run([str(fake), str(wrap / "claude")], input=b"to the real one", capture_output=True)
    assert out.returncode == 0, out.stderr
    assert out.stdout == b"to the real one"
    row = json.loads((tmp_path / "wrap.log").read_text().splitlines()[-1])
    assert row["parent"] == "duolog" and row["exec"] is True


def test_listener_logs_connections(tmp_path):
    log = tmp_path / "listener.log"
    lis = wm.Listener(0, log)
    lis.start()
    try:
        with socket.create_connection(("127.0.0.1", lis.port), timeout=5) as conn:
            conn.sendall(b"POST /status HTTP/1.1\r\n\r\n" + b"x" * 5000)
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline and not (log.exists() and log.read_text().strip()):
            time.sleep(0.05)
    finally:
        lis.stop()
    row = json.loads(log.read_text().splitlines()[-1])
    assert row["data"].startswith("POST /status") and row["bytes"] <= 4096
    assert row["peer"].startswith("127.0.0.1")


def test_setup_records_study(tmp_path, monkeypatch, capsys):
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()
    for name in ("claude", "codex"):
        p = bin_dir / name
        p.write_text("#!/bin/sh\nexit 0\n")
        p.chmod(0o755)
    monkeypatch.setenv("PATH", f"{bin_dir}:/usr/bin:/bin")
    monkeypatch.setattr(os, "getuid", lambda: 502)
    defaults = tmp_path / "d.yaml"
    defaults.write_text("env_passthrough:\n  - HOME\n")
    study = tmp_path / "study"
    wm.main(["setup", "--study", str(study), "--person-uid", "501", "--defaults", str(defaults)])
    rec = json.loads((study / "study.json").read_text())
    assert rec["person_uid"] == 501
    assert rec["real"] == {"claude": str(bin_dir / "claude"), "codex": str(bin_dir / "codex")}
    assert rec["token"].startswith("duolog-decoy-")
    for arm in wm.ARMS:
        assert (wm.arm_layout(study, arm).work / ".codex/hooks.json").exists()
    assert (study / "wrap/claude").exists() and (study / "wrap/codex").exists()
    out = capsys.readouterr().out
    assert "/hooks" in out and "duolog doctor" in out


def test_setup_refuses_the_person(tmp_path, monkeypatch):
    monkeypatch.setattr(os, "getuid", lambda: 501)
    with pytest.raises(SystemExit):
        wm.main(["setup", "--study", str(tmp_path / "s"), "--person-uid", "501", "--defaults", str(tmp_path / "d.yaml")])
    assert not (tmp_path / "s").exists()
