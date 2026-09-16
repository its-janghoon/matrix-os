'use client';

import Link from 'next/link';
import { useSearchParams } from 'next/navigation';
import { Suspense, useCallback, useEffect, useMemo, useState } from 'react';

import Navigation from '@/components/Navigation';
import { PAGE_COLUMN } from '@/lib/layout';
import {
  DEFAULT_ENDPOINT,
  getBalance,
  listModels,
  reportProblem,
  type ModelOffer,
  type SellerChoice,
} from '@/lib/wallet/node';
import { MatrixRuntimeProvider } from '@/components/assistant/runtime';
import { Thread } from '@/components/assistant/thread';
import { browserSigner } from '@/lib/wallet/browserSigner';
import { connectMetamask, isEvmSigner, MetamaskSigner, metamaskAvailable } from '@/lib/wallet/metamask';
import type { Signer } from '@/lib/wallet/signer';
import { createWallet, forgetWallet, loadWallet, walletSupported } from '@/lib/wallet/wallet';

/**
 * A chat client that holds its own key.
 *
 * It exists to be the honest end of the client-signed path: nothing here asks a
 * node to sign on the reader's behalf, and no API key is involved, because a
 * page cannot keep one secret. The reader's key is generated in the browser,
 * non-extractable, and used to sign two things per message - the run
 * authorization before any work happens, and the payment after.
 *
 * Every answer comes with the seller's signed receipt, checked HERE - the bytes
 * the node signed, against the prompt this page actually sent and the answer it
 * got back. A receipt the seller's own node vouched for would be evidence of
 * nothing; the point of one is that the holder can check it without asking the
 * seller, or us, for anything.
 *
 * It is deliberately plain about what it cannot do. The account starts empty and
 * the page points at the bridge rather than pretending a public on-ramp exists.
 * There is no streaming, because the completion is withheld
 * until the payment is signed. The provider sees the prompt, which no amount of
 * browser-side key handling changes. And a verified receipt means the seller
 * MADE its claim, not that the claim is true: no signature can tell a buyer that
 * the model named is the model that ran.
 */

const CARD = 'rounded-xl border border-gray-800 bg-gray-900/50 p-6';
const FIELD =
  'w-full rounded-lg border border-gray-700 bg-black/60 px-3 py-2 font-mono text-sm text-gray-100 ' +
  'outline-none focus:border-gray-500';

export default function ChatPage() {
  // useSearchParams suspends, and the whole page suspending would blank it while
  // the query is read. The boundary keeps that to the part that needs it.
  return (
    <Suspense fallback={null}>
      <Chat />
    </Suspense>
  );
}

