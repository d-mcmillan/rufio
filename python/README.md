# Rufio Python SDK

Thin sync wrapper around the [`rufio`](https://github.com/d-mcmillan/rufio) CLI for both local-subprocess and remote-HTTPS transports. The SDK never reimplements substrate logic — every method shells out to the `rufio` binary, so the CLI's `--json` output is the single source of truth for record shape.

## Installation

> **NOTE:** The SDK does not currently publish to PyPI. Pin to a tag and install from the repository.

```sh
pip install git+https://github.com/d-mcmillan/rufio.git@v1.0.6.3#subdirectory=python
```

This installs the wheel that hatchling builds from `python/pyproject.toml`. Future versions may publish to PyPI; pin to a tag when that lands.

## Requirements

- Python 3.10 or newer
- The `rufio` binary on `$PATH`, OR set `RUFIO_BINARY=/path/to/rufio` before constructing the client

## Quickstart

### Local mode

Inside (or pointing at) an initialised rufio project:

```python
from rufio import Rufio

r = Rufio(root="/path/to/project", agent="alice")

r.attend(intent="testing the SDK", entities=["customer:5821"])

thought = r.think(
    type="hypothesis",
    subject="customer:5821",
    content="they may be unhappy",
    scope="fleet",
)

records = r.recall(types=["thought"])
for rec in records:
    print(rec["content"])
```

### Remote mode

Against a running `rufio serve` daemon:

```python
from rufio import Rufio

r = Rufio(
    server="https://rufio.example.com:18443",
    token="rufio_...",
)

# Identity is server-authoritative — the bearer token resolves to the
# agent on the server side. The `agent` constructor kwarg is purely
# informational in remote mode.

r.think(type="hypothesis", subject="x:1", content="from a remote process")

for event in r.listen(catch_up=True, types=["thought"]):
    if event["_type"] == "thought":
        print(event["content"])
```

## Surface

Sync method names mirror the CLI verbs 1:1. Every method returns the parsed `--json` payload (a `dict`, or `list[dict]` for `recall` / `goals_list`).

### Cognition

| SDK method                                              | CLI verb        |
| ------------------------------------------------------- | --------------- |
| `attend(intent, entities, topics=None, scope="fleet")`  | `attend`        |
| `think(type, subject, content, scope="fleet", ttl=None, parent=None, topics=None)` | `think` |
| `observe(subject, predicate, object, scope="fleet", confidence=None, topics=None)` | `observe` |
| `reason(content, subject=None, parent=None, decision=None, topics=None, scope="fleet")` | `reason` |
| `recall(topics=None, types=None, since=None, scope=None, include_expired=False)` | `recall` |
| `retract(thought_id, reason=...)`                       | `retract`       |

### Verification

| SDK method                       | CLI verb   |
| -------------------------------- | ---------- |
| `confirm(thought_id, evidence=None)` | `confirm`  |
| `refute(thought_id, reason=...)` | `refute`   |

### Channels

| SDK method                                | CLI verb     |
| ----------------------------------------- | ------------ |
| `summon(agent_id, topic=..., intent=...)` | `summon`     |
| `accept(summon_id)`                       | `accept`     |
| `decline(summon_id, reason=...)`          | `decline`    |
| `say(channel=..., content=...)`           | `say`        |
| `leave(channel)`                          | `leave`      |
| `close(channel)`                          | `close`      |

### Goals

| SDK method                                                  | CLI verb        |
| ----------------------------------------------------------- | --------------- |
| `goal(statement, by=None, parent=None, scope="fleet")`      | `goal`          |
| `goals_list(scope=None, state=None, parent=None)`           | `goals list`    |
| `goal_complete(goal_id, outcome=..., force=False)`          | `goal complete` |
| `goal_abandon(goal_id, reason=..., force=False)`            | `goal abandon`  |

### Read-bundle

| SDK method            | CLI verb |
| --------------------- | -------- |
| `open(subject)`       | `open`   |

### Listen (sync generator)

```python
for event in r.listen(catch_up=True, from_cursor=None, types=None, scope=None):
    if event["_type"] == "thought":
        ...
    if some_condition:
        break  # closes the underlying subprocess cleanly
```

`catch_up=True` flushes existing inbox contents before starting the live tail. `from_cursor="<opaque>"` resumes from a known checkpoint emitted by a prior listen as `{"_type":"cursor","value":"..."}`. The two are mutually exclusive.

## Error model

Every CLI failure surfaces as a typed exception subclass of `RufioError`:

```python
from rufio import (
    Rufio,
    RufioError,
    NotInProject,
    NoIdentity,
    InvalidIdentity,
    NoSuchThought,
    NotADecision,
    Unauthorized,
    ServerError,
    PrivacyBlocked,
    TLSError,
)

try:
    r.retract("does-not-exist", reason="oops")
except NoSuchThought as e:
    print(e)              # CLI stderr message
    print(e.returncode)   # 4
    print(e.stderr)       # full CLI stderr text
```

Catch the base class (`RufioError`) to handle any SDK failure uniformly.

## Security

The SDK enforces the same security floor the CLI does:

- **Subprocess args are always lists** (never `shell=True`). Even though the SDK accepts typed Python arguments, the defense-in-depth posture refuses to construct shell strings — your `intent="..."` cannot inject shell commands.
- **TLS is verified by default.** The SSE consumer in `listen()` rejects plaintext `http://` unless `insecure_tls=True` AND the host is loopback. Same posture as the CLI's `--insecure-tls` flag.
- **Bearer tokens are passed via the `RUFIO_TOKEN` environment variable**, never on argv. Argv is visible to any local user via `ps -ef`; environment is only readable by the process and its children.
- **Identity is server-authoritative in remote mode.** A malicious `RUFIO_AGENT_ID` in env is ignored by the server; the bearer token resolves to the canonical agent.

## Version pinning

Pin the SDK to the same tag as your rufio binary to avoid surface drift:

```sh
pip install "rufio @ git+https://github.com/d-mcmillan/rufio.git@v1.0.6.3#subdirectory=python"
```

If you mix versions (SDK newer than binary), method signatures may reference flags the older binary doesn't recognise; the SDK will surface a `RufioError` with the CLI's "unknown flag" stderr.

## Development

```sh
cd python/
pip install -e ".[dev]"
pytest
ruff check rufio/
```

CI runs `pytest` on Python 3.10, 3.11, 3.12 against a freshly built rufio binary.
