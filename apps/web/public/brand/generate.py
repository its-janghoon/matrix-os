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
# The blue ramp, darkest to lightest, used to light the faces of a solid.
BLUE_500 = "#1B4FD6"
BLUE_600 = "#12358F"

# Faceted concepts paint a solid's faces rather than a stroke, so they need a
# light-to-dark ramp instead of the single sweep. A face is emitted with two
# tokens: FACE_x for its paint and OPA_x for its group opacity. In colour the
# ramp does the shading and every opacity is 1; in one colour the paint
# collapses to currentColor and the OPACITY does the shading, so a solid still
# reads as a solid at 16px instead of flattening to a blob.
FACE_PAINT = {"A": CYAN, "B": BLUE, "C": BLUE_500, "D": BLUE_600}
FACE_MONO_OPACITY = {"A": "1", "B": "0.74", "C": "0.5", "D": "0.3"}


def facet(points, key, seam=0.6):
    """One face of a solid. The hairline stroke matching the fill closes the
    antialiasing seam that otherwise shows between abutting polygons."""
    pts = " ".join(f"{x:.2f},{y:.2f}" for x, y in points)
    return (
        f'<g opacity="OPA_{key}"><polygon points="{pts}" fill="FACE_{key}" '
        f'stroke="FACE_{key}" stroke-width="{seam}" stroke-linejoin="round"/></g>'
    )


def resolve_faces(body, mono):
    for key, paint in FACE_PAINT.items():
        body = body.replace(f"FACE_{key}", "currentColor" if mono else paint)
        body = body.replace(f"OPA_{key}", FACE_MONO_OPACITY[key] if mono else "1")
    return body

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



# --- angular / faceted concepts (round 2) -----------------------------------
# Round 1 was drawn in rounded strokes, which reads as developer-tooling rather
# than as an L1. These four are cut in the register the genre actually uses:
# faceted solids, hexagons and isometric blocks, mitred corners, no round caps.
# None of them copies the Ethereum octahedron - a four-sided faceted diamond is
# that logo, so the solids here are hexagons, a cube and a letterform instead.


def _hexagon(cx, cy, r):
    """Pointy-top hexagon: a vertex at 12 o'clock, flats on the left and right."""
    return [polar(cx, cy, r, a) for a in (-90, -30, 30, 90, 150, 210)]


def concept_block(slug):
    """An isometric cube: the block, drawn as the unit it is.

    Three faces off one blue ramp. The top face carries a coral inset, so the
    newest block reads as the lit one.
    """
    w, rh, side = 11.0, 6.35, 9.0
    cx = 16.0
    ty = 16.0 - side / 2                     # centre of the top rhombus
    n = (cx, ty - rh)
    e = (cx + w, ty)
    ss = (cx, ty + rh)                       # the cube's near vertical edge, top
    wv = (cx - w, ty)
    e2 = (cx + w, ty + side)
    s2 = (cx, ty + rh + side)
    w2 = (cx - w, ty + side)
    inset = 0.42                             # coral rhombus, as a share of the top face
    top_inset = [
        (cx, ty - rh * inset), (cx + w * inset, ty),
        (cx, ty + rh * inset), (cx - w * inset, ty),
    ]
    body = (
        facet([wv, ss, s2, w2], "C")         # left face, in shadow
        + facet([e, ss, s2, e2], "B")        # right face
        + facet([n, e, ss, wv], "A")         # top face, lit
        + f'<polygon points="{" ".join(f"{x:.2f},{y:.2f}" for x, y in top_inset)}" fill="ACCENT"/>'
    )
    return body, "An isometric block, lit on the newest face."


def concept_hex_quorum(slug):
    """The quorum in hexagonal geometry: six validators as six discrete edges.

    Round 1 drew this as a circular arc. Here the six edges make the validator
    set countable, and five are lit because a six-validator set commits on five
    - a quorum is strictly more than two thirds, not exactly two thirds.
    """
    cx = cy = 16.0
    v = _hexagon(cx, cy, 12.0)
    trim = 0.55                              # notch the vertices, do not sever them
    segs = []
    for i in range(6):
        x1, y1 = v[i]
        x2, y2 = v[(i + 1) % 6]
        dx, dy = x2 - x1, y2 - y1
        ln = math.hypot(dx, dy)
        ux, uy = dx / ln, dy / ln
        a = (x1 + ux * trim, y1 + uy * trim)
        b = (x2 - ux * trim, y2 - uy * trim)
        lit = i != 5                         # the one validator that has not voted
        segs.append(
            f'<path d="M{a[0]:.2f} {a[1]:.2f} L{b[0]:.2f} {b[1]:.2f}" fill="none" '
            f'stroke="url(#{slug}-g)" stroke-width="3.1"'
            + ("" if lit else ' stroke-opacity="0.46"')
            + "/>"
        )
    cr = 4.0
    core = [(cx, cy - cr), (cx + cr, cy), (cx, cy + cr), (cx - cr, cy)]
    body = "".join(segs) + (
        f'<polygon points="{" ".join(f"{x:.2f},{y:.2f}" for x, y in core)}" fill="ACCENT"/>'
    )
    return body, "Five of six validators carrying a block."


