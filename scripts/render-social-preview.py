#!/usr/bin/env python3
"""Render the checked-in 1280×640 GitHub social preview from brand primitives."""

from __future__ import annotations

from pathlib import Path

from PIL import Image, ImageDraw, ImageFont


ROOT = Path(__file__).resolve().parents[1]
OUTPUT = ROOT / "docs/assets/brand/social-preview.png"
SANS = (
    "/System/Library/Fonts/SFNS.ttf",
    "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
)
MONO = (
    "/System/Library/Fonts/SFNSMono.ttf",
    "/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf",
)


def font(candidates: tuple[str, ...], size: int) -> ImageFont.FreeTypeFont:
    for candidate in candidates:
        if Path(candidate).exists():
            return ImageFont.truetype(candidate, size=size)
    raise FileNotFoundError(f"no supported preview font found: {', '.join(candidates)}")


def bezier(
    draw: ImageDraw.ImageDraw,
    points: tuple[tuple[float, float], tuple[float, float], tuple[float, float], tuple[float, float]],
    *,
    fill: str,
    width: int,
) -> None:
    p0, p1, p2, p3 = points
    samples = []
    for index in range(41):
        t = index / 40
        inverse = 1 - t
        samples.append(
            (
                inverse**3 * p0[0] + 3 * inverse**2 * t * p1[0] + 3 * inverse * t**2 * p2[0] + t**3 * p3[0],
                inverse**3 * p0[1] + 3 * inverse**2 * t * p1[1] + 3 * inverse * t**2 * p2[1] + t**3 * p3[1],
            )
        )
    draw.line(samples, fill=fill, width=width, joint="curve")


def main() -> None:
    image = Image.new("RGB", (1280, 640), "#09150f")
    draw = ImageDraw.Draw(image)

    draw.ellipse((970, -150, 1390, 270), fill="#0d281a")
    draw.rounded_rectangle((72, 64, 144, 136), radius=20, fill="#24a367")
    for y, end in ((89, 126), (101, 119), (113, 110)):
        draw.rounded_rectangle((92, y, end, y + 5), radius=3, fill="#ffffff")
    draw.ellipse((124, 96, 133, 105), fill="#ffc857")

    draw.text((164, 73), "AI Flight Recorder", font=font(SANS, 38), fill="#effaf3")
    draw.text((72, 180), "Find where the run split.", font=font(SANS, 62), fill="#ffffff", stroke_width=1)
    draw.text(
        (72, 268),
        "Freeze recorded dependencies. Change one value. Replay safely.",
        font=font(SANS, 26),
        fill="#a8c4b4",
    )

    bezier(draw, ((84, 408), (430, 408), (526, 364), (678, 364)), fill="#7f9689", width=5)
    bezier(draw, ((688, 364), (820, 364), (850, 330), (1150, 330)), fill="#24a367", width=7)
    bezier(draw, ((688, 364), (820, 364), (850, 454), (1150, 454)), fill="#e25b5b", width=7)
    draw.ellipse((671, 356, 687, 372), fill="#ffc857")
    draw.ellipse((1141, 321, 1159, 339), fill="#24a367")
    draw.ellipse((1141, 445, 1159, 463), fill="#e25b5b")

    draw.text((820, 294), "CONTROLLED REPLAY  ·  APPROVE", font=font(MONO, 18), fill="#7de2a9")
    draw.text((840, 480), "RECORDED RUN       ·  DENY", font=font(MONO, 18), fill="#ff9999")
    draw.text((612, 397), "FIRST DIVERGENCE", font=font(MONO, 16), fill="#ffc857")
    draw.text(
        (72, 568),
        "OPEN SOURCE  ·  OPENTELEMETRY  ·  ISOLATED REPLAY",
        font=font(MONO, 18),
        fill="#779487",
    )

    OUTPUT.parent.mkdir(parents=True, exist_ok=True)
    image.save(OUTPUT, format="PNG", optimize=True)
    print(f"social-preview: wrote {OUTPUT.relative_to(ROOT)} ({OUTPUT.stat().st_size} bytes)")


if __name__ == "__main__":
    main()
