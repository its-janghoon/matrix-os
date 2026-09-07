'use client';

import { useCallback, useEffect, useRef, useState } from 'react';

import Navigation from '@/components/Navigation';
import {
  chat,
  DEFAULT_ENDPOINT,
  getBalance,
  listModels,
  reportProblem,
  type ModelOffer,
  type Settled,
} from '@/lib/wallet/node';
import type { Message } from '@/lib/wallet/signing';
import { createWallet, forgetWallet, loadWallet, walletSupported, type Wallet } from '@/lib/wallet/wallet';

/**
 * A chat client that holds its own key.
 *
 * It exists to be the honest end of the client-signed path: nothing here asks a
 * node to sign on the reader's behalf, and no API key is involved, because a
 * page cannot keep one secret. The reader's key is generated in the browser,
 * non-extractable, and used to sign two things per message - the run
 * authorization before any work happens, and the payment after.
 *
 * It is deliberately plain about what it cannot do. There is no on-ramp, so the
 * account starts empty and the page hands over the command to fund it rather
 * than pretending. There is no streaming, because the completion is withheld
 * until the payment is signed. And the provider sees the prompt, which no amount
 * of browser-side key handling changes.
 */

interface Turn {
  role: 'user' | 'assistant';
  content: string;
  settled?: Settled;
}

const CARD = 'rounded-xl border border-gray-800 bg-gray-900/50 p-6';
const FIELD =
  'w-full rounded-lg border border-gray-700 bg-black/60 px-3 py-2 font-mono text-sm text-gray-100 ' +
  'outline-none focus:border-gray-500';

