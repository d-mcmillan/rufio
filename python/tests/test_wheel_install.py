"""Wheel-install regression test — the v1.0.5 launch-blocker bug.

The v1.0.5 SDK shipped with a broken hatchling build configuration:
the wheel built successfully but contained ZERO source files, only
dist-info metadata. ``pip install`` succeeded silently, but
``import rufio`` failed from any directory other than the source
tree's ``python/`` dir.

CI's pytest masked the bug because it runs from ``python/`` (where
Python's implicit cwd-on-sys-path finds the local ``rufio/`` source
directory — NOT the installed package). The failure mode only
appeared for actual consumers running ``pip install
git+https://github.com/d-mcmillan/rufio.git#subdirectory=python``
followed by ``import rufio`` from any other cwd.

Root cause hypothesis (per audit triage):
  1. ``license = { file = "../LICENSE" }`` references an
     out-of-tree path; hatchling's source-file discovery got
     confused about where ``rufio/`` lives.
  2. Missing explicit ``[tool.hatch.build] include = [...]``
     directive.

This test catches the regression by:
  1. Building a wheel via ``python -m build``.
  2. Inspecting the wheel's contents — ``rufio/__init__.py``,
     ``rufio/_client.py``, etc. MUST be present.
  3. Installing the wheel into a fresh tmp venv.
  4. Running ``python -c "import rufio"`` from a DIFFERENT cwd
     (``/tmp``) — bypassing the implicit-path masking.

Skipped if ``python -m build`` isn't available (CI installs it
explicitly; local devs may want to ``pip install build``).
"""

from __future__ import annotations

import shutil
import subprocess
import sys
import zipfile
from pathlib import Path

import pytest


def _python_dir() -> Path:
    """Return the python/ subdir of the repo (this test's parent's parent)."""
    return Path(__file__).resolve().parent.parent


def _has_build_module() -> bool:
    """Probe whether `python -m build` is installable / available."""
    try:
        # `build` is typically installed alongside pytest in CI; locally
        # devs may want to add it. We do a soft-probe via importlib.
        proc = subprocess.run(  # noqa: S603
            [sys.executable, "-c", "import build"],
            capture_output=True,
            check=False,
        )
        return proc.returncode == 0
    except Exception:  # noqa: BLE001
        return False


def test_wheel_contains_rufio_package(tmp_path: Path) -> None:
    """Built wheel MUST contain rufio/ source files, not just dist-info.

    This is the load-bearing assertion: pre-fix, the wheel
    contained only METADATA/WHEEL/RECORD in the dist-info dir and
    ZERO actual source files. Post-fix the wheel must include
    rufio/__init__.py, _client.py, _errors.py, etc.
    """
    if not _has_build_module():
        pytest.skip("python -m build not available; install `build` to enable")

    src = _python_dir()
    out = tmp_path / "dist"
    out.mkdir()

    result = subprocess.run(  # noqa: S603
        [sys.executable, "-m", "build", "--wheel", "--outdir", str(out), str(src)],
        check=False,
        capture_output=True,
        text=True,
    )
    if result.returncode != 0:
        pytest.fail(f"python -m build failed:\nSTDOUT:\n{result.stdout}\nSTDERR:\n{result.stderr}")

    wheels = list(out.glob("rufio-*.whl"))
    assert len(wheels) == 1, f"expected exactly one wheel; got {wheels}"
    wheel = wheels[0]

    with zipfile.ZipFile(wheel) as zf:
        names = zf.namelist()

    # Required source files — if any of these is absent, the wheel
    # is broken regardless of how the rest looks.
    required = {
        "rufio/__init__.py",
        "rufio/_client.py",
        "rufio/_errors.py",
        "rufio/_listen.py",
        "rufio/_security.py",
        "rufio/_version.py",
    }
    missing = required - set(names)
    assert not missing, (
        f"wheel is missing source files: {missing!r}\nwheel contents: {names!r}"
    )

    # And the dist-info MUST be present (sanity check that the
    # build produced a valid wheel, not a tarball or something).
    assert any(n.endswith(".dist-info/METADATA") for n in names), (
        f"wheel has no METADATA file: {names!r}"
    )


