import { CodeSample } from '@/components/CodeSample';
import DocSidebar from '@/components/DocSidebar';
import Navigation from '@/components/Navigation';

export const metadata = {
  title: 'Selling a GPU',
  description:
    'Turning one GPU host into a paid inference provider on a running Matrix OS network, and the preflight that catches the mistakes that are expensive after the first start.',
};

/**
 * The web half of docs/runbooks/gpu-provider.md.
 *
 * The runbook is complete and long. This page is what a stranger with a GPU
 * needs before they decide to spend an afternoon on it: what the box ends up
 * being, the four decisions that are actually theirs, and the one command that
 * tells them whether it will work. Everything it asserts is checked by
 * scripts/gpu-provider-preflight.sh, so the page and the tool cannot drift into
 * saying different things.
 */

const PREFLIGHT = `# from a checkout of matrix-os, on the GPU box itself
scripts/gpu-provider-preflight.sh /etc/matrix/gpu-provider.yaml`;

const PREFLIGHT_OUTPUT = `== the config, read as the node reads it
   ok    genesis: 4 allocations
   ok    consensus.validators: 3 entries
   ok    consensus.participate_in_open_set: false, a peer that sells
   FAIL  admin.addr: 0.0.0.0:9090 is not loopback. The admin API's key can move
         funds; the only thing between it and the internet would be a firewall
         rule. Bind 127.0.0.1 and reach it over an SSH tunnel.
   FAIL  inference.echo_provider: set to "demo-inference-provider". That is a
         GPU-free stub answering paying prompts. Clear it.

== the model server
   ok    eth:0x8f2c...: /health answers 200
   ok    eth:0x8f2c...: answered as "llama-3.3-70b", reporting 9 + 3 tokens

NOT READY: 2 failed, 3 to decide.`;

const MODEL_SERVER = `export MATRIX_VLLM_API_KEY="$(openssl rand -hex 32)"

vllm serve <model-id> \\
  --host 127.0.0.1 \\
  --port 8000 \\
  --api-key "$MATRIX_VLLM_API_KEY" \\
  --served-model-name <the-name-you-will-advertise> \\
  --max-model-len <fits-in-vram> \\
  --max-num-seqs <concurrent-sequences> \\
  --gpu-memory-utilization 0.90`;

const BACKEND = `inference:
  # A freshly initialized node sets this to a GPU-free stub. Clear it.
  echo_provider: ""
  backends:
    # The id IS the payout account. Use the wallet address you already hold.
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
      quote_ttl: 24h`;

const JOINING = `# on a node already in the network, copy both sections verbatim:
awk '/^[a-z_]+:/{sec=$1} sec=="consensus:"||sec=="genesis:"' /etc/matrix/config.yaml`;

const SALE = `curl -s https://<your-host>/v1/chat/completions \\
  -H "Authorization: Bearer <a matrix api key whose account is funded>" \\
  -H 'Content-Type: application/json' \\
  -d '{"model":"<the-name-you-will-advertise>","messages":[{"role":"user","content":"hi"}]}'

# then confirm the money actually moved, which is a different fact
matrix --api-key <key> balance --account eth:0x<your-wallet-address>`;

const PORTS = `9000  libp2p P2P            open to your bootstrap peers and the network
9090  admin API             never. Loopback only; its key can move funds
9093  Connect HTTP          the buyer door. Behind TLS
8000  the model server      loopback only, always`;

