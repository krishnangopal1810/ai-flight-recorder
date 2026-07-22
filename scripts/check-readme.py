#!/usr/bin/env python3
"""Validate the public README's narrative, links, anchors, and launch media."""

from __future__ import annotations

import re
import struct
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
README = ROOT / "README.md"


def github_slug(value: str) -> str:
    value = re.sub(r"<[^>]+>", "", value).strip().lower()
    value = re.sub(r"[^\w\- ]", "", value, flags=re.UNICODE)
    return re.sub(r"[ -]+", "-", value).strip("-")


def gif_duration_seconds(data: bytes) -> float:
    duration = 0
    cursor = 0
    marker = b"\x21\xf9\x04"
    while True:
        cursor = data.find(marker, cursor)
        if cursor < 0:
            return duration / 100
        if cursor + 6 < len(data):
            duration += int.from_bytes(data[cursor + 4 : cursor + 6], "little")
        cursor += len(marker)


def image_dimensions(path: Path) -> tuple[int, int]:
    data = path.read_bytes()
    if data.startswith(b"\x89PNG\r\n\x1a\n"):
        return struct.unpack(">II", data[16:24])
    if data.startswith((b"GIF87a", b"GIF89a")):
        return struct.unpack("<HH", data[6:10])
    raise ValueError(f"unsupported media type: {path.relative_to(ROOT)}")


def paragraph_word_counts(text: str) -> list[tuple[int, str]]:
    results: list[tuple[int, str]] = []
    current: list[str] = []
    in_fence = False

    def finish() -> None:
        if current:
            paragraph = " ".join(current)
            words = re.findall(r"\b[\w’'-]+\b", re.sub(r"\[[^]]+\]\([^)]+\)", "link", paragraph))
            results.append((len(words), paragraph))
            current.clear()

    for line in text.splitlines():
        if line.startswith("```"):
            finish()
            in_fence = not in_fence
            continue
        stripped = line.strip()
        if in_fence or not stripped:
            finish()
            continue
        if stripped.startswith(("#", "<", "|", "- ", "> ")) or re.match(r"\d+\. ", stripped):
            finish()
            continue
        current.append(stripped)
    finish()
    return results


def main() -> None:
    text = README.read_text()
    errors: list[str] = []

    required = [
        "Your agent failed in production. The world has already moved.",
        "AI Flight Recorder turns one broken run into a controlled experiment.",
        "## Watch the run split",
        "## Run the incident yourself",
        "## Why not just trace or re-run?",
        "## Record an agent",
        "## What never happens during replay?",
        "## Turn the fix into a regression",
        "## Honest alpha boundaries",
        "ghcr.io/krishnangopal1810/ai-flight-recorder-demo:v0.1.0-alpha.1 start",
    ]
    for phrase in required:
        if phrase not in text:
            errors.append(f"README.md: missing launch narrative contract {phrase!r}")

    headings = [match.group(1) for match in re.finditer(r"^#{1,6}\s+(.+)$", text, re.MULTILINE)]
    anchors = {github_slug(heading) for heading in headings}
    anchors.update(re.findall(r'<a\s+id="([^"]+)"', text))
    for anchor in re.findall(r"\]\(#([^)]+)\)", text):
        if anchor not in anchors:
            errors.append(f"README.md: missing local anchor #{anchor}")

    for target in re.findall(r"\[[^]]+\]\(([^)]+)\)", text):
        if target.startswith(("http://", "https://", "#")):
            continue
        path = target.split("#", 1)[0]
        if path and not (ROOT / path).exists():
            errors.append(f"README.md: missing link target {target}")

    for alt, source in re.findall(r"!\[([^]]*)\]\(([^)]+)\)", text):
        if not alt.strip():
            errors.append(f"README.md: image {source} needs useful alt text")
    for source in re.findall(r"(?:src|srcset)=\"([^\"]+)\"", text):
        if source.startswith(("http://", "https://")):
            continue
        if not (ROOT / source).exists():
            errors.append(f"README.md: missing media {source}")

    gif = ROOT / "docs/assets/readme/run-split.gif"
    fallback = ROOT / "docs/assets/readme/run-split.png"
    if gif.exists():
        if gif.stat().st_size >= 5 * 1024 * 1024:
            errors.append("README GIF must remain under 5 MB")
        width, height = image_dimensions(gif)
        if width < 800 or height < 400:
            errors.append(f"README GIF is too small to read: {width}x{height}")
        duration = gif_duration_seconds(gif.read_bytes())
        if not 8 <= duration <= 12:
            errors.append(f"README GIF must run for 8–12 seconds, got {duration:.2f}")
    if not fallback.exists() or 'src="docs/assets/readme/run-split.png"' not in text:
        errors.append("README animation needs the checked-in static PNG fallback")

    social = ROOT / "docs/assets/brand/social-preview.png"
    if not social.exists():
        errors.append("GitHub social preview PNG is missing")
    else:
        if image_dimensions(social) != (1280, 640):
            errors.append(f"social preview must be 1280x640, got {image_dimensions(social)}")
        if social.stat().st_size > 1_000_000:
            errors.append("social preview should remain below GitHub's 1 MB recommendation")

    for count, paragraph in paragraph_word_counts(text):
        if count > 60:
            errors.append(f"README.md: {count}-word paragraph breaks visual pacing: {paragraph[:80]}…")

    generic = {"features", "overview"}
    for heading in headings:
        if heading.strip().lower() in generic:
            errors.append(f"README.md: replace generic heading {heading!r} with an outcome")

    if errors:
        raise SystemExit("\n".join(errors))
    print(f"readme-check: validated {len(headings)} headings, real media, anchors, links, and pacing")


if __name__ == "__main__":
    main()
