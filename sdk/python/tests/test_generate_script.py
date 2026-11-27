import os
import shutil
import subprocess
from pathlib import Path

import pytest

SCRIPT = Path(__file__).resolve().parents[1] / "scripts" / "generate.sh"
GENERATOR = '#!/usr/bin/env bash\nprintf "%s\\n" "$@" > "$STUB_ARGS"\nexit "${STUB_EXIT:-0}"\n'
CONVERTER = '#!/usr/bin/env bash\n[[ "$1" == -w ]] || exit 2\nprintf "WIN:%s\\n" "$2"\n'
SUFFIXES = {
    "--path": "/docs/api/openapi/cordum-api.yaml",
    "--config": "/openapi-python-client.config.yaml",
    "--output-path": "/src/cordum_sdk/_generated",
}


@pytest.fixture
def bash() -> Path:
    if os.name == "nt":
        git = shutil.which("git")
        if git:
            for parent in Path(git).resolve().parents:
                candidate = parent / "usr" / "bin" / "bash.exe"
                if candidate.is_file():
                    return candidate
        pytest.skip("Git Bash is not installed")
    executable = shutil.which("bash")
    if executable is None:
        pytest.skip("Bash is not installed")
    return Path(executable)


def write_stub(path: Path, content: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8", newline="\n")
    path.chmod(0o755)


@pytest.fixture
def harness(tmp_path: Path, bash: Path) -> tuple[Path, dict[str, str]]:
    root = tmp_path / "repo with spaces" / "sdk" / "python"
    script = root / "scripts" / "generate.sh"
    script.parent.mkdir(parents=True)
    shutil.copyfile(SCRIPT, script)
    stub_dir = tmp_path / "stub bin"
    write_stub(stub_dir / "openapi-python-client", GENERATOR)
    write_stub(stub_dir / "cygpath", CONVERTER)
    env = {
        **os.environ,
        "PATH": os.pathsep.join((str(stub_dir), str(bash.parent), os.environ["PATH"])),
        "MSYS_NO_PATHCONV": "1",
        "STUB_ARGS": (tmp_path / "args.txt").as_posix(),
        "STUB_EXIT": "0",
    }
    return script, env


def run_script(bash: Path, script: Path, env: dict[str, str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [str(bash), script.as_posix()], env=env, capture_output=True, text=True, timeout=20
    )


def assert_paths(env: dict[str, str], *, windows: bool) -> None:
    args = Path(env["STUB_ARGS"]).read_text(encoding="utf-8").splitlines()
    for flag, suffix in SUFFIXES.items():
        value = args[args.index(flag) + 1]
        assert value.startswith("WIN:" if windows else "/"), (flag, value)
        assert value.endswith(suffix), (flag, value)
        assert "repo with spaces" in value


def test_path_discovered_generator_gets_windows_paths(
    bash: Path, harness: tuple[Path, dict[str, str]]
) -> None:
    script, env = harness
    result = run_script(bash, script, env)
    assert result.returncode == 0, result.stderr
    assert_paths(env, windows=True)


def test_local_venv_generator_still_preferred(
    bash: Path, harness: tuple[Path, dict[str, str]]
) -> None:
    script, env = harness
    write_stub(
        script.parents[1] / ".venv" / "Scripts" / "openapi-python-client.exe", GENERATOR
    )
    stub_dir = Path(env["PATH"].split(os.pathsep)[0])
    write_stub(stub_dir / "openapi-python-client", "#!/usr/bin/env bash\nexit 99\n")
    result = run_script(bash, script, env)
    assert result.returncode == 0, result.stderr
    assert_paths(env, windows=True)


def test_generator_failure_propagates(
    bash: Path, harness: tuple[Path, dict[str, str]]
) -> None:
    script, env = harness
    result = run_script(bash, script, {**env, "STUB_EXIT": "3"})
    assert result.returncode == 3, result.stderr


def posix_path(bash: Path, env: dict[str, str]) -> Path:
    stub_dir = Path(env["PATH"].split(os.pathsep)[0])
    (stub_dir / "cygpath").unlink()
    # Only expose the script's tools, never a host's real cygpath or wslpath.
    for name in ("bash", "dirname"):
        executable = str(bash) if name == "bash" else shutil.which(name)
        assert executable is not None
        (stub_dir / name).symlink_to(executable)
    env["PATH"] = str(stub_dir)
    return stub_dir


@pytest.mark.skipif(os.name == "nt", reason="POSIX-only path behavior")
def test_without_converter_paths_stay_posix(
    bash: Path, harness: tuple[Path, dict[str, str]]
) -> None:
    script, env = harness
    posix_path(bash, env)
    result = run_script(bash, script, env)
    assert result.returncode == 0, result.stderr
    assert_paths(env, windows=False)


@pytest.mark.skipif(os.name == "nt", reason="POSIX-only WSL path behavior")
def test_wslpath_converts_only_exe_generators(
    bash: Path, harness: tuple[Path, dict[str, str]]
) -> None:
    script, env = harness
    stub_dir = posix_path(bash, env)
    write_stub(stub_dir / "wslpath", CONVERTER)
    result = run_script(bash, script, env)
    assert result.returncode == 0, result.stderr
    assert_paths(env, windows=False)
    write_stub(
        script.parents[1] / ".venv" / "Scripts" / "openapi-python-client.exe", GENERATOR
    )
    result = run_script(bash, script, env)
    assert result.returncode == 0, result.stderr
    assert_paths(env, windows=True)
