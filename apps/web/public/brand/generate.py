#!/usr/bin/env python3
"""
Generates the Matrix OS / ECIR Labs logo concept SVGs.

Geometry for each concept is defined ONCE here and emitted in three variants, so
a mark, its monochrome sibling, and its lockups can never drift apart:

  mark.svg        32x32, brand gradient, for a dark canvas
  mark-mono.svg   32x32, currentColor, for favicons/print/light backgrounds
  lockup-*.svg    mark + wordmark, one per brand name

All marks live inside a 28x28 optical area on a 32x32 canvas (a 2px safe
margin), so any two concepts drop into the same slot at the same visual weight.
"""
import math, os, re

# Design tokens, copied from apps/web/tailwind.config.ts.
NAVY = "#060A16"
BLUE = "#2E6BFF"
CYAN = "#22D3EE"
CORAL = "#FF7A66"
WHITE = "#FFFFFF"
GRAY400 = "#8B96AC"

OUT = os.environ.get("BRAND_OUT", ".")

# The site's signature sweep is linear-gradient(101deg, #2E6BFF, #22D3EE).
# 101deg in CSS (0deg = up, clockwise) maps to this SVG vector on a 32x32 box.
_a = math.radians(101 - 90)
GRAD_X1, GRAD_Y1 = 0.5 - math.cos(_a) / 2, 0.5 + math.sin(_a) / 2
GRAD_X2, GRAD_Y2 = 0.5 + math.cos(_a) / 2, 0.5 - math.sin(_a) / 2


def grad(gid):
    return (
        f'<linearGradient id="{gid}" x1="{GRAD_X1:.4f}" y1="{GRAD_Y1:.4f}" '
        f'x2="{GRAD_X2:.4f}" y2="{GRAD_Y2:.4f}">'
        f'<stop offset="0" stop-color="{BLUE}"/>'
        f'<stop offset="1" stop-color="{CYAN}"/>'
        f"</linearGradient>"
    )


def polar(cx, cy, r, deg):
    """Screen-space polar: 0deg = right, angles increase clockwise (y grows down)."""
    t = math.radians(deg)
    return cx + r * math.cos(t), cy + r * math.sin(t)


# --- concepts ---------------------------------------------------------------
# Each concept is a function (slug) -> (body, notes). `body` is SVG that may
# reference two paint sources:
#   url(#<slug>-g)  the brand gradient
#   ACCENT          the third-colour highlight (coral), swapped out in mono
# Mono rendering replaces both with currentColor, so a concept's silhouette is
# identical across variants and only the paint changes.

def concept_quorum(slug):
    cx = cy = 16.0
    r = 11.0
    # A block commits on a >2/3 quorum, so the lit arc is exactly 240 of 360
    # degrees: the proportion IS the idea, not a decorative sweep.
    start, sweep = -90.0, 240.0
    x1, y1 = polar(cx, cy, r, start)
    x2, y2 = polar(cx, cy, r, start + sweep)
    large = 1 if sweep > 180 else 0
    blk = 4.9  # side of the committed-block diamond
    body = (
        # The full validator set. The third that has not voted is the same ring
        # held back, so it is the brand paint at low opacity rather than a second
        # hue: a coral dim ring muddies to brown against the blue arc.
        f'<circle cx="{cx}" cy="{cy}" r="{r}" fill="none" stroke="url(#{slug}-g)" '
        f'stroke-opacity="0.22" stroke-width="3.4"/>'
        # The quorum that carried the block.
        f'<path d="M{x1:.3f} {y1:.3f} A{r} {r} 0 {large} 1 {x2:.3f} {y2:.3f}" '
        f'fill="none" stroke="url(#{slug}-g)" stroke-width="3.4" stroke-linecap="round"/>'
        # The committed block: a diamond, not a dot, so the mark does not read
        # as an eye or a power button.
        f'<rect x="{cx - blk / 2:.2f}" y="{cy - blk / 2:.2f}" width="{blk}" height="{blk}" '
        f'rx="1" fill="ACCENT" transform="rotate(45 {cx} {cy})"/>'
    )
    return body, "A >2/3 quorum closing over a committed block."