function Chat() {
  const params = useSearchParams();
  // What the directory page handed over: WHICH seller, never where it lives. The
  // address is read back from the directory when the purchase runs, so a link
  // cannot point somebody's prompt at a host of the sender's choosing.
  const [chosen, setChosen] = useState<SellerChoice | null>(() => {
    const node = params.get('node');
    const provider = params.get('provider');
    return node && provider ? { nodeId: node, providerId: provider } : null;
  });

  const [signer, setSigner] = useState<Signer | null>(null);
  const [checking, setChecking] = useState(true);
  const [minBond, setMinBond] = useState('');
  const [endpoint, setEndpoint] = useState(params.get('endpoint') ?? DEFAULT_ENDPOINT);
  const [balance, setBalance] = useState<bigint | null>(null);
  const [models, setModels] = useState<ModelOffer[]>([]);
  const [model, setModel] = useState(params.get('model') ?? '');
  const [problem, setProblem] = useState('');

  // Parsed here rather than where it is used, because a half-typed figure is an
  // ordinary state of a text field and must not throw during a render. An
  // unreadable value means "no floor", which is what an empty field means too.
  const minBondUnits = useMemo(() => {
    const raw = minBond.trim();
    if (raw === '') return undefined;
    try {
      return BigInt(raw);
    } catch {
      return undefined;
    }
  }, [minBond]);

  useEffect(() => {
    // Only the browser key can be picked up automatically. MetaMask needs an
    // explicit connect, because silently reading an account a user has not
    // authorised for this page is exactly what a wallet prompt exists to stop.
    void loadWallet()
      .then((w) => setSigner(w ? browserSigner(w) : null))
      .catch(() => setSigner(null))
      .finally(() => setChecking(false));
  }, []);

  // MetaMask can change account underneath the page, and until this existed it
  // did so invisibly: the header kept naming the account that connected while
  // the extension had moved on, so the balance shown, the receipts checked and
  // the charges settled all belonged to an account the reader was no longer
  // using. Following the change is the only honest option - a page cannot hold
  // a wallet to an account, and pretending it did is what made the mismatch
  // silent.
  useEffect(() => {
    if (!signer || !isEvmSigner(signer)) return;
    return signer.subscribe((change) => {
      if (change.address === undefined || change.address === signer.address) return;
      setBalance(null);
      setProblem('');
      setSigner(change.address === null ? null : new MetamaskSigner(signer.provider, change.address));
    });
  }, [signer]);

  // A counter rather than a boolean, so a stale reload cannot clobber a newer
  // one when the endpoint is edited twice in quick succession.
  const [reloads, setReloads] = useState(0);
  const reload = useCallback(() => setReloads((n) => n + 1), []);

  useEffect(() => {
    if (!signer) return;
    let live = true;

    void (async () => {
      try {
        const [bal, offers] = await Promise.all([getBalance(endpoint, signer.accountId), listModels(endpoint)]);
        if (!live) return;
        setProblem('');
        setBalance(bal);
        setModels(offers);
        setModel((current) => (current !== '' ? current : (offers[0]?.id ?? '')));
      } catch (err) {
        if (live) setProblem(reportProblem(err));
      }
    })();

    return () => {
      live = false;
    };
  }, [signer, endpoint, reloads]);

  if (checking) {
    return (
      <>
        <Navigation />
        <main className='min-h-screen bg-black px-4 pt-24 text-gray-400'>
          <p className={PAGE_COLUMN}>Looking for a wallet in this browser...</p>
        </main>
      </>
    );
  }

  return (
    <>
      <Navigation />
      <main className='min-h-screen bg-black px-4 pb-16 pt-24'>
        <div className={`${PAGE_COLUMN} space-y-6`}>
          <header>
            <h1 className='mb-2 text-3xl font-bold text-white'>Chat, paying with your own key</h1>
            <p className='max-w-3xl text-gray-300'>
              Your own key signs every message, with MetaMask or with a key this page generates. The node never has
              it, no API key is involved, and nothing you type is stored here.
            </p>
          </header>

          {chosen ? <PickedSeller chosen={chosen} onClear={() => setChosen(null)} /> : null}

          {!signer ? <NoWallet onReady={setSigner} /> : null}

          {signer ? (
            <>
              <section className={CARD}>
                <div className='mb-4 space-y-1'>
                  <p className='text-sm text-gray-400'>
                    Your account{' '}
                    <span className='text-gray-500'>
                      ({signer.kind === 'metamask' ? 'MetaMask' : 'a key in this browser'})
                    </span>
                  </p>
                  <p className='break-all font-mono text-sm text-gray-100'>{signer.accountId}</p>
                </div>
                <label className='mb-1 block text-sm text-gray-400' htmlFor='endpoint'>
                  Node endpoint
                </label>
                <input
                  id='endpoint'
                  className={FIELD}
                  value={endpoint}
                  onChange={(e) => setEndpoint(e.target.value)}
                  spellCheck={false}
                />
                <div className='mt-4 flex flex-wrap items-center gap-4 text-sm'>
                  <span className='text-gray-400'>
                    Balance:{' '}
                    <span className='font-mono text-gray-100'>
                      {balance === null ? 'unknown' : balance.toString()}
                    </span>{' '}
                    base units
                  </span>
                  <button
                    className='text-gray-400 underline hover:text-gray-200'
                    onClick={reload}
                  >
                    refresh
                  </button>
                  {signer.kind === 'metamask' ? (
                    <button
                      className='text-gray-400 underline hover:text-gray-200'
                      onClick={async () => {
                        // Disconnecting first and connecting again does NOT do
                        // this: the wallet already holds a permission for this
                        // site and hands the same account straight back without
                        // asking. Only a permission request re-opens the picker.
                        try {
                          setSigner(await connectMetamask({ chooseAccount: true }));
                          setBalance(null);
                          setProblem('');
                        } catch (err) {
                          setProblem(err instanceof Error ? err.message : String(err));
                        }
                      }}
                    >
                      use a different account
                    </button>
                  ) : null}
                  <button
                    className='text-gray-500 underline hover:text-gray-300'
                    onClick={async () => {
                      // Only the browser key is ours to destroy. Disconnecting
                      // MetaMask drops our handle on it and nothing else, which
                      // is the honest thing for a wallet we do not own.
                      if (signer.kind === 'browser') await forgetWallet();
                      setSigner(null);
                      setBalance(null);
                    }}
                  >
                    {signer.kind === 'metamask' ? 'disconnect' : 'forget this wallet'}
                  </button>
                </div>

                {balance === 0n ? <Funding account={signer.accountId} /> : null}
              </section>

              {problem !== '' ? (
                <p className='rounded-lg border border-red-500/30 bg-red-500/10 p-4 text-sm text-red-200'>{problem}</p>
              ) : null}

              <section className={CARD}>
                <label className='mb-1 block text-sm text-gray-400' htmlFor='model'>
                  Model
                </label>
                {models.length === 0 ? (
                  <p className='text-sm text-gray-400'>
                    This node is advertising no models. A provider declares them under{' '}
                    <code className='text-gray-200'>inference.backends[].models</code>, or with{' '}
                    <code className='text-gray-200'>matrix provider register --models</code>.
                  </p>
                ) : (
                  <select
                    id='model'
                    className={FIELD}
                    value={model}
                    onChange={(e) => setModel(e.target.value)}
                  >
                    {models.map((m) => (
                      <option key={m.id} value={m.id}>
                        {m.id} - {m.pricePerUnit.toString()}/unit, {m.providers} provider
                        {m.providers === 1 ? '' : 's'}
                      </option>
                    ))}
                  </select>
                )}

                {chosen ? null : <AutomaticSeller minBond={minBond} setMinBond={setMinBond} />}
              </section>

              {/*
                Keyed by the account, so switching wallet starts a new thread
                rather than carrying the old one over. One account's paid
                answers above another account's next question would be showing
                someone else's purchase as theirs - and the next message would
                sign that transcript.
              */}
              <MatrixRuntimeProvider
                key={signer.accountId}
                settings={{
                  endpoint,
                  signer,
                  model,
                  chosen: chosen ?? undefined,
                  minBond: minBondUnits,
                }}
                onSettled={reload}
              >
                <Thread />
              </MatrixRuntimeProvider>

              <Caveats kind={signer.kind} />
            </>
          ) : null}
        </div>
      </main>
    </>
  );
}

