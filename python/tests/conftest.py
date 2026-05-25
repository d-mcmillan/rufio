"""pytest fixtures for the rufio Python SDK test suite.

Strategy: the SDK is a subprocess wrapper, so tests need a real rufio
binary to exercise. The conftest:

* Resolves the binary via ``RUFIO_BINARY`` env (set by CI) or falls
  back to building it on demand into a session-scoped tmp dir.
* Provides a ``rufio_project`` fixture that ``rufio init``s a fresh
  substrate in a temp dir and returns the root.
* Provides a ``rufio_binary`` fixture that yields the absolute path so
  tests can pass ``binary=...`` to ``Rufio()``.

Skip-marker: tests that depend on a built binary skip cleanly if the
binary can't be located + can't be built (e.g. running ``pytest`` on a
host without Go installed).
"""

from __future__ import annotations

import os
import shutil
import subprocess
import sys
from collections.abc import Iterator
from pathlib import Path

import pytest


def _repo_root() -> Path:
    """Walk up from this file until we find go.mod (the repo root)."""
    here = Path(__file__).resolve()
    for parent in [here.parent, *here.parents]:
        if (parent / "go.mod").exists():
            return parent
    raise RuntimeError("could not locate repo root (no go.mod found above conftest)")


def _build_binary(target_dir: Path) -> Path | None:
    """Try ``go build``-ing the rufio CLI into target_dir; None on fail."""
    if shutil.which("go") is None:
        return None
    root = _repo_root()
    binary = target_dir / ("rufio.exe" if sys.platform == "win32" else "rufio")
    try:
        subprocess.run(  # noqa: S603 — list args
            ["go", "build", "-o", str(binary), "./cmd/rufio"],
            cwd=root,
            check=True,
            capture_output=True,
        )
    except subprocess.CalledProcessError:
        return None
    return binary


@pytest.fixture(scope="session")
def rufio_binary(tmp_path_factory: pytest.TempPathFactory) -> Path:
    """Resolve the rufio binary path.

    Priority:
        1. ``RUFIO_BINARY`` env var
        2. ``go build`` into a session temp dir

    Skips the test cleanly if no binary can be located.
    """
    env_path = os.environ.get("RUFIO_BINARY")
    if env_path and Path(env_path).is_file():
        return Path(env_path)

    cache_dir = tmp_path_factory.mktemp("rufio-binary")
    binary = _build_binary(cache_dir)
    if binary is not None and binary.is_file():
        return binary

    pytest.skip("rufio binary not available (set RUFIO_BINARY or install Go)")


@pytest.fixture
def rufio_project(rufio_binary: Path, tmp_path: Path) -> Path:
    """Initialise a fresh substrate at tmp_path and return the root.

    ``rufio init`` does NOT create a subdirectory — it scaffolds at the
    current working directory. The optional positional arg only labels
    the project (recorded in rufio.gdl).
    """
    root = tmp_path / "substrate"
    root.mkdir()
    subprocess.run(  # noqa: S603 — list args
        [str(rufio_binary), "init", "test"],
        cwd=root,
        check=True,
        capture_output=True,
    )
    if not (root / "rufio.gdl").exists():
        raise RuntimeError(f"init produced no rufio.gdl at {root}")
    return root


@pytest.fixture
def remote_server(rufio_binary: Path, tmp_path: Path) -> Iterator[dict]:
    """Spin up a real rufio serve daemon on a free port; mint a token.

    Yields a dict with: ``root``, ``url``, ``token`` so tests can
    construct ``Rufio(server=...)``.

    SECURITY: --insecure is fine here — the server binds 127.0.0.1
    only, and the test exits within seconds.
    """
    import socket
    import time

    # Init a substrate. rufio init scaffolds in cwd; create a fresh
    # subdir so the test's tmp_path stays clean.
    root = tmp_path / "remote-substrate"
    root.mkdir()
    subprocess.run(  # noqa: S603
        [str(rufio_binary), "init", "remote"],
        cwd=root,
        check=True,
        capture_output=True,
    )

    # Mint a token via admin. Output is "token_id=...\ntoken=rufio_...\n..."
    mint = subprocess.run(  # noqa: S603
        [str(rufio_binary), "admin", "token", "mint", "--agent=alice"],
        cwd=root,
        check=True,
        capture_output=True,
        text=True,
    )
    token = ""
    for line in mint.stdout.splitlines():
        stripped = line.strip()
        if stripped.startswith("token="):
            token = stripped[len("token="):]
            break
    if not token:
        raise RuntimeError(f"could not parse minted token from: {mint.stdout!r}")

    # Find a free port.
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]

    # Spawn rufio serve.
    proc = subprocess.Popen(  # noqa: S603
        [
            str(rufio_binary),
            "serve",
            "--bind=127.0.0.1",
            f"--port={port}",
            "--insecure",
        ],
        cwd=root,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )

    # Wait for /health to come up (up to 5s).
    import urllib.request

    deadline = time.time() + 5
    url = f"http://127.0.0.1:{port}"
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(f"{url}/health", timeout=0.5) as resp:  # noqa: S310
                if resp.status == 200:
                    break
        except Exception:  # noqa: BLE001
            time.sleep(0.1)
    else:
        proc.terminate()
        raise RuntimeError("rufio serve did not come up within 5s")

    try:
        yield {"root": root, "url": url, "token": token}
    finally:
        proc.terminate()
        try:
            proc.wait(timeout=2)
        except subprocess.TimeoutExpired:
            proc.kill()