export default function ChatPage() {
  const [wallet, setWallet] = useState<Wallet | null>(null);
  const [checking, setChecking] = useState(true);
  const [endpoint, setEndpoint] = useState(DEFAULT_ENDPOINT);
  const [balance, setBalance] = useState<bigint | null>(null);
  const [models, setModels] = useState<ModelOffer[]>([]);
  const [model, setModel] = useState('');
  const [draft, setDraft] = useState('');
  const [turns, setTurns] = useState<Turn[]>([]);
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState('');
  const bottom = useRef<HTMLDivElement>(null);

  useEffect(() => {
    void loadWallet()
      .then(setWallet)
      .catch(() => setWallet(null))
      .finally(() => setChecking(false));
  }, []);

  // A counter rather than a boolean, so a stale reload cannot clobber a newer
  // one when the endpoint is edited twice in quick succession.
  const [reloads, setReloads] = useState(0);
  const reload = useCallback(() => setReloads((n) => n + 1), []);

  useEffect(() => {
    if (!wallet) return;
    let live = true;

    void (async () => {
      try {
        const [bal, offers] = await Promise.all([getBalance(endpoint, wallet.accountId), listModels(endpoint)]);
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
  }, [wallet, endpoint, reloads]);

  useEffect(() => {
    bottom.current?.scrollIntoView({ behavior: 'smooth' });
  }, [turns, busy]);

  const send = async () => {
    if (!wallet || draft.trim() === '' || model === '' || busy) return;

    // The transcript that gets signed is the whole conversation, so the model
    // sees the context and the signature covers exactly what was sent.
    const asked = draft.trim();
    const history: Message[] = [
      ...turns.map((t) => ({ role: t.role, content: t.content })),
      { role: 'user' as const, content: asked },
    ];

    setDraft('');
    setTurns((prev) => [...prev, { role: 'user', content: asked }]);
    setBusy(true);
    setProblem('');

    try {
      const settled = await chat(endpoint, wallet, { model, messages: history });
      setTurns((prev) => [...prev, { role: 'assistant', content: settled.completion, settled }]);
      setBalance(await getBalance(endpoint, wallet.accountId));
    } catch (err) {
      setProblem(reportProblem(err));
    } finally {
      setBusy(false);
    }
  };

  if (checking) {
    return (
      <>
        <Navigation />
        <main className='min-h-screen bg-black px-4 pt-24 text-gray-400'>
          <p className='mx-auto max-w-3xl'>Looking for a wallet in this browser...</p>
        </main>
      </>
    );
  }

  return (
    <>
      <Navigation />
      <main className='min-h-screen bg-black px-4 pb-16 pt-24'>
        <div className='mx-auto max-w-3xl space-y-6'>
          <header>
            <h1 className='mb-2 text-3xl font-bold text-white'>Chat, paying with your own key</h1>
            <p className='text-gray-300'>
              This page holds an ed25519 key generated in your browser and signs with it. The node never has it, no
              API key is involved, and nothing you type is stored here.
            </p>
          </header>

          {!wallet ? <NoWallet onCreate={setWallet} /> : null}

          {wallet ? (
            <>
              <section className={CARD}>
                <div className='mb-4 space-y-1'>
                  <p className='text-sm text-gray-400'>Your account</p>
                  <p className='break-all font-mono text-sm text-gray-100'>{wallet.accountId}</p>
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
                  <button
                    className='text-gray-500 underline hover:text-gray-300'
                    onClick={async () => {
                      await forgetWallet();
                      setWallet(null);
                      setTurns([]);
                      setBalance(null);
                    }}
                  >
                    forget this wallet
                  </button>
                </div>

                {balance === 0n ? <Funding account={wallet.accountId} /> : null}
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
              </section>

              <Transcript turns={turns} busy={busy} bottom={bottom} />

              <section className='flex gap-3'>
                <input
                  className={FIELD}
                  placeholder={busy ? 'waiting for the model...' : 'Say something'}
                  value={draft}
                  disabled={busy || models.length === 0}
                  onChange={(e) => setDraft(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') void send();
                  }}
                />
                <button
                  className='rounded-lg bg-white px-5 py-2 text-sm font-semibold text-black disabled:opacity-40'
                  disabled={busy || draft.trim() === '' || models.length === 0}
                  onClick={() => void send()}
                >
                  Send
                </button>
              </section>

              <Caveats />
            </>
          ) : null}
        </div>
      </main>
    </>
  );
}

function NoWallet({ onCreate }: { onCreate: (w: Wallet) => void }) {
  const [problem, setProblem] = useState('');

  if (!walletSupported()) {
    return (
      <section className={CARD}>
        <p className='text-gray-300'>
          This browser cannot hold a wallet here. It needs IndexedDB and WebCrypto, and WebCrypto only works in a
          secure context - so open this page over https, or on localhost.
        </p>
      </section>
    );
  }

  return (
    <section className={CARD}>
      <p className='mb-4 text-gray-300'>
        No wallet in this browser yet. Creating one generates an ed25519 keypair whose private half is{' '}
        <strong>non-extractable</strong>: this page can ask it to sign, but no script can read the key material, and
        it never leaves your browser.
      </p>
      <p className='mb-4 text-sm text-gray-400'>
        There is no backup, and there cannot be - a key that could be written down would not be non-extractable. Clear
        this site&apos;s data and the account is gone with whatever it held.
      </p>
      <button
        className='rounded-lg bg-white px-5 py-2 text-sm font-semibold text-black'
        onClick={async () => {
          try {
            onCreate(await createWallet());
          } catch (err) {
            setProblem(err instanceof Error ? err.message : String(err));
          }
        }}
      >
        Create a wallet
      </button>
      {problem !== '' ? <p className='mt-3 text-sm text-red-300'>{problem}</p> : null}
    </section>
  );
}

function Funding({ account }: { account: string }) {
  return (
    <div className='mt-4 rounded-lg border border-yellow-500/20 bg-yellow-500/5 p-4'>
      <p className='mb-2 text-sm text-yellow-100'>
        This account holds nothing, so a provider will refuse the job before doing any work. There is no on-ramp yet -
        buying MATRIX with USDC over the bridge is built but undeployed - so on a node you run, fund it yourself:
      </p>
      <pre className='overflow-x-auto rounded bg-black/60 p-3 text-xs text-gray-200'>
        <code>{`matrix --api-key <key> fund --account ${account} --amount 1000000`}</code>
      </pre>
    </div>
  );
}

function Transcript({
  turns,
  busy,
  bottom,
}: {
  turns: Turn[];
  busy: boolean;
  bottom: React.RefObject<HTMLDivElement | null>;
}) {
  return (
    <section className='space-y-3'>
      {turns.length === 0 && !busy ? (
        <p className={`${CARD} text-sm text-gray-400`}>Nothing yet. Each message costs a signature and some MATRIX.</p>
      ) : null}
      {turns.map((turn, i) => (
        <div
          key={i}
          className={`rounded-xl border p-4 ${
            turn.role === 'user' ? 'border-gray-800 bg-gray-900/40' : 'border-gray-700 bg-gray-900/70'
          }`}
        >
          <p className='mb-1 text-xs uppercase tracking-wide text-gray-500'>{turn.role}</p>
          <p className='whitespace-pre-wrap text-gray-100'>{turn.content}</p>
          {turn.settled ? (
            <p className='mt-3 font-mono text-xs text-gray-500'>
              paid {turn.settled.units.toString()} base units to {turn.settled.provider} - {turn.settled.promptTokens}{' '}
              prompt + {turn.settled.completionTokens} completion tokens
            </p>
          ) : null}
        </div>
      ))}
      {busy ? (
        <div className='rounded-xl border border-gray-800 bg-gray-900/40 p-4 text-sm text-gray-400'>
          Signing the run, waiting for the model, then signing the payment...
        </div>
      ) : null}
      <div ref={bottom} />
    </section>
  );
}

function Caveats() {
  return (
    <section className='rounded-xl border border-gray-800 p-6 text-sm text-gray-400'>
      <h2 className='mb-3 text-base font-semibold text-gray-200'>What this does and does not protect</h2>
      <ul className='list-disc space-y-2 pl-5'>
        <li>
          Your key is non-extractable, so no script can read it and use it elsewhere. It can still ask this page&apos;s
          key to sign while the page is open - non-extractability is not a hardware signer.
        </li>
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
