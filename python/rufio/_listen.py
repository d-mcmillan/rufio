"""Remote-mode listen support — consumes the server's ``/listen`` SSE.

The local-mode listen path wraps the CLI subprocess directly (see
``_client.Rufio.listen``). Remote mode uses ``sseclient-py`` over a
``requests`` connection so we can verify TLS by default.

Security floor (mirrors internal/lib/client/sse.go on the Go side):

* TLS verification is ON by default. Disable only via ``insecure_tls=True``
  on the ``Rufio`` constructor — the same opt-in surface the CLI exposes.
* Bearer token is attached on the initial request.
* Empty/non-JSON ``data:`` frames are silently skipped.
* Heartbeat (``:`` comments) are not surfaced as events.
"""

from __future__ import annotations

import json
from collections.abc import Iterator
from typing import Any

import requests
import sseclient

from ._errors import RufioError, ServerError, TLSError, Unauthorized
from ._security import _validate_endpoint_scheme

# Cap on the response body bytes read when the SSE connect fails
# with a non-200 status. 4 KB is well below any pathological "drips
# bytes forever" attack while still capturing enough error context
# to be useful in the surfaced exception message. Audit M3.
_ERROR_BODY_READ_CAP = 4096


class ServerSentEventsStream:
    """Thin wrapper around ``sseclient.SSEClient`` for testing.

    Public so an advanced consumer can swap out the HTTP layer. The
    default ``RemoteListenStream`` constructs one internally.
    """

    def __init__(self, response: requests.Response) -> None:
        self._client = sseclient.SSEClient(response)

    def __iter__(self) -> Iterator[Any]:
        return iter(self._client.events())


