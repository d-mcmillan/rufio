"""Tests for the listen() sync generator (local + remote)."""

from __future__ import annotations

import threading
import time
from pathlib import Path

import pytest

import rufio


def test_listen_local_catch_up_yields_seeded_events(
    rufio_project: Path,
    rufio_binary: Path,
) -> None:
    """``listen(catch_up=True)`` flushes existing inbox events first.

    Stream events from listen() carry the on-disk record in ``raw``
    and the canonical path in ``path`` — see internal/lib/stream.Event.
    The thought id appears in both fields; we check the raw contains
    both seeded ids as a stable invariant.
    """
    client = rufio.Rufio(
        root=str(rufio_project),
        agent="alice",
        binary=str(rufio_binary),
    )
    # Seed two thoughts. The author (alice) sees her own outbox via
    # listen's writer-layout dirs.
    t1 = client.think(type="hypothesis", subject="x:1", content="first")
    t2 = client.think(type="hypothesis", subject="x:1", content="second")

    seen: list[dict] = []
    gen = client.listen(catch_up=True, types=["thought"])
    try:
        # Read up to 5s worth or until we see both thoughts.
        deadline = time.time() + 5
        for event in gen:
            seen.append(event)
            raws = " ".join(e.get("raw", "") for e in seen)
            if t1["id"] in raws and t2["id"] in raws:
                break
            if time.time() > deadline:
                break
    finally:
        gen.close()

    raws = " ".join(e.get("raw", "") for e in seen)
    assert t1["id"] in raws, f"first thought id missing; seen={seen!r}"
    assert t2["id"] in raws, f"second thought id missing; seen={seen!r}"


def test_listen_generator_closes_cleanly(
    rufio_project: Path,
    rufio_binary: Path,
) -> None:
    """Closing the generator terminates the underlying subprocess."""
    client = rufio.Rufio(
        root=str(rufio_project),
        agent="alice",
        binary=str(rufio_binary),
    )
    gen = client.listen(catch_up=True)
    # Pull one event (or timeout fast).
    got_one = False

    def consume() -> None:
        nonlocal got_one
        for _ in gen:
            got_one = True
            break

    thread = threading.Thread(target=consume)
    thread.start()
    thread.join(timeout=2)
    # Whether or not we got an event, closing the generator should
    # be idempotent and not hang.
    gen.close()
    thread.join(timeout=2)
    assert not thread.is_alive(), "generator close failed to unblock consumer"


def test_listen_catch_up_and_from_cursor_mutually_exclusive(
    rufio_project: Path,
    rufio_binary: Path,
) -> None:
    """The SDK rejects the impossible combo BEFORE shelling out."""
    client = rufio.Rufio(
        root=str(rufio_project),
        agent="alice",
        binary=str(rufio_binary),
    )
    gen = client.listen(catch_up=True, from_cursor="abc")
    with pytest.raises(rufio.RufioError):
        next(gen)


def test_listen_remote_url_builder() -> None:
    """The internal URL builder normalises /mcp/trailing-slash forms."""
    from rufio._listen import _build_listen_url

    cases = [
        ("https://r.example.com:8443", "https://r.example.com:8443/listen"),
        ("https://r.example.com:8443/", "https://r.example.com:8443/listen"),
        ("https://r.example.com:8443/mcp", "https://r.example.com:8443/listen"),
        ("https://r.example.com:8443/mcp/", "https://r.example.com:8443/listen"),
    ]
    for server, expected in cases:
        assert _build_listen_url(server) == expected, (
            f"_build_listen_url({server!r}) -> got {_build_listen_url(server)!r}, want {expected!r}"
        )
