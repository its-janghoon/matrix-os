import { CodeSample } from '@/components/CodeSample';
import DocSidebar from '@/components/DocSidebar';
import Navigation from '@/components/Navigation';
import { GITHUB_URL } from '@/lib/releases';

export const metadata = {
  title: 'matrix CLI',
  description:
    'The matrix command line: every command, every flag, and which of the node’s three ports each one talks to. Verified against the binary’s own --help.',
};

/**
 * This page carried a box stating that "the matrix.inference.v1
 * InferenceService is not yet wired into the node, so the CLI does not expose
 * an inference command". Both halves were false: the node serves it on 9092
 * and `matrix inference submit` has been there the whole time. It also omitted
 * `fund`, `quickstart`, `tx get`, and the --wallet and --inference-addr flags.
 *
 * Everything below was taken from `matrix --help` and from running the
 * commands against a node, including the outputs quoted verbatim.
 */
const BUILD = `# from services/core
go build -o matrix ./cmd/matrix
go install ./cmd/matrix     # or put it on your PATH

matrix --help               # the full command tree`;

const DEMO = `matrix --api-key <key> quickstart
# wallet -> funded buyer -> registered provider -> job -> settlement, in one command
# --provider --capacity --price --units --fund --wallet all override the defaults`;

const PROVIDERS = `matrix status                  # endpoint + serving status
matrix health                  # the gRPC health Check: SERVING / NOT_SERVING

# --price is the final manual quote in native MATRIX base units per compute unit.
matrix provider register --id gpu-1 --capacity 100 --price 5
matrix provider register --id gpu-2 --capacity 100 --price 3 \\
  --models llama-3.3-70b,qwen-2.5-72b   # what a model request routes on
# Refresh existing quotes this way; re-registering can reset reserved capacity.
matrix provider quote-update --id gpu-2 --price 4
matrix provider list                     # local providers with fresh quotes
matrix provider list --include-remote    # plus providers discovered over p2p`;

const PROVIDER_OUT = `ID     CAPACITY  AVAILABLE  PRICE/UNIT  COST/UNIT  MARKUP(BPS)  QUOTE       VERSION  OBSERVED              VALID UNTIL           ORIGIN  PEER  MODELS
gpu-2  100       100        3           0          0            q-8f2...     1        2026-01-01T00:00:00Z  2026-01-02T00:00:00Z local         llama-3.3-70b,qwen-2.5-72b`;

const JOBS = `matrix job submit --buyer <account-id> --provider gpu-1 --units 10
matrix job complete --id <job-id>    # settles: moves native MATRIX buyer -> provider
matrix job cancel --id <job-id>      # releases the reserved capacity, charges nothing

matrix balance --account <account-id>
matrix fund --account <account-id> --amount 1000000   # from the genesis reward pool`;

const WALLET = `matrix wallet create           # encrypted, and shows a 12-word recovery phrase once
matrix wallet import           # restore that phrase (omit --mnemonic to be asked)
matrix wallet show             # the account id. No passphrase: it is a public value
matrix wallet balance          # likewise
matrix wallet transfer --to <recipient-account-id> --amount 200   # this one unlocks

# The passphrase comes from MATRIX_WALLET_PASSPHRASE when set, otherwise a
# prompt that does not echo. A non-interactive caller must set the variable;
# the tool refuses to read a passphrase off a pipe.
#
# every wallet command takes --wallet to use a file other than ~/.matrix/wallet.json`;

const INFERENCE = `matrix inference submit \\
  --buyer <account-id> \\
  --provider demo-inference-provider \\
  --model demo \\
  --prompt "one sentence about peer-to-peer compute"

matrix inference get --id <job-id>
# --fulfill defaults to true: the job is reserved, run and settled in one call.
# --fulfill=false reserves only.

matrix inference submit --client-signed \\
  --buyer <your wallet's account-id> \\
  --provider gpu-2 --model llama-3.3-70b --prompt "hello"
# The node runs the model, returns the exact transfer to sign and withholds the
# completion; this signs it with the local wallet and the node settles. Use it
# where the node should not hold your key.`;