function NoWallet({ onReady }: { onReady: (s: Signer) => void }) {
  const [problem, setProblem] = useState('');

  // Whether a wallet extension is present is checked ON CLICK, not during
  // render: window.ethereum does not exist in the server render, so reading it
  // there would make the two renders disagree. Checking on click also means the
  // button can say WHY it did not work, which a disabled button cannot.
  const canHoldAKey = walletSupported();

  return (
    <section className={`${CARD} space-y-6`}>
      <div>
        <h2 className='mb-2 text-lg font-semibold text-white'>Choose a wallet</h2>
        <p className='text-sm text-gray-400'>
          Either way the node never holds your key: it verifies your signature and settles.
        </p>
      </div>

      <div className='rounded-lg border border-gray-800 p-4'>
        <h3 className='mb-2 font-semibold text-gray-100'>MetaMask</h3>
        <p className='mb-3 text-sm text-gray-400'>
          Your Ethereum address controls a native account (<code>eth:0x...</code>). MetaMask cannot sign the
          chain&apos;s ordinary ed25519 transactions, so these are signed as EIP-712 typed data instead - which is
          also why the prompt shows what you are approving rather than a hex blob.
        </p>
        <p className='mb-3 text-sm text-gray-400'>
          This is the account that survives: it works anywhere MetaMask is installed, and clearing this site&apos;s
          data does not touch it.
        </p>
        <p className='mb-3 text-sm text-yellow-200/80'>
          <strong>Sending costs two confirmations, every message.</strong> One authorises the run before the seller
          spends GPU time on it; one pays for it once the tokens have been counted, which is the first moment the
          amount is known. Neither can be dropped while the key is yours - a seller will not work unpaid, and the
          chain will not move money without your signature on the amount.
        </p>
        <button
          className='rounded-lg bg-white px-5 py-2 text-sm font-semibold text-black'
          onClick={async () => {
            if (!metamaskAvailable()) {
              setProblem('No wallet extension is installed on this page. Install MetaMask, or use a browser key below.');
              return;
            }
            try {
              onReady(await connectMetamask({ chooseAccount: true }));
            } catch (err) {
              setProblem(err instanceof Error ? err.message : String(err));
            }
          }}
        >
          Connect MetaMask
        </button>
      </div>

      <div className='rounded-lg border border-gray-800 p-4'>
        <h3 className='mb-2 font-semibold text-gray-100'>A key in this browser</h3>
        {canHoldAKey ? (
          <>
            <p className='mb-3 text-sm text-gray-400'>
              An ed25519 keypair generated here, whose private half is <strong>non-extractable</strong>: this page
              can ask it to sign, but no script can read the key material.
            </p>
            <p className='mb-3 text-sm text-gray-400'>
              There is no backup, and there cannot be - a key that could be written down would not be
              non-extractable. Clear this site&apos;s data and the account is gone with whatever it held.
            </p>
            <p className='mb-3 text-sm text-gray-300'>
              <strong>Nothing pops up when you send.</strong> This key signs in the page, so a message costs one
              Enter and no confirmations. That is the whole difference in feel - and it is the same fact as the
              risk: a key that signs without asking is a key that signs without asking. Fund it with what you are
              willing to lose to a cleared browser.
            </p>
            <button
              className='rounded-lg border border-gray-600 px-5 py-2 text-sm font-semibold text-gray-100'
              onClick={async () => {
                try {
                  onReady(browserSigner(await createWallet()));
                } catch (err) {
                  setProblem(err instanceof Error ? err.message : String(err));
                }
              }}
            >
              Create a browser wallet
            </button>
          </>
        ) : (
          <p className='text-sm text-gray-400'>
            This browser cannot hold a key here. It needs IndexedDB and WebCrypto, and WebCrypto only works in a
            secure context - so open this page over https, or on localhost.
          </p>
        )}
      </div>

      {problem !== '' ? <p className='text-sm text-red-300'>{problem}</p> : null}
    </section>
  );
}

