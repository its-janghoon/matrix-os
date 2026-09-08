# Screenshots

Every image here is a 1280x577 viewport capture, which is what makes them drop into the
root [`README.md`](../../README.md) without reflowing its layout. Match that size when
re-capturing.

## Embedded in the root README

Five, not nine: one per distinct claim, so nothing embedded duplicates another shot.

| File | What it shows | Route |
| --- | --- | --- |
| `web-home.png` | The hero: the peer-to-peer market for compute and inference. | `/` |
| `web-marketplace.png` | Compute Marketplace: capacity announce and discovery, signed jobs settling in native MATRIX. | `/products/marketplace` |
| `web-inference.png` | LLM Inference: local runner and provider-API proxy, settled through the same consensus path. | `/products/inference` |
| `web-consensus.png` | Consensus: leader-based BFT, one globally agreed ledger. | `/products/consensus` |
| `console-app.png` | Matrix Console on the Providers tab, connected to the built-in demo backend: all six tabs, and local versus remote providers with their peer ids, capacity and price. | `apps/console` on `http://localhost:5173` |

## Present but referenced by nothing

`web-introduction.png`, `web-cli.png`, `web-network-setup.png` and `web-agent-dev.png` are
captures of doc pages that no README or page embeds. They are re-captured along with the
rest so nothing here is stale, but they are dead weight until something links them - and
`web-cli` in particular was deliberately removed once, on the reasoning that a doc page is
navigational rather than a claim, then re-added without being wired in. Either embed them
or delete them; leaving them is the state this note exists to stop being invisible.

## Re-capturing the web screenshots

```sh
cd apps/web
corepack yarn install
corepack yarn build     # Turbopack is the default in Next 16; no --turbopack flag
corepack yarn start -p 3000
```

Then capture `http://localhost:3000` at each route in the table above, plus
`/docs/introduction`, `/docs/cli`, `/docs/guides/network-setup` and
`/docs/guides/agent-development` if those four are still here.

Two things a capture script has to get right, both learned by getting them wrong:

- **Wait for the entry animation to finish, not for a guessed delay.** The hero uses a
  `fade-up`, and a capture taken mid-fade renders the headline almost invisible. Await
  `document.getAnimations()` - but only the FINITE ones. A looping background animation
  never resolves its `finished` promise, so awaiting all of them hangs forever. Then assert
  no visible element is left below full opacity, so a mid-fade capture fails loudly instead
  of shipping.
- **Chromium in a container needs `--no-sandbox --disable-dev-shm-usage`.** Without them
  the renderer dies partway through and every later wait hangs against a closed target.

## Re-capturing the console screenshot

```sh
cd apps/console
corepack yarn install
corepack yarn dev --port 5173
```

Open `http://localhost:5173`, press **Connect** with the backend left on `Demo (in-memory)`,
then switch to the **Providers** tab and capture. The demo backend exists so the console can
be driven with no daemon running; connecting first is the difference between a screenshot of
an empty form and one of the product working. The Tauri desktop bundle needs system
libraries (webkit2gtk, gtk) that a plain container will not have, and it is not needed for
this shot.
