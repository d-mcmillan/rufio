"""Constructor / identity / transport resolution tests."""

from __future__ import annotations

from pathlib import Path

import pytest

import rufio


def test_local_mode_explicit_args(tmp_path: Path, rufio_binary: Path) -> None:
    """``Rufio(root=...)`` produces a local-mode handle."""
    r = rufio.Rufio(root=str(tmp_path), agent="alice", binary=str(rufio_binary))
    assert r.mode == "local"
    assert r.agent == "alice"
    assert r.root == str(tmp_path)


def test_remote_mode_explicit_args(rufio_binary: Path) -> None:
    """A ``server`` URL flips the handle into remote mode."""
    r = rufio.Rufio(
        server="https://rufio.example.com:18443",
        token="rufio_test_token",
        agent="alice",
        binary=str(rufio_binary),
    )
    assert r.mode == "remote"
    assert r.server == "https://rufio.example.com:18443"


def test_env_precedence(
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
    rufio_binary: Path,
) -> None:
    """RUFIO_AGENT_ID + RUFIO_SERVER + RUFIO_TOKEN are read from env."""
    monkeypatch.setenv("RUFIO_AGENT_ID", "env-alice")
    monkeypatch.setenv("RUFIO_SERVER", "https://env.example.com:18443")
    monkeypatch.setenv("RUFIO_TOKEN", "rufio_env_token")
    r = rufio.Rufio(root=str(tmp_path), binary=str(rufio_binary))
    assert r.agent == "env-alice"
    assert r.server == "https://env.example.com:18443"
    assert r.mode == "remote"


def test_explicit_overrides_env(
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
    rufio_binary: Path,
) -> None:
    """Explicit kwargs win over env vars."""
    monkeypatch.setenv("RUFIO_AGENT_ID", "env-alice")
    r = rufio.Rufio(
        root=str(tmp_path),
        agent="explicit-bob",
        binary=str(rufio_binary),
    )
    assert r.agent == "explicit-bob"


def test_binary_missing_raises(monkeypatch: pytest.MonkeyPatch) -> None:
    """A non-existent binary path surfaces ``RufioError`` immediately."""
    monkeypatch.setenv("RUFIO_BINARY", "/definitely/not/a/real/path/rufio")
    monkeypatch.delenv("PATH", raising=False)
    with pytest.raises(rufio.RufioError):
        rufio.Rufio(root="/tmp")
