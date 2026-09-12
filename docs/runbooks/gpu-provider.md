# Joining spare GPU capacity as an inference provider

This runbook turns one GPU host into a paid inference provider on an already-launched Matrix OS network. It assumes the network exists: genesis applied, validators running, the bridge and any DEX listing already handled. Nothing here touches consensus, genesis, or the bridge.

The end state is one box that runs two processes:

1. A model server holding the weights on the GPU and answering an HTTP API on loopback.
2. A `matrixd` node that joins the network as an ordinary peer, registers that model server as a provider on its order book, and settles the tokens it sells through the same consensus path compute jobs use.

A provider is not a validator. The GPU box does not need to bond stake, does not need to be in the genesis validator set, and does not need an attestor keystore. Keep `consensus.participate_in_open_set: false` on it and let it be a peer that sells work.

## What you are actually selling

One unit is one token. `inference.UnitsFor` sums the prompt and completion token counts the backend reported, and that number multiplies your per-unit price. There is no separate per-request or per-second charge.

`price_per_unit` is denominated in native MATRIX base units at 9 decimals, so `1000000000` is one whole MATRIX per token, which would be absurd. Real quotes on this scale are small integers.

`capacity` is a concurrency budget measured in those same token units, not a lifetime quota. Each job reserves its estimated units up front and the reservation is returned on completion or expiry (`market.ReleaseAsCompleted`). Size it to how many tokens you are willing to have in flight at once, not to how many you intend to sell this month.

Provider emission at launch is `0` per block. Your revenue is job settlement only, so the price you set is the whole of it.

## Decision 1: which model server

| | vLLM (`kind: openai`) | Ollama (`kind: local-http`) |
| --- | --- | --- |
| Wire protocol | `/v1/chat/completions` | `/api/chat` |
| Streaming to buyers | Yes, `InferStream` forwards deltas | No, the node falls back to a single response |
| Token counts | Reported by the server, via `stream_options.include_usage` on the streaming path | `prompt_eval_count` / `eval_count` |
| Throughput | Continuous batching, so concurrent buyers share the GPU | One request at a time in practice |

Use vLLM unless you have a reason not to. Streaming and real batching are the two things that decide whether a single GPU can serve more than one buyer at a time, and only the `openai` backend has them.

Reported token counts matter beyond convenience. They are what settles. When an upstream does not report usage on a streamed response the node derives the count locally from the prompt and the completion, which is a documented approximation of somebody else's tokeniser - honest, deterministic, and still not as good as the number the server that did the work produced.

## Decision 2: how buyers reach you

Two paths exist and they are not interchangeable.

**Gossip discovery announces you; it does not carry prompts.** The P2P exchange publishes your signed quote on `matrix/market/announce/v2`, and other nodes keep you in their remote-provider registry for as long as you keep announcing. That is what makes you visible, and it carries compute-unit jobs. It does not carry an inference prompt or a completion.

**Inference is served by the node that owns the provider.** `/v1/chat/completions` routes a request to the cheapest LOCAL provider advertising the requested model. So a buyer runs their prompt against *your* node's HTTP endpoint, not against their own. The GPU box needs a reachable Connect endpoint (`connect.addr`, default `0.0.0.0:9093`) and buyers need its address.

There are two buyer doors on that endpoint:

- `/v1/chat/completions` is the hosted-wallet door. The API key names the account it spends from, and your node signs settlement on that buyer's behalf with a key it holds. It only works for accounts you custody keys for.
- `RunInferenceJob` over Connect with `connect.signed_writes: true` is the self-custody door. The buyer signs a `RunAuthorization` bound to one provider, one prompt, and one moment. This is the one to open for buyers who are not you.

Turning on `signed_writes` also makes `RunInferenceJob` require that signature. That is deliberate and not a separate switch: without it, `buyer` is just a string, and anyone could name a funded account, have your GPU do the work, and never sign for it.

## Prerequisites

- A GPU host. On the AWS G family a `.2xlarge` is a single-GPU size, so plan for one GPU's worth of VRAM. Read the real number rather than trusting the instance name:

  ```sh
  nvidia-smi --query-gpu=name,memory.total,driver_version --format=csv
  ```

