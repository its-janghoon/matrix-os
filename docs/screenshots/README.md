# Screenshots

Every image here is a 1280x577 viewport capture, which is what makes them drop into the
root [`README.md`](../../README.md) without reflowing its layout. Match that size when
re-capturing.

## The five

One per distinct claim, so nothing here duplicates another shot. Every file in this
directory is embedded in the root README; a capture that nothing embeds is dead weight and
gets deleted rather than kept "in case".

| File | What it shows | Route |
| --- | --- | --- |
| `web-home.png` | The hero: the peer-to-peer market for compute and inference. | `/` |
| `web-marketplace.png` | Compute Marketplace: capacity announce and discovery, signed jobs settling in native MATRIX. | `/products/marketplace` |
| `web-inference.png` | LLM Inference: local runner and provider-API proxy, settled through the same consensus path. | `/products/inference` |
| `web-consensus.png` | Consensus: leader-based BFT, one globally agreed ledger. | `/products/consensus` |
| `console-app.png` | Matrix Console on the Providers tab, connected to the built-in demo backend: all six tabs, and local versus remote providers with their peer ids, capacity and price. | `apps/console` on `http://localhost:5173` |

## Re-capturing the web screenshots

```sh
cd apps/web
corepack yarn install
corepack yarn build     # Turbopack is the default in Next 16; no --turbopack flag
corepack yarn start -p 3000
```

Then capture `http://localhost:3000` at each route in the table above.

Doc pages are deliberately not captured. `web-introduction`, `web-cli`, `web-network-setup`
and `web-agent-dev` existed here once, embedded nowhere: a doc page is navigational rather
than a claim, and a screenshot of prose goes stale every time the prose changes while
nothing points at it to notice. `web-cli` was removed on that reasoning, came back
unreferenced, and has been removed again. Add a capture here only together with the README
change that embeds it.

The capture is deterministic in everything that matters. Re-running it against
unchanged pages reproduces `web-inference.png` and `console-app.png` BYTE for
byte; the other three come back within a few hundred bytes of ~380 KB, because
the hero's looping background gradient lands on a different frame each time. So
a diff of a few hundred bytes on one of those is the backdrop, not the page - do
not commit it. A real content change moves the file by far more than that, and
the byte-identical pair is the control that tells you which kind you have.

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
