"""Sync Rufio client — thin subprocess + HTTPS wrapper around the rufio CLI.

The SDK NEVER reimplements substrate logic. Every method routes through
the ``rufio`` binary:

* Local mode (default) — ``subprocess.run(["rufio", verb, ...], --json)``
* Remote mode — same ``rufio`` binary called with ``--server`` and
  ``--token`` flags, which the CLI already knows how to honor.

Subprocess invocation MUST use list args (never ``shell=True``). Even
though the SDK accepts typed Python args, the defense-in-depth posture
is: refuse to construct shell strings, ever. This rules out a hostile
``intent="$(rm -rf /)"`` argument silently invoking the shell.

The passthrough-fidelity contract is enforced in tests: every SDK
method's JSON output is byte-structurally equal to a direct CLI
invocation with the same args. No parallel data shape lives here —
the CLI's --json renderer is the single source of truth.
"""

from __future__ import annotations

import json
import os
import shutil
import subprocess
from collections.abc import Iterable, Iterator
from typing import Any

from . import _errors
from ._errors import RufioError
from ._listen import RemoteListenStream
from ._security import _validate_endpoint_scheme
from ._version import __version__

# Public re-export. Keeps the constant available without parsing
# `_version.py` from third-party code.
__all__ = ["Rufio", "__version__"]


