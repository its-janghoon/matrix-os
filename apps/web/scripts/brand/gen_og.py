#!/usr/bin/env python3
"""Renders public/og-image.png (1200x630) from the chosen brand mark.

  python3 gen_og.py [slug]

og:image has to be a raster: Twitter, Facebook and Slack do not render an SVG
og:image, which is what the site shipped before. Inter is embedded as a base64
@font-face so the wordmark is real Inter rather than whatever sans the rendering
box happens to have - an SVG or HTML referencing a font by name renders in the
crawler's fallback, not yours.
"""
import base64, os, subprocess, sys, tempfile

SLUG = "shard"
FONT_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), ".fonts")
args = list(sys.argv[1:])
if args and not args[0].startswith("--"):
    SLUG = args.pop(0)

# Paths are derived from this file so the scripts run from any checkout.
WEB = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
PUBLIC = os.path.join(WEB, "public")
CHROME = "/opt/pw-browsers/chromium"
W, H = 1200, 630
# --window-size is the OUTER window; headless Chromium takes its chrome out of
# the layout viewport, so render into a generous window and crop the top-left.
PAD = 260
NAVY, INK, MUTED, CYAN = "#060A16", "#FFFFFF", "#8B96AC", "#22D3EE"

svg = open(os.path.join(PUBLIC, "brand", SLUG, "mark.svg"), encoding="utf-8").read()
svg = svg.replace(' width="32" height="32"', "", 1)

WEIGHTS = (400, 600, 700)


def ensure_inter():
    """Cache the Inter TTFs next to this script.

    The wordmark has to be embedded as a base64 @font-face: a crawler rendering
    the card has no access to the site's next/font copy of Inter, and naming the
    family in CSS would silently fall back to its own sans. The TTFs are fetched
    rather than committed so ~1MB of binary stays out of the repo.
    """
    os.makedirs(FONT_DIR, exist_ok=True)
    missing = [w for w in WEIGHTS if not os.path.exists(os.path.join(FONT_DIR, f"Inter-{w}.ttf"))]
    if not missing:
        return
    import re as _re, urllib.request
    css_url = ("https://fonts.googleapis.com/css2?family=Inter:wght@"
               + ";".join(map(str, WEIGHTS)) + "&display=swap")
    req = urllib.request.Request(css_url, headers={"User-Agent": "Mozilla/5.0"})
    css = urllib.request.urlopen(req, timeout=30).read().decode()
    urls = {}
    for block in _re.findall(r"@font-face\s*\{(.*?)\}", css, _re.S):
        w = _re.search(r"font-weight:\s*(\d+)", block)
        u = _re.search(r"url\((https://[^)]+)\)", block)
        if w and u:
            urls[int(w.group(1))] = u.group(1)
    for w in missing:
        if w not in urls:
            raise SystemExit(f"Google Fonts did not offer Inter weight {w}")
        urllib.request.urlretrieve(urls[w], os.path.join(FONT_DIR, f"Inter-{w}.ttf"))
        print(f"  cached Inter-{w}.ttf")


ensure_inter()

faces = ""
for weight in WEIGHTS:
    path = os.path.join(FONT_DIR, f"Inter-{weight}.ttf")
    b64 = base64.b64encode(open(path, "rb").read()).decode()
    faces += (
        "@font-face{font-family:'Inter';font-style:normal;font-weight:%d;"
        "src:url(data:font/ttf;base64,%s) format('truetype')}" % (weight, b64)
    )

