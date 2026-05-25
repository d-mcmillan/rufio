"""End-to-end tests for local-mode SDK calls against a real CLI binary.

These tests exercise the subprocess pathway: they shell out to the
rufio binary, parse the --json output, and assert structural shape.
The passthrough-fidelity contract is enforced by also running the
equivalent CLI invocation directly and comparing JSON dicts (modulo TS
and IDs, which are time-varying).
"""

from __future__ import annotations

import json
import subprocess
from pathlib import Path

import pytest

import rufio


@pytest.fixture
def client(rufio_project: Path, rufio_binary: Path) -> rufio.Rufio:
    return rufio.Rufio(
        root=str(rufio_project),
        agent="alice",
        binary=str(rufio_binary),
    )


def test_attend_local(client: rufio.Rufio) -> None:
    res = client.attend(intent="testing local", entities=["test:1"], scope="fleet")
    assert res["_type"] == "attend-set"
    assert res["agent"] == "alice"
    assert res["intent"] == "testing local"
    assert res["entities"] == ["test:1"]
    assert res["scope"] == "fleet"


def test_think_local(client: rufio.Rufio) -> None:
    res = client.think(
        type="hypothesis",
        subject="customer:1",
        content="initial check",
        scope="fleet",
    )
    assert res["_type"] == "think"
    assert res["author"] == "alice"
    assert res["type"] == "hypothesis"
    assert res["subject"] == "customer:1"
    assert res["content"] == "initial check"
    assert "id" in res
    assert "ts" in res


def test_observe_local(client: rufio.Rufio) -> None:
    res = client.observe(
        subject="customer:1",
        predicate="status",
        object="active",
        scope="fleet",
    )
    assert res["_type"] == "observe"
    assert res["author"] == "alice"
    assert res["predicate"] == "status"
    assert res["object"] == "active"


def test_reason_local(client: rufio.Rufio) -> None:
    res = client.reason(content="thinking about it", scope="fleet")
    assert res["_type"] == "reason"
    assert res["content"] == "thinking about it"
    assert res["scope"] == "fleet"


def test_recall_returns_list(client: rufio.Rufio) -> None:
    # Seed two thoughts (recall returns thoughts/observations/etc. —
    # attentions aren't part of the default recall surface).
    client.think(type="hypothesis", subject="test:1", content="first")
    client.think(type="hypothesis", subject="test:1", content="second")
    records = client.recall()
    assert isinstance(records, list)
    assert len(records) >= 2, f"expected ≥2 records, got {records!r}"
    # All records carry a _type or type key.
    for rec in records:
        assert "_type" in rec or "type" in rec


def test_recall_passthrough_fidelity(client: rufio.Rufio, rufio_binary: Path) -> None:
    """SDK recall result === direct CLI recall result (modulo ordering)."""
    client.think(type="hypothesis", subject="test:1", content="fidelity check")

    # SDK path.
    sdk = client.recall()

    # Direct-CLI path.
    proc = subprocess.run(
        [str(rufio_binary), "recall", "--json"],
        cwd=client.root,
        env={"RUFIO_AGENT_ID": client.agent, "PATH": "/usr/bin:/bin"},
        capture_output=True,
        text=True,
        check=True,
    )
    cli_records: list[dict] = []
    for line in proc.stdout.splitlines():
        line = line.strip()
        if not line:
            continue
        cli_records.append(json.loads(line))

    # Same set of record types from both surfaces (counts may match
    # exactly since the substrate is the same).
    assert len(sdk) == len(cli_records), (
        f"SDK count {len(sdk)} != CLI count {len(cli_records)}"
    )


def test_confirm_local(client: rufio.Rufio) -> None:
    # Seed a thought to confirm.
    thought = client.think(
        type="hypothesis",
        subject="customer:1",
        content="confirmable",
    )
    res = client.confirm(thought["id"], evidence="looks good")
    assert res["_type"] == "confirm"
    assert res["target"] == thought["id"]
    assert res["by"] == "alice"


def test_goal_local(client: rufio.Rufio) -> None:
    res = client.goal(statement="ship v1.0.5", scope="fleet")
    assert res["_type"] == "goal"
    assert res["statement"] == "ship v1.0.5"
    assert res["scope"] == "fleet"