class Rufio:
    """Sync handle to a Rufio substrate — local or remote.

    The constructor's job is identity + transport resolution; the
    method surface (attend/think/observe/...) is a thin wrapper that
    builds CLI args and parses the ``--json`` output.

    Local mode::

        r = Rufio(root="/path/to/substrate", agent="alice")
        r.attend(intent="testing", entities=["test:1"])

    Remote mode (HTTPS to a rufio serve daemon)::

        r = Rufio(
            server="https://rufio.example.com:18443",
            token="rufio_...",
            agent="alice",  # informational; the server resolves
                            # identity from the token
        )
        r.attend(intent="testing", entities=["test:1"])

    Environment fallback (matches the CLI exactly):

    * ``RUFIO_AGENT_ID`` -> ``agent``
    * ``RUFIO_SERVER`` -> ``server``
    * ``RUFIO_TOKEN`` -> ``token``
    * ``RUFIO_BINARY`` -> path to the rufio binary (falls back to ``$PATH``)
    """

    def __init__(
        self,
        *,
        root: str | None = None,
        agent: str | None = None,
        server: str | None = None,
        token: str | None = None,
        insecure_tls: bool = False,
        binary: str | None = None,
        timeout: float = 30.0,
    ) -> None:
        # Identity precedence mirrors the CLI: explicit arg > env >
        # falls through to whatever the binary resolves. agent is
        # informational in remote mode (server uses the token's
        # resolved identity), but we keep it locally for error
        # messages.
        self.agent = agent or os.environ.get("RUFIO_AGENT_ID") or ""
        self.root = root or ""
        self.server = server or os.environ.get("RUFIO_SERVER") or ""
        self._token = token or os.environ.get("RUFIO_TOKEN") or ""
        self.insecure_tls = insecure_tls
        self.timeout = timeout

        # Audit H2 (v1.0.5 follow-up): fail-fast scheme validation.
        # The CLI's Dial-time validateEndpointScheme refuses
        # plaintext http:// unless insecure_tls AND host is loopback;
        # mirror that here so a misconfigured Rufio(server=...)
        # surfaces the error at construction, not on the first call
        # (and not via a Python-listen path that goes around the CLI).
        if self.server:
            _validate_endpoint_scheme(self.server, insecure_tls=self.insecure_tls)

        # Binary resolution: explicit > env > PATH. We resolve at
        # construct time so a missing binary surfaces immediately
        # rather than on the first call.
        candidate = binary or os.environ.get("RUFIO_BINARY") or "rufio"
        resolved = shutil.which(candidate) if candidate else None
        if resolved is None:
            # Direct-path fallback: if the user passed an absolute
            # path that exists but isn't on $PATH, accept it.
            if candidate and os.path.isfile(candidate) and os.access(candidate, os.X_OK):
                resolved = candidate
            else:
                raise RufioError(
                    f"rufio binary not found: tried {candidate!r}; "
                    "set RUFIO_BINARY=<path> or install rufio on $PATH"
                )
        self.binary = resolved

    @property
    def mode(self) -> str:
        """``"remote"`` if a server URL is configured, else ``"local"``."""
        return "remote" if self.server else "local"

    # ----- internal -----

    def _invoke(
        self,
        verb_argv: list[str],
        *,
        text_output_ok: bool = False,
        check: bool = True,
    ) -> dict[str, Any] | list[dict[str, Any]] | str:
        """Run ``rufio <verb> ...`` and return the parsed JSON result.

        ``verb_argv`` is the verb-and-flags portion (e.g.
        ``["attend", "--intent=foo", "--entities=test:1"]``). We
        prepend the binary path and ``--server`` from config (the
        bearer token is injected via ``RUFIO_TOKEN`` env, never argv —
        see audit M1 below), then call ``subprocess.run`` with list
        args.

        Returns the decoded JSON object. Recall and goals_list emit
        JSONL (one object per line) so we return a list[dict] in those
        cases. Verbs that emit a single object return dict. Text-only
        responses (no JSON shape — e.g. import summary) return str
        when ``text_output_ok=True``.

        SECURITY: subprocess invocation is ALWAYS list-args, never
        shell=True. The CLI's flag parsing handles the rest. The
        bearer token is passed via the ``RUFIO_TOKEN`` env var
        (never argv) — see audit M1 below.
        """
        argv: list[str] = [self.binary]
        if self.server:
            argv.extend(["--server", self.server])
            if self.insecure_tls:
                argv.append("--insecure-tls")
        argv.extend(verb_argv)

        # cwd: in local mode, use the explicit root so FindProjectRoot
        # picks it up. Empty root => fall through to the OS cwd, which
        # matches CLI behaviour (the user might have already cd'd).
        cwd = self.root if (self.root and self.mode == "local") else None

        # env: copy + inject agent identity + token if configured.
        #
        # Audit M1 (v1.0.5 follow-up): the bearer token is injected
        # via env (RUFIO_TOKEN), never argv. On Unix, ps -ef and
        # /proc/<pid>/cmdline are world-readable to the same uid —
        # a token on argv is visible to every sibling process during
        # the subprocess's lifetime. /proc/<pid>/environ is
        # owner-only-readable. Env is the strictly less-exposed
        # channel. The CLI already reads RUFIO_TOKEN via the existing
        # global flag binding so no Go-side change is needed.
        env = os.environ.copy()
        if self.agent:
            env["RUFIO_AGENT_ID"] = self.agent
        if self.server and self._token:
            env["RUFIO_TOKEN"] = self._token

        try:
            proc = subprocess.run(  # noqa: S603 — list args, no shell
                argv,
                cwd=cwd,
                env=env,
                check=False,
                capture_output=True,
                text=True,
                timeout=self.timeout,
            )
        except subprocess.TimeoutExpired as exc:
            raise RufioError(
                f"rufio {verb_argv[0]} timed out after {self.timeout}s",
                cause=exc,
            ) from exc
        except FileNotFoundError as exc:
            raise RufioError(
                f"rufio binary not invokable: {self.binary!r}",
                cause=exc,
            ) from exc

        if check and proc.returncode != 0:
            err = _errors.classify_cli_error(proc.returncode, proc.stderr or "")
            raise err

        stdout = proc.stdout or ""
        if not stdout.strip():
            return {}
        return _parse_json_or_jsonl(stdout, text_output_ok=text_output_ok)

    # ----- cognition methods -----

    def attend(
        self,
        *,
        intent: str,
        entities: Iterable[str],
        topics: Iterable[str] | None = None,
        scope: str = "fleet",
    ) -> dict[str, Any]:
        """Declare what this agent is attending to right now.

        Mirrors ``rufio attend --intent=... --entities=...``. Entities
        and topics are passed as CSV (the CLI splits on commas).
        """
        argv = [
            "attend",
            f"--intent={intent}",
            f"--entities={','.join(entities)}",
            f"--scope={scope}",
            "--json",
        ]
        if topics:
            argv.insert(-1, f"--topics={','.join(topics)}")
        return _expect_dict(self._invoke(argv))

    def think(
        self,
        *,
        type: str,
        subject: str,
        content: str,
        scope: str = "fleet",
        ttl: int | None = None,
        parent: str | None = None,
        topics: Iterable[str] | None = None,
    ) -> dict[str, Any]:
        """Write a new thought (ambient broadcast)."""
        argv = [
            "think",
            f"--type={type}",
            f"--subject={subject}",
            f"--content={content}",
            f"--scope={scope}",
            "--json",
        ]
        if ttl is not None:
            argv.insert(-1, f"--ttl={ttl}")
        if parent:
            argv.insert(-1, f"--parent={parent}")
        if topics:
            argv.insert(-1, f"--topics={','.join(topics)}")
        return _expect_dict(self._invoke(argv))

    def observe(
        self,
        *,
        subject: str,
        predicate: str,
        object: str | None = None,  # noqa: A002 — CLI flag name parity; alias below
        object_: str | None = None,
        scope: str = "fleet",
        confidence: float | None = None,
        topics: Iterable[str] | None = None,
    ) -> dict[str, Any]:
        """Record a durable observation under ``learned/``.

        Accepts ``object=`` (matches the CLI ``--object=`` flag name) OR
        ``object_=`` (the trailing-underscore form that avoids shadowing
        the Python builtin ``object``). Pass exactly one; if both are
        given, ``object_`` wins (it's the more deliberate spelling). The
        on-disk and CLI representation is unchanged either way.
        """
        obj_value = object_ if object_ is not None else object
        if obj_value is None:
            raise ValueError("observe() requires `object` or `object_`")
        argv = [
            "observe",
            f"--subject={subject}",
            f"--predicate={predicate}",
            f"--object={obj_value}",
            f"--scope={scope}",
            "--json",
        ]
        if confidence is not None:
            argv.insert(-1, f"--confidence={confidence}")
        if topics:
            argv.insert(-1, f"--topics={','.join(topics)}")
        return _expect_dict(self._invoke(argv))

    def reason(
        self,
        *,
        content: str,
        subject: str | None = None,
        parent: str | None = None,
        decision: str | None = None,
        topics: Iterable[str] | None = None,
        scope: str = "fleet",
    ) -> dict[str, Any]:
        """Capture a step in this agent's reasoning chain."""
        argv = [
            "reason",
            f"--content={content}",
            f"--scope={scope}",
            "--json",
        ]
        if subject:
            argv.insert(-1, f"--subject={subject}")
        if parent:
            argv.insert(-1, f"--parent={parent}")
        if decision:
            argv.insert(-1, f"--decision={decision}")
        if topics:
            argv.insert(-1, f"--topics={','.join(topics)}")
        return _expect_dict(self._invoke(argv))

    def recall(
        self,
        query: str | None = None,
        *,
        topics: Iterable[str] | None = None,
        types: Iterable[str] | None = None,
        since: str | None = None,
        scope: str | None = None,
        include_expired: bool = False,
    ) -> list[dict[str, Any]]:
        """Read records from the substrate (server-filtered for privacy).

        ``query`` is the optional positional argument matching the CLI's
        ``rufio recall "<query>"`` form. The substrate matches the query
        against subject (primarily) and topics — use this when you wrote
        a thought with ``--subject=foo`` and want to find it without
        having tagged ``--topics=foo`` at write time. (This is the
        subject-vs-topics trap the v1.0.6 primer teaches as the cold-
        agent workaround.) ``topics=`` and ``query`` can be combined.
        """
        argv = ["recall"]
        if query is not None:
            argv.append(query)
        argv.append("--json")
        if topics:
            argv.insert(-1, f"--topics={','.join(topics)}")
        if types:
            argv.insert(-1, f"--types={','.join(types)}")
        if since:
            argv.insert(-1, f"--since={since}")
        if scope:
            argv.insert(-1, f"--scope={scope}")
        if include_expired:
            argv.insert(-1, "--include-expired")
        result = self._invoke(argv)
        # recall's JSON output is a single object with a "records" key in
        # remote mode (MCP shape) and JSONL of records in local mode.
        # _parse_json_or_jsonl collapses a single JSONL line to a dict,
        # so a one-record substrate looks like one record dict — wrap.
        if isinstance(result, dict):
            if "records" in result:
                recs = result["records"]
                return list(recs) if isinstance(recs, list) else []
            # Single-record JSONL was collapsed to a dict; re-wrap.
            return [result]
        if isinstance(result, list):
            return result
        return []

    def retract(
        self,
        thought_id: str,
        *,
        reason: str,
    ) -> dict[str, Any]:
        """Retract one of this agent's own thoughts."""
        argv = [
            "retract",
            thought_id,
            f"--reason={reason}",
            "--json",
        ]
        return _expect_dict(self._invoke(argv))

    def confirm(
        self,
        thought_id: str,
        *,
        evidence: str | None = None,
    ) -> dict[str, Any]:
        """Confirm another agent's thought (or your own)."""
        argv = ["confirm", thought_id, "--json"]
        if evidence:
            argv.insert(-1, f"--evidence={evidence}")
        return _expect_dict(self._invoke(argv))

    def refute(
        self,
        thought_id: str,
        *,
        reason: str,
    ) -> dict[str, Any]:
        """Refute a thought (record a contradiction)."""
        argv = [
            "refute",
            thought_id,
            f"--reason={reason}",
            "--json",
        ]
        return _expect_dict(self._invoke(argv))

    # ----- channel methods -----

    def summon(
        self,
        agent_id: str,
        *,
        topic: str,
        intent: str,
    ) -> dict[str, Any]:
        """Open a private channel by summoning another agent."""
        argv = [
            "summon",
            agent_id,
            f"--topic={topic}",
            f"--intent={intent}",
            "--json",
        ]
        return _expect_dict(self._invoke(argv))

    def accept(self, summon_id: str) -> dict[str, Any]:
        """Accept a pending summon and open the channel."""
        return _expect_dict(self._invoke(["accept", summon_id, "--json"]))

    def decline(self, summon_id: str, *, reason: str) -> dict[str, Any]:
        """Decline a pending summon."""
        return _expect_dict(
            self._invoke(["decline", summon_id, f"--reason={reason}", "--json"])
        )

    def say(self, *, channel: str, content: str) -> dict[str, Any]:
        """Write a message to a channel this agent is a member of."""
        return _expect_dict(
            self._invoke(["say", f"--channel={channel}", f"--content={content}", "--json"])
        )

    def leave(self, channel: str) -> dict[str, Any]:
        """Leave a channel (audit trail preserved; idempotent)."""
        return _expect_dict(self._invoke(["leave", channel, "--json"]))

    def close(self, channel: str) -> dict[str, Any]:
        """Close a channel (opener only)."""
        return _expect_dict(self._invoke(["close", channel, "--json"]))

    # ----- goal methods -----

    def goal(
        self,
        *,
        statement: str,
        by: str | None = None,
        parent: str | None = None,
        scope: str = "fleet",
    ) -> dict[str, Any]:
        """Declare a coordination goal."""
        argv = [
            "goal",
            f"--statement={statement}",
            f"--scope={scope}",
            "--json",
        ]
        if by:
            argv.insert(-1, f"--by={by}")
        if parent:
            argv.insert(-1, f"--parent={parent}")
        return _expect_dict(self._invoke(argv))

    def goals_list(
        self,
        *,
        scope: str | None = None,
        state: str | None = None,
        parent: str | None = None,
    ) -> list[dict[str, Any]]:
        """List goals across the project, optionally filtered."""
        argv = ["goals", "list", "--json"]
        if scope:
            argv.insert(-1, f"--scope={scope}")
        if state:
            argv.insert(-1, f"--state={state}")
        if parent:
            argv.insert(-1, f"--parent={parent}")
        result = self._invoke(argv)
        # goals_list MCP tool returns {"goals": [...]}; local CLI emits JSONL.
        # A single-goal JSONL collapses to a dict — re-wrap it.
        if isinstance(result, dict):
            if "goals" in result:
                return list(result["goals"])
            return [result]
        if isinstance(result, list):
            return result
        return []

    def goal_complete(
        self,
        goal_id: str,
        *,
        outcome: str,
        force: bool = False,
    ) -> dict[str, Any]:
        """Mark an active goal as completed (author-only)."""
        argv = [
            "goal",
            "complete",
            goal_id,
            f"--outcome={outcome}",
            "--json",
        ]
        if force:
            argv.insert(-1, "--force")
        return _expect_dict(self._invoke(argv))

    def goal_abandon(
        self,
        goal_id: str,
        *,
        reason: str,
        force: bool = False,
    ) -> dict[str, Any]:
        """Abandon an active goal (author-only)."""
        argv = [
            "goal",
            "abandon",
            goal_id,
            f"--reason={reason}",
            "--json",
        ]
        if force:
            argv.insert(-1, "--force")
        return _expect_dict(self._invoke(argv))

    # ----- read-bundle -----

    def open(self, subject: str) -> dict[str, Any]:
        """Read the open-bundle for a subject (canonical read shape)."""
        return _expect_dict(self._invoke(["open", subject, "--json"]))

    # ----- listen (generator) -----

    def listen(
        self,
        *,
        catch_up: bool = False,
        from_cursor: str | None = None,
        types: Iterable[str] | None = None,
        scope: str | None = None,
    ) -> Iterator[dict[str, Any]]:
        """Yield events from the agent's inbox as they land.

        Sync generator semantics:

        * Each yielded value is a parsed event dict (matches the local
          CLI ``listen --json`` JSONL shape).
        * Generator runs until the consumer breaks out OR the
          underlying stream closes.
        * ``catch_up=True`` flushes existing inbox contents first.
        * ``from_cursor=<value>`` resumes from a known checkpoint.

        Local mode wraps the CLI's stdout JSONL stream. Remote mode
        consumes the server's ``/listen`` SSE endpoint via the
        ``sseclient-py`` library (TLS-by-default).

        ``KeyboardInterrupt`` propagates through the generator so a
        ``try/except KeyboardInterrupt`` in the calling code can shut
        down gracefully without leaving subprocesses around.
        """
        if catch_up and from_cursor is not None:
            raise RufioError("listen: catch_up and from_cursor are mutually exclusive")

        if self.mode == "remote":
            stream = RemoteListenStream(
                server=self.server,
                token=self._token,
                insecure_tls=self.insecure_tls,
                types=list(types) if types else None,
                scope=scope,
                from_cursor=from_cursor,
            )
            yield from stream
            return

        argv = [self.binary, "listen"]
        if catch_up:
            argv.append("--catch-up")
        if from_cursor is not None:
            argv.append(f"--from={from_cursor}")
        if types:
            argv.append(f"--types={','.join(types)}")
        if scope:
            argv.append(f"--scope={scope}")

        env = os.environ.copy()
        if self.agent:
            env["RUFIO_AGENT_ID"] = self.agent
        cwd = self.root if self.root else None

        # Stream JSONL from stdout. The subprocess is owned by the
        # generator; closing the generator (or KeyboardInterrupt) sends
        # SIGTERM and waits for shutdown.
        #
        # Audit F-NEW-4 (v1.0.5 follow-up): stderr=DEVNULL, not PIPE.
        # Pre-fix, stderr=PIPE with nothing reading it meant a CLI
        # that wrote >64 KB to stderr (warnings, log lines, deprecation
        # notices) blocked on stderr write — at which point the CLI
        # stopped emitting stdout JSONL too, and the Python generator
        # hung forever. The SDK doesn't currently surface CLI stderr
        # to the consumer (errors come through the JSONL `_type:error`
        # frame or via subprocess exit code on terminate), so DEVNULL
        # is the safe + simple choice. Consumers who want stderr for
        # debugging can run the CLI directly or set RUFIO_BINARY to a
        # wrapper that tees stderr.
        proc = subprocess.Popen(  # noqa: S603 — list args, no shell
            argv,
            cwd=cwd,
            env=env,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            text=True,
            bufsize=1,
        )
        try:
            assert proc.stdout is not None
            for line in proc.stdout:
                line = line.strip()
                if not line:
                    continue
                try:
                    yield json.loads(line)
                except json.JSONDecodeError:
                    # Non-JSON noise (shouldn't happen with --json) —
                    # skip rather than crash the consumer.
                    continue
        finally:
            if proc.poll() is None:
                proc.terminate()
                try:
                    proc.wait(timeout=2.0)
                except subprocess.TimeoutExpired:
                    proc.kill()


