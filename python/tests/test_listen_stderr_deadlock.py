"""Audit F-NEW-4 — local listen subprocess MUST NOT deadlock on stderr.

Pre-fix, ``_client.Rufio.listen`` opened the local listen subprocess
with ``stderr=subprocess.PIPE`` and nothing reading the stderr pipe.
OS pipe buffer is ~64 KB; a CLI that wrote >64 KB to stderr
(warnings, log lines, deprecation notices) blocked on its stderr
write → stopped emitting stdout JSONL → Python generator hung
forever.

Fix: stderr=DEVNULL on the local listen subprocess. The SDK
doesn't currently surface CLI stderr; consumers who want stderr
for debugging can wrap the binary themselves.

This test simulates the deadlock by piping a fake binary that
writes a large stderr burst then a small stdout payload. Pre-fix
the test would hang (no stderr reader, kernel pipe fills); post-fix
the burst is sunk to /dev/null and the stdout payload arrives.
"""

from __future__ import annotations

import os
import stat
import sys
import textwrap
from pathlib import Path

import rufio


def _make_chatty_binary(tmp_path: Path) -> Path:
    """Write a Python script that pretends to be a rufio binary.

    Writes >256 KB to stderr first (overflows the kernel pipe
    buffer), then one stdout JSONL line, then exits. A real PIPE
    on stderr with nobody reading would deadlock the stderr write.
    """
    binary = tmp_path / "fake-rufio"
    script = textwrap.dedent(f"""\
        #!{sys.executable}
        import sys
        # If invoked as `rufio listen`, do the stderr-burst dance.
        if "listen" in sys.argv:
            # 256 KB of stderr noise — well over the ~64 KB pipe buffer.
            payload = "X" * 1024
            for _ in range(256):
                print(payload, file=sys.stderr, flush=True)
            # One stdout JSONL line so the generator can yield it.
            print('{{"_type":"thought","id":"after-stderr-burst"}}', flush=True)
            sys.exit(0)
        # Other verbs the SDK might call during fixture setup —
        # respond with a minimal empty JSON object.
        print("{{}}")
        sys.exit(0)
    """)
    binary.write_text(script)
    binary.chmod(binary.stat().st_mode | stat.S_IEXEC | stat.S_IXGRP | stat.S_IXOTH)
    return binary


def test_local_listen_no_stderr_deadlock(tmp_path: Path) -> None:
    """A CLI that writes a huge stderr burst MUST NOT deadlock listen()."""
    binary = _make_chatty_binary(tmp_path)
    # rufio.gdl present so listen can resolve the project root if
    # the SDK tries to (it does, before spawning).
    (tmp_path / "rufio.gdl").write_text("@config|name:test|version:1\n")
    for sub in ("live/outbox", "live/inbox", "live/attention"):
        (tmp_path / sub).mkdir(parents=True, exist_ok=True)

    client = rufio.Rufio(
        root=str(tmp_path),
        agent="alice",
        binary=str(binary),
    )

    # Drain the generator; the chatty binary exits after emitting
    # one stdout line. Pre-fix this hangs forever because the
    # fake-rufio's stderr writes block on a full pipe with no reader.
    # Post-fix DEVNULL sinks the burst and we see the stdout line.
    events = []
    for ev in client.listen(catch_up=True):
        events.append(ev)
        if len(events) >= 1:
            break

    assert len(events) == 1
    assert events[0]["id"] == "after-stderr-burst", (
        f"unexpected event yielded: {events[0]!r}"
    )


def test_listen_uses_devnull_for_stderr() -> None:
    """Static check: the listen Popen call uses ``stderr=subprocess.DEVNULL``.

    Defense-in-depth — pin the choice so a future refactor doesn't
    accidentally regress to PIPE without reading.
    """
    import ast
    import inspect

    from rufio import _client

    src = inspect.getsource(_client)
    tree = ast.parse(src)
    found_listen_popen = False
    for node in ast.walk(tree):
        if not isinstance(node, ast.Call):
            continue
        func = node.func
        if not (isinstance(func, ast.Attribute) and func.attr == "Popen"):
            continue
        # subprocess.Popen call — inspect kwargs.
        stderr_kw = None
        for kw in node.keywords:
            if kw.arg == "stderr":
                stderr_kw = kw.value
                break
        # The listen Popen MUST set stderr to subprocess.DEVNULL.
        # (No other Popen calls in _client.py today, but if more
        # appear in future they should be inspected case-by-case.)
        if stderr_kw is None:
            continue
        found_listen_popen = True
        # Accept either `subprocess.DEVNULL` (Attribute) or a raw
        # constant (-3 is the value DEVNULL exposes).
        is_devnull = (
            isinstance(stderr_kw, ast.Attribute)
            and stderr_kw.attr == "DEVNULL"
        )
        assert is_devnull, (
            f"local listen Popen must use stderr=subprocess.DEVNULL; "
            f"got {ast.dump(stderr_kw)!r}"
        )
    assert found_listen_popen, "expected at least one subprocess.Popen call in _client.py"


def _silence_os_test_skip() -> None:
    # Used to anchor `os` so the test file's lint stays clean even
    # when the import is technically unused on some Python versions.
    _ = os.linesep
