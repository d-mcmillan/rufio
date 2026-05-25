"""Audit M1 — bearer token MUST go via env, never argv.

On Unix, ``ps -ef`` + ``/proc/<pid>/cmdline`` are world-readable to
the same uid. A bearer token on argv is visible to every sibling
process during the subprocess's lifetime. Multi-tenant CI runners /
shared dev hosts can scrape tokens that way.

``/proc/<pid>/environ`` (and macOS's equivalent) are owner-only.
Env is the strictly less-exposed channel. Pre-fix the Python SDK
emitted ``--token <plaintext>`` on argv with a comment defending
the choice ("no env leakage to other children") — but that
reasoning has it backwards: argv is more exposed than env.

These tests inspect the constructed subprocess invocation:
  1. Patch subprocess.run to capture argv + env without spawning.
  2. Assert --token does NOT appear in argv.
  3. Assert RUFIO_TOKEN IS present in the subprocess env.
  4. Cross-check: server flag stays on argv (it's not secret).
"""

from __future__ import annotations

import subprocess
from pathlib import Path
from unittest import mock

import rufio


def _make_remote_client(rufio_binary: Path) -> rufio.Rufio:
    return rufio.Rufio(
        server="https://rufio.example.com:18443",
        token="rufio_super_secret_must_not_leak",
        agent="alice",
        binary=str(rufio_binary),
    )


def test_token_not_on_argv(rufio_binary: Path) -> None:
    """The subprocess argv MUST NOT contain --token or the token plaintext.

    Patches subprocess.run to capture the call; we never let it
    actually spawn (the binary would refuse a fake server URL).
    """
    captured: dict = {}

    def fake_run(argv, **kwargs):
        captured["argv"] = argv
        captured["env"] = kwargs.get("env", {})
        # Return a fake CompletedProcess so the SDK's flow proceeds.
        return subprocess.CompletedProcess(
            args=argv,
            returncode=0,
            stdout="{}",
            stderr="",
        )

    client = _make_remote_client(rufio_binary)
    with mock.patch("subprocess.run", side_effect=fake_run):
        client.attend(intent="probe", entities=["test:1"])

    argv = captured["argv"]
    env = captured["env"]

    # 1. --token must NOT be on argv.
    assert "--token" not in argv, (
        f"--token leaked onto argv (visible via ps): {argv!r}"
    )
    # 2. The token plaintext must NOT appear in any argv element.
    for arg in argv:
        assert "rufio_super_secret_must_not_leak" not in arg, (
            f"token plaintext leaked into argv element {arg!r}"
        )
    # 3. RUFIO_TOKEN must be in the subprocess env.
    assert env.get("RUFIO_TOKEN") == "rufio_super_secret_must_not_leak", (
        f"RUFIO_TOKEN missing or wrong in subprocess env: {env.get('RUFIO_TOKEN')!r}"
    )
    # 4. --server STAYS on argv (it's not secret) and remains the
    #    configured URL.
    assert "--server" in argv
    server_idx = argv.index("--server")
    assert argv[server_idx + 1] == "https://rufio.example.com:18443"


def test_local_mode_no_token_anywhere(rufio_binary: Path, tmp_path: Path) -> None:
    """Local mode (no server set) MUST NOT emit RUFIO_TOKEN at all."""
    captured: dict = {}

    def fake_run(argv, **kwargs):
        captured["argv"] = argv
        captured["env"] = kwargs.get("env", {})
        return subprocess.CompletedProcess(
            args=argv,
            returncode=0,
            stdout="{}",
            stderr="",
        )

    client = rufio.Rufio(
        root=str(tmp_path),
        agent="alice",
        binary=str(rufio_binary),
    )
    with mock.patch("subprocess.run", side_effect=fake_run):
        client.attend(intent="probe", entities=["test:1"])

    assert "--token" not in captured["argv"]
    assert "RUFIO_TOKEN" not in captured["env"], (
        "RUFIO_TOKEN leaked to local-mode subprocess env"
    )


def test_listen_local_no_token_in_argv(rufio_binary: Path, tmp_path: Path) -> None:
    """The local-listen Popen also goes via env, never argv."""
    captured: dict = {}

    def fake_popen(argv, **kwargs):
        captured["argv"] = argv
        captured["env"] = kwargs.get("env", {})
        # Return a mock Popen-like that closes immediately.
        proc = mock.MagicMock()
        proc.stdout = iter([])  # empty stream
        proc.poll.return_value = 0
        proc.wait.return_value = 0
        return proc

    # Init a project so listen has somewhere to walk.
    subprocess.run(
        [str(rufio_binary), "init", "test"],
        cwd=tmp_path,
        check=True,
        capture_output=True,
    )

    client = rufio.Rufio(
        root=str(tmp_path),
        agent="alice",
        binary=str(rufio_binary),
    )
    with mock.patch("subprocess.Popen", side_effect=fake_popen):
        gen = client.listen(catch_up=True)
        list(gen)  # drain (the iter([]) returns immediately)

    assert "--token" not in captured["argv"]
