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
| Accent | `accent.DEFAULT` | `#FF7A66` |
| Sweep | `decorative-1` | `linear-gradient(101deg, #2E6BFF, #22D3EE)` |

## The concepts

| Slug | Name | What it says |
| --- | --- | --- |
| `quorum` | Quorum | A >2/3 quorum closing over a committed block. The lit arc is exactly 240 of 360 degrees, so the consensus threshold is the mark's proportion, not decoration. |
| `lattice-m` | Lattice M | An M drawn as a peer graph: five nodes, four links. Reads as both the letter and the network. |
| `ecir-e` | ECIR E | An E monogram for the company, built on one geometric grid. |
| `aperture` | Aperture | A square aperture closing on a running core: the platform framing whatever runs inside it. |

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

## Wordmark caveat

The lockups set the wordmark as **live text in Inter**, not outlines. On this site
that is the better choice: Inter is already loaded via `next/font`, so the text
stays crisp at every size and editable in place. It does mean the lockup files are
**not self-contained** - opened somewhere without Inter, they fall back down the
declared stack and the spacing shifts.

Before handing a lockup to a printer, a press kit, or any third party, outline the
text once in a vector editor. The `mark.svg` and `mark-mono.svg` files have no text
in them at all and are safe to send anywhere as-is.

## Regenerating

`generate.py` holds the geometry for all four concepts and emits every variant, so
a mark and its monochrome sibling can never drift apart. Edit the geometry there,
never the SVGs by hand:

```sh
cd apps/web/public/brand && BRAND_OUT=. python3 generate.py
```

The script records, in each concept's docstring, the versions that were cut and
why - a fan-out that rendered as the system share icon, a converging-nodes mark
that read as a trident, a two-square bridge that read as a toggle switch. Worth
reading before proposing a fifth concept.

## Wiring the chosen mark

The header logo is currently a placeholder: a gradient rounded square with the
letters `ECIR` set at 11px. In
[`src/components/Navigation.tsx`](../../src/components/Navigation.tsx):

```tsx
<div className='h-9 w-9 rounded-xl bg-decorative-1 flex items-center justify-center shadow-glow'>
  <span className='text-[11px] font-bold text-white tracking-wider'>ECIR</span>
</div>
<span className='ml-2.5 text-lg font-semibold text-white tracking-tight'>Matrix</span>
```

Replace both elements with the chosen lockup:

```tsx
import Image from 'next/image';

<Image
  src='/brand/<slug>/lockup-matrix-os.svg'
  alt='Matrix OS'
  width={168}
  height={44}
  className='h-9 w-auto'
  priority
/>
```

Then, still to do once a concept is chosen:

- **Favicons.** `public/` currently holds PNG favicons and app icons generated from
  the old mark. Re-export them from the chosen `mark-mono.svg` (or `mark.svg` for
  the coloured tiles) at the sizes already present, and update
  `manifest.json` and `browserconfig.xml` if the theme colour changes.
- **`public/og-image.svg`.** Still the old placeholder: plain text on `#111111`,
  a canvas that predates the navy design system. Rebuild it around the chosen mark.