def _expect_dict(result: Any) -> dict[str, Any]:
    """Type-assert a single-object result; surface a clear error if not."""
    if isinstance(result, dict):
        return result
    if isinstance(result, list):
        if len(result) == 1 and isinstance(result[0], dict):
            return result[0]
        raise RufioError(
            f"expected a single JSON object, got list of {len(result)}"
        )
    raise RufioError(f"expected a JSON object, got {type(result).__name__}")


def _parse_json_or_jsonl(
    stdout: str,
    *,
    text_output_ok: bool,
) -> dict[str, Any] | list[dict[str, Any]] | str:
    """Decode the CLI's stdout: try single-JSON, then JSONL, then text."""
    text = stdout.strip()
    if not text:
        return {}
    # Single JSON object case.
    try:
        decoded = json.loads(text)
        if isinstance(decoded, dict):
            return decoded
        if isinstance(decoded, list):
            return decoded
    except json.JSONDecodeError:
        pass
    # JSONL case — line-by-line decode. If every line parses, return
    # the list. If not, fall through to text-output handling.
    lines = [line for line in text.splitlines() if line.strip()]
    decoded_lines: list[dict[str, Any]] = []
    for line in lines:
        try:
            obj = json.loads(line)
        except json.JSONDecodeError:
            decoded_lines = []
            break
        if isinstance(obj, dict):
            decoded_lines.append(obj)
    if decoded_lines:
        if len(decoded_lines) == 1:
            return decoded_lines[0]
        return decoded_lines
    if text_output_ok:
        return text
    raise RufioError(f"could not decode CLI output as JSON: {text[:200]!r}")


