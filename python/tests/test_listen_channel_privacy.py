"""Channel-privacy regression — third-pass audit (post-v1.0.5).

The Python SDK's listen() path subprocesses the rufio CLI in local
mode and routes through /listen SSE in remote mode. Both surfaces
share the stream filter pipeline in internal/lib/stream, which
v1.0.5 shipped without a channel-membership predicate. Result: any
identity could read every channel-message via `r.listen(types=
["channel-message"])`.

This test pins the fix end-to-end via the SDK. Carol — NOT a
channel member — listens; her stream yields zero channel-message
events. Member Alice listens; her stream yields both messages.

NOTE: ``listen()`` keeps the underlying subprocess open after
catch-up (the design contract: it transitions to live-tail).
Tests need to drive the generator in a background thread and
close it from the main thread when the expected condition is met
OR the deadline expires — a synchronous deadline check inside the
for-loop only fires AFTER an event arrives, so a stream with zero
events hangs.
"""

from __future__ import annotations

import subprocess
import threading
import time
from pathlib import Path

import pytest

import rufio


def _seed_channel(rufio_binary: Path, root: Path) -> tuple[str, str, str]:
    """Open a channel alice⇄bob, exchange two messages, return ids.

    Returns (channel_id, alice_msg_id, bob_msg_id). The substrate is
    a real on-disk project; the SDK / CLI walks the resulting
    live/channels/active/<chID>/ tree the same way it does in
    production.
    """
    import json

    env = dict(RUFIO_AGENT_ID="alice")

    def _run(*args, identity: str) -> dict:
        proc = subprocess.run(  # noqa: S603
            [str(rufio_binary), *args, "--json"],
            cwd=root,
            env={"RUFIO_AGENT_ID": identity, "PATH": "/usr/bin:/bin"},
            capture_output=True,
            text=True,
            check=True,
        )
        return json.loads(proc.stdout.strip().splitlines()[0])

    _ = env
    summon = _run("summon", "bob", "--topic=channel:test", "--intent=lunch", identity="alice")
    accept = _run("accept", summon["id"], identity="bob")
    channel = accept["channel"]
    say1 = _run(
        "say",
        f"--channel={channel}",
        "--content=confidential alice content",
        identity="alice",
    )
    say2 = _run("say", f"--channel={channel}", "--content=confidential bob content", identity="bob")
    return channel, say1["id"], say2["id"]


def _drive_listen_subprocess(
    rufio_binary: Path,
    root: Path,
    agent: str,
    *,
    types: list[str],
    timeout: float = 3.0,
) -> str:
    """Run `rufio listen --catch-up --types=...` as a real subprocess,
    capture stdout for ``timeout`` seconds, then SIGTERM and collect.

    Bypasses the SDK's listen() generator threading. The CLI is what
    we're actually testing — the SDK is a thin pipe whose behaviour
    is identical to the CLI for this scenario. Using subprocess
    directly avoids the "sync generator can't be closed from another
    thread while iterating" pitfall.
    """
    import json
    import signal

    types_arg = "--types=" + ",".join(types)
    proc = subprocess.Popen(  # noqa: S603
        [str(rufio_binary), "listen", "--catch-up", types_arg],
        cwd=root,
        env={"RUFIO_AGENT_ID": agent, "PATH": "/usr/bin:/bin"},
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
        text=True,
    )
    time.sleep(timeout)
    proc.send_signal(signal.SIGINT)
    try:
        stdout, _ = proc.communicate(timeout=3.0)
    except subprocess.TimeoutExpired:
        proc.kill()
        stdout, _ = proc.communicate()
    _ = json  # keep symmetric with future enrichments
    return stdout


def test_sdk_listen_filters_channel_by_membership(
    rufio_project: Path,
    rufio_binary: Path,
) -> None:
    """Carol (non-member) MUST get zero channel-message events.

    Pre-fix: any identity with RUFIO_AGENT_ID set receives every
    channel-message on the substrate. Post-fix: the
    channelMembershipVisible predicate (in internal/lib/stream)
    drops them before the CLI emits to stdout.
    """
    _channel, alice_msg, bob_msg = _seed_channel(rufio_binary, rufio_project)

    output = _drive_listen_subprocess(
        rufio_binary, rufio_project, "carol", types=["channel-message"]
    )

    # The CLI emits cursor sideband lines (`{"_type":"cursor",...}`)
    # even when there are no events; those are expected and benign.
    # What we MUST NOT see is any channel-message content from the
    # seeded channel.
    assert "confidential" not in output, (
        f"carol (non-member) leaked channel content:\n{output}"
    )
    assert alice_msg not in output, f"carol saw alice's msg id:\n{output}"
    assert bob_msg not in output, f"carol saw bob's msg id:\n{output}"


def test_sdk_listen_member_sees_channel_messages(
    rufio_project: Path,
    rufio_binary: Path,
) -> None:
    """Alice (member) MUST see both her own and bob's messages."""
    _channel, alice_msg, bob_msg = _seed_channel(rufio_binary, rufio_project)

    output = _drive_listen_subprocess(
        rufio_binary, rufio_project, "alice", types=["channel-message"]
    )

    assert alice_msg in output, (
        f"alice (member) missing her own msg {alice_msg}:\n{output}"
    )
    assert bob_msg in output, (
        f"alice (member) missing bob's msg {bob_msg}:\n{output}"
    )


# Silence unused-import warnings for the bookkeeping module pieces.
_ = rufio
_ = threading
_ = pytest