# Everything sits inside one explicitly sized .card rather than being
# positioned against <body>. An earlier version anchored the bottom rule to the
# body and it silently did not paint; a single fixed-size containing block
# removes the ambiguity, and the pixel assertions below catch a regression.
html = f"""<!doctype html><meta charset="utf-8"><style>
{faces}
html,body{{margin:0;padding:0;background:{NAVY}}}
.card{{position:relative;width:{W}px;height:{H}px;overflow:hidden;background:{NAVY};
  font-family:'Inter',sans-serif;color:{INK}}}
/* The same mesh + grid the site's hero uses, so the card reads as the site. */
.mesh,.grid{{position:absolute;inset:0}}
.mesh{{background:
  radial-gradient(60% 60% at 12% 18%, rgba(46,107,255,.30) 0%, rgba(46,107,255,0) 60%),
  radial-gradient(50% 50% at 88% 12%, rgba(34,211,238,.22) 0%, rgba(34,211,238,0) 55%),
  radial-gradient(55% 55% at 78% 88%, rgba(110,155,255,.16) 0%, rgba(110,155,255,0) 60%)}}
.grid{{opacity:.55;background-image:
  linear-gradient(to bottom, rgba(255,255,255,.035) 1px, transparent 1px),
  linear-gradient(to right, rgba(255,255,255,.035) 1px, transparent 1px);
  background-size:48px 48px}}
/* An oversized mark bled off the right edge balances the left-set type. */
.ghost{{position:absolute;right:-96px;top:50%;transform:translateY(-50%);
  width:520px;height:520px;opacity:.16}}
.ghost svg{{display:block;width:100%;height:100%}}
.inner{{position:absolute;left:96px;top:50%;transform:translateY(-50%);right:400px}}
.lock{{display:flex;align-items:center;gap:22px;margin-bottom:36px}}
.lock svg{{display:block;width:76px;height:76px}}
.wm{{font-size:52px;font-weight:700;letter-spacing:-.025em;line-height:1}}
.wm i{{font-style:normal;color:{CYAN}}}
h1{{margin:0;font-size:60px;font-weight:700;letter-spacing:-.03em;line-height:1.06}}
p{{margin:26px 0 0;font-size:25px;font-weight:400;color:{MUTED};line-height:1.4}}
.rule{{position:absolute;left:0;bottom:0;width:{W}px;height:8px;
  background:linear-gradient(101deg,#2E6BFF 0%,#22D3EE 100%)}}
</style>
<div class="card">
  <div class="mesh"></div><div class="grid"></div>
  <div class="ghost">{svg}</div>
  <div class="inner">
    <div class="lock">{svg}<div class="wm">Matrix <i>OS</i></div></div>
    <h1>A peer-to-peer marketplace for compute.</h1>
    <p>Native MATRIX settles every job through leader-based BFT consensus on one global ledger.</p>
  </div>
  <div class="rule"></div>
</div>
"""

with tempfile.NamedTemporaryFile("w", suffix=".html", delete=False, encoding="utf-8") as f:
    f.write(html)
    page = f.name
out = os.path.join(PUBLIC, "og-image.png")
raw = out + ".raw.png"
try:
    subprocess.run(
        [CHROME, "--headless", "--disable-gpu", "--no-sandbox", "--hide-scrollbars",
         "--force-device-scale-factor=1", f"--window-size={W + PAD},{H + PAD}",
         "--virtual-time-budget=3000", f"--screenshot={raw}", page],
        check=True, capture_output=True)
finally:
    os.unlink(page)

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import pngtool

rw, rh, rpx = pngtool.decode(raw)
px = pngtool.crop(rw, rh, rpx, 0, 0, W, H)
pngtool.encode(W, H, px, out)
os.unlink(raw)
gw, gh = W, H


def rgb(x, y):
    i = (y * gw + x) * 4
    return tuple(px[i:i + 3])


def region(x0, y0, x1, y1, test, step=3):
    """Count pixels in a rect satisfying `test`. Region checks rather than
    single-pixel probes, so a layout tweak does not make the guard lie."""
    return sum(
        1
        for y in range(y0, y1, step)
        for x in range(x0, x1, step)
        if test(rgb(x, y))
    )


# The gradient rule, the white headline and the ghost mark have each silently
# failed to paint during this file's development, so each is verified.
assert sum(rgb(600, H - 4)) > 200, f"bottom gradient rule did not paint: {rgb(600, H - 4)}"

white = region(90, 200, 800, 500, lambda c: min(c) > 200)
assert white > 400, f"headline type did not paint (only {white} near-white px)"

ghost = region(950, 150, 1190, 500, lambda c: c != (6, 10, 22) and sum(c) > 40)
assert ghost > 200, f"ghost mark did not paint (only {ghost} lit px)"

print(f"og-image.png  {gw}x{gh}  {os.path.getsize(out)} bytes  (mark: brand/{SLUG})")
print(f"  rule={rgb(600, H - 4)}  headline={white} px  ghost={ghost} px")
