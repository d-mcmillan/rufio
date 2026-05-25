"""Rufio Python SDK — thin sync wrapper around the rufio CLI.

Quickstart::

    from rufio import Rufio

    # Local mode (inside an initialised rufio project)
    r = Rufio(root="/path/to/project", agent="alice")
    r.attend(intent="testing", entities=["test:1"])
    r.think(type="hypothesis", subject="test:1", content="...")

    # Remote mode
    r = Rufio(
        server="https://rufio.example.com:18443",
        token="rufio_...",
    )
    for event in r.listen(catch_up=True, types=["thought"]):
        if event["_type"] == "thought":
            ...

See ``python/README.md`` for the full surface, error model, and the
v1.0.5 PyPI-publishing disclaimer.
"""

from __future__ import annotations

from ._client import Rufio
from ._errors import (
    InvalidIdentity,
    NoIdentity,
    NoSuchThought,
    NotADecision,
    NotInProject,
    PrivacyBlocked,
    RufioError,
    ServerError,
    TLSError,
    Unauthorized,
)
from ._version import __version__

__all__ = [
    "InvalidIdentity",
    "NoIdentity",
    "NoSuchThought",
    "NotADecision",
    "NotInProject",
    "PrivacyBlocked",
    "Rufio",
    "RufioError",
    "ServerError",
    "TLSError",
    "Unauthorized",
    "__version__",
]
