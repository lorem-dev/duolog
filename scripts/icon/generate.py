#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = ["resvg-py", "pillow"]
# ///
"""Generate duolog's icon: two chat bubbles in dialogue, a half-hidden burst and a prompt.

The burst and the prompt are drawn here from scratch. They hint at the two kinds of agent
and copy no vendor's logo.
"""
import io
import math
import os
import sys

import resvg_py
from PIL import Image, ImageOps


def squircle(center: float, radius: float, n: float = 5.0, steps: int = 72) -> str:
    points: list[str] = []
    for i in range(steps):
        t = 2 * math.pi * i / steps
        c, s = math.cos(t), math.sin(t)
        x = center + radius * math.copysign(abs(c) ** (2 / n), c)
        y = center + radius * math.copysign(abs(s) ** (2 / n), s)
        points.append(f"{x:.1f},{y:.1f}")
    return "M" + " L".join(points) + "Z"


def bubble(x: int, y: int, w: int, h: int, r: int, tx: int, tw: int, side: str) -> str:
    """A bubble with a short, softly rounded tail at its lower left or lower right."""
    b = y + h
    head = f"M{x + r},{y}H{x + w - r}A{r},{r} 0 0 1 {x + w},{y + r}V{b - r}A{r},{r} 0 0 1 {x + w - r},{b}"
    if side == "left":
        tail = (
            f"H{tx + tw}C{tx + tw - 8},{b + 26} {tx + tw * 0.45:.0f},{b + 52} {tx + 12},{b + 64}"
            f"Q{tx - 8},{b + 72} {tx - 2},{b + 52}C{tx + 4},{b + 34} {tx + 2},{b + 16} {tx},{b}"
        )
    else:
        tail = (
            f"H{tx + tw}C{tx + tw - 2},{b + 16} {tx + tw - 4},{b + 34} {tx + tw + 2},{b + 52}"
            f"Q{tx + tw + 8},{b + 72} {tx + tw - 12},{b + 64}C{tx + tw * 0.55:.0f},{b + 52} {tx + 8},{b + 26} {tx},{b}"
        )
    return head + tail + f"H{x + r}A{r},{r} 0 0 1 {x},{b - r}V{y + r}A{r},{r} 0 0 1 {x + r},{y}Z"


def burst(cx: float, cy: float, r: float) -> str:
    """Thick, stubby rays of uneven length and tilt, packed around a centre."""
    lengths = [1.0, 0.8, 0.95, 0.72, 0.9, 0.84, 1.0, 0.7, 0.88, 0.96, 0.76, 0.92, 0.82, 0.98, 0.74]
    tilt = [0, 3, -4, 2, -2, 4, -3, 1, 3, -4, 2, -1, 4, -2, 3]
    parts: list[str] = []
    for i, (length, dt) in enumerate(zip(lengths, tilt)):
        a = math.radians(360 / len(lengths) * i + dt)
        ux, uy = math.cos(a), math.sin(a)
        px, py = -uy, ux
        base, tip = r * 0.06, r * 0.052
        bx, by = cx + ux * r * 0.02, cy + uy * r * 0.02
        ex, ey = cx + ux * r * length, cy + uy * r * length
        parts.append(
            f"M{bx + px * base:.1f},{by + py * base:.1f}L{ex + px * tip:.1f},{ey + py * tip:.1f}"
            f"A{tip:.1f},{tip:.1f} 0 0 0 {ex - px * tip:.1f},{ey - py * tip:.1f}L{bx - px * base:.1f},{by - py * base:.1f}Z"
        )
    return "".join(parts)


clip = squircle(512, 512)
rim = squircle(512, 498)
back = bubble(92, 164, 500, 360, 96, 176, 88, "left")
front = bubble(432, 432, 500, 360, 96, 752, 88, "right")