def concept_lattice_m(slug):
    pts = [(6.5, 25.0), (6.5, 8.0), (16.0, 18.5), (25.5, 8.0), (25.5, 25.0)]
    d = "M" + " L".join(f"{x:.1f} {y:.1f}" for x, y in pts)
    dots = "".join(
        f'<circle cx="{x:.1f}" cy="{y:.1f}" r="2.7" fill="{"ACCENT" if i == 1 else f"url(#{slug}-g)"}"/>'
        for i, (x, y) in enumerate([pts[1], pts[2], pts[3]])
    )
    body = (
        f'<path d="{d}" fill="none" stroke="url(#{slug}-g)" stroke-width="3.2" '
        f'stroke-linecap="round" stroke-linejoin="round"/>{dots}'
    )
    return body, "An M drawn as a peer graph: five nodes, four links."


def concept_ecir_e(slug):
    t = 3.6           # bar thickness
    x0, y0 = 6.2, 6.2
    h, w, wm = 19.6, 19.6, 13.2
    r = t / 2
    body = (
        f'<rect x="{x0}" y="{y0}" width="{t}" height="{h}" rx="{r}" fill="url(#{slug}-g)"/>'
        f'<rect x="{x0}" y="{y0}" width="{w}" height="{t}" rx="{r}" fill="url(#{slug}-g)"/>'
        f'<rect x="{x0}" y="{y0 + (h - t) / 2:.2f}" width="{wm}" height="{t}" rx="{r}" fill="ACCENT"/>'
        f'<rect x="{x0}" y="{y0 + h - t:.2f}" width="{w}" height="{t}" rx="{r}" fill="url(#{slug}-g)"/>'
    )
    return body, "An E monogram for ECIR, built on one geometric grid."


def concept_aperture(slug):
    """A square frame broken at the corners, closing on a running core.

    Two earlier versions were rejected at render time. A fan-out (one job
    splitting to three providers) read as the system share icon, far too
    established a glyph to use as a logo. Replacing it with four bars in
    90-degree ROTATIONAL symmetry gave each side a tangential offset, and a
    four-fold pinwheel of bars can read as a swastika, so the rotational bias is
    gone: every bar is centred on its side and the symmetry is reflective.
    """
    t = 3.6
    r = t / 2
    ln = 15.0                  # bar length
    lo = 16.0 - ln / 2         # tangential start, centred on the side
    near, far = 5.4, 26.6      # the two bar offsets from the canvas edges
    body = (
        f'<rect x="{lo}" y="{near - r}" width="{ln}" height="{t}" rx="{r}" fill="url(#{slug}-g)"/>'
        f'<rect x="{far - r}" y="{lo}" width="{t}" height="{ln}" rx="{r}" fill="url(#{slug}-g)"/>'
        f'<rect x="{lo}" y="{far - r}" width="{ln}" height="{t}" rx="{r}" fill="url(#{slug}-g)"/>'
        f'<rect x="{near - r}" y="{lo}" width="{t}" height="{ln}" rx="{r}" fill="url(#{slug}-g)"/>'
        # The running core the frame closes on.
        f'<rect x="{16 - 2.7}" y="{16 - 2.7}" width="5.4" height="5.4" rx="1" '
        f'fill="ACCENT" transform="rotate(45 16 16)"/>'
    )
    return body, "A square aperture closing on a running core."


CONCEPTS = [
    ("quorum", "Quorum", concept_quorum),
    ("lattice-m", "Lattice M", concept_lattice_m),
    ("ecir-e", "ECIR E", concept_ecir_e),
    ("aperture", "Aperture", concept_aperture),
]

HEADER = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {w} {h}" width="{w}" height="{h}" fill="none" role="img" aria-label="{label}">'


def mark_svg(slug, name, body):
    return (
        HEADER.format(w=32, h=32, label=f"{name} mark")
        + f"<defs>{grad(slug + '-g')}</defs>"
        + body.replace("ACCENT", CORAL)
        + "</svg>\n"
    )


