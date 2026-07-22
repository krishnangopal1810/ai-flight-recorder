#!/usr/bin/env python3
"""Fail closed on licenses in shipped Python and TypeScript dependency graphs."""

from __future__ import annotations

import json
import re
import subprocess
from importlib.metadata import PackageNotFoundError, metadata
from pathlib import Path

from packaging.markers import Marker


ROOT = Path(__file__).resolve().parents[1]
PYTHON_PROJECT = ROOT / "sdk/python"
NODE_LOCKS = (
    ROOT / "web/package-lock.json",
    ROOT / "sdk/typescript/package-lock.json",
    ROOT / "examples/reference-typescript/package-lock.json",
)
ALLOWED = {
    "0BSD",
    "Apache-2.0",
    "BSD-2-Clause",
    "BSD-3-Clause",
    "CC0-1.0",
    "ISC",
    "MIT",
    "MPL-2.0",
    "PSF-2.0",
    "Python-2.0",
    "Unlicense",
}
ALIASES = {
    "3-Clause BSD License": "BSD-3-Clause",
    "Apache 2.0": "Apache-2.0",
    "Apache License, Version 2.0": "Apache-2.0",
    "Modified BSD License": "BSD-3-Clause",
}
CLASSIFIERS = {
    "OSI Approved :: Apache Software License": "Apache-2.0",
    "OSI Approved :: BSD License": "BSD-3-Clause",
    "OSI Approved :: MIT License": "MIT",
    "OSI Approved :: Mozilla Public License 2.0 (MPL 2.0)": "MPL-2.0",
    "OSI Approved :: Python Software Foundation License": "PSF-2.0",
}


def accepted(expression: str) -> bool:
    expression = ALIASES.get(expression.strip(), expression.strip())
    identifiers = set(re.findall(r"[A-Za-z0-9][A-Za-z0-9.+-]*", expression))
    identifiers -= {"AND", "OR"}
    return bool(identifiers) and identifiers <= ALLOWED


def node_licenses() -> tuple[int, list[str]]:
    checked = 0
    errors: list[str] = []
    for lock_path in NODE_LOCKS:
        lock = json.loads(lock_path.read_text())
        for package_path, package in lock.get("packages", {}).items():
            if not package_path or package.get("dev") or package.get("link"):
                continue
            checked += 1
            license_name = str(package.get("license", "")).strip()
            if not accepted(license_name):
                name = package_path.rsplit("node_modules/", 1)[-1]
                errors.append(
                    f"{lock_path.relative_to(ROOT)}: {name}@{package.get('version')} "
                    f"has unapproved or missing license {license_name!r}"
                )
    return checked, errors


def python_licenses() -> tuple[int, list[str]]:
    exported = subprocess.check_output(
        [
            "uv",
            "export",
            "--frozen",
            "--no-dev",
            "--all-extras",
            "--no-emit-project",
            "--format",
            "requirements-txt",
            "--no-hashes",
        ],
        cwd=PYTHON_PROJECT,
        text=True,
    )
    exported_requirements = re.findall(
        r"^([A-Za-z0-9_.-]+)==([^\s;]+)(?:\s*;\s*(.+))?$", exported, re.MULTILINE
    )
    requirements = [
        (name, version)
        for name, version, marker in exported_requirements
        if not marker or Marker(marker).evaluate()
    ]
    errors: list[str] = []
    for name, version in requirements:
        try:
            package = metadata(name)
        except PackageNotFoundError:
            errors.append(f"sdk/python/uv.lock: {name} expected {version}, not installed")
            continue
        installed_version = package.get("Version", "")
        if installed_version != version:
            errors.append(
                f"sdk/python/uv.lock: {name} expected {version}, found {installed_version}"
            )
            continue
        license_name = package.get("License-Expression") or package.get("License") or ""
        license_name = ALIASES.get(license_name.strip(), license_name.strip())
        if not license_name:
            for classifier in package.get_all("Classifier", []):
                if classifier.startswith("License :: "):
                    license_name = CLASSIFIERS.get(classifier.removeprefix("License :: "), "")
                    if license_name:
                        break
        if not accepted(license_name):
            errors.append(
                f"sdk/python/uv.lock: {name}@{version} has unapproved or missing "
                f"license {license_name!r}"
            )
    return len(requirements), errors


def main() -> None:
    node_count, node_errors = node_licenses()
    python_count, python_errors = python_licenses()
    errors = node_errors + python_errors
    if errors:
        raise SystemExit("\n".join(errors))
    print(
        f"sdk-license-audit: {node_count} production Node packages and "
        f"{python_count} frozen Python packages use allowed licenses"
    )


if __name__ == "__main__":
    main()