svg = f"""<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024" width="1024" height="1024">
<title>duolog: a conversation between two agents</title>
<defs>
<linearGradient id="bg" x1=".15" y1="0" x2=".85" y2="1"><stop offset="0" stop-color="#3B2F9E"/><stop offset=".55" stop-color="#1D1F63"/><stop offset="1" stop-color="#0C1036"/></linearGradient>
<radialGradient id="halo" cx=".34" cy=".38" r=".5"><stop offset="0" stop-color="#8E7BFF" stop-opacity=".55"/><stop offset="1" stop-color="#8E7BFF" stop-opacity="0"/></radialGradient>
<linearGradient id="rim" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#fff" stop-opacity=".55"/><stop offset=".45" stop-color="#fff" stop-opacity=".1"/><stop offset="1" stop-color="#fff" stop-opacity=".28"/></linearGradient>
<linearGradient id="gloss" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#fff" stop-opacity=".22"/><stop offset=".42" stop-color="#fff" stop-opacity=".04"/><stop offset="1" stop-color="#fff" stop-opacity="0"/></linearGradient>
<linearGradient id="warm" x1="0" y1="0" x2=".3" y2="1"><stop offset="0" stop-color="#FFBE98"/><stop offset="1" stop-color="#E97B4E"/></linearGradient>
<linearGradient id="steel" x1="0" y1="0" x2=".4" y2="1"><stop offset="0" stop-color="#fff"/><stop offset=".55" stop-color="#ECEBFF"/><stop offset="1" stop-color="#B6B8EA"/></linearGradient>
<filter id="lift" x="-20%" y="-20%" width="140%" height="150%"><feDropShadow dx="0" dy="26" stdDeviation="26" flood-color="#05061F" flood-opacity=".62"/></filter>
<filter id="liftbig" x="-35%" y="-35%" width="170%" height="190%"><feDropShadow dx="0" dy="44" stdDeviation="48" flood-color="#05061F" flood-opacity=".85"/></filter>
<clipPath id="clip"><path d="{clip}"/></clipPath>
<clipPath id="inback"><rect x="92" y="164" width="500" height="360" rx="96"/></clipPath>
</defs>
<g clip-path="url(#clip)">
<rect width="1024" height="1024" fill="url(#bg)"/><rect width="1024" height="1024" fill="url(#halo)"/>
<path d="{back}" fill="url(#warm)" filter="url(#lift)"/>
<g clip-path="url(#inback)"><g transform="rotate(53 467 524)"><path d="{burst(467, 524, 236)}" fill="#FFF6EF"/></g></g>
<path d="{front}" fill="url(#steel)" filter="url(#liftbig)"/>
<path d="M586,556l66,56-66,56M684,668H778" fill="none" stroke="#2A2A78" stroke-width="32" stroke-linecap="round" stroke-linejoin="round"/>
<rect width="1024" height="1024" fill="url(#gloss)"/>
</g>
<path d="{rim}" fill="none" stroke="url(#rim)" stroke-width="7" opacity=".85"/>
</svg>
"""


def small_png(size: int) -> bytes:
    raw = bytes(resvg_py.svg_to_bytes(svg_string=svg, width=size, height=size))
    out = io.BytesIO()
    img = Image.open(io.BytesIO(raw)).convert("RGBA")
    # Six bits per colour channel keep gradients smooth and cut the size by half.
    r, g, b, a = img.split()
    rgb = ImageOps.posterize(Image.merge("RGB", (r, g, b)), 6)
    Image.merge("RGBA", (*rgb.split(), a)).save(out, "PNG", optimize=True, compress_level=9)
    return out.getvalue()


root = sys.argv[1]
for d in (f"{root}/assets", f"{root}/plugin/claude/.claude-plugin"):
    os.makedirs(d, exist_ok=True)
    with open(f"{d}/icon.svg", "w", encoding="utf-8") as svg_file:
        svg_file.write(svg)
    with open(f"{d}/icon.png", "wb") as png_file:
        png_file.write(small_png(256))
print("ok")
