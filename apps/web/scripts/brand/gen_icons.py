#!/usr/bin/env python3
"""Re-exports the site's PNG app icons from the chosen brand mark.

  python3 gen_icons.py [slug]        # default: shard

Renders two 512x512 masters through headless Chromium, then resamples every
shipped size from them with pngtool (stdlib only). It does NOT ask Chromium for
the small sizes directly: Chromium clamps both the viewport and
--force-device-scale-factor, so a 16x16 screenshot request comes back blank and
mid sizes come back clipped. Render big once, resample down.

Two families, because they are composited differently:

  favicon-*   transparent, mark filling the canvas. The mark's own 2/32 safe
              margin is the breathing room a favicon needs.
  app icons   opaque navy square with the mark inset, for iOS home screens,
              Android launchers and Windows tiles, none of which composite onto
              a page background.
"""
import os, struct, subprocess, sys, tempfile
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import pngtool

SLUG = sys.argv[1] if len(sys.argv) > 1 else "shard"
# Paths are derived from this file so the scripts run from any checkout.
WEB = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
PUBLIC = os.path.join(WEB, "public")
MARK = os.path.join(PUBLIC, "brand", SLUG, "mark.svg")
CHROME = "/opt/pw-browsers/chromium"
NAVY = "#060A16"          # tailwind.config.ts `background`
MASTER = 512              # master render size; every icon is resampled from it
# --window-size is the OUTER window; headless Chromium takes its chrome out of
# the layout viewport (measured: 87px of height), which is what made small
# direct renders come back blank and mid sizes come back clipped. Render into a
# generous window and crop the top-left instead of trusting that constant.
PAD = 260
APP_INSET = 0.72          # share of the canvas the mark occupies on an app icon

TRANSPARENT = {"favicon-16x16.png": 16, "favicon-32x32.png": 32, "favicon-96x96.png": 96}
OPAQUE = {
    "android-icon-36x36.png": 36, "android-icon-48x48.png": 48,
    "android-icon-72x72.png": 72, "android-icon-96x96.png": 96,
    "android-icon-144x144.png": 144, "android-icon-192x192.png": 192,
    "android-icon-512x512.png": 512,            # new: PWA installability
    "apple-icon-57x57.png": 57, "apple-icon-60x60.png": 60,
    "apple-icon-72x72.png": 72, "apple-icon-76x76.png": 76,
    "apple-icon-114x114.png": 114, "apple-icon-120x120.png": 120,
    "apple-icon-144x144.png": 144, "apple-icon-152x152.png": 152,
    "apple-icon-180x180.png": 180,
    "apple-icon.png": 192, "apple-icon-precomposed.png": 192,
    "apple-touch-icon.png": 180,                # new: the name layout.tsx asks for
    "ms-icon-70x70.png": 70, "ms-icon-144x144.png": 144,
    "ms-icon-150x150.png": 150, "ms-icon-310x310.png": 310,
}
ICO_SIZES = [16, 32, 48]

svg = open(MARK, encoding="utf-8").read()
# Drop the intrinsic size so CSS controls it; the viewBox still scales the art.
svg_fluid = svg.replace(' width="32" height="32"', "", 1)
assert svg_fluid != svg, f"{MARK}: expected a width/height pair to strip"


def render_master(opaque, out):
    raw = out + ".raw.png"
    inner = round(MASTER * APP_INSET) if opaque else MASTER
    html = (
        '<!doctype html><meta charset="utf-8"><style>'
        f"html,body{{margin:0;padding:0;width:{MASTER}px;height:{MASTER}px;overflow:hidden;"
        f"{'background:' + NAVY + ';' if opaque else ''}}}"
        "body{display:flex;align-items:center;justify-content:center}"
        f"svg{{display:block;width:{inner}px;height:{inner}px}}"
        f"</style>{svg_fluid}"
    )
    with tempfile.NamedTemporaryFile("w", suffix=".html", delete=False, encoding="utf-8") as f:
        f.write(html)
        page = f.name
    try:
        cmd = [CHROME, "--headless", "--disable-gpu", "--no-sandbox", "--hide-scrollbars",
               "--force-device-scale-factor=1",
               f"--window-size={MASTER + PAD},{MASTER + PAD}",
               "--virtual-time-budget=2000", f"--screenshot={raw}", page]
        if not opaque:
            cmd.insert(1, "--default-background-color=00000000")
        subprocess.run(cmd, check=True, capture_output=True)
    finally:
        os.unlink(page)
    rw, rh, rpx = pngtool.decode(raw)
    px = pngtool.crop(rw, rh, rpx, 0, 0, MASTER, MASTER)
    pngtool.encode(MASTER, MASTER, px, out)
    os.unlink(raw)
    w = h = MASTER
    # A blank master is the exact failure this script exists to avoid, so check
    # that the art actually painted rather than trusting the screenshot.
    if opaque:
        assert tuple(px[(MASTER // 2 * MASTER + MASTER // 2) * 4:][:3]) != (6, 10, 22), \
            "opaque master centre is still the background: art did not paint"
    else:
        assert px[(MASTER // 2 * MASTER + MASTER // 2) * 4 + 3] > 250, \
            "transparent master centre is transparent: art did not paint"
    return w, h, px


def write_ico(pngs, out):
    """A PNG-payload .ico (Vista+). Browsers request /favicon.ico by default
    whatever the markup says, so this file has to actually exist."""
    offset = 6 + 16 * len(pngs)
    entries, blobs = b"", b""
    for size, data in pngs:
        entries += struct.pack("<BBBBHHII", size, size, 0, 0, 1, 32, len(data), offset)
        blobs += data
        offset += len(data)
    with open(out, "wb") as f:
        f.write(struct.pack("<HHH", 0, 1, len(pngs)) + entries + blobs)


with tempfile.TemporaryDirectory() as tmp:
    for opaque, targets in ((False, TRANSPARENT), (True, OPAQUE)):
        master = os.path.join(tmp, f"master-{'opaque' if opaque else 'alpha'}.png")
        w, h, px = render_master(opaque, master)
        for name, size in sorted(targets.items(), key=lambda kv: kv[1]):
            dest = os.path.join(PUBLIC, name)
            pngtool.encode(size, size, pngtool.resize(w, h, px, size, size), dest)
            print(f"  {name:32} {size}x{size} {'navy' if opaque else 'transparent'}")

    # favicon.ico reuses the alpha master rather than the written favicons, so
    # its 48px entry does not depend on a 48px file the site does not ship.
    w, h, px = pngtool.decode(os.path.join(tmp, "master-alpha.png"))
    payload = []
    for size in ICO_SIZES:
        p = os.path.join(tmp, f"ico-{size}.png")
        pngtool.encode(size, size, pngtool.resize(w, h, px, size, size), p)
        payload.append((size, open(p, "rb").read()))
    write_ico(payload, os.path.join(PUBLIC, "favicon.ico"))
    print(f"  {'favicon.ico':32} {'/'.join(map(str, ICO_SIZES))} transparent")

with open(os.path.join(PUBLIC, "favicon.svg"), "w", encoding="utf-8") as f:
    f.write(svg)
print(f"  {'favicon.svg':32} vector")
print(f"\n{len(TRANSPARENT) + len(OPAQUE) + 2} files written from brand/{SLUG}/mark.svg")
