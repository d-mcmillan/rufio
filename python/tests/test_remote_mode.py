"""End-to-end tests for remote-mode SDK calls against a real rufio serve.

These tests spin up a localhost rufio serve daemon via the
``remote_server`` fixture in conftest, mint a token, and exercise the
SDK with ``server=`` + ``token=``. The CLI handles the actual HTTPS;
the SDK's job is to construct the right command line.
"""

from __future__ import annotations

import pytest

import rufio


@pytest.fixture
def client(rufio_binary, remote_server) -> rufio.Rufio:
    return rufio.Rufio(
        server=remote_server["url"],
        token=remote_server["token"],
        insecure_tls=True,
        binary=str(rufio_binary),
    )


def test_attend_remote(client: rufio.Rufio) -> None:
    res = client.attend(intent="remote testing", entities=["test:1"], scope="fleet")
    # Server-side identity from token = "alice" (minted in remote_server fixture).
    assert res["agent"] == "alice"
    assert res["intent"] == "remote testing"


def test_think_remote(client: rufio.Rufio) -> None:
    res = client.think(
        type="hypothesis",
        subject="customer:1",
        content="remote check",
    )
    assert res["author"] == "alice"
    assert res["type"] == "hypothesis"
    assert res["content"] == "remote check"


def test_recall_remote(client: rufio.Rufio) -> None:
    client.think(type="hypothesis", subject="x:1", content="seed for recall")
    records = client.recall()
    assert isinstance(records, list)
    assert any(rec.get("content") == "seed for recall" for rec in records)


def test_goal_remote(client: rufio.Rufio) -> None:
    res = client.goal(statement="remote goal", scope="fleet")
    assert res["statement"] == "remote goal"
    assert res["author"] == "alice"


def test_summon_remote(client: rufio.Rufio) -> None:
    res = client.summon("bob", topic="remote-planning", intent="coordinate")
    assert res["from"] == "alice"
    assert res["to"] == "bob"


def test_remote_identity_overrides_env(
    rufio_binary,
    remote_server,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """A malicious RUFIO_AGENT_ID is ignored — server uses the token's identity."""
    # Mint-time identity = alice (per remote_server fixture). The
    # SDK consumer SHOULD see the agent from the token, not env.
    monkeypatch.setenv("RUFIO_AGENT_ID", "impersonate-eve")
    r = rufio.Rufio(
        server=remote_server["url"],
        token=remote_server["token"],
        insecure_tls=True,
        binary=str(rufio_binary),
    )
    res = r.attend(intent="probe", entities=["test:1"])
    assert res["agent"] == "alice", (
        f"server-authoritative identity must win over env; got {res['agent']!r}"
    )
