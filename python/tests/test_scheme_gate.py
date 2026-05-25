"""Audit H2 + M3 — TLS scheme gate + bounded error-body read.

The Python SDK's listen path used `requests`/`sseclient` directly
(rather than subprocessing the CLI), so the Go CLI's HTTPS-scheme
gate at validateEndpointScheme did NOT apply. Pre-fix,
``Rufio(server="http://attacker.com/", token=...).listen()`` shipped
the bearer in plaintext.

The fix exposes _validate_endpoint_scheme in rufio._security and
calls it both at Rufio construction time (so a misconfigured server=
fails fast) and inside RemoteListenStream._connect (defense in
depth — if a future caller bypasses Rufio.__init__).

The bounded error-body read (M3 follow-up) reads at most ~4 KB of
the response when the SSE connect fails with a non-200 status, so
a hostile/misconfigured server that drips bytes forever doesn't
OOM the SDK consumer.
"""

from __future__ import annotations

import pytest

import rufio
from rufio._security import _validate_endpoint_scheme

# --- F2 — scheme gate ---


def test_validate_endpoint_scheme_accepts_https() -> None:
    """https://* with any host is always allowed."""
    _validate_endpoint_scheme("https://example.com/", insecure_tls=False)
    _validate_endpoint_scheme("https://127.0.0.1:18443/mcp", insecure_tls=False)
    _validate_endpoint_scheme("https://rufio.example.com:8443", insecure_tls=True)


def test_validate_endpoint_scheme_refuses_plain_http_without_insecure() -> None:
    """http://* without insecure_tls=True is always refused."""
    with pytest.raises(rufio.RufioError, match="plaintext|https"):
        _validate_endpoint_scheme("http://attacker.com/", insecure_tls=False)


def test_validate_endpoint_scheme_refuses_plain_http_non_loopback_with_insecure() -> None:
    """insecure_tls=True is honoured ONLY for loopback hosts."""
    with pytest.raises(rufio.RufioError, match="loopback"):
        _validate_endpoint_scheme("http://attacker.com/", insecure_tls=True)
    with pytest.raises(rufio.RufioError, match="loopback"):
        _validate_endpoint_scheme("http://example.com:8443/mcp", insecure_tls=True)


def test_validate_endpoint_scheme_allows_http_loopback_with_insecure() -> None:
    """The localhost dev path: http://127.0.0.1 + insecure_tls=True works."""
    for host in ("127.0.0.1", "localhost", "::1"):
        endpoint = f"http://{host}:18443/mcp" if host != "::1" else "http://[::1]:18443/mcp"
        _validate_endpoint_scheme(endpoint, insecure_tls=True)


def test_validate_endpoint_scheme_refuses_unknown_scheme() -> None:
    """Anything not http/https is refused outright."""
    with pytest.raises(rufio.RufioError, match="https"):
        _validate_endpoint_scheme("ftp://example.com/mcp", insecure_tls=True)
    with pytest.raises(rufio.RufioError, match="https"):
        _validate_endpoint_scheme("ws://example.com/mcp", insecure_tls=False)


def test_rufio_constructor_refuses_plaintext_non_loopback(rufio_binary) -> None:
    """``Rufio(server="http://attacker.com/", token=...)`` raises at construct time.

    The fix-fast posture means the SDK consumer sees the configuration
    error immediately, not on first HTTP call. Mirrors the Go CLI's
    Dial-time validation.
    """
    with pytest.raises(rufio.RufioError, match="plaintext|https"):
        rufio.Rufio(
            server="http://attacker.example.com:8080",
            token="rufio_test_token",
            binary=str(rufio_binary),
        )


def test_rufio_constructor_accepts_https_anywhere(rufio_binary) -> None:
    """``Rufio(server="https://anything/", token=...)`` is accepted."""
    r = rufio.Rufio(
        server="https://rufio.example.com:18443",
        token="rufio_test_token",
        binary=str(rufio_binary),
    )
    assert r.mode == "remote"


def test_rufio_constructor_refuses_insecure_non_loopback(rufio_binary) -> None:
    """``insecure_tls=True`` + non-loopback host is refused."""
    with pytest.raises(rufio.RufioError, match="loopback"):
        rufio.Rufio(
            server="http://example.com:8080",
            token="rufio_test_token",
            insecure_tls=True,
            binary=str(rufio_binary),
        )


def test_rufio_constructor_accepts_insecure_loopback(rufio_binary) -> None:
    """``insecure_tls=True`` + 127.0.0.1 is the localhost dev path."""
    r = rufio.Rufio(
        server="http://127.0.0.1:18443",
        token="rufio_test_token",
        insecure_tls=True,
        binary=str(rufio_binary),
    )
    assert r.mode == "remote"


# --- F5 — bounded error-body read ---


def test_remote_listen_bounded_error_body() -> None:
    """A non-200 SSE connect MUST cap the error-body read.

    Pre-fix, ``self._response.text[:200]`` read the entire response
    body into memory before slicing. A hostile server returning 401
    + a multi-megabyte body OOM'd the SDK. Post-fix the SDK reads
    a small chunk via iter_content and slices THAT.
    """
    import threading
    from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

    chunk_count = {"reads": 0}

    class Handler(BaseHTTPRequestHandler):
        def do_GET(self):  # noqa: N802
            self.send_response(401)
            self.send_header("Content-Type", "text/plain")
            # Send Content-Length so requests doesn't go indefinite,
            # but make it large enough that .text would have loaded
            # it all if the SDK hadn't switched to iter_content.
            payload_size = 5 * 1024 * 1024  # 5 MB
            self.send_header("Content-Length", str(payload_size))
            self.end_headers()
            # Stream the body in chunks so we can observe how many
            # the SDK actually pulled.
            chunk = b"X" * 4096
            written = 0
            try:
                while written < payload_size:
                    self.wfile.write(chunk)
                    written += len(chunk)
                    chunk_count["reads"] += 1
            except (BrokenPipeError, ConnectionResetError):
                # SDK closed the connection — expected once it gave
                # up on the over-cap body.
                pass

        def log_message(self, *args):  # noqa: D401
            pass

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    port = server.server_address[1]
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()

    try:
        stream = rufio._listen.RemoteListenStream(
            server=f"http://127.0.0.1:{port}",
            token="rufio_test",
            insecure_tls=True,
        )
        with pytest.raises(rufio.Unauthorized):
            stream._connect()
    finally:
        server.shutdown()
        server.server_close()

    # The SDK should NOT have pulled all 5 MB. The pre-fix behaviour
    # consumed every byte (1280 chunks ≈ 5 MB) before slicing — so even
    # a generous bound proves the M3 fix. Bound is intentionally loose
    # (~512 chunks ≈ 2 MB) to absorb OS-level kernel buffering variance
    # across runners: an observed run hit 268 chunks under the previous
    # 256 bound, flaking on the duplicate-workflow trigger while the
    # primary run passed. Still well under the 1280-chunk no-bound
    # baseline.
    assert chunk_count["reads"] < 512, (
        f"SDK read too much error body: {chunk_count['reads']} chunks (~{chunk_count['reads'] * 4096} bytes)"
    )