def test_wheel_install_is_importable_from_external_cwd(tmp_path: Path) -> None:
    """Installed wheel MUST be importable from a cwd OUTSIDE the source tree.

    This is the regression guard for the launch-blocker bug.
    Pre-fix, ``import rufio`` worked from python/ (cwd-on-sys-path
    masking) but failed from anywhere else. Post-fix it MUST work
    from any cwd — which is what real consumers experience.

    Test flow:
      1. Build the wheel.
      2. Create a fresh venv (no Python source on its path).
      3. ``pip install`` the wheel into the venv.
      4. Run ``python -c "import rufio; ..."`` from a DIFFERENT
         cwd. Importable / correct __file__ / version pin.
    """
    if not _has_build_module():
        pytest.skip("python -m build not available; install `build` to enable")

    src = _python_dir()
    dist = tmp_path / "dist"
    dist.mkdir()
    venv = tmp_path / "venv"
    foreign_cwd = tmp_path / "foreign"
    foreign_cwd.mkdir()

    # Build the wheel.
    build_proc = subprocess.run(  # noqa: S603
        [sys.executable, "-m", "build", "--wheel", "--outdir", str(dist), str(src)],
        check=False,
        capture_output=True,
        text=True,
    )
    if build_proc.returncode != 0:
        pytest.fail(f"build failed:\n{build_proc.stderr}")

    wheels = list(dist.glob("rufio-*.whl"))
    assert wheels, "no wheel produced"

    # Fresh venv. We use venv.EnvBuilder via subprocess so the
    # interpreter inside is genuinely isolated.
    subprocess.run(  # noqa: S603
        [sys.executable, "-m", "venv", str(venv)],
        check=True,
        capture_output=True,
    )
    venv_python = venv / ("Scripts" if sys.platform == "win32" else "bin") / (
        "python.exe" if sys.platform == "win32" else "python"
    )
    assert venv_python.is_file(), f"venv python not found at {venv_python}"

    # pip install the wheel (and its runtime deps).
    pip_proc = subprocess.run(  # noqa: S603
        [str(venv_python), "-m", "pip", "install", "--quiet", str(wheels[0])],
        check=False,
        capture_output=True,
        text=True,
    )
    if pip_proc.returncode != 0:
        pytest.fail(f"pip install failed:\n{pip_proc.stderr}")

    # Critical step: import from a DIFFERENT cwd. If we ran this
    # from python/, the implicit cwd-on-sys-path would find the
    # local source and mask the bug. foreign_cwd has no rufio/.
    probe = (
        "import rufio; "
        "print(rufio.__file__); "
        "print(rufio.__version__); "
        "from rufio import Rufio, RufioError, NoSuchThought; "
        "assert rufio.__version__ == '1.0.6', rufio.__version__; "
        "print('imports OK')"
    )
    import_proc = subprocess.run(  # noqa: S603
        [str(venv_python), "-c", probe],
        cwd=str(foreign_cwd),
        check=False,
        capture_output=True,
        text=True,
    )
    if import_proc.returncode != 0:
        pytest.fail(
            "import rufio FAILED from external cwd — wheel install is broken\n"
            f"stdout:\n{import_proc.stdout}\nstderr:\n{import_proc.stderr}"
        )

    # Sanity check the printed package path — MUST be inside the
    # venv's site-packages, NOT pointing at python/rufio/ on disk.
    output_lines = [line.strip() for line in import_proc.stdout.splitlines() if line.strip()]
    assert output_lines, f"no output from import probe: {import_proc.stdout!r}"
    pkg_path = Path(output_lines[0])
    assert "site-packages" in str(pkg_path), (
        f"rufio.__file__ ({pkg_path!r}) is NOT in site-packages — installed package "
        "is not the one being imported"
    )
    # And NOT pointing at the source tree (the bug-mask).
    assert str(src) not in str(pkg_path), (
        f"rufio.__file__ ({pkg_path!r}) points at the source tree, not the install"
    )
    assert "imports OK" in import_proc.stdout, (
        f"missing 'imports OK' marker; full stdout:\n{import_proc.stdout}"
    )


def test_pip_install_directly_from_source_dir(tmp_path: Path) -> None:
    """``pip install <python/>`` MUST produce a functional install.

    Mirrors the v1.0.5 documented install path:
        pip install git+https://github.com/d-mcmillan/rufio.git
                   #subdirectory=python

    pip resolves the subdirectory, runs the same hatchling build,
    and installs the result. If the wheel-build path is broken,
    this path fails the same way — and this is the path consumers
    actually use.
    """
    src = _python_dir()
    venv = tmp_path / "venv"
    foreign_cwd = tmp_path / "foreign"
    foreign_cwd.mkdir()

    subprocess.run(  # noqa: S603
        [sys.executable, "-m", "venv", str(venv)],
        check=True,
        capture_output=True,
    )
    venv_python = venv / ("Scripts" if sys.platform == "win32" else "bin") / (
        "python.exe" if sys.platform == "win32" else "python"
    )

    # pip install the source dir directly — this is what `git+...
    # #subdirectory=python` reduces to once pip has cloned and
    # extracted.
    pip_proc = subprocess.run(  # noqa: S603
        [str(venv_python), "-m", "pip", "install", "--quiet", str(src)],
        check=False,
        capture_output=True,
        text=True,
    )
    if pip_proc.returncode != 0:
        pytest.fail(f"pip install <python/> failed:\n{pip_proc.stderr}")

    probe = "import rufio; from rufio import Rufio; print(rufio.__version__)"
    import_proc = subprocess.run(  # noqa: S603
        [str(venv_python), "-c", probe],
        cwd=str(foreign_cwd),
        check=False,
        capture_output=True,
        text=True,
    )
    if import_proc.returncode != 0:
        pytest.fail(
            "import rufio FAILED after pip install <python/> — the documented "
            f"install path is broken\nstdout: {import_proc.stdout}\nstderr: {import_proc.stderr}"
        )
    assert "1.0.6" in import_proc.stdout, (
        f"expected version 1.0.6 in output; got {import_proc.stdout!r}"
    )


# Cleanup helper for any stranded venvs — pytest's tmp_path tearing
# down already covers the normal case, but if a test fails mid-flight
# and leaves a .venv that pytest can't clean, this gives an explicit
# fallback.
@pytest.fixture(autouse=True)
def _cleanup_helper() -> None:
    yield
    # Defensive sweep: tmp dirs older than the session aren't ours.
    _ = shutil  # quiet ruff — kept for future extensions