const INFERENCE_OUT = `id:         13ac1c62-5013-4c31-8cbd-c5f3b7848d23
buyer:      c5b097824f2e2222a278af3dbe4b519fa653a96e44ff08891668b3a2a708d5d9
provider:   demo-inference-provider
model:      demo
status:     completed
units:      5
completion: echo: user: one sentence about peer-to-peer compute`;

const BRIDGE = `# One-time operator setup: create a fixed EVM committee key.
matrix bridge attestor-new --out ~/.matrix/bridge-attestor.json

# User flow: minimum 100 MATRIX (100000000000 native base units).
matrix bridge lock --wallet ~/.matrix/wallet.json \
  --to 0x<base-recipient> --amount 100000000000
matrix bridge attestation --lock-id 0x<lock-id> \
  --validator validator-1.example.org:9091 \
  --validator validator-2.example.org:9091 > attestations.json

# Compare native escrow accounting with WrappedMatrix.totalSupply().
matrix bridge reconcile`;

const TX = `matrix tx list --limit 20      # ascending commit order
matrix tx list --start 10 --limit 20
matrix tx get --index 3`;

export default function MatrixCliDocs() {
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
                    <h1 className='mb-4 text-4xl font-bold text-white'>The matrix CLI</h1>
                    <p className='text-xl text-gray-100'>
                      <code className='text-white'>matrix</code> drives a running node: health, providers, jobs,
                      balances, inference, bridge lock/attestation/reconciliation, and a local wallet that signs
                      transfers so the node never sees a private key.
                    </p>
                  </div>

                  <h2 className='mb-4 mt-8 text-3xl font-bold text-white'>Build it</h2>
                  <CodeSample label='shell' code={BUILD} />
                  <p className='mt-4 text-gray-300'>
                    Or take a prebuilt binary from{' '}
                    <a href='/download' className='text-accent-200 underline hover:text-accent-100'>
                      a release
                    </a>
                    .
                  </p>

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>Global flags</h2>
                  <ul className='list-disc space-y-3 pl-6 text-gray-300'>
                    <li>
                      <code className='text-white'>--addr</code> (default{' '}
                      <code className='text-white'>127.0.0.1:9091</code>) - the market gRPC endpoint. Almost every
                      command talks to this one.
                    </li>
                    <li>
                      <code className='text-white'>--inference-addr</code> (default{' '}
                      <code className='text-white'>127.0.0.1:9092</code>) - the inference gRPC endpoint. It is a
                      separate server on a separate port, so pointing <code className='text-white'>--addr</code> at a
                      remote node is not enough for <code className='text-white'>matrix inference</code>; it is a flag
                      on the <code className='text-white'>inference</code> commands only.
                    </li>
                    <li>
                      <code className='text-white'>--api-key</code> - sent as the{' '}
                      <code className='text-white'>authorization</code> gRPC metadata header. Required on a node with{' '}
                      <code className='text-white'>security.enable_acls</code> on, which is the default a generated
                      config writes.
                    </li>
                    <li>
                      <code className='text-white'>--timeout</code> (default <code className='text-white'>10s</code>)
                      and <code className='text-white'>--json</code> for machine-readable output.
                    </li>
                  </ul>

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>The whole loop in one command</h2>
                  <CodeSample label='shell' code={DEMO} />

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>Node and providers</h2>
                  <CodeSample label='shell' code={PROVIDERS} />
                  <p className='mb-4 mt-4 text-gray-300'>
                    <code className='text-white'>provider list</code> on a freshly initialized node already has one
                    entry - the GPU-free echo provider the node registers so the inference path works before you have
                    a model server:
                  </p>
                  <CodeSample label='output' code={PROVIDER_OUT} />
                  <p className='mt-4 text-gray-300'>
                    <code className='text-white'>--price</code> is a manually supplied final quote in native MATRIX
                    base units per compute unit, not credits, USD or a stablecoin peg. The marketplace gives each
                    registration a quote identity and expiry; a submitted job snapshots the accepted price and quote
                    metadata so a later provider refresh cannot reprice it. Refresh an expired manual quote before
                    accepting more work.
                  </p>

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>Jobs and balances</h2>
                  <CodeSample label='shell' code={JOBS} />
                  <p className='mt-4 text-gray-300'>
                    <code className='text-white'>fund</code> moves native MATRIX out of the genesis reward pool; it
                    never mints, so the billion-MATRIX cap holds however often you call it.
                  </p>
                  <p className='mt-4 text-gray-300'>
                    On a <strong>validator set</strong> it refuses instead, with{' '}
                    <code className='text-white'>FailedPrecondition</code>. Funding from the pool is not
                    consensus-ordered: it moves value on the node you called and nowhere else, so the pools diverge
                    and the provider emission forks. This was found by running two nodes - funding an account on
                    one left the other reporting a balance of zero for it. Put the allocation in every
                    node&apos;s <code className='text-white'>genesis</code> instead, or move value with a signed
                    transfer, which every node applies from the committed block.
                  </p>
                  <p className='mt-4 text-gray-300'>
                    <code className='text-white'>job complete</code> settles through consensus: the payment is a
                    transfer <em>signed by the buyer</em> that a quorum commits and every node applies. The node
                    therefore needs the buyer&apos;s signing key, which it resolves from the wallet files under{' '}
                    <code className='text-white'>~/.matrix</code>. A job whose buyer it holds no key for is refused
                    with <code className='text-white'>FailedPrecondition</code> and the reservation left intact, so
                    you can cancel it. That refusal is the point: paying out of an account without its owner&apos;s
                    signature is what this path exists to stop.
                  </p>

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>Wallet</h2>
                  <p className='mb-4 text-gray-300'>
                    An ed25519 keypair at <code className='text-white'>~/.matrix/wallet.json</code>, mode{' '}
                    <code className='text-white'>0600</code> and <strong>encrypted</strong> under a passphrase you
                    choose (scrypt, then AES-256-GCM). The private key is never printed or logged: a transfer is
                    signed locally and only the public key, the signature and the transfer fields go to the node.
                  </p>
                  <p className='mb-4 text-gray-300'>
                    <code className='text-white'>create</code> shows a 12-word BIP-39 recovery phrase, once. It is
                    the only way to restore the account if the file is lost, and it is derived at the standard
                    SLIP-0010 ed25519 path <code className='text-white'>m/44&apos;/9004&apos;/0&apos;/0&apos;</code>,
                    so it also restores in other wallets that implement it. A mistyped word fails BIP-39&apos;s
                    checksum and is refused, rather than silently deriving a different, empty account.
                  </p>
                  <p className='mb-4 text-gray-300'>
                    Wallets written before this existed stored the key in the clear. They are still read, so nothing
                    is stranded, but every command that signs with one prints a warning: there is no migration a tool
                    can do unasked, because it cannot invent a passphrase. Restore the phrase with{' '}
                    <code className='text-white'>wallet import</code>, or move the balance to a new wallet.
                  </p>
                  <CodeSample label='shell' code={WALLET} />

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>Inference</h2>
                  <CodeSample label='shell' code={INFERENCE} />
                  <p className='mb-4 mt-4 text-gray-300'>What a run against the demo provider prints:</p>
                  <CodeSample label='output' code={INFERENCE_OUT} />
                  <p className='mt-4 text-gray-300'>
                    An inference job is a compute job with a prompt attached: it reserves capacity from a registered
                    provider, runs on that provider&apos;s backend, and settles at that provider&apos;s price through
                    consensus - which means the node needs the buyer&apos;s signing key. It resolves one from the
                    wallet files under <code className='text-white'>~/.matrix</code>, so the wallet you created above
                    works and an arbitrary account id does not.
                  </p>

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>Base bridge</h2>
                  <CodeSample label='shell' code={BRIDGE} />
                  <p className='mt-4 text-gray-300'>
                    <code className='text-white'>attestor-new</code> creates one encrypted secp256k1 key for an
                    address in the fixed WrappedMatrix committee selected at deployment. It is not granted to every
                    native validator, and bonded-open validator membership does not change the contract committee.
                    <code className='text-white'> lock</code> commits native escrow,{' '}
                    <code className='text-white'>attestation</code> emits the JSON signatures the Base mint consumes,
                    and <code className='text-white'>reconcile</code> reports the 1:1 backing invariant.
                  </p>
                  <p className='mt-4 text-gray-300'>
                    Before the web bridge asks MetaMask to sign a native lock, it reads the exact WrappedMatrix code,
                    threshold, attestor count, cap, supply, and conversion. It then sends one random 32-byte challenge
                    to every configured validator&apos;s rate-limited public <code className='text-white'>GetBridgeReadiness</code>{' '}
                    RPC, recovers unique registered signers, and requires the live threshold. A chain, contract,
                    minimum, challenge, signature, registration, conversion, or cap mismatch stops before payment
                    signing, local recovery storage, or native submission. Post-lock attestation and reconciliation
                    checks remain required.
                  </p>
                  <p className='mt-4 text-gray-300'>
                    Base Sepolia is chain 84532 for rehearsal and Base is 8453 for production. The user broadcasts
                    mint, burn, approval or swap transactions and pays Base gas from their wallet; Matrix provides no
                    gas relayer. A burn release is always ordered by native consensus. The production wrapped mint
                    ceiling is 6% of native maximum supply, and wMATRIX remains a native-backed mirror rather than a
                    USD or USDC stablecoin.
                  </p>

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>Transactions</h2>
                  <CodeSample label='shell' code={TX} />
                  <div className='my-8 rounded-xl border border-semantic-processing/40 bg-semantic-processing/10 p-6'>
                    <h3 className='mb-2 text-lg font-bold text-white'>What tx list shows</h3>
                    <p className='mb-0 text-gray-100'>
                      These read the consensus transaction history: the ordered sequence of value transfers that
                      committed blocks carried. A{' '}
                      <code className='text-white'>wallet transfer</code>,{' '}
                      <code className='text-white'>SubmitSignedTransfer</code>, and the buyer-to-provider payment
                      behind a compute or inference settlement all settle through consensus, so they all appear here
                      in commit order. Because the history comes from the committed block chain, two nodes list the
                      same transfers in the same order. Reserved consensus operations (bonds, withdrawals, validator
                      set changes) are protocol state, not transfers, so they are omitted.
                    </p>
                  </div>

                  <div className='mt-10 rounded-xl border border-primary-400/20 bg-primary-400/10 p-6'>
                    <h2 className='mb-4 text-2xl font-bold text-white'>Next</h2>
                    <ul className='mb-0 list-disc space-y-3 pl-6 text-gray-100'>
                      <li>
                        <a href='/docs/quickstart' className='text-accent-200 underline hover:text-accent-100'>
                          Quickstart
                        </a>{' '}
                        - a node and a settled job in about a minute
                      </li>
                      <li>
                        <a href='/docs/configuration' className='text-accent-200 underline hover:text-accent-100'>
                          Configuration
                        </a>{' '}
                        - every field, including where the API key lives
                      </li>
                      <li>
                        <a
                          href={`${GITHUB_URL}/blob/main/services/core/cmd/matrix/README.md`}
                          target='_blank'
                          rel='noopener noreferrer'
                          className='text-accent-200 underline hover:text-accent-100'
                        >
                          The CLI&apos;s own README
                        </a>
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
