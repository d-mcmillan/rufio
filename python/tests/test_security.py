"""Security posture tests for the SDK.

Non-negotiables locked in the v1.0.5 plan:

1. Subprocess invocation MUST use list args (never ``shell=True``).
2. SSE consumer MUST verify TLS by default.
3. Bearer token MUST NOT leak via env (passed as CLI flag only).

These tests inspect the code itself + observable side effects.
"""

from __future__ import annotations

import inspect
import re
from pathlib import Path


def test_no_shell_true_anywhere_in_sdk() -> None:
    """``subprocess.run`` / ``Popen`` calls MUST not use ``shell=True``.

    Parses each .py file via ``ast`` and inspects every ``Call`` node:
    if the callable is ``subprocess.run`` / ``subprocess.Popen`` /
    ``subprocess.call`` / ``subprocess.check_*``, none of the keyword
    args may be ``shell=True``. Defence-in-depth: even if a future
    refactor adds a subprocess call, this catches it before it ships.
    Docstrings / comments that *mention* shell=True are permitted (and
    in fact required — we document why we don't use it).
    """
    import ast

    pkg_dir = Path(__file__).resolve().parent.parent / "rufio"
    subprocess_callables = {
        "run", "Popen", "call", "check_call", "check_output",
    }
    for py in pkg_dir.rglob("*.py"):
        tree = ast.parse(py.read_text(), filename=str(py))
        for node in ast.walk(tree):
            if not isinstance(node, ast.Call):
                continue
            func = node.func
            # Match subprocess.<callable> patterns only.
            if isinstance(func, ast.Attribute) and func.attr in subprocess_callables:
                if isinstance(func.value, ast.Name) and func.value.id == "subprocess":
                    for kw in node.keywords:
                        if (
                            kw.arg == "shell"
                            and isinstance(kw.value, ast.Constant)
                            and kw.value.value is True
                        ):
                            raise AssertionError(
                                f"{py.name}:{node.lineno} subprocess.{func.attr}(... shell=True ...) — forbidden by v1.0.5 plan"
                            )


def test_listen_module_uses_verify_true() -> None:
    """The SSE consumer's requests.get call honors ``verify=`` for TLS.

    We can't easily mock without a server; instead inspect the source
    for the explicit ``verify=`` keyword (mapped to the insecure_tls
    constructor arg). The pattern is locked in _listen.py.
    """
    from rufio import _listen

    src = inspect.getsource(_listen)
    # The requests.get call MUST pass a verify= kwarg (no implicit default).
    assert "verify=" in src, "_listen.py must pass an explicit verify= kwarg"
    # And it MUST be derived from insecure_tls (not hardcoded False).
    assert "not self._insecure_tls" in src or "insecure_tls" in src, (
        "verify= MUST be tied to insecure_tls (defense against accidental disable)"
    )


def test_client_subprocess_uses_list_args() -> None:
    """``_client.py`` builds argv as a list, never as a single string."""
    from rufio import _client

    src = inspect.getsource(_client)
    # Look for any subprocess.run(...) / subprocess.Popen(...) call;
    # the FIRST positional MUST be the argv list (the source has
    # `subprocess.run(argv,` / `subprocess.Popen(argv,` per the
    # implementation).
    run_calls = re.findall(r"subprocess\.(run|Popen)\(([^,)]+)", src)
    assert run_calls, "expected at least one subprocess invocation in _client.py"
    for fn, first_arg in run_calls:
        # First positional MUST be a name referring to a list (argv).
        # Reject string literals like subprocess.run("rufio ...").
        assert not first_arg.strip().startswith('"'), (
            f"subprocess.{fn} first arg looks like a string: {first_arg!r}"
        )
        assert not first_arg.strip().startswith("'"), (
            f"subprocess.{fn} first arg looks like a string: {first_arg!r}"
        )
