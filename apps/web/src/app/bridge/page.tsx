'use client';

import { useEffect, useMemo, useState } from 'react';
import type { Hash } from 'viem';
import { MinBridgeLockAmount, NativeUnit } from '@matrix-os/protocol';
import Navigation from '@/components/Navigation';
import { Footer } from '@/components/Footer';
import { bridgeConfigState } from '@/lib/bridge/config';
import { prepareMint, simulateAndWriteBurn, simulateAndWriteMint, type PreparedMint } from '@/lib/bridge/evm';
import {
  clearPendingLock,
  collectLockAttestations,
  loadPendingLock,
  submitBridgeLock,
  wholeMatrixToErc20BaseUnits,
  wholeMatrixToNativeBaseUnits,
  type PendingBridgeLock,
} from '@/lib/bridge/lock';
import { connectMetamask, metamaskAvailable, type EvmSigner } from '@/lib/wallet/metamask';
import { getBalance, reportProblem } from '@/lib/wallet/node';

const CARD = 'rounded-2xl border border-white/10 bg-white/[0.035] p-6';
const FIELD = 'w-full rounded-lg border border-white/15 bg-black/60 px-3 py-2 text-sm text-white outline-none focus:border-primary-400';
const PRIMARY = 'rounded-lg bg-white px-5 py-2 text-sm font-semibold text-black disabled:cursor-not-allowed disabled:opacity-40';
const SECONDARY = 'rounded-lg border border-white/20 px-5 py-2 text-sm font-semibold text-white disabled:cursor-not-allowed disabled:opacity-40';