class RemoteListenStream:
    """Iterator wrapping the remote server's /listen SSE feed.

    Each ``next()`` yields one event dict (the parsed ``data:`` payload).
    Empty data frames, heartbeats, and unknown events are silently
    skipped — matching the CLI ``listen --server`` output contract.
    """

    def __init__(
        self,
        *,
        server: str,
        token: str,
        insecure_tls: bool = False,
        types: list[str] | None = None,
        scope: str | None = None,
        from_cursor: str | None = None,
    ) -> None:
        if not server:
            raise RufioError("listen --server: server URL required")
        if not token:
            raise RufioError("listen --server: bearer token required")
        self._server = server
        self._token = token
        self._insecure_tls = insecure_tls
        self._types = types
        self._scope = scope
        self._from_cursor = from_cursor
        self._response: requests.Response | None = None
        self._client: sseclient.SSEClient | None = None

    def _connect(self) -> sseclient.SSEClient:
        """Open the SSE connection lazily on first iteration."""
        if self._client is not None:
            return self._client
        endpoint = _build_listen_url(self._server)

        # Audit H2 (v1.0.5 follow-up): defense-in-depth scheme gate.
        # Rufio.__init__ already validates the scheme at construction
        # time, but a caller that constructs RemoteListenStream
        # directly (advanced consumers, tests) must not bypass the
        # plaintext-http guard. Cheap, deterministic, runs before any
        # network I/O.
        _validate_endpoint_scheme(endpoint, insecure_tls=self._insecure_tls)

        params: dict[str, str] = {}
        if self._types:
            params["types"] = ",".join(self._types)
        if self._scope:
            params["scope"] = self._scope
        if self._from_cursor:
            params["cursor"] = self._from_cursor

        headers = {
            "Authorization": f"Bearer {self._token}",
            "Accept": "text/event-stream",
            "Cache-Control": "no-cache",
        }

        try:
            # SECURITY: verify=True by default; only False when the
            # operator explicitly opted in. requests handles TLS;
            # sseclient drives the HTTP/1.1 SSE protocol on top.
            #
            # Timeout posture: (connect=10s, read=60s for the
            # header phase). After the initial response arrives the
            # SSE iterator manages its own per-event timing — we
            # don't want an arbitrary read timeout closing a quiet
            # but valid stream. Audit M3: the 60s header-phase
            # read floor bounds how long a hostile server can drip
            # response headers before the SDK gives up.
            self._response = requests.get(  # noqa: S113 — stream=True
                endpoint,
                params=params,
                headers=headers,
                stream=True,
                verify=not self._insecure_tls,
                timeout=(10, 60),
            )
        except requests.exceptions.SSLError as exc:
            raise TLSError(f"TLS verification failed for {endpoint}", cause=exc) from exc
        except requests.exceptions.RequestException as exc:
            raise RufioError(f"failed to connect to {endpoint}: {exc}", cause=exc) from exc

        if self._response.status_code != 200:
            # Audit M3 (v1.0.5 follow-up): bound the error-body read.
            # Pre-fix, self._response.text pulled the ENTIRE body
            # into memory before slicing — a hostile/misconfigured
            # server returning a non-200 + a multi-megabyte body
            # would OOM the SDK consumer. Now we read at most
            # _ERROR_BODY_READ_CAP bytes via iter_content (which
            # honors stream=True semantics).
            body_preview = _read_bounded_error_body(self._response)
            status = self._response.status_code
            # Close the connection NOW so the server can stop
            # writing — important when it's drip-feeding bytes.
            self.close()
            if status == 401:
                raise Unauthorized(
                    f"server rejected bearer token (HTTP 401): {body_preview}",
                    returncode=None,
                    stderr=body_preview,
                )
            if status >= 500:
                raise ServerError(
                    f"server returned HTTP {status}: {body_preview}",
                    returncode=None,
                    stderr=body_preview,
                )
            raise RufioError(
                f"unexpected HTTP {status} from {endpoint}: {body_preview}"
            )

        self._client = sseclient.SSEClient(self._response)
        return self._client

    def __iter__(self) -> Iterator[dict[str, Any]]:
        client = self._connect()
        try:
            for event in client.events():
                if not event.data:
                    continue
                # Heartbeats arrive as :-prefixed comments which
                # sseclient surfaces as events with empty data and no
                # event-type — already filtered above.
                try:
                    payload = json.loads(event.data)
                except json.JSONDecodeError:
                    continue
                if isinstance(payload, dict):
                    yield payload
        finally:
            self.close()

    def close(self) -> None:
        """Release the underlying HTTP connection."""
        if self._response is not None:
            try:
                self._response.close()
            except Exception:  # noqa: BLE001
                pass
            self._response = None
            self._client = None


def _build_listen_url(server: str) -> str:
    """Compose <server-base>/listen — strips trailing slashes and /mcp."""
    base = server.rstrip("/")
    if base.endswith("/mcp"):
        base = base[: -len("/mcp")]
    return base.rstrip("/") + "/listen"


def _read_bounded_error_body(response: requests.Response) -> str:
    """Read at most ``_ERROR_BODY_READ_CAP`` bytes of an error response.

    Audit M3 (v1.0.5 follow-up). ``requests.Response.text`` reads the
    entire body into memory before slicing — fine for small JSON error
    bodies, catastrophic for a multi-megabyte adversarial drip. We
    pull at most ``_ERROR_BODY_READ_CAP`` bytes via ``iter_content``
    (which honors the ``stream=True`` semantics) and decode whatever
    we got. Decode errors degrade to a fixed marker so the surfaced
    exception message stays useful.
    """
    try:
        chunk = next(
            response.iter_content(chunk_size=_ERROR_BODY_READ_CAP, decode_unicode=False),
            b"",
        )
    except (requests.exceptions.RequestException, StopIteration):
        return "<could not read response body>"
    if not chunk:
        return ""
    if isinstance(chunk, bytes):
        try:
            return chunk.decode("utf-8", errors="replace")
        except UnicodeDecodeError:
            return "<non-utf-8 response body>"
    return str(chunk)