export default function GpuProviderPage() {
  return (
    <>
      <Navigation />
      <div className='min-h-screen bg-black'>
        <div className='pt-16'>
          <div className='flex flex-col lg:flex-row'>
            <DocSidebar />

            <main className='min-w-0 flex-1 p-4 sm:p-6 lg:ml-64 lg:p-8'>
              <div className='mx-auto max-w-4xl'>
                <article className='text-gray-100'>
                  <div className='mb-10 rounded-xl border border-primary-400/20 bg-gradient-to-r from-primary-400/10 via-accent-300/10 to-primary-400/10 p-8'>
                    <h1 className='mb-4 text-4xl font-bold text-white'>Selling a GPU</h1>
                    <p className='text-xl text-gray-100'>
                      One host, two processes, and a payout address you already hold. A provider is not a validator:
                      it posts no stake, joins no validator set, and needs no attestor key. It is a peer that sells
                      work.
                    </p>
                  </div>

                  <h2 className='mb-4 mt-8 text-3xl font-bold text-white'>Run the preflight first</h2>
                  <p className='mb-4 text-gray-300'>
                    Start here, not at the end. Genesis is applied once and the fact is recorded, so a node started
                    against the wrong one cannot be corrected in place - the store has to be deleted and the node
                    started again. Every other mistake on this page is cheap before the first start and expensive
                    after it.
                  </p>
                  <CodeSample label='shell' code={PREFLIGHT} />
                  <p className='mb-4 mt-4 text-gray-300'>
                    It reads the config the way the node reads it, probes the model server for a real completion,
                    checks what this box is listening on and whether it can reach its bootstrap peers, and hands the
                    config to the node&apos;s own production preflight rather than keeping a second copy of those
                    rules. It writes nothing and starts nothing.
                  </p>
                  <CodeSample label='output' code={PREFLIGHT_OUTPUT} />
                  <p className='mt-4 text-gray-300'>
                    Failures are mistakes. Warnings are decisions - selling at cost, validating as well as selling,
                    reselling a vendor&apos;s API instead of your own weights - and they are listed so you make them
                    deliberately rather than inherit them from a generated file.
                  </p>

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>What you are selling</h2>
                  <p className='mb-4 text-gray-300'>
                    One unit is one token. The prompt and completion counts the model server reports are summed and
                    multiplied by your per-unit price, and there is no separate per-request or per-second charge.
                    Those counts are what settles, which is why the preflight sends one real prompt and reads the{' '}
                    <code className='text-white'>usage</code> object back: a server that reports nothing leaves the
                    node deriving the number itself, from an approximation of somebody else&apos;s tokeniser.
                  </p>
                  <p className='mb-4 text-gray-300'>
                    A reasoning model bills for its working as well as its answer, and it is delivered with the
                    completion for that reason. The receipt commits to both, so a seller cannot charge for one body
                    of reasoning and hand over another.
                  </p>
                  <p className='mt-4 text-gray-300'>
                    Provider emission at launch is <strong>0 per block</strong>. Settlement is the whole of your
                    revenue, so the price you set is the whole of it.
                  </p>

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>1. The model server, on loopback</h2>
                  <CodeSample label='shell' code={MODEL_SERVER} />
                  <p className='mt-4 text-gray-300'>
                    Bind it to <code className='text-white'>127.0.0.1</code> and nothing else. It has no
                    authentication worth exposing and no metering; the node in front of it is what authenticates,
                    meters and charges. A model server on a public interface is free inference for whoever finds the
                    port, and the preflight fails on it.
                  </p>
                  <p className='mt-4 text-gray-300'>
                    <code className='text-white'>--api-key</code> is not optional even on loopback. The{' '}
                    <code className='text-white'>openai</code> backend fails construction on an empty key, so a
                    keyless server means the node refuses to start rather than advertising capacity it cannot reach.
                    That failure is the good outcome.
                  </p>

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>2. Join the network you are selling on</h2>
                  <p className='mb-4 text-gray-300'>
                    This is the step that looks optional and is not. A provider does not validate, but it is a full
                    node: it replays the chain from height zero and checks each block&apos;s state root against its
                    own ledger. A node that starts with an empty or invented genesis computes a different root at
                    height zero and refuses every block the network sends it. What you see is a node that connects to
                    its peers and never advances.
                  </p>
                  <CodeSample label='shell' code={JOINING} />
                  <p className='mt-4 text-gray-300'>
                    The validator list has to be the <strong>genesis</strong> set, not the set validating today. Each
                    replayed block is checked against the set as it stood at that height, so a node handed the
                    current set rejects early history and never catches up. Neither section holds a secret: validator
                    ids are public keys and genesis allocations are public chain data.
                  </p>

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>3. Declare the backend</h2>
                  <CodeSample label='config.yaml' code={BACKEND} />
                  <p className='mt-4 text-gray-300'>
                    The id is the account revenue is paid into. Settlement credits it directly and nothing later asks
                    whether anyone holds its key, so an invented name is a listing that earns money nobody can spend.
                    Use the address you already have in a wallet.
                  </p>
                  <p className='mt-4 text-gray-300'>
                    <code className='text-white'>request_timeout</code> defaults to 60 seconds, which is a fair cap
                    on somebody else&apos;s hosted API and the wrong one on a GPU you own. A local model asked for a
                    few thousand tokens routinely runs longer, and the fixed cap fails the job <em>after</em> the GPU
                    has produced the answer: electricity spent, nothing sold.
                  </p>
                  <p className='mt-4 text-gray-300'>
                    <code className='text-white'>health_check_path</code> should point at{' '}
                    <code className='text-white'>/health</code>, which reports on the inference engine. The default{' '}
                    <code className='text-white'>/v1/models</code> can still answer from a list built at startup
                    while the engine is wedged. A failed probe suspends the listing on the first failure, because
                    being off the market for one interval costs a few routing decisions while staying on it costs a
                    buyer a reservation, a wait, and a failed request.
                  </p>

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>4. Expose exactly what buyers need</h2>
                  <CodeSample label='ports' code={PORTS} />
                  <p className='mt-4 text-gray-300'>
                    Terminate TLS in front of 9093. The Connect endpoint speaks plain HTTP and a buyer&apos;s API key
                    travels in an <code className='text-white'>Authorization</code> header, so without TLS every
                    buyer credential crosses the network in the clear.
                  </p>
                  <p className='mt-4 text-gray-300'>
                    Turn on <code className='text-white'>connect.signed_writes</code> if you want to sell to buyers
                    who are not you. Without it, <code className='text-white'>buyer</code> is a bare string and only
                    accounts whose keys your node custodies can pay; with it, the buyer signs an authorization bound
                    to one provider, one prompt, and one moment.
                  </p>

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>5. Buy from yourself once</h2>
                  <p className='mb-4 text-gray-300'>
                    A completion that returns and a job that settles are different facts. Prove both before telling
                    anyone the endpoint exists.
                  </p>
                  <CodeSample label='shell' code={SALE} />
                  <p className='mt-4 text-gray-300'>
                    An API key with no <code className='text-white'>account</code> field can drive every other
                    surface and cannot buy inference. That is the safe reading, and it is also the first thing to
                    check when this returns 401 with a key that works everywhere else.
                  </p>

                  <div className='my-10 rounded-xl border border-semantic-processing/40 bg-semantic-processing/10 p-6'>
                    <h2 className='mb-2 text-xl font-bold text-white'>Two things to tell your buyers</h2>
                    <p className='mb-3 text-gray-100'>
                      <strong>Your payout address is public.</strong> It is your order-book id, so every buyer and
                      every peer that receives an announcement sees it, and so does anyone reading the chain. Worth
                      knowing before you use an address that is also your personal wallet.
                    </p>
                    <p className='mb-0 text-gray-100'>
                      <strong>The provider sees the prompt.</strong> The model runs on your hardware, so the
                      plaintext passes through it. There is no confidential-compute claim here. Say so rather than
                      letting buyers assume otherwise.
                    </p>
                  </div>

                  <div className='mt-10 rounded-xl border border-primary-400/20 bg-primary-400/10 p-6'>
                    <h2 className='mb-4 text-2xl font-bold text-white'>Next</h2>
                    <ul className='mb-0 list-disc space-y-3 pl-6 text-gray-100'>
                      <li>
                        <a
                          href='https://github.com/savagemanage/matrix-os/blob/main/docs/runbooks/gpu-provider.md'
                          className='text-accent-200 underline hover:text-accent-100'
                        >
                          The full runbook
                        </a>{' '}
                        - sizing the model to the GPU, pricing from a measured cost basis, and the operating notes
                      </li>
                      <li>
                        <a href='/docs/compute-marketplace' className='text-accent-200 underline hover:text-accent-100'>
                          Compute marketplace
                        </a>{' '}
                        - how a quote becomes a reservation and a reservation becomes a settlement
                      </li>
                      <li>
                        <a href='/docs/guides/network-setup' className='text-accent-200 underline hover:text-accent-100'>
                          Network setup
                        </a>{' '}
                        - what a node needs to agree with its peers at all
                      </li>
                    </ul>
                  </div>
                </article>
              </div>
            </main>
          </div>
        </div>
      </div>
    </>
  );
}