- The peer id and reachable address of at least one node already on the network, for `bootstrap_peers`.
- An account for this provider to be paid into. Use the Ethereum address you hold in MetaMask: the order-book id a provider registers under IS the account it is paid into, so listing under `eth:0x...` means the proceeds land somewhere you can already see and spend from. There is no wallet file to manage and the node never holds your key.
- Outbound TCP to your bootstrap peers, and inbound TCP on the P2P port (default 9000) from them.

## 1. Size the model to the GPU

Weights, KV cache, and activation memory all come out of the same VRAM. A rough working rule for a single card:

- Model weights in GiB are roughly parameters in billions multiplied by bytes per parameter: 2 for fp16/bf16, 1 for fp8, about 0.5 for 4-bit quantization.
- Leave headroom for the KV cache. It grows with `--max-model-len` times `--max-num-seqs`, and it is what actually decides how many buyers you can serve at once.

Pick the model first, then set `--max-model-len` and `--max-num-seqs` to what fits, then derive `capacity` from that. Advertising a context length the GPU cannot hold produces jobs that reserve capacity and then fail after the buyer has been quoted.

## 2. Run the model server on loopback

Bind the model server to `127.0.0.1` only. It has no authentication worth exposing and no rate limiting; the node in front of it is the thing that authenticates, meters, and charges. A model server on a public interface is free inference for anyone who finds the port.

```sh
export MATRIX_VLLM_API_KEY="$(openssl rand -hex 32)"

vllm serve <model-id> \
  --host 127.0.0.1 \
  --port 8000 \
  --api-key "$MATRIX_VLLM_API_KEY" \
  --served-model-name <the-name-you-will-advertise> \
  --max-model-len <fits-in-vram> \
  --max-num-seqs <concurrent-sequences> \
  --gpu-memory-utilization 0.90
```

`--api-key` is not optional even on loopback. The `openai` backend fails construction when its key environment variable is empty, so a keyless server means the node refuses to start rather than advertising capacity it cannot reach. That failure is the good outcome; set the key.

`--served-model-name` is the string buyers will put in `"model"`. Whatever you choose here has to match `inference.backends[].models` exactly, modulo case: the order book normalizes model names to lowercase.

Verify before going further:

```sh
curl -s http://127.0.0.1:8000/v1/chat/completions \
  -H "Authorization: Bearer $MATRIX_VLLM_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"<the-name-you-will-advertise>","messages":[{"role":"user","content":"hi"}]}' | jq .usage
```

A `usage` object with nonzero counts is what settlement will be computed from. If it is missing or zero, fix that here; do not go on and discover it at settlement time.

## 3. Bring up the node

```sh
matrixd -init -config /etc/matrix/gpu-provider.yaml
```

Then merge the leaf values from [`gpu-provider.overlay.yaml.example`](../../services/core/configs/gpu-provider.overlay.yaml.example) into the generated file. The generated API keys, storage paths, and identity are yours to keep; do not overwrite them.

Start the node once before completing the config: it prints the peer id and the multiaddrs other nodes should be given, and labels every address that is loopback, private, or a wildcard. On a cloud instance it will report that none of its bound addresses is reachable from another host, which is expected: the public or Elastic IP belongs to the NAT, not to an interface on the machine. Bind the wildcard and publish `/ip4/<public-ip>/tcp/9000/p2p/<printed-peer-id>`.

## 4. Declare the backend

The whole of provider onboarding is one config block. Before `inference.backends` existed, a real backend could only be installed in-process through `GetInference().Registry()`, which meant contributing a GPU meant writing Go and rebuilding the node.

```yaml
inference:
  backends:
    # The id IS the payout account. Use your wallet's address so revenue lands
    # where you can spend it, rather than in an account whose key the node holds.
    - id: "eth:0x<your-wallet-address-lowercase>"
      kind: openai
      base_url: "http://127.0.0.1:8000"
      api_key_env: MATRIX_VLLM_API_KEY
      request_timeout: 10m
      health_check_path: /health
      health_check_interval: 30s
      models:
        - <the-name-you-will-advertise>
      capacity: 200000
      price_per_unit: 4
      quote_ttl: 24h
```

