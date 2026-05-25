"""Package-level smoke tests — version pin, public surface."""

from __future__ import annotations

import sys
from pathlib import Path


def test_package_imports() -> None:
    """`import rufio` and `__version__` are publicly accessible."""
    import rufio

    assert rufio.__version__ == "1.0.6"
    assert hasattr(rufio, "Rufio")
    assert hasattr(rufio, "RufioError")


def test_pyproject_is_valid_pep621() -> None:
    """pyproject.toml is well-formed PEP-621 and pins requires-python."""
    if sys.version_info >= (3, 11):
        import tomllib
    else:
        import tomli as tomllib  # type: ignore[no-redef]

    pyproject = Path(__file__).resolve().parent.parent / "pyproject.toml"
    data = tomllib.loads(pyproject.read_text())
    assert data["project"]["name"] == "rufio"
    assert data["project"]["requires-python"] == ">=3.10"
    assert data["build-system"]["build-backend"] == "hatchling.build"


def test_public_error_classes_subclass_rufio_error() -> None:
    """Every typed error subclasses ``RufioError`` so a broad except works."""
    import rufio

    for name in (
        "NotInProject",
        "NoIdentity",
        "InvalidIdentity",
        "NoSuchThought",
        "NotADecision",
        "ServerError",
        "Unauthorized",
        "PrivacyBlocked",
        "TLSError",
    ):
        cls = getattr(rufio, name)
        assert issubclass(cls, rufio.RufioError), f"{name} is not a RufioError"
