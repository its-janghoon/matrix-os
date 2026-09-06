# Screenshots

This directory holds the screenshots embedded in the repository READMEs. The files are
captured against the running apps and dropped in here; the image tags that reference them
already live in the root [`README.md`](../../README.md).

## Expected files

| File | What it should show | How to capture |
| --- | --- | --- |
| `web-home.png` | The marketing site hero (`/`) — the Matrix OS hero with the Download/Documentation buttons. | `apps/web` on `http://localhost:3000`, top of the page. |
| `web-token.png` | The MATRIX Token section (`/#token`) — name/symbol/decimals/max-supply cards and the "Built on audited standards" panel. | `apps/web` on `http://localhost:3000`, scroll to the Token section. |
| `web-consensus.png` | The Global Consensus section (`/#consensus`) — the speed-first BFT and globally agreed order cards. | `apps/web` on `http://localhost:3000`, scroll to the Consensus section. |
| `web-inference.png` | The LLM Inference section (`/#inference`) — local runner and provider-API proxy cards. | `apps/web` on `http://localhost:3000`, scroll to the Inference section. |
| `web-docs.png` | A docs page (`/docs/compute-marketplace`) — the Compute Marketplace documentation. | `apps/web` on `http://localhost:3000/docs/compute-marketplace`. |
| `console-app.png` | The Matrix Console desktop app with its tabs (Node Connection, Providers, Jobs, Wallet & Token, Consensus, Inference). | `apps/console` dev server on `http://localhost:5173`. |

See the [FEAT-006 findings](../../../.agents/tasks/task-coin-consensus-console-llm/features/FEAT-006.json)
for the exact run commands, ports, and routes used to produce these images.