function Funding({ account }: { account: string }) {
  return (
    <div className='mt-4 space-y-3 rounded-lg border border-yellow-500/20 bg-yellow-500/5 p-4'>
      <p className='text-sm text-yellow-100'>
        This account holds nothing, so a provider will refuse the job before doing any work.
      </p>
      <p className='text-sm text-yellow-100/90'>
        If you hold wMATRIX on Base, the{' '}
        <a className='underline underline-offset-2' href='/bridge'>
          bridge
        </a>{' '}
        is the way in: burn it there naming this account as the recipient, and the escrow is released to you here. You
        pay Base gas, and the page uses only the configured contract.
      </p>
      <p className='text-sm text-yellow-100/70'>
        Otherwise somebody sends you some. MATRIX is not pegged to anything and this page does not claim a public
        on-ramp that does not exist. On a node you run yourself, seed the account from its genesis allocation - a reward
        pool transfer is refused on a multi-validator network, because it is not consensus-ordered and would leave the
        validators disagreeing about the pool.
      </p>
      <CopyableAccount account={account} />
    </div>
  );
}

/**
 * The account id, with a way to take it that does not involve selecting 64
 * characters of monospace by hand.
 *
 * Funding an account means getting this string somewhere else exactly - into a
 * wallet's recipient field, or a `matrix wallet transfer --to`. One character
 * wrong is not a failed transfer, it is a successful transfer to an account
 * nobody holds the key for.
 */
function CopyableAccount({ account }: { account: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className='flex flex-wrap items-center gap-3'>
      <p className='break-all font-mono text-xs text-yellow-100/60'>{account}</p>
      <button
        className='shrink-0 text-xs text-yellow-100/80 underline underline-offset-2 hover:text-yellow-100'
        onClick={() => {
          // Clipboard access is denied outside a secure context and can be
          // refused inside one. Saying nothing happened is better than a
          // "copied" that did not.
          void navigator.clipboard
            ?.writeText(account)
            .then(() => setCopied(true))
            .catch(() => setCopied(false));
        }}
      >
        {copied ? 'copied' : 'copy'}
      </button>
    </div>
  );
}

