# Brand marks: logo concepts

Four logo concepts for the Matrix OS / ECIR Labs identity. **Nothing here is wired
into the site yet** - these are candidates. Pick one, then follow
[Wiring the chosen mark](#wiring-the-chosen-mark) below.

Every mark is drawn from the design tokens already in
[`apps/web/tailwind.config.ts`](../../tailwind.config.ts), so a chosen mark needs
no new colour added to the system:

| Role | Token | Value |
| --- | --- | --- |
| Canvas | `background` | `#060A16` |
| Primary | `primary.DEFAULT` | `#2E6BFF` |
| Secondary | `secondary.DEFAULT` | `#22D3EE` |
| Accent | `accent.DEFAULT` | `#9B6CF9` |
| Sweep | `decorative-1` | `linear-gradient(101deg, #2E6BFF, #22D3EE)` |

## The concepts

All four are cut in the register the crypto/L1 genre actually uses: faceted
solids, hexagons, isometric blocks, mitred corners, no round caps.

**None of these copies the Ethereum mark.** A four-sided faceted diamond *is* that
logo, so the solids here are a hexagon, a cube and a letterform instead - the
genre's vocabulary without its most recognisable shape.

| Slug | Name | What it says |
| --- | --- | --- |
| `block` | Block | An isometric block, lit on the newest face. Three faces off one blue ramp, accent inset on the lit top. |
| `hex-quorum` | Hex Quorum | Six validators as six countable edges, five of them lit - five, not four, because a six-validator set commits on *more* than two thirds. |
| `facet-m` | Facet M | The M re-cut as four flat facets, each stroke the same width by construction. |
| `shard` | Shard | A hexagonal stone cut in six wedges, lit from the upper right. The one concept with no accent colour. |

## Files per concept

```
<slug>/mark.svg               32x32, brand gradient, for a dark canvas
<slug>/mark-mono.svg          32x32, currentColor - favicons, print, either ground
<slug>/lockup-matrix-os.svg   mark + "Matrix OS"
<slug>/lockup-ecir-labs.svg   mark + "ECIR LABS"
```

Every mark sits inside a 28x28 optical area on a 32x32 canvas (a 2px safe
margin), so any two concepts drop into the same slot at the same visual weight.

`mark-mono.svg` paints with `currentColor`, so it inherits the surrounding text
colour - drop it in a dark header and it is white; drop it on a light page and it
is ink. Nothing to swap per theme.

The faceted concepts (`block`, `facet-m`, `shard`) shade their faces off a blue
ramp in colour. In one colour the paint collapses to `currentColor` and the face
**opacity** carries the shading instead, so a solid still reads as a solid at
16px rather than flattening to a blob.

## Wordmark caveat

The lockups set the wordmark as **live text in Inter**, not outlines, so the lockup
files are **not self-contained**: opened anywhere without Inter they fall back down
the declared stack and the spacing shifts.

**That includes this site.** An earlier version of this file claimed the lockups
were safe here because Inter is loaded via `next/font` - that is wrong. An SVG
referenced through `<img>` or `next/image` renders in an *isolated* context: it
cannot reach the document's fonts or `currentColor`. Dropping
`lockup-matrix-os.svg` into the header would render the wordmark in whatever
system sans the visitor has.

So:

- **In the site**, use `BrandMark` (`src/components/BrandMark.tsx`) next to real
  HTML text. That is what the header does. The text is then real Inter, real DOM,
  selectable and translatable.
- **Outside the site** - a deck, a press kit, a printer, a README badge - use a
  lockup, and outline its text once in a vector editor first.
- `mark.svg` and `mark-mono.svg` contain no text at all and are safe to send
  anywhere as-is.

## Regenerating

The generators live in [`apps/web/scripts/brand/`](../../scripts/brand). They take
paths from their own location, so they run from any checkout:

```sh
cd apps/web
python3 scripts/brand/generate.py         # the marks and lockups, into public/brand/
python3 scripts/brand/gen_icons.py shard  # 27 favicons / app icons + favicon.ico
python3 scripts/brand/gen_og.py shard     # public/og-image.png
```

`generate.py` holds the geometry for every concept and emits all their variants, so
a mark and its monochrome sibling can never drift apart. Edit the geometry there,
never the SVGs by hand.

### What got cut, and why

Eleven concepts and revisions were dropped. Each was rejected after being
*rendered* - none of these read wrong on paper, which is the whole point. This is
the record, since the code that held it has been deleted:

**A whole first round.** `quorum` (a >2/3 arc closing over a committed block),
`lattice-m` (an M as a five-node peer graph), `ecir-e` (an E monogram) and
`aperture` (a square frame closing on a core), all drawn in soft rounded strokes.
Individually fine; together they read as developer tooling rather than an L1.
`facet-m` is `lattice-m` re-cut in the angular register.

**Individual concepts:**

| Concept | Was | Why it went |
| --- | --- | --- |
| Fan-out | One job dispatched to three providers | Rendered as the system share icon - far too established a glyph to hand a brand |
| One Ledger | Nodes converging on a single bar | Three legs meeting above a bar read as a trident, not consensus |
| Bridge | Native and wrapped locked 1:1 around a shared escrow | Side by side the two squares read as a UI toggle; offset diagonally they muddied at 16px. Cut after two attempts rather than shipped weak |

**Revisions within the surviving concepts:**

| Version | Why it went |
| --- | --- |
| Aperture, four bars in 90&deg; rotational symmetry | A four-fold pinwheel of bars can read as a swastika. Rebuilt on reflective symmetry |
| Shard, crown triangle over a rectangular girdle | Read as a house - a roof on a box. Recut as six wedges from the centre |
| Facet M, outline from hand-picked inner vertices | The legs came out lighter than the diagonals, so the letter read as a clumsy slab. Every stroke is now one offset quad at a constant width |
| Facet M, diagonals extended past the legs | Left a nub poking out of each apex that read as a rendering glitch. Only ends that meet another stroke are extended now |
| Quorum, coral dim ring | Muddied to brown against the blue arc. The unlit third is now the same paint at low opacity |
| Hex Quorum, 1.35 vertex trim | Opened the vertices so wide the hexagon became scattered dashes. It notches them now instead of severing them |

Worth reading before proposing another concept. `concept_facet_m` also asserts its
own bounds, because a mis-set stroke extension pushes a corner off-canvas silently
and only shows up once rendered.

### Why there is a PNG codec in here

`gen_icons.py` and `gen_og.py` render through the headless Chromium already
installed for Playwright, and resample with `pngtool.py` (decode / area-average
resize / crop / encode, stdlib only) because this repo has no image library.

Two Chromium behaviours are worth knowing before touching these scripts, since
both fail *silently* - they produce a plausible file rather than an error:

- **`--window-size` is the outer window.** Chromium subtracts its own chrome from
  the layout viewport (measured: 87px of height). Asking for a 1200x630 window
  lays out at 1200x543 and pads the screenshot with white, which is why a bottom
  edge element vanishes. Both scripts render into a generous window and crop the
  top-left instead of trusting that number.
- **Small windows and fractional scale factors are clamped.** A direct 16x16
  screenshot comes back blank and mid sizes come back clipped;
  `--force-device-scale-factor` will not go below 0.5. So every icon is resampled
  from one 512px master rather than rendered at its final size.

Both scripts assert on the pixels they produce - that the master actually painted,
that the og-image's gradient rule, headline and ghost mark are all there - because
a blank or clipped render is exactly the failure that otherwise ships unnoticed.

## What is wired up

`shard` is the chosen mark, and it is live:

| Surface | File |
| --- | --- |
| Header logo | [`src/components/BrandMark.tsx`](../../src/components/BrandMark.tsx), inlined next to real HTML text |
| Browser tab | `favicon.svg`, `favicon.ico`, `favicon-16x16.png`, `favicon-32x32.png`, `favicon-96x96.png` |
| iOS home screen | `apple-touch-icon.png` + the `apple-icon-*` set |
| Android launcher | `android-icon-*`, including a 512 and a maskable entry in `manifest.json` |
| Windows tiles | `ms-icon-*`, `browserconfig.xml` |
| Link previews | `og-image.png` (1200x630) |

The header mark is **inlined as a component** rather than loaded from
`/brand/shard/mark.svg`: it saves a request on every page, and an SVG loaded
through `<img>` cannot inherit the page's font or `currentColor` (see the wordmark
caveat above). Its geometry is a copy of `shard/mark.svg` - regenerate the mark and
update the component's `FACES` together.

