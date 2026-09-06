# Screenshots

The five images embedded in the root [`README.md`](../../README.md). Five, not ten: one per
distinct claim, so nothing here duplicates another shot. Five earlier captures were removed
along with their README entries - `web-token`, `web-console`, `web-how-it-works`, `web-cli`
and `web-docs` - because token economics are covered in prose, `web-console` showed the
product *page* for an app that `console-app.png` shows running, and the rest were
navigational rather than claims.

## Files

| File | What it shows | Route |
| --- | --- | --- |
| `web-home.png` | The hero: the peer-to-peer market for compute and inference. | `/` |
| `web-marketplace.png` | Compute Marketplace: capacity announce and discovery, signed jobs settling in native MATRIX. | `/products/marketplace` |
| `web-inference.png` | LLM Inference: local runner and provider-API proxy, settled through the same consensus path. | `/products/inference` |
| `web-consensus.png` | Consensus: leader-based BFT, one globally agreed ledger. | `/products/consensus` |
| `console-app.png` | Matrix Console with its tabs: Node Connection, Providers, Jobs, Wallet & Token, Consensus, Inference. | `apps/console` on `http://localhost:5173` |

The web routes are real pages. An earlier version of this file pointed at `/#token`,
`/#consensus` and `/#inference`, anchors on a single-page site that no longer exists.

## Re-capturing the web screenshots

```sh
cd apps/web
corepack yarn install
corepack yarn build     # Turbopack is the default in Next 16; no --turbopack flag
corepack yarn start -p 3000
```

Then capture `http://localhost:3000` at each route in the table. Note that the hero uses a
`fade-up` entry animation, so a capture taken too early catches the headline mid-fade and
renders it almost invisible - let the page settle before shooting.

## Re-capturing the console screenshot

Run the `apps/console` dev server and capture `http://localhost:5173`. The Tauri desktop
bundle needs system libraries (webkit2gtk, gtk) that a plain container will not have; the
dev server in a browser is enough for this shot.
