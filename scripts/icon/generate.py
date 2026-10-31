#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = ["resvg-py", "pillow"]
# ///
"""Generate duolog's icon: two chat bubbles in dialogue, each with half a hint of its agent."""
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


def bubble(x: int, y: int, w: int, h: int, r: int, tail_x: int, tail_w: int, tip_x: int, tip_y: int) -> str:
    b = y + h
    return (
        f"M{x + r},{y}H{x + w - r}A{r},{r} 0 0 1 {x + w},{y + r}V{b - r}A{r},{r} 0 0 1 {x + w - r},{b}"
        f"H{tail_x + tail_w}Q{tail_x + tail_w * 0.42:.0f},{b + 10} {tip_x},{tip_y}"
        f"Q{tail_x + tail_w * 0.12:.0f},{b + 42} {tail_x},{b}H{x + r}A{r},{r} 0 0 1 {x},{b - r}V{y + r}A{r},{r} 0 0 1 {x + r},{y}Z"
    )


def spark(cx: float, cy: float, r: float, rays: int = 12) -> str:
    parts: list[str] = []
    for i in range(rays):
        a = 2 * math.pi * i / rays - math.pi / 2
        length = r if i % 2 == 0 else r * 0.72
        parts.append(
            f"M{cx + 0.14 * r * math.cos(a):.1f},{cy + 0.14 * r * math.sin(a):.1f}"
            f"L{cx + length * math.cos(a):.1f},{cy + length * math.sin(a):.1f}"
        )
    return "".join(parts)


clip = squircle(512, 512)
rim = squircle(512, 498)
back = bubble(156, 196, 456, 328, 88, 244, 104, 192, 616)
front = bubble(412, 412, 456, 328, 88, 676, 104, 832, 832)

svg = f"""<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024" width="1024" height="1024">
<title>duolog: a conversation between two agents</title>
<defs>
<linearGradient id="bg" x1=".15" y1="0" x2=".85" y2="1"><stop offset="0" stop-color="#3B2F9E"/><stop offset=".55" stop-color="#1D1F63"/><stop offset="1" stop-color="#0C1036"/></linearGradient>
<radialGradient id="halo" cx=".34" cy=".38" r=".5"><stop offset="0" stop-color="#8E7BFF" stop-opacity=".55"/><stop offset="1" stop-color="#8E7BFF" stop-opacity="0"/></radialGradient>
<linearGradient id="rim" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#fff" stop-opacity=".55"/><stop offset=".45" stop-color="#fff" stop-opacity=".1"/><stop offset="1" stop-color="#fff" stop-opacity=".28"/></linearGradient>
<linearGradient id="gloss" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#fff" stop-opacity=".22"/><stop offset=".42" stop-color="#fff" stop-opacity=".04"/><stop offset="1" stop-color="#fff" stop-opacity="0"/></linearGradient>
<radialGradient id="vig" cx=".5" cy=".42" r=".78"><stop offset=".45" stop-opacity="0"/><stop offset="1" stop-opacity=".38"/></radialGradient>
<linearGradient id="warm" x1="0" y1="0" x2=".3" y2="1"><stop offset="0" stop-color="#F2B090" stop-opacity=".78"/><stop offset="1" stop-color="#C96A4A" stop-opacity=".46"/></linearGradient>
<linearGradient id="steel" x1="0" y1="0" x2=".4" y2="1"><stop offset="0" stop-color="#fff"/><stop offset=".55" stop-color="#ECEBFF"/><stop offset="1" stop-color="#B6B8EA"/></linearGradient>
<filter id="lift" x="-20%" y="-20%" width="140%" height="150%"><feDropShadow dx="0" dy="20" stdDeviation="24" flood-color="#05061F" flood-opacity=".5"/></filter>
<clipPath id="clip"><path d="{clip}"/></clipPath>
<clipPath id="inback"><path d="{back}"/></clipPath>
<clipPath id="infront"><path d="{front}"/></clipPath>
</defs>
<g clip-path="url(#clip)">
<rect width="1024" height="1024" fill="url(#bg)"/><rect width="1024" height="1024" fill="url(#halo)"/>
<path d="{back}" fill="url(#warm)"/>
<g clip-path="url(#inback)"><path d="{spark(156, 336, 118)}" fill="none" stroke="#FFF6EF" stroke-width="23" stroke-linecap="round"/></g>
<path d="{back}" fill="none" stroke="#FFD9C4" stroke-width="8" stroke-linejoin="round"/>
<path d="{front}" fill="url(#steel)" filter="url(#lift)"/>
<g clip-path="url(#infront)"><path d="M738,494l92,82-92,82" fill="none" stroke="#2A2A78" stroke-width="40" stroke-linecap="round" stroke-linejoin="round"/></g>
<rect width="1024" height="1024" fill="url(#gloss)"/><rect width="1024" height="1024" fill="url(#vig)"/>
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
