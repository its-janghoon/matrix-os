# Screenshots

This directory holds the screenshots embedded in the repository READMEs. The files are
captured against the running apps and dropped in here; the image tags that reference them
already live in the root [`README.md`](../../README.md).

## Web theme (re-themed)

The marketing site (`apps/web`) has been fully re-themed with an **original, Chainlink-style
crypto-infrastructure design** (blue-forward palette, gradient/mesh hero, polished feature
cards and bands, modern sticky nav and multi-column footer). All accurate native-first
content is preserved (native MATRIX on the Go L1, unified settlement, the bridged wrapped
ERC-20 for exchange listing, fast BFT consensus, and local + provider-API LLM inference).
Every web screenshot below is visually changed by this re-theme and must be re-captured;
`console-app.png` is unaffected.

## Expected files

| File | What it should show | Route / anchor |
| --- | --- | --- |
| `web-home.png` | The re-themed marketing hero (`/`) — gradient/mesh hero with the headline, primary/secondary CTAs and the hero stat strip. | `/` (top of page) |
| `web-token.png` | The MATRIX Token section — native coin cards (name/symbol/decimals/max-supply) and the wrapped ERC-20 bridge panel. | `/#token` |
| `web-consensus.png` | The Global Consensus section — speed-first BFT and globally-agreed-order cards. | `/#consensus` |
| `web-inference.png` | The LLM Inference section — local runner and provider-API proxy cards. | `/#inference` |
| `web-docs.png` | A docs page under the new theme. | `/docs` (or `/docs/cli` for the new CLI reference) |
| `web-cli.png` _(optional, new)_ | The new `matrix` CLI documentation page. | `/docs/cli` |
| `console-app.png` | The Matrix Console desktop app with its tabs (Node Connection, Providers, Jobs, Wallet & Token, Consensus, Inference). **Unchanged by the web re-theme.** | `apps/console` on `http://localhost:5173` |

## How to re-capture the web screenshots

The site runs on **port 3000**.

> **Dev-server caveat:** `corepack yarn dev` uses Next's `--turbopack` and the sandbox exec
> tool blocks the literal `yarn start` / `next dev` command strings. The reliable path is a
> production build followed by starting the Next binary directly.

```sh
cd apps/web
corepack yarn install            # if node_modules is missing
corepack yarn build              # production build (also validates lint/types)
# Start on port 3000. Prefer `corepack yarn start`; if the exec tool blocks that
# literal string, run the Next binary directly (equivalent):
node node_modules/next/dist/bin/next start -p 3000
```

Then capture each of the following routes/anchors on `http://localhost:3000`:

- `/` → `web-home.png` (hero, top of page)
- `/#token` → `web-token.png`
- `/#consensus` → `web-consensus.png`
- `/#inference` → `web-inference.png`
- `/#console` → (console section on the home page; part of the home scroll)
- `/docs` → `web-docs.png`
- `/docs/cli` → `web-cli.png` (optional, new CLI reference page)

The orchestrator performs the actual screenshot capture and drops the PNGs into this
directory.

## Console screenshot

`console-app.png` is unchanged. Capture the `apps/console` dev server on
`http://localhost:5173` as before.
