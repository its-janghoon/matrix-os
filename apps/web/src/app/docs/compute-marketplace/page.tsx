'use client';

import DocSidebar from '@/components/DocSidebar';
import { ConsensusRound, JobLifecycle, TokenBridge } from '@/components/diagrams';
import Navigation from '@/components/Navigation';

export default function ComputeMarketplaceDocs() {
  return (
    <>
      <Navigation />
      <div className='min-h-screen bg-black'>
        <div className='pt-16'>
          <div className='flex flex-col lg:flex-row'>
            <DocSidebar />

            {/* Main Content */}
            <main className='min-w-0 flex-1 p-4 sm:p-6 lg:ml-64 lg:p-8'>
              <div className='max-w-4xl mx-auto'>
                <article className='text-gray-100'>
                  {/* Hero Section */}
                  <div className='bg-gradient-to-r from-blue-500/10 via-purple-500/10 to-blue-500/10 rounded-xl p-8 mb-12 border border-blue-500/20'>
                    <h1 className='text-4xl font-bold text-white mb-4'>Compute Marketplace</h1>
                    <p className='text-xl text-gray-100'>
                      What is shipped and how the pieces fit: the coin, the ledger that agrees it, the backends that do
                      the work, and the wrapped mirror on Ethereum.
                    </p>
                  </div>

                  <h2 className='text-3xl font-bold text-white mt-8 mb-6'>A job, start to finish</h2>
                  <JobLifecycle className='mx-auto max-w-md' />

                  {/* Token */}
                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>Native MATRIX, the settlement coin</h2>
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800 mb-8'>
                    <p className='text-gray-100 leading-relaxed mb-4'>
                      Buyers pay in it, providers earn it, and it is the single source of truth for balances and supply.
                      One currency end to end, not a separate notion of credits.
                    </p>
                    <ul className='text-gray-100 space-y-2 list-disc pl-6'>
                      <li>Symbol <strong>MATRIX</strong>, 9 native decimals on the Matrix L1</li>
                      <li>Supply capped at 1,000,000,000 whole MATRIX; balances live on the consensus ledger</li>
                      <li>A genesis allocation plus a genesis-funded reward pool back earnings as capped, supply-tracked issuance (no unlimited minting)</li>
                      <li>All settlement is applied deterministically once a consensus block commits</li>
                    </ul>
                  </div>

                  {/* Bridge */}
                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>The bridged wrapped ERC-20 mirror</h2>
                  <TokenBridge />
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800 mb-8'>
                    <ul className='text-gray-100 space-y-2 list-disc pl-6'>
                      <li><strong>Lock to mint</strong> - native MATRIX is locked on the L1, and a threshold of validator secp256k1 attestations authorizes minting the matching wMATRIX</li>
                      <li>
                        <strong>Burn to unlock</strong> - burning wMATRIX emits an on-chain Burned event. A watcher
                        inside the node polls Ethereum for those events past a confirmation depth, keeps a persisted
                        scan cursor across restarts, and releases the escrowed native MATRIX exactly once per event
                        (keyed by transaction hash and log index).
                      </li>
                      <li>Native has 9 decimals and wMATRIX has 18, so one native base unit equals 1e9 wrapped base units and the 1,000,000,000 MATRIX cap maps to the same money on both sides</li>
                      <li>Runs against local and test networks only; it is not deployed to any public Ethereum network</li>
                    </ul>
                  </div>

                  {/* Consensus */}
                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>Fast consensus chain</h2>
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800 mb-8'>
                    <ConsensusRound className='mx-auto max-w-xl' />
                    <ul className='text-gray-100 space-y-2 list-disc pl-6'>
                      <li>ed25519 validator set, round-robin leader, no proof-of-work</li>
                      <li>A quorum is more than two thirds of the set, in both voting phases</li>
                      <li>
                        Round timeouts rotate the leader, and the next leader must re-propose the block that last
                        reached a polka, carrying the polka as proof
                      </li>
                      <li>Committed blocks are SHA-256 hash-linked into one chain every node shares</li>
                      <li>A node that missed a block fetches it from a peer with the quorum that committed it</li>
                    </ul>
                    <p className='text-gray-100 leading-relaxed mt-4 mb-0'>
                      Consensus is the authoritative, globally agreed ledger for marketplace settlement. Both the
                      compute marketplace and the LLM inference marketplace settle buyer to provider through consensus
                      in native MATRIX by default. The earlier per-node pairwise settlement and the direct token-chain
                      transfer remain as separate, non-default primitives for point-to-point transfers, but the
                      marketplace flows no longer use them.
                    </p>
                  </div>

                  {/* Inference */}
                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>LLM inference backends</h2>
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800 mb-8'>
                    <p className='text-gray-100 leading-relaxed mb-4'>
                      Inference is a pluggable backend, so a provider can contribute compute in two ways:
                    </p>
                    <ul className='text-gray-100 space-y-2 list-disc pl-6'>
                      <li>
                        <strong>Local runner</strong> - serve a model from your own hardware through an Ollama-style
                        local HTTP API. A GPU-free echo backend runs the whole flow for testing.
                      </li>
                      <li>
                        <strong>Provider-API proxy</strong> - proxy requests through an OpenAI-compatible API. The key
                        is read from an environment variable and never hardcoded or committed.
                      </li>
                    </ul>
                    <p className='text-gray-100 leading-relaxed mt-4 mb-0'>
                      Jobs are submitted over the <code>matrix.inference.v1</code> InferenceService, capacity is
                      reserved with an up-front affordability check on the reserved price, the backend runs the request,
                      and the job settles buyer-to-provider in native MATRIX through the consensus ledger. The charge is scaled
                      by the provider&apos;s price per unit and capped at the reserved amount, and the job is reported
                      COMPLETED only once that settlement commits and applies on the ledger.
                    </p>
                  </div>

                  {/* OpenAI-compatible */}
                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>The OpenAI-compatible route</h2>
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800 mb-8'>
                    <p className='text-gray-100 leading-relaxed mb-4'>
                      A node serves <code>POST /v1/chat/completions</code> and <code>GET /v1/models</code> on its
                      HTTP endpoint (port 9093 by default), so reaching the marketplace is a one-line change:
                    </p>
                    <pre className='bg-black/50 rounded-lg p-4 overflow-x-auto text-sm text-gray-100 mb-4'>
                      <code>{`from openai import OpenAI

client = OpenAI(
    base_url="http://127.0.0.1:9093/v1",   # your node, or any node
    api_key="<your matrix api key>",
)

r = client.chat.completions.create(
    model="llama-3.3-70b",
    messages=[{"role": "user", "content": "hello"}],
)`}</code>
                    </pre>
                    <p className='text-gray-100 leading-relaxed mb-4'>
                      Three things the OpenAI protocol does not carry, and where they come from here:
                    </p>
                    <ul className='text-gray-100 space-y-2 list-disc pl-6'>
                      <li>
                        <strong>Who pays</strong> - the API key. OpenAI identifies the caller by the key alone, so the
                        key names the on-chain account to charge (<code>account</code> under{' '}
                        <code>security.api_keys</code>). The balance stays on the ledger; the key is a proof of account
                        ownership, not a stored credit balance. A key with no account cannot buy inference.
                      </li>
                      <li>
                        <strong>Which provider</strong> - the model. The request is routed to the cheapest provider
                        advertising that model with capacity to spare, ties breaking on provider id so two identical
                        requests do not scatter at random. The response adds a <code>provider</code> field naming who
                        served it, which an SDK ignores.
                      </li>
                      <li>
                        <strong>How much to reserve</strong> - an upfront estimate from the request. The settled charge
                        comes from the tokens the backend actually reported, so the estimate decides the reservation
                        and never the price.
                      </li>
                    </ul>
                    <p className='text-gray-100 leading-relaxed mt-4 mb-0'>
                      <code>&quot;stream&quot;: true</code> works, as server-sent events: a role frame, a content
                      frame per delta, a stop frame, then the usage and <code>[DONE]</code>. Every frame is flushed as
                      it is produced. A backend that cannot stream is not hidden - the final frame carries{' '}
                      <code>streamed_one_shot</code> so a UI knows the whole answer arrived at once instead of
                      pretending there was a typing effect. A failure after the first frame travels in the body as an
                      error frame, because the status is already 200 by then and a stream that merely stopped would
                      look complete.
                    </p>
                  </div>

                  {/* Client-signed */}
                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>
                    Paying with your own key
                  </h2>
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800 mb-8'>
                    <p className='text-gray-100 leading-relaxed mb-4'>
                      There are two ways an inference job gets paid for, and which one you want depends on who holds
                      the key.
                    </p>
                    <ul className='text-gray-100 space-y-2 list-disc pl-6 mb-4'>
                      <li>
                        <strong>Hosted</strong> - <code>SubmitInferenceJob</code> then{' '}
                        <code>FulfillInferenceJob</code>. The node signs the payment on the buyer&apos;s behalf with a
                        key it already holds. Right for a node you run yourself; it makes the operator a custodian of
                        every caller&apos;s balance on a node other people use.
                      </li>
                      <li>
                        <strong>Client-signed</strong> - <code>RunInferenceJob</code> then{' '}
                        <code>SettleInferenceJob</code>. The node needs no key for you at all.
                      </li>
                      <li>
                        <strong>Hosted and streamed</strong> - <code>StreamInferenceJob</code>, or{' '}
                        <code>&quot;stream&quot;: true</code> on the OpenAI route. Streaming is deliberately
                        unavailable on the client-signed path: there the completion is withheld until the invoice is
                        signed, and streaming the answer out before asking to be paid gives away the only thing
                        holding the buyer to the bargain.
                      </li>
                    </ul>
                    <p className='text-gray-100 leading-relaxed mb-4'>
                      The obvious approach, pre-signing the transfer the way a wallet transfer does, cannot work here,
                      and the reason shapes the design: a transaction signs over an <em>exact</em> amount, and the
                      price of an inference is not knowable until the work is done, because it comes from the tokens
                      the backend reported. Nobody can sign for a number that does not exist yet.
                    </p>
                    <p className='text-gray-100 leading-relaxed mb-4'>
                      So the flow is sign-the-invoice. <code>RunInferenceJob</code> reserves capacity, runs the model,
                      and returns the exact transfer to sign - <strong>without the completion</strong>. You sign it and
                      call <code>SettleInferenceJob</code>, which verifies the signature, checks every signable field
                      against the invoice, submits it to consensus, waits for it to commit and apply, and then hands
                      over the completion.
                    </p>
                    <p className='text-gray-100 leading-relaxed mb-4'>
                      Withholding the completion is the enforcement, because the provider has already done the work.
                      Their exposure is the compute for one job per defecting buyer, bounded further by the
                      affordability check that ran when capacity was reserved - the same exposure any metered API
                      carries, and the price of not holding the buyer&apos;s key. A job whose buyer never signs has its
                      reservation released, so nobody can park a provider&apos;s capacity for free.
                    </p>
                    <p className='text-gray-100 leading-relaxed mb-0'>
                      A valid signature is not sufficient on its own. A transfer of one base unit to an account you
                      control verifies perfectly, so every field of the signed transfer must equal the invoice&apos;s or
                      the settlement is refused.
                    </p>
                  </div>

                  {/* Wallets */}
                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>Wallets, and MetaMask</h2>
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800 mb-8'>
                    <p className='text-gray-100 leading-relaxed mb-4'>
                      MetaMask works fully for <strong>wMATRIX</strong>: it is a standard ERC-20 with EIP-2612
                      permit, so holding, sending, approving and swapping it need nothing from us. What MetaMask
                      cannot do is sign a NATIVE transaction, because native accounts are ed25519 and MetaMask is
                      secp256k1. That is a structural mismatch, and it is the same one Solana, Near, Aptos and Sui
                      all have.
                    </p>
                    <p className='text-gray-100 leading-relaxed mb-4'>
                      Rather than leave people with two wallets, a native account can be controlled by an Ethereum
                      key. There are two kinds of account now:
                    </p>
                    <ul className='text-gray-100 space-y-2 list-disc pl-6 mb-4'>
                      <li>
                        <code>&lt;64 hex&gt;</code> - an ed25519 account, unchanged byte for byte.
                      </li>
                      <li>
                        <code>eth:0x&lt;40 hex&gt;</code> - controlled by that Ethereum address. It funds, spends and
                        reads like any other account.
                      </li>
                    </ul>
                    <p className='text-gray-100 leading-relaxed mb-4'>
                      An eth account signs <strong>EIP-712 typed data</strong>, which is the only thing MetaMask
                      signs, and is also what makes the prompt readable rather than a hex blob. The domain carries no
                      chainId and no verifyingContract - claiming an EVM chain id we do not own would squat on
                      someone else&apos;s domain separator - so a signature made here has no meaning on any EVM
                      chain.
                    </p>
                    <p className='text-gray-100 leading-relaxed mb-4'>
                      The two kinds are told apart by the ACCOUNT ID, not by a field in the signed payload.
                      That matters: adding a scheme byte to the canonical transaction bytes would have invalidated
                      every ed25519 signature ever produced and re-hashed every committed block. The digests are
                      pinned against ethers v6&apos;s own output in the tests, because agreeing with a real Ethereum
                      library is the only thing that proves a wallet will produce a signature the node accepts.
                    </p>
                    <p className='text-gray-100 leading-relaxed mb-0'>
                      For an ed25519 wallet there is now a 12-word BIP-39 recovery phrase and an encrypted keystore,
                      derived at the SLIP-0010 path <code>m/44&apos;/9004&apos;/0&apos;/0&apos;</code>. See the{' '}
                      <a href='/docs/cli' className='text-accent-200 underline hover:text-accent-100'>
                        CLI reference
                      </a>
                      .
                    </p>
                  </div>

                  {/* Console */}
                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>Matrix Console</h2>
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                    <p className='text-gray-100 leading-relaxed mb-4'>
                      Matrix Console is a Tauri + React + Vite desktop app that connects to a local matrixd node to
                      observe and control the marketplace from one window: node connection, providers, jobs, the native
                      MATRIX wallet and settled ledger, the consensus chain, and LLM inference.
                    </p>
                    <p className='text-gray-100 leading-relaxed mb-0'>
                      The React frontend builds and runs everywhere. Packaging the native desktop bundle additionally
                      needs the WebKitGTK/libsoup system libraries.
                    </p>
                  </div>

                  {/* Next Steps */}
                  <div className='bg-blue-500/10 rounded-xl p-6 mt-8 border border-blue-500/20'>
                    <h2 className='text-2xl font-bold text-white mb-4'>Next Steps</h2>
                    <ul className='text-gray-100 space-y-3 list-disc pl-6 mb-0'>
                      <li>
                        <a href='/docs/architecture' className='text-blue-400 hover:text-blue-300 underline'>
                          See how the marketplace fits the Matrix OS architecture
                        </a>
                      </li>
                      <li>
                        <a href='/docs/matrix-protocol' className='text-blue-400 hover:text-blue-300 underline'>
                          Learn about the Matrix Protocol
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