def mark_mono_svg(slug, name, body):
    mono = body.replace(f"url(#{slug}-g)", "currentColor").replace("ACCENT", "currentColor")
    mono = mono.replace(f'stroke="{CYAN}"', 'stroke="currentColor"')
    mono = mono.replace(f'stroke="{BLUE}"', 'stroke="currentColor"')
    # The dim-ring device only reads in colour; in one colour it must still show
    # the unvoted third, so keep it as a reduced-opacity currentColor stroke.
    return (
        HEADER.format(w=32, h=32, label=f"{name} mark, monochrome")
        + mono
        + "</svg>\n"
    )


def lockup_svg(slug, name, body, head, tail, tail_color, tail_weight, tail_tracking, wordmark_label):
    """Mark + wordmark. The wordmark is live text in Inter (which the site already
    loads via next/font), not outlines: crisp at every size and editable. Outline
    it in a vector editor before handing the file to a third party or a printer."""
    mark_size, gap = 34.0, 12.0
    text_x = mark_size + gap
    baseline = 30.0
    total_h = 44.0
    # Advance estimate for Inter at 26px/700, used only to size the canvas.
    adv = 0.60 * 26
    head_w = len(head) * adv
    tail_w = len(tail) * adv * 0.98 + (len(tail) * 1.4 if tail_tracking else 0)
    total_w = text_x + head_w + (gap * 0.55 + tail_w if tail else 0) + 4
    tail_el = ""
    if tail:
        tail_el = (
            f'<text x="{text_x + head_w + gap * 0.55:.1f}" y="{baseline}" '
            f'font-family="Inter, ui-sans-serif, system-ui, -apple-system, Segoe UI, Helvetica, Arial, sans-serif" '
            f'font-size="26" font-weight="{tail_weight}" fill="{tail_color}" '
            f'letter-spacing="{tail_tracking}">{tail}</text>'
        )
    return (
        HEADER.format(w=round(total_w), h=round(total_h), label=wordmark_label)
        + f"<defs>{grad(slug + '-g')}</defs>"
        + f'<g transform="translate(0 {(total_h - mark_size) / 2:.1f}) scale({mark_size / 32:.5f})">'
        + body.replace("ACCENT", CORAL)
        + "</g>"
        + f'<text x="{text_x}" y="{baseline}" '
        f'font-family="Inter, ui-sans-serif, system-ui, -apple-system, Segoe UI, Helvetica, Arial, sans-serif" '
        f'font-size="26" font-weight="700" fill="{WHITE}" letter-spacing="-0.5">{head}</text>'
        + tail_el
        + "</svg>\n"
    )


_NUM = re.compile(r"\d+\.\d{3,}")


def tidy(svg):
    """Round long float tails (0.1+0.2 artefacts) so shipped assets read cleanly."""
    return _NUM.sub(lambda m: f"{float(m.group()):.2f}".rstrip("0").rstrip("."), svg)


def write(path, text):
    text = tidy(text)
    full = os.path.join(OUT, path)
    os.makedirs(os.path.dirname(full), exist_ok=True)
    with open(full, "w", encoding="utf-8") as f:
        f.write(text)
    return path


written = []
for slug, name, fn in CONCEPTS:
    body, note = fn(slug)
    written.append(write(f"{slug}/mark.svg", mark_svg(slug, name, body)))
    written.append(write(f"{slug}/mark-mono.svg", mark_mono_svg(slug, name, body)))
    written.append(write(
        f"{slug}/lockup-matrix-os.svg",
        lockup_svg(slug, name, body, "Matrix", "OS", CYAN, 700, "0.6", "Matrix OS"),
    ))
    written.append(write(
        f"{slug}/lockup-ecir-labs.svg",
        lockup_svg(slug, name, body, "ECIR", "LABS", GRAY400, 500, "2.2", "ECIR Labs"),
    ))

for p in written:
    print(p)
