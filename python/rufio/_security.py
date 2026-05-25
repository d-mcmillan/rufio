"""Security helpers for the rufio Python SDK.

Currently houses ``_validate_endpoint_scheme`` — the HTTPS-by-default
gate that mirrors the Go CLI's ``internal/lib/client/remote.go::
validateEndpointScheme``. The Python SDK's listen path uses
``requests``/``sseclient`` directly (not the subprocess CLI), so
without this guard ``Rufio(server="http://attacker.com/").listen()``
would ship the bearer token in plaintext.

The function is intentionally module-private (single leading
underscore) but exported under that name so tests + other internal
modules can call it without an extra abstraction layer.
"""

from __future__ import annotations

from urllib.parse import urlparse

from ._errors import RufioError

# Hosts that are considered loopback for the purpose of allowing
# plaintext http:// with insecure_tls=True. Mirrors the Go side's
# isLoopbackHost helper at internal/lib/client/remote.go.
_LOOPBACK_HOSTS = {"127.0.0.1", "::1", "localhost"}


def _validate_endpoint_scheme(endpoint: str, *, insecure_tls: bool) -> None:
    """Refuse server URLs that would ship a bearer in plaintext.

    Mirrors the Go CLI's validateEndpointScheme contract:

    * ``https://*`` — always allowed.
    * ``http://*`` — allowed ONLY when ``insecure_tls=True`` AND the
      host is loopback (127.0.0.1, ::1, localhost). Otherwise refused.
    * Anything else (ftp, ws, gopher, ...) — refused outright.

    Raises :class:`RufioError` with a clear, actionable message
    pointing at the fix. The error is intentionally a base
    ``RufioError`` (not a more specific subclass) so a caller using
    ``except RufioError`` catches it uniformly with other
    configuration failures.
    """
    u = urlparse(endpoint)
    scheme = u.scheme.lower()
    if scheme == "https":
        return
    if scheme == "http":
        if not insecure_tls:
            raise RufioError(
                f"refusing to send bearer token over plaintext http:// (got {endpoint!r}); "
                "use https:// or pass insecure_tls=True with a loopback host for localhost dev"
            )
        host = (u.hostname or "").lower()
        if host not in _LOOPBACK_HOSTS:
            raise RufioError(
                f"insecure_tls=True only honoured for loopback hosts (127.0.0.1, ::1, localhost); "
                f"got host {host!r} (endpoint={endpoint!r})"
            )
        return
    raise RufioError(
        f"server scheme must be https (or http+insecure_tls=True for loopback dev); got {scheme!r}"
    )
