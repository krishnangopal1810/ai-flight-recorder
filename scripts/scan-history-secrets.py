#!/usr/bin/env python3
"""Offline, redacted secret scan for every reachable Git blob plus the worktree."""

from __future__ import annotations

import re
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
MAX_BLOB_BYTES = 5 * 1024 * 1024


@dataclass(frozen=True)
class Rule:
    name: str
    pattern: re.Pattern[str]


RULES = (
    Rule("private-key", re.compile(r"-----BEGIN (?:RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----")),
    Rule("aws-access-key", re.compile(r"\b(?:AKIA|ASIA)[A-Z0-9]{16}\b")),
    Rule("github-token", re.compile(r"\bgh[pousr]_[A-Za-z0-9]{30,}\b")),
    Rule("slack-token", re.compile(r"\bxox[baprs]-[A-Za-z0-9-]{20,}\b")),
    Rule("openai-key", re.compile(r"\bsk-(?:proj-)?[A-Za-z0-9_-]{20,}\b")),
    Rule("anthropic-key", re.compile(r"\bsk-ant-[A-Za-z0-9_-]{20,}\b")),
    Rule("google-api-key", re.compile(r"\bAIza[0-9A-Za-z_-]{35}\b")),
    Rule(
        "credential-assignment",
        re.compile(
            r"(?i)\b(?:api[_-]?key|access[_-]?token|auth[_-]?token|client[_-]?secret|password|private[_-]?key|secret)\b"
            r"\s*[:=]\s*(?:\"([^\"\s]{16,})\"|'([^'\s]{16,})'|([A-Za-z0-9+/=_-]{16,}))"
        ),
    ),
    Rule(
        "credential-url",
        re.compile(r"(?i)\b[a-z][a-z0-9+.-]*://[^\s/:]+:([^\s/@]{8,})@"),
    ),
)

ALLOWLIST = (
    re.compile(r"^frt_A{43}$"),
    re.compile(r"^AFR_TEST_(?:API_KEY|TOKEN|PASSWORD)_[A-Z0-9]+$"),
    re.compile(r"^flight-(?:dev|demo|test)(?:-only)?(?:-password)?$"),
    re.compile(r"^example(?:-only)?$", re.IGNORECASE),
)


def run_git(*args: str, text: bool = True) -> str | bytes:
    return subprocess.check_output(["git", *args], cwd=ROOT, text=text, stderr=subprocess.DEVNULL)


def allowed(match: re.Match[str]) -> bool:
    values = [match.group(0), *match.groups()]
    return any(pattern.fullmatch(value or "") for value in values for pattern in ALLOWLIST)


def findings(content: str) -> list[tuple[str, int]]:
    result: list[tuple[str, int]] = []
    for rule in RULES:
        for match in rule.pattern.finditer(content):
            if allowed(match):
                continue
            line = content.count("\n", 0, match.start()) + 1
            result.append((rule.name, line))
    return result


def text_blob(data: bytes) -> str | None:
    if b"\0" in data[:8192]:
        return None
    try:
        return data.decode("utf-8")
    except UnicodeDecodeError:
        return None


def history_blobs() -> list[tuple[str, str]]:
    # Scan every publishable repository ref. Codex's local checkpoint refs are
    # work-in-progress artifacts and are deliberately outside release history.
    objects = str(
        run_git("rev-list", "--objects", "--branches", "--tags", "--remotes")
    ).splitlines()
    result: list[tuple[str, str]] = []
    seen: set[str] = set()
    for item in objects:
        object_id, _, path = item.partition(" ")
        if not path or object_id in seen:
            continue
        if str(run_git("cat-file", "-t", object_id)).strip() != "blob":
            continue
        if int(str(run_git("cat-file", "-s", object_id)).strip()) > MAX_BLOB_BYTES:
            continue
        seen.add(object_id)
        result.append((object_id, path))
    return result


def worktree_paths() -> list[Path]:
    names = str(run_git("ls-files", "--cached", "--others", "--exclude-standard", "-z"))
    return [ROOT / name for name in names.split("\0") if name]


def self_test() -> None:
    examples = {
        "aws-access-key": "AKIA" + "IOSFODNN7EXAMPLE",
        "private-key": "-----BEGIN " + "PRIVATE KEY-----",
        "credential-assignment": (
            "client_" + "secret" + "=" + "self-test-value-that-must-be-detected"
        ),
    }
    for expected, value in examples.items():
        names = {name for name, _ in findings(value)}
        if expected not in names:
            raise SystemExit(f"secret-scan self-test failed to detect {expected}")
    for value in ("frt_" + "A" * 43, 'password="flight-demo-only-password"'):
        if findings(value):
            raise SystemExit("secret-scan self-test rejected an exact documented fixture")


def main() -> None:
    self_test()
    errors: list[str] = []
    blobs = history_blobs()
    paths = worktree_paths()

    for object_id, path in blobs:
        data = run_git("cat-file", "blob", object_id, text=False)
        content = text_blob(data if isinstance(data, bytes) else data.encode())
        if content is None:
            continue
        for rule, line in findings(content):
            errors.append(f"history:{object_id[:12]}:{path}:{line}: {rule} (value redacted)")

    for path in paths:
        if not path.is_file() or path.stat().st_size > MAX_BLOB_BYTES:
            continue
        content = text_blob(path.read_bytes())
        if content is None:
            continue
        for rule, line in findings(content):
            errors.append(f"worktree:{path.relative_to(ROOT)}:{line}: {rule} (value redacted)")

    if errors:
        raise SystemExit("\n".join(sorted(set(errors))))
    print(
        f"secret-scan: checked {len(blobs)} reachable Git blobs and "
        f"{len(paths)} worktree files; no findings"
    )


if __name__ == "__main__":
    if len(sys.argv) > 1 and sys.argv[1] != "--self-test":
        raise SystemExit("usage: scripts/scan-history-secrets.py [--self-test]")
    if len(sys.argv) > 1:
        self_test()
        print("secret-scan: self-test passed")
    else:
        main()