export default function BridgePage() {
  const config = bridgeConfigState.config;
  const [wallet, setWallet] = useState<EvmSigner | null>(null);
  const [chainId, setChainId] = useState<number | null>(null);
  const [nativeBalance, setNativeBalance] = useState<bigint | null>(null);
  const [amount, setAmount] = useState('100');
  const [acknowledged, setAcknowledged] = useState(false);
  const [pending, setPending] = useState<PendingBridgeLock | null>(null);
  const [prepared, setPrepared] = useState<PreparedMint | null>(null);
  const [mintTx, setMintTx] = useState<Hash | null>(null);
  const [burnAmount, setBurnAmount] = useState('');
  const [nativeRecipient, setNativeRecipient] = useState('');
  const [burnTx, setBurnTx] = useState<Hash | null>(null);
  const [busy, setBusy] = useState('');
  const [status, setStatus] = useState('');
  const [problem, setProblem] = useState('');

  useEffect(() => {
    if (!config) return;
    void loadPendingLock(config)
      .then((saved) => {
        if (saved) {
          setPending(saved);
          setStatus('Recovered a committed pending lock from this browser. Reconnect the same wallet to continue.');
        }
      })
      .catch((error) => setProblem(reportProblem(error)));
  }, [config]);

  useEffect(() => {
    if (!wallet || !config) return;
    return wallet.subscribe((change) => {
      if (change.chainId !== undefined) setChainId(change.chainId);
      if (change.address !== undefined && change.address !== wallet.address) {
        setWallet(null);
        setPrepared(null);
        setProblem('The wallet account changed. Reconnect before continuing.');
      }
    });
  }, [wallet, config]);

  const onConnect = async () => {
    if (!config) return;
    setBusy('connect');
    setProblem('');
    try {
      if (!metamaskAvailable()) throw new Error('No injected EIP-1193 wallet was found. Install MetaMask first.');
      const connected = await connectMetamask();
      const selectedChain = await connected.getChainId();
      setWallet(connected);
      setChainId(selectedChain);
      const saved = await loadPendingLock(config, connected.address).catch((error) => {
        setProblem(reportProblem(error));
        return null;
      });
      const balance = await getBalance(config.validatorUrls[0] as string, connected.accountId);
      setPending(saved);
      setNativeBalance(balance);
      if (saved) setStatus('Recovered the pending lock for this wallet.');
      else if (!problem) setStatus('Wallet connected.');
    } catch (error) {
      setProblem(reportProblem(error));
    } finally {
      setBusy('');
    }
  };

  const onSwitch = async () => {
    if (!wallet || !config) return;
    setBusy('switch');
    setProblem('');
    try {
      await wallet.switchChain(config.chain);
      setChainId(await wallet.getChainId());
      setStatus(`Switched to ${config.chain.name}.`);
    } catch (error) {
      setProblem(reportProblem(error));
    } finally {
      setBusy('');
    }
  };

  const onLock = async () => {
    if (!wallet || !config || chainId !== config.chain.id || !acknowledged) return;
    setBusy('lock');
    setProblem('');
    setPrepared(null);
    try {
      const nativeAmount = wholeMatrixToNativeBaseUnits(amount);
      if (nativeBalance !== null && nativeAmount > nativeBalance) throw new Error('native MATRIX balance is too low for this lock');
      const saved = await submitBridgeLock({
        wallet,
        config,
        validatorEndpoint: config.validatorUrls[0] as string,
        nativeAmount,
      });
      setPending(saved);
      setNativeBalance(await getBalance(config.validatorUrls[0] as string, wallet.accountId));
      setStatus('Native lock committed and applied. Its metadata is saved locally until mint submission.');
    } catch (error) {
      setProblem(reportProblem(error));
      // The signed intent is persisted before submission. A lost response may
      // still mean consensus committed, so expose recovery instead of enabling
      // a fresh lock with a different nonce.
      const recoverable = await loadPendingLock(config, wallet.address).catch(() => null);
      if (recoverable) {
        setPending(recoverable);
        setStatus('Lock submission outcome is not confirmed. The signed intent was preserved; collect attestations to check for commit safely.');
      }
    } finally {
      setBusy('');
    }
  };

  const onCollect = async () => {
    if (!wallet || !config || !pending || chainId !== config.chain.id) return;
    setBusy('collect');
    setProblem('');
    try {
      const attestations = await collectLockAttestations({ pending, validatorUrls: config.validatorUrls });
      const mint = await prepareMint(wallet, config, attestations);
      setPrepared(mint);
      setStatus(`Verified ${mint.signers.length} registered attestor signatures; mint simulation is ready.`);
    } catch (error) {
      setProblem(reportProblem(error));
    } finally {
      setBusy('');
    }
  };

  const onMint = async () => {
    if (!wallet || !config || !prepared || chainId !== config.chain.id) return;
    setBusy('mint');
    setProblem('');
    try {
      const hash = await simulateAndWriteMint(wallet, config, prepared);
      setMintTx(hash);
      clearPendingLock();
      setPending(null);
      setPrepared(null);
      setStatus('Mint transaction confirmed and the lock is marked minted on-chain.');
    } catch (error) {
      setProblem(reportProblem(error));
    } finally {
      setBusy('');
    }
  };

  const onBurn = async () => {
    if (!wallet || !config || chainId !== config.chain.id) return;
    setBusy('burn');
    setProblem('');
    try {
      const hash = await simulateAndWriteBurn(
        wallet,
        config,
        wholeMatrixToErc20BaseUnits(burnAmount),
        nativeRecipient,
      );
      setBurnTx(hash);
      setStatus('Burn transaction submitted. Native release follows the validator-observed Burned event.');
    } catch (error) {
      setProblem(reportProblem(error));
    } finally {
      setBusy('');
    }
  };

  const stages = useMemo(() => [
    ['1. Connect', wallet ? 'complete' : 'waiting'],
    ['2. Correct Base chain', wallet && chainId === config?.chain.id ? 'complete' : 'waiting'],
    ['3. Native lock', pending?.settlement === 'committed' || prepared || mintTx ? 'complete' : pending ? 'checking' : 'waiting'],
    ['4. Attestor threshold', prepared || mintTx ? 'complete' : 'waiting'],
    ['5. Mint', mintTx ? 'submitted' : 'waiting'],
  ], [wallet, chainId, config, pending, prepared, mintTx]);

  if (!config) {
    return (
      <>
        <Navigation />
        <main className='min-h-screen bg-black px-4 pb-20 pt-28 text-white'>
          <section className='mx-auto max-w-3xl rounded-2xl border border-yellow-500/30 bg-yellow-500/10 p-8'>
            <p className='text-sm font-semibold uppercase tracking-wider text-yellow-300'>Launch not configured</p>
            <h1 className='mt-3 text-3xl font-bold'>The public Base bridge is inactive.</h1>
            <p className='mt-4 text-gray-300'>
              No contract address or validator endpoint is assumed. Active controls stay disabled until a deployment
              supplies a supported Base chain ID, WrappedMatrix address, and explicit validator URLs.
            </p>
            <p className='mt-4 text-sm text-yellow-100'>{bridgeConfigState.error}</p>
          </section>
        </main>
        <Footer />
      </>
    );
  }

  const wrongChain = wallet !== null && chainId !== config.chain.id;
  return (
    <>
      <Navigation />
      <main className='min-h-screen bg-black px-4 pb-20 pt-24 text-white'>
        <div className='mx-auto max-w-4xl space-y-6'>
          <header>
            <p className='text-sm font-semibold uppercase tracking-wider text-primary-300'>Native MATRIX ↔ wMATRIX</p>
            <h1 className='mt-2 text-4xl font-bold'>Base bridge</h1>
            <p className='mt-3 max-w-3xl text-gray-300'>
              MetaMask signs the native lock as readable Matrix EIP-712 data and submits Base contract transactions
              from the same address. You pay Base gas directly; there is no relayer or gas sponsorship.
            </p>
          </header>

          <section className={CARD}>
            <h2 className='font-semibold'>Progress</h2>
            <div className='mt-4 grid gap-2 sm:grid-cols-5'>
              {stages.map(([label, state]) => (
                <div key={label} className={`rounded-lg border p-3 text-xs ${state === 'waiting' ? 'border-white/10 text-gray-500' : 'border-green-500/30 bg-green-500/10 text-green-200'}`}>
                  <div>{label}</div><div className='mt-1'>{state}</div>
                </div>
              ))}
            </div>
          </section>

          {problem ? <p className='rounded-lg border border-red-500/30 bg-red-500/10 p-4 text-sm text-red-200'>{problem}</p> : null}
          {status ? <p className='rounded-lg border border-blue-500/30 bg-blue-500/10 p-4 text-sm text-blue-100'>{status}</p> : null}

          <section className={CARD}>
            <h2 className='text-xl font-semibold'>1. Wallet and network</h2>
            <div className='mt-3 rounded-lg border border-white/10 p-3 text-xs text-gray-400'>
              <p>Deployment: {config.chain.name} ({config.chain.id})</p>
              <p className='mt-1 break-all font-mono'>WrappedMatrix: {config.wrappedMatrixAddress}</p>
              <p className='mt-1'>Explicit validators: {config.validatorUrls.length}</p>
            </div>
            {!wallet ? (
              <button className={`${PRIMARY} mt-4`} disabled={busy !== ''} onClick={() => void onConnect()}>
                {busy === 'connect' ? 'Connecting…' : 'Connect MetaMask'}
              </button>
            ) : (
              <div className='mt-4 space-y-2 text-sm text-gray-300'>
                <p className='break-all font-mono text-white'>{wallet.address}</p>
                <p>Native account: <span className='font-mono'>{wallet.accountId}</span></p>
                <p>Native balance: <span className='font-mono'>{nativeBalance?.toString() ?? 'unknown'}</span> base units</p>
                <p>Selected chain: {chainId ?? 'unknown'} · required: {config.chain.name} ({config.chain.id})</p>
                {wrongChain ? (
                  <button className={PRIMARY} disabled={busy !== ''} onClick={() => void onSwitch()}>
                    {busy === 'switch' ? 'Switching…' : `Switch to ${config.chain.name}`}
                  </button>
                ) : null}
              </div>
            )}
          </section>

          <section className={CARD}>
            <h2 className='text-xl font-semibold'>2. Lock native MATRIX</h2>
            <div className='mt-4 rounded-lg border border-orange-500/35 bg-orange-500/10 p-4 text-sm text-orange-100'>
              <strong>Irreversible lock warning:</strong> native funds are escrowed once this signed transfer commits.
              They become mintable only after the immutable configured attestor threshold responds. If validators do
              not provide enough valid attestations, the browser cannot mint wMATRIX for you.
            </div>
            <label className='mt-4 block text-sm text-gray-300' htmlFor='bridge-amount'>Whole MATRIX to lock</label>
            <input id='bridge-amount' className={`${FIELD} mt-1`} inputMode='numeric' value={amount} onChange={(event) => setAmount(event.target.value)} disabled={Boolean(pending)} />
            <p className='mt-2 text-xs text-gray-500'>Minimum: {MinBridgeLockAmount / NativeUnit} MATRIX. Recipient is fixed to {wallet?.address ?? 'the connected EVM address'}.</p>
            <label className='mt-4 flex items-start gap-3 text-sm text-gray-300'>
              <input type='checkbox' className='mt-1' checked={acknowledged} onChange={(event) => setAcknowledged(event.target.checked)} disabled={Boolean(pending)} />
              I understand the native lock commits before validator attestations and Base minting.
            </label>
            {!pending ? (
              <button className={`${PRIMARY} mt-4`} disabled={!wallet || wrongChain || !acknowledged || busy !== ''} onClick={() => void onLock()}>
                {busy === 'lock' ? 'Signing and locking…' : 'Sign and submit native lock'}
              </button>
            ) : (
              <div className='mt-4 rounded-lg border border-white/10 p-4 text-sm text-gray-300'>
                <p>Pending lock recovered/saved ({pending.settlement === 'committed' ? 'commit confirmed' : 'submission outcome unknown'}):</p><p className='mt-1 break-all font-mono text-white'>{pending.lockId}</p>
                <p className='mt-2'>{pending.nativeAmount} native base units → {pending.erc20Amount} wMATRIX base units</p>
              </div>
            )}
          </section>

          <section className={CARD}>
            <h2 className='text-xl font-semibold'>3. Collect, verify, and mint</h2>
            <p className='mt-2 text-sm text-gray-400'>Only <code>not_found</code> is retried while polling the explicit configured validators. Every signature is recovered from the contract&apos;s exact digest before simulation.</p>
            <div className='mt-4 flex flex-wrap gap-3'>
              <button className={SECONDARY} disabled={!wallet || wrongChain || !pending || busy !== ''} onClick={() => void onCollect()}>
                {busy === 'collect' ? 'Collecting and verifying…' : 'Collect attestations'}
              </button>
              <button className={PRIMARY} disabled={!prepared || busy !== ''} onClick={() => void onMint()}>
                {busy === 'mint' ? 'Simulating and submitting…' : 'Simulate and mint (you pay gas)'}
              </button>
            </div>
            {prepared ? <p className='mt-3 text-sm text-green-200'>Ready: {prepared.signers.length} verified signatures, threshold {prepared.threshold.toString()}.</p> : null}
            {mintTx ? <ExplorerLink explorer={config.chain.explorerUrl} hash={mintTx} label='View mint transaction' /> : null}
          </section>

          <section className={CARD}>
            <h2 className='text-xl font-semibold'>Burn wMATRIX to native</h2>
            <p className='mt-2 text-sm text-gray-400'>Burn a positive whole wMATRIX amount and name the exact native account that validators should unlock to. You submit the Base transaction and pay its gas.</p>
            <label className='mt-4 block text-sm text-gray-300' htmlFor='burn-amount'>Whole wMATRIX to burn</label>
            <input id='burn-amount' className={`${FIELD} mt-1`} inputMode='numeric' value={burnAmount} onChange={(event) => setBurnAmount(event.target.value)} />
            <label className='mt-4 block text-sm text-gray-300' htmlFor='native-recipient'>Native recipient account</label>
            <input id='native-recipient' className={`${FIELD} mt-1 font-mono`} value={nativeRecipient} onChange={(event) => setNativeRecipient(event.target.value)} spellCheck={false} />
            <button className={`${PRIMARY} mt-4`} disabled={!wallet || wrongChain || busy !== '' || !burnAmount || !nativeRecipient.trim()} onClick={() => void onBurn()}>
              {busy === 'burn' ? 'Simulating and submitting…' : 'Simulate and burn (you pay gas)'}
            </button>
            {burnTx ? <ExplorerLink explorer={config.chain.explorerUrl} hash={burnTx} label='View burn transaction' /> : null}
          </section>

          {config.dexUrl ? (
            <section className={CARD}>
              <h2 className='text-xl font-semibold'>Optional DEX handoff</h2>
              <p className='mt-2 text-sm text-gray-400'>This link is shown only because the deployment explicitly configured a trusted HTTPS URL. Verify token and network details there.</p>
              <a className='mt-4 inline-flex text-primary-300 underline' href={config.dexUrl} target='_blank' rel='noopener noreferrer'>Open configured DEX</a>
            </section>
          ) : null}
        </div>
      </main>
      <Footer />
    </>
  );
}

function ExplorerLink({ explorer, hash, label }: { explorer: string; hash: Hash; label: string }) {
  return <a className='mt-4 block break-all text-sm text-primary-300 underline' href={`${explorer}/tx/${hash}`} target='_blank' rel='noopener noreferrer'>{label}: {hash}</a>;
}
