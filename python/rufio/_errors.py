"""Typed exception subclasses for the rufio Python SDK.

The SDK is a thin subprocess + HTTPS wrapper around the rufio CLI. The
CLI surfaces semantic errors via its exit code + stderr message; we
classify those into a typed hierarchy so SDK consumers can branch on
``except NotInProject`` instead of inspecting the underlying CalledProcess
output.

Hierarchy:

    RufioError                — base for every SDK-surfaced error
        NotInProject          — exit 4 + "not in a rufio project"
        NoIdentity            — exit 4 + "no identity set"
        InvalidIdentity       — exit 2 + "invalid agent id"
        NoSuchThought         — exit 4 + "no such thought"
        NotADecision          — exit 4 + "not a decision"
        ServerError           — remote mode: HTTP 5xx
        Unauthorized          — remote mode: HTTP 401
        PrivacyBlocked        — scope filter rejected
        TLSError              — cert verification failed

Each error preserves the original CLI stderr message as ``.stderr`` and
the CLI exit code as ``.returncode`` when applicable. Code that wants
the raw CalledProcessError can read ``.cause``.
"""

from __future__ import annotations


class RufioError(Exception):
    """Base for every error surfaced by the rufio Python SDK.

    Always preserves the underlying CLI/HTTP failure detail so the
    operator can debug a misbehaving call without re-running the
    subprocess. ``stderr`` is the CLI stderr text (or HTTP response
    body), ``returncode`` is the CLI exit code when applicable.
    """

    def __init__(
        self,
        message: str,
        *,
        stderr: str | None = None,
        returncode: int | None = None,
        cause: BaseException | None = None,
    ) -> None:
        super().__init__(message)
        self.stderr = stderr
        self.returncode = returncode
        self.cause = cause


class NotInProject(RufioError):
    """Raised when local-mode is invoked outside a rufio project root.

    Local subprocess CLI exit code 4 with "not in a rufio project" on
    stderr. The fix is either ``cd`` into a project root or pass
    ``root=`` explicitly to ``Rufio()``.
    """


class NoIdentity(RufioError):
    """Raised when no identity is configured for a write verb.

    Local-mode: CLI exit code 4 with "no identity set" on stderr. The
    fix is ``RUFIO_AGENT_ID=<id>`` or ``Rufio(agent=...)``.
    """


class InvalidIdentity(RufioError):
    """Raised when an agent id fails the [a-z0-9][a-z0-9-]{0,63} regex.

    CLI exit code 2 with "invalid agent id" on stderr.
    """


class NoSuchThought(RufioError):
    """Raised when a thought id (or short suffix) doesn't resolve.

    CLI exit code 4 with "no such thought" on stderr. Also surfaces
    for retract/confirm/refute against an unknown target.
    """


class NotADecision(RufioError):
    """Raised when ``--decision=<id>`` names a non-decision thought.

    CLI exit code 4 with "not a decision" on stderr.
    """


class ServerError(RufioError):
    """Remote-mode HTTP 5xx — the server failed to process the request."""


class Unauthorized(RufioError):
    """Remote-mode HTTP 401 — bearer token rejected.

    The fix is checking ``--token`` / ``RUFIO_TOKEN`` or asking an
    admin to re-mint a token via ``rufio admin token mint``.
    """


class PrivacyBlocked(RufioError):
    """Privacy floor (#147) refused the operation.

    Examples: confirm against a scope:agent thought authored by another
    agent; refute against a private decision. CLI exit code 4.
    """


class TLSError(RufioError):
    """TLS certificate verification failed.

    Remote-mode only. The fix is supplying a correct CA bundle or, for
    localhost dev, passing ``insecure_tls=True``.
    """


def classify_cli_error(returncode: int, stderr: str) -> RufioError:
    """Map a CLI exit code + stderr to the most specific RufioError.

    Surface contract: the CLI's HandleError emits one of a small set of
    stable error strings; we substring-match on those rather than parse
    structured JSON because the substrate doesn't emit JSON errors
    today. New patterns get added here as the CLI surface evolves.
    """
    msg = stderr.strip() if stderr else "rufio CLI failed"
    lower = msg.lower()
    # CLI emits "not inside a Rufio project" (current) — older drafts
    # had "not in a rufio project". Match both.
    if "not inside a rufio project" in lower or "not in a rufio project" in lower:
        return NotInProject(msg, stderr=stderr, returncode=returncode)
    if "no identity set" in lower:
        return NoIdentity(msg, stderr=stderr, returncode=returncode)
    if "invalid agent id" in lower or "invalid identity" in lower:
        return InvalidIdentity(msg, stderr=stderr, returncode=returncode)
    if "no such thought" in lower or "no such record" in lower:
        return NoSuchThought(msg, stderr=stderr, returncode=returncode)
    if "not a decision" in lower:
        return NotADecision(msg, stderr=stderr, returncode=returncode)
    if "private" in lower and ("rejected" in lower or "blocked" in lower):
        return PrivacyBlocked(msg, stderr=stderr, returncode=returncode)
    return RufioError(msg, stderr=stderr, returncode=returncode)