### Bugs fixed while wiring this up

- `layout.tsx` pointed at **three files that did not exist** - `/favicon.ico`,
  `/apple-touch-icon.png` and `/site.webmanifest` - so the site served no icon at
  all and no manifest. All three now exist (the manifest as its real name,
  `/manifest.json`).
- `og:image` was an **SVG**, which Twitter, Facebook and Slack do not render. It is
  now a PNG.
- `manifest.json` carried `theme_color: #0B111B` and `background_color: #ffffff`,
  and `browserconfig.xml` a white `TileColor` - all predating the navy design
  system. All three are now `#060A16`.
- `manifest.json` also still described a different company: **"ECIR Labs -
  Making Tech Uncool"**, *"an inclusive research community that comprises people
  from various backgrounds. We make tech fun and accessible."* That is what would
  have appeared in the install prompt and under the home-screen icon. It now
  matches the product, with `short_name` "Matrix OS" for the icon label.
- The maskable icon entry is the 512 app icon, which is correct rather than
  convenient: the mark's painted extent measures 58.4% of the icon as a diameter,
  inside the 80% centre circle Android guarantees, and the ground is opaque navy
  with no transparency.

### Still open

- `apps/web` has no test runner, so `BrandMark` has no component test. Adding
  jest/vitest to a Next 15 / React 19 / Tailwind v4 project risks the `next lint`
  and `next build` gates, which is why it was deferred rather than done.