function Caveats({ kind }: { kind: Signer['kind'] }) {
  return (
    <section className='rounded-xl border border-gray-800 p-6 text-sm text-gray-400'>
      <h2 className='mb-3 text-base font-semibold text-gray-200'>What this does and does not protect</h2>
      <ul className='list-disc space-y-2 pl-5'>
        {kind === 'metamask' ? (
          <li>
            Your key is in MetaMask, and this page never sees it. Every signature is EIP-712 typed data, so the
            prompt shows what you are approving; read it, because approving one is how the money moves. That is
            also why a message asks twice: the run is authorised before the work and paid for after it, and the
            amount does not exist until the tokens are counted. One approval covering a whole session needs a
            funded escrow the seller can draw on, which is a change to the chain and not to this page.
          </li>
        ) : (
          <li>
            Your key is non-extractable, so no script can read it and use it elsewhere. It can still ask this
            page&apos;s key to sign while the page is open - non-extractability is not a hardware signer. And there
            is no backup: clear this site&apos;s data and the account is gone.
          </li>
        )}
        <li>
          <strong>Your prompt is not private.</strong> The provider runs the model on their hardware, so they see it.
          This page stores nothing; that is a different claim.
        </li>
        <li>
          <strong>Your payments are public and permanent.</strong> Who paid whom, how much, and when is on the chain
          forever. Only the prompt and the answer stay off it.
        </li>
        <li>
          No streaming here, and that is a consequence rather than a gap: the node withholds the completion until you
          have signed the invoice, and streaming it out first would hand over the only thing holding you to the
          bargain. The hosted path streams, and there the node holds a key for you.
        </li>
      </ul>
    </section>
  );
}

/**
 * Who this page will buy from.
 *
 * Two ways to end up here, and the page has to say which is in force. Arriving
 * from the directory picks ONE seller, by identity - the address is read back
 * from the directory when the purchase runs, so a link cannot route a prompt at
 * a host of the sender's choosing. Arriving directly leaves it automatic:
 * cheapest that can be reached, which is the right default and is also how an
 * attacker undercutting everybody wins the traffic. The bond floor is the answer
 * to that, and it is only meaningful while the choice IS automatic - somebody
 * who picked a seller from a table showing its stake has already decided.
 *
 * The picked seller is shown ABOVE the wallet prompt, before there is a wallet
 * at all. Arriving from the directory and being shown nothing but "choose a
 * wallet" reads as the choice having been dropped on the way.
 */
function PickedSeller({ chosen, onClear }: { chosen: SellerChoice; onClear: () => void }) {
  return (
    <section className='rounded-xl border border-gray-800 bg-gray-900/40 p-4 text-sm'>
      <p className='text-gray-300'>
        Buying from the seller you picked, on node{' '}
        <span className='font-mono text-xs text-gray-400'>{chosen.nodeId.slice(0, 12)}...</span>
      </p>
      <p className='mt-1 text-xs text-gray-500'>
        Its address is read from the directory when you send, not from this link. If it has stopped announcing, the send
        fails and says so rather than quietly going somewhere else.
      </p>
      <button className='mt-2 text-xs text-gray-400 underline underline-offset-2 hover:text-gray-200' onClick={onClear}>
        Let the page pick instead
      </button>
    </section>
  );
}

function AutomaticSeller({ minBond, setMinBond }: { minBond: string; setMinBond: (v: string) => void }) {
  return (
    <div className='mt-4 rounded-lg border border-gray-800 bg-black/40 p-3 text-sm'>
      <p className='text-gray-400'>
        The cheapest seller that can be reached will serve this.{' '}
        <Link className='underline underline-offset-2 hover:text-gray-200' href='/market'>
          Pick one yourself
        </Link>{' '}
        to see who they are first.
      </p>
      <label className='mt-3 block text-xs uppercase tracking-wide text-gray-500' htmlFor='min-bond'>
        Refuse sellers staking less than
      </label>
      <input
        id='min-bond'
        className='mt-1 w-full rounded-lg border border-gray-700 bg-black/60 px-3 py-2 font-mono text-xs text-gray-100 outline-none focus:border-gray-500'
        placeholder='0 - any seller, staked or not'
        value={minBond}
        onChange={(e) => setMinBond(e.target.value.replace(/[^0-9]/g, ''))}
        spellCheck={false}
      />
      <p className='mt-1 text-xs text-gray-500'>
        A stake does not make a seller honest - nothing can - but it makes a listing cost capital, which is what stops
        one attacker offering ten thousand of them at a price nobody can match.
      </p>
    </div>
  );
}