Two registrations happen from this one block and both are needed. The inference registry answers "what fulfills a job for this provider"; the order book answers "does this provider exist, what does it cost, and does it have capacity to reserve". An inference job is a market job, and registering only the backend fails at submit time with `market: provider not found`.

`request_timeout` deserves a moment. It bounds one upstream request end to end, including the time spent reading a streamed body, and it defaults to 60s. Sixty seconds is a fair cap on somebody else's hosted API and the wrong one on a GPU you own: a local model asked for a few thousand tokens routinely runs longer, and the fixed cap failed the job *after* the GPU had already produced the answer - electricity spent, nothing sold. Size it from the model and the completion length you actually advertise. Do not set it enormous either; the timeout is also what stops a wedged runner from holding a reservation forever.

`health_check_*` is how the listing follows the model server. The node probes the backend on an interval and suspends the provider on the order book when it stops answering, so a crashed runner stops winning routing decisions instead of taking reservations it cannot serve. It is on by default at 30s. Point `health_check_path` at `/health` on vLLM, SGLang or TGI: that reports on the inference engine, while the default `/v1/models` can still answer from a list built at startup even when the engine is wedged. Turn it off with `health_check: false` only when the backend is a paid third-party vendor, where each probe is a billed request against a rate limit.

A failed probe suspends on the first failure, deliberately. The two outcomes are not symmetric: being off the market for one interval costs the provider a few routing decisions and reverses on the next good probe, while staying on it costs a buyer a reservation, a wait, and a failed request. Suspension never cancels work already reserved, because a probe cannot tell a dead backend from one busy finishing a real completion.

`MATRIX_VLLM_API_KEY` has to be in the node process's environment, not only in your shell. Under systemd use `EnvironmentFile=` with a root-owned `0600` file. The key is never carried in config; `api_key_env` names the variable and the backend reads it at construction.

## 4b. Let buyers reach you from a wallet

Two node settings turn the chain into something an ordinary Ethereum wallet can
use, and a buyer paying you from MetaMask needs both:

```yaml
consensus:
  # This chain's EIP-155 id. It is inside the signature of every wallet-signed
  # transaction, which is what stops one signed for another network being
  # replayed here. Identical on every node; never changed on a running chain.
  chain_id: <this-network's-chain-id>

eth_rpc:
  # The endpoint a wallet adds as a network. Public by intent, so put TLS in
  # front of it. It holds no keys: the only write is eth_sendRawTransaction,
  # which carries the sender's own signature and cannot spend anyone else's
  # balance.
  addr: "0.0.0.0:9095"
  allowed_origins:
    - "https://<exact-buyer-origin-host>"
```

The node refuses to start the endpoint without `chain_id`, because an endpoint on
a chain that accepts no wallet-signed transaction is a trap: the wallet connects,
shows a balance, and every send fails.

What a buyer sees is honest in one direction and incomplete in another. Balances
and transfers are correct, and a receipt reports whether the transfer actually
applied rather than only that it was ordered. But the wallet shows gas as free -
which it is, there being no EVM to meter - while this chain's protocol fee is a
percentage of the value moved, and Ethereum's fee model has nowhere to put that.
Tell buyers the fee rate; do not let the wallet's zero be the only number they
see.

## 5. Price it

Two modes, mutually exclusive:

- `price_per_unit` is an already-final gross customer quote. You are responsible for the protocol fee coming out of it.
- `cost_per_unit` plus `markup_basis_points` is the loss-protected form. The node grosses up for `consensus.fee_basis_points` so your markup survives the fee. `cost_per_unit` is your own manually observed cost basis converted to MATRIX; it is not a peg and not a DEX oracle, and nothing in the node pretends otherwise.

On owned hardware the honest cost basis is the instance hour divided by the tokens that hour actually produces. Measure the second number under load rather than from a spec sheet:

```sh
# tokens/sec under your real concurrency, from vLLM's metrics
curl -s http://127.0.0.1:8000/metrics | grep -E 'generation_tokens_total|prompt_tokens_total'
```

A quote carries `observed_at` and `valid_until`, and `quote_ttl` defaults to 24 hours. A stale quote fails the job before any upstream work happens, which is the right direction to fail. Every node restart re-applies the config quote with a fresh observation time, so a nightly restart is a legitimate refresh mechanism; an existing provider keeps its live reservations across that.