def _stroke_quad(p, q, t, ext_p=0.0, ext_q=0.0):
    """One stroke of a letterform as an explicit quad, offset t/2 either side.

    Ends are extended along the stroke so abutting strokes overlap at the joins;
    with a different tint per stroke those overlaps read as facet seams, which
    is the intent. Returns the four corners in order.
    """
    dx, dy = q[0] - p[0], q[1] - p[1]
    ln = math.hypot(dx, dy)
    ux, uy = dx / ln, dy / ln
    nx, ny = -uy, ux
    a = (p[0] - ux * ext_p, p[1] - uy * ext_p)
    b = (q[0] + ux * ext_q, q[1] + uy * ext_q)
    h = t / 2
    return [
        (a[0] + nx * h, a[1] + ny * h), (b[0] + nx * h, b[1] + ny * h),
        (b[0] - nx * h, b[1] - ny * h), (a[0] - nx * h, a[1] - ny * h),
    ]


def concept_facet_m(slug):
    """Round 1's lattice M, re-cut as a four-facet crystal.

    Same letter, but each of the four strokes is a flat facet off one blue ramp
    instead of a rounded polyline, so the M lights like a cut stone. Every
    stroke is the same width by construction - an earlier version picked inner
    vertices by hand and the legs came out lighter than the diagonals.
    """
    t = 4.6
    x_l, x_r = 6.4, 25.6
    y_top, y_bot = 7.4, 26.2
    valley = (16.0, 18.4)
    e = t / 2
    strokes = [
        # Only the ends that meet ANOTHER stroke are extended. Extending a
        # diagonal's outer end too pushed it past the leg it joins, leaving a
        # nub sticking up out of each apex that read as a rendering glitch.
        (_stroke_quad((x_l, y_bot), (x_l, y_top), t, 0.0, e), "C"),   # left leg
        (_stroke_quad((x_l, y_top), valley, t, 0.0, e), "B"),         # left diagonal
        (_stroke_quad(valley, (x_r, y_top), t, e, 0.0), "A"),         # right diagonal
        (_stroke_quad((x_r, y_top), (x_r, y_bot), t, e, 0.0), "B"),   # right leg
    ]
    # Guard the 2px safe margin: a mis-set extension silently pushes a corner
    # off-canvas, which only shows up once the mark is rendered.
    xs = [x for quad, _ in strokes for x, _ in quad]
    ys = [y for quad, _ in strokes for _, y in quad]
    assert 2.0 <= min(xs) and max(xs) <= 30.0, f"facet-m x out of bounds: {min(xs):.2f}..{max(xs):.2f}"
    assert 2.0 <= min(ys) and max(ys) <= 30.0, f"facet-m y out of bounds: {min(ys):.2f}..{max(ys):.2f}"

    d = 3.3
    vy = valley[1] + 2.2
    node = [(16.0, vy - d), (16.0 + d, vy), (16.0, vy + d), (16.0 - d, vy)]
    body = "".join(facet(q, k) for q, k in strokes) + (
        f'<polygon points="{" ".join(f"{x:.2f},{y:.2f}" for x, y in node)}" fill="ACCENT"/>'
    )
    return body, "The M cut as four facets off one ramp."


def concept_shard(slug):
    """A hexagonal stone cut into six wedges, lit from the upper right.

    The one concept with no accent colour: six facets off a single blue ramp.
    An earlier cut (crown triangle over a rectangular girdle) read as a house,
    a roof on a box; wedges from the centre read as a cut stone and keep the
    silhouette a solid hexagon at favicon size.
    """
    cx = cy = 16.0
    v = _hexagon(cx, cy, 12.6)
    # Ramp around the hexagon so the light sits upper-right and the shadow
    # lower-left, which is what makes a flat shape read as a solid.
    keys = ["A", "B", "C", "D", "C", "B"]
    body = "".join(
        facet([(cx, cy), v[i], v[(i + 1) % 6]], keys[i], seam=0.7)
        for i in range(6)
    )
    return body, "A hexagonal stone, cut in six facets."


CONCEPTS = [
    # Round 2: angular / faceted, the register the crypto genre uses.
    ("block", "Block", concept_block),
    ("hex-quorum", "Hex Quorum", concept_hex_quorum),
    ("facet-m", "Facet M", concept_facet_m),
    ("shard", "Shard", concept_shard),
    # Round 1: rounded strokes, kept for reference.
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
        + resolve_faces(body.replace("ACCENT", CORAL), mono=False)
        + "</svg>\n"
    )


def mark_mono_svg(slug, name, body):
    mono = body.replace(f"url(#{slug}-g)", "currentColor").replace("ACCENT", "currentColor")
    mono = mono.replace(f'stroke="{CYAN}"', 'stroke="currentColor"')
    mono = mono.replace(f'stroke="{BLUE}"', 'stroke="currentColor"')
    mono = resolve_faces(mono, mono=True)
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
        + resolve_faces(body.replace("ACCENT", CORAL), mono=False)
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