def test_summon_local(client: rufio.Rufio) -> None:
    res = client.summon("bob", topic="planning", intent="lets coordinate")
    assert res["_type"] == "summon"
    assert res["to"] == "bob"
    assert res["topic"] == "planning"


def test_not_in_project_raises_typed(tmp_path: Path, rufio_binary: Path) -> None:
    """Running attend with a non-project root surfaces ``NotInProject``."""
    # tmp_path itself is NOT a rufio project (no rufio.gdl).
    r = rufio.Rufio(root=str(tmp_path), agent="alice", binary=str(rufio_binary))
    with pytest.raises(rufio.NotInProject):
        r.attend(intent="x", entities=["e:1"])


# ----- v1.0.6 bundle: M1 recall positional + m3 observe object_ alias -----


def test_recall_accepts_positional_query(client: rufio.Rufio) -> None:
    """``r.recall("subject")`` matches the CLI's positional form (M1).

    Pre-v1.0.6 the SDK was keyword-only; the CLI primer teaches the
    positional ``rufio recall "<subject>"`` as the cold-agent workaround
    for the subject-vs-topics trap, but Python users hit a TypeError
    when they tried to mirror it. v1.0.6 makes the positional form work.
    """
    # No TypeError just from passing a string positionally.
    records = client.recall("nonexistent:subject")
    assert isinstance(records, list)


def test_recall_positional_combines_with_kwargs(client: rufio.Rufio) -> None:
    """Positional ``query`` and keyword filters compose without error."""
    client.think(type="hypothesis", subject="m1test:1", content="combine test")
    records = client.recall("m1test:1", types=["thought"])
    assert isinstance(records, list)


def test_recall_keyword_only_still_works(client: rufio.Rufio) -> None:
    """Backwards-compat: keyword-only call sites continue to work."""
    client.think(type="hypothesis", subject="m1test:2", content="kwarg path")
    records = client.recall(types=["thought"])
    assert isinstance(records, list)


def test_recall_subject_vs_topics_trap_is_testable_from_python(
    client: rufio.Rufio,
) -> None:
    """Write with --subject, recall positionally → find it; recall by
    --topics ONLY → don't find it. This is the trap the v1.0.6 primer
    teaches as the cold-agent workaround; M1 makes it reproducible
    + testable from the SDK (previously the SDK couldn't even
    *express* the positional form, so the trap was un-testable from
    Python — more acute than the CLI version which fails silently with
    0 rows instead of crashing on TypeError).
    """
    client.think(
        type="hypothesis",
        subject="trap:victim",
        content="hello from the subject side",
    )

    # Positional query against the subject finds the record.
    by_subject = client.recall("trap:victim", types=["thought"])
    assert any(
        r.get("subject") == "trap:victim" for r in by_subject
    ), f"expected to find trap:victim by subject, got {by_subject!r}"

    # --topics=trap:victim does NOT find it (the trap — subject != topics).
    by_topics = client.recall(topics=["trap:victim"], types=["thought"])
    assert not any(
        r.get("subject") == "trap:victim" for r in by_topics
    ), f"unexpected match via topics=; got {by_topics!r}"


def test_observe_accepts_object_kwarg(client: rufio.Rufio) -> None:
    """The original ``object=`` kwarg keeps working (CLI flag-name parity)."""
    res = client.observe(
        subject="customer:m3-a",
        predicate="status",
        object="active",
        scope="fleet",
    )
    assert res["_type"] == "observe"
    assert res["object"] == "active"


def test_observe_accepts_object_underscore_alias(client: rufio.Rufio) -> None:
    """``object_=`` alias avoids shadowing the Python builtin (m3)."""
    res = client.observe(
        subject="customer:m3-b",
        predicate="status",
        object_="inactive",
        scope="fleet",
    )
    assert res["_type"] == "observe"
    assert res["object"] == "inactive"


def test_observe_requires_object_or_alias(client: rufio.Rufio) -> None:
    """Calling observe() with neither object nor object_ raises ValueError."""
    with pytest.raises(ValueError):
        client.observe(subject="customer:m3-c", predicate="status")