## 6. Expose exactly what buyers need

| Port | What it is | Exposure |
| --- | --- | --- |
| 9000 | libp2p P2P | Open to bootstrap peers and the network |
| 9090 | Admin API | Never. Loopback only. Its key can move funds; reach it over an SSH tunnel |
| 9091 | Market gRPC | Only if you intend to serve gRPC buyers |
| 9092 | Inference gRPC | Only if you intend to serve gRPC buyers |
| 9093 | Connect HTTP, including `/v1/chat/completions` | This is the buyer door. Behind TLS |
| 8000 | The model server | Loopback only, always |

Terminate TLS in front of 9093. The Connect endpoint speaks plain HTTP and an OpenAI-compatible API key travels in an `Authorization` header, so without TLS every buyer credential crosses the network in the clear.

`connect.allowed_origins` is deny-by-default and only ever gates browsers. `curl`, the Go client, and the SDK under Node send no `Origin` header and need no entry. Do not widen it to `"*"` to make a non-browser caller work; that is not what is failing.

Keep `connect.public_reads` and `connect.signed_writes` as deliberate decisions, not defaults inherited from a dev config. On a public provider endpoint `signed_writes: true` is the one that lets buyers who are not you pay you.

## 7. Verify end to end

The node prints one line per registered backend at startup:

```
Inference: registered openai backend for provider "gpu-box-1" (capacity 200000, price 4/unit, models: <name>).
```

No line means no backend, and a node with no backend still starts. Check for it.

Then confirm the order book and the model routing agree:

```sh
matrix --api-key <key> provider list
matrix --api-key <key> provider list --models <the-name-you-will-advertise>
```

And buy from yourself once, with a funded account, before telling anyone the endpoint exists:

```sh
curl -s https://<your-host>/v1/chat/completions \
  -H "Authorization: Bearer <a-matrix-api-key-with-an-account>" \
  -H 'Content-Type: application/json' \
  -d '{"model":"<the-name-you-will-advertise>","messages":[{"role":"user","content":"hi"}]}'
```

An API key with no `account` field can drive every other surface and cannot buy inference. That is the safe reading, and it is also the first thing to check when this returns 401 with a key that works elsewhere.

Finally confirm the money moved, since a completion that returns and a job that settles are different facts:

```sh
matrix --api-key <key> balance --account <provider-account>
```

## Operating notes

**The health check is not a substitute for supervising the model server.** The probe takes a dead provider off the market; it does not bring the model server back. Supervise it, and make the node's restart depend on it (`After=` plus `Requires=` under systemd) so the two do not drift apart. Watch for the transition lines - `provider "..." SUSPENDED` and `is serving again` - as the signal that the two processes are disagreeing.

**A suspension survives a restart.** It is persisted with the provider, and a config quote refresh on startup preserves it, so a node that restarts while its model server is still down does not put itself back on the market for one interval. The first successful probe clears it.

**Capacity is not throughput.** `capacity` bounds tokens reserved concurrently on the order book; `--max-num-seqs` bounds what the GPU will actually run at once. Setting capacity far above what the GPU can serve converts a queue into timeouts, and a timeout after the work is done is the expensive kind.

**One box can host several providers.** The registry maps provider id to backend, so a second `inference.backends` entry with a different `id` - a second model, or spare hosted-API credits resold through `kind: openai` against a vendor base URL - is a second listing on the same node. Ids must be unique and must not collide with `inference.echo_provider`.

**Turn off the demo provider.** A freshly initialized node sets `inference.echo_provider: demo-inference-provider` and registers a GPU-free echo backend so a new node can fulfill inference without hardware. On a real provider that is a listing that answers prompts with a stub. Clear it.

**Your payout address is public.** It is your order-book id, so every buyer and
every peer that receives an announcement sees it, and so does anyone reading the
chain. That is true of any account that receives payment on a public ledger; it
is worth knowing before you use an address that is also your personal wallet.

**The provider sees the prompt.** The model runs on your hardware, so the plaintext passes through it. There is no confidential-compute claim here. Say so to buyers rather than letting them assume otherwise.
