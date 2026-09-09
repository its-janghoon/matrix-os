import {
  MinBridgeLockAmount,
  NativeUnit,
  bridgeLockRecipient,
  deriveBridgeLockId,
  validateBridgeLockAmount,
  validateBridgeLockAttestations,
  type NormalizedBridgeLockAttestation,
} from '@matrix-os/protocol';
import { getAddress, type Address, type Hash } from 'viem';
import type { BridgeConfig } from './config';
import { verifyBridgeReadiness } from './evm';
import type { EvmSigner } from '@/lib/wallet/metamask';
import {
  getLockAttestation,
  isNotFoundNodeError,
  submitSignedTransfer,
  type LockAttestation,
} from '@/lib/wallet/node';

export const PENDING_LOCK_STORAGE_KEY = 'matrix.bridge.pending-lock.v1';
const UINT64_MAX = (1n << 64n) - 1n;

export interface PendingBridgeLock {
  version: 1;
  lockId: Hash;
  sender: string;
  recipient: Address;
  nativeAmount: string;
  erc20Amount: string;
  nonce: string;
  timestamp: string;
  validatorEndpoint: string;
  chainId: number;
  contract: Address;
  createdAt: string;
  settlement: 'submitted' | 'committed';
}

export class PendingLockContextError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'PendingLockContextError';
  }
}

export function wholeMatrixToNativeBaseUnits(value: string): bigint {
  const trimmed = value.trim();
  if (!/^[0-9]+$/.test(trimmed)) throw new Error('amount must be a whole number of MATRIX');
  const amount = BigInt(trimmed) * NativeUnit;
  if (amount > UINT64_MAX) throw new Error('amount exceeds the native uint64 range');
  return amount;
}

export function wholeMatrixToErc20BaseUnits(value: string): bigint {
  const trimmed = value.trim();
  if (!/^[0-9]+$/.test(trimmed)) throw new Error('amount must be a whole number of wMATRIX');
  return BigInt(trimmed) * 1_000_000_000_000_000_000n;
}

export function randomUint64(): bigint {
  const bytes = new Uint8Array(8);
  crypto.getRandomValues(bytes);
  return new DataView(bytes.buffer).getBigUint64(0, false);
}

function storage(): Storage | null {
  return typeof window === 'undefined' ? null : window.localStorage;
}

export function persistPendingLock(lock: PendingBridgeLock): void {
  storage()?.setItem(PENDING_LOCK_STORAGE_KEY, JSON.stringify(lock));
}

export function clearPendingLock(): void {
  storage()?.removeItem(PENDING_LOCK_STORAGE_KEY);
}

export async function loadPendingLock(
  config: BridgeConfig,
  connectedAddress?: Address,
): Promise<PendingBridgeLock | null> {
  const raw = storage()?.getItem(PENDING_LOCK_STORAGE_KEY);
  if (!raw) return null;
  let value: Partial<PendingBridgeLock>;
  try {
    value = JSON.parse(raw) as Partial<PendingBridgeLock>;
  } catch {
    clearPendingLock();
    throw new Error('saved pending bridge lock is not valid JSON and was removed');
  }

  // Context mismatches are not corruption: never delete a valid recovery record
  // just because the site or wallet is temporarily on another deployment/account.
  if (value.chainId !== config.chain.id || value.contract?.toLowerCase() !== config.wrappedMatrixAddress.toLowerCase()) {
    throw new PendingLockContextError('saved bridge lock belongs to a different configured deployment; the record was preserved');
  }
  let recipient: Address;
  try {
    recipient = getAddress(value.recipient ?? '');
  } catch {
    clearPendingLock();
    throw new Error('saved pending bridge lock has an invalid recipient and was removed');
  }
  if (connectedAddress && recipient !== connectedAddress) {
    throw new PendingLockContextError(`saved bridge lock belongs to ${recipient}; reconnect that wallet to continue`);
  }

  try {
    if (value.version !== 1) throw new Error('unsupported saved bridge lock version');
    if (value.settlement !== 'submitted' && value.settlement !== 'committed') {
      throw new Error('saved bridge lock has an invalid settlement state');
    }
    const amount = validateBridgeLockAmount(BigInt(value.nativeAmount ?? ''));
    const nonce = BigInt(value.nonce ?? '');
    const timestamp = BigInt(value.timestamp ?? '');
    const sender = value.sender ?? '';
    const lockId = await deriveBridgeLockId(nonce, sender, recipient, amount);
    if (lockId !== value.lockId?.toLowerCase()) throw new Error('saved bridge lock id does not match its fields');
    if (BigInt(value.erc20Amount ?? '') !== amount * 1_000_000_000n) {
      throw new Error('saved bridge lock conversion is invalid');
    }
    if (!value.validatorEndpoint || !value.createdAt) throw new Error('saved bridge lock is incomplete');
    return {
      version: 1,
      lockId: lockId as Hash,
      sender,
      recipient,
      nativeAmount: amount.toString(),
      erc20Amount: (amount * 1_000_000_000n).toString(),
      nonce: nonce.toString(),
      timestamp: timestamp.toString(),
      validatorEndpoint: value.validatorEndpoint,
      chainId: config.chain.id,
      contract: config.wrappedMatrixAddress,
      createdAt: value.createdAt,
      settlement: value.settlement,
    };
  } catch (error) {
    clearPendingLock();
    throw new Error(`saved pending bridge lock was rejected and removed: ${error instanceof Error ? error.message : String(error)}`);
  }
}

/** Sign, submit, verify, derive, then durably record one native lock. */
export async function submitBridgeLock(input: {
  wallet: EvmSigner;
  config: BridgeConfig;
  validatorEndpoint: string;
  nativeAmount: bigint;
  nonce?: bigint;
}): Promise<PendingBridgeLock> {
  const amount = validateBridgeLockAmount(input.nativeAmount);
  if (amount < MinBridgeLockAmount) throw new Error('bridge lock amount is below the protocol minimum');
  const nonce = input.nonce ?? randomUint64();
  if (nonce < 0n || nonce > UINT64_MAX) throw new Error('nonce must be a uint64');
  const timestamp = BigInt(Date.now()) * 1_000_000n;
  const recipient = input.wallet.address;
  const to = bridgeLockRecipient(recipient);
  const prevHash = new Uint8Array();

  // This is the last reversible boundary. Prove the configured contract and a
  // live on-chain threshold of endpoint keys before opening a payment prompt,
  // writing recovery intent, or submitting native value.
  await verifyBridgeReadiness(input.wallet, input.config, amount);
  const signingChain = await input.wallet.getChainId();
  if (signingChain !== input.config.chain.id) {
    throw new Error(`wrong wallet chain ${signingChain}; switch to ${input.config.chain.name} (${input.config.chain.id}) first`);
  }
  const signed = await input.wallet.signPayment({ to, amount, nonce, timestamp, prevHash });
  const lockId = await deriveBridgeLockId(nonce, input.wallet.accountId, recipient, amount) as Hash;
  const pending: PendingBridgeLock = {
    version: 1,
    lockId,
    sender: input.wallet.accountId,
    recipient,
    nativeAmount: amount.toString(),
    erc20Amount: (amount * 1_000_000_000n).toString(),
    nonce: nonce.toString(),
    timestamp: timestamp.toString(),
    validatorEndpoint: input.validatorEndpoint,
    chainId: input.config.chain.id,
    contract: input.config.wrappedMatrixAddress,
    createdAt: new Date().toISOString(),
    settlement: 'submitted',
  };
  // Persist the exact signed intent before network submission. If the response
  // is lost after consensus accepts it, refresh can still recover this lock id.
  persistPendingLock(pending);

  const settlement = await submitSignedTransfer(input.validatorEndpoint, {
    fromPublicKey: signed.fromPublicKey,
    to,
    amount,
    nonce,
    timestamp,
    prevHash,
    signature: signed.signature,
  });
  if (!settlement.committed || !settlement.applied) {
    throw new Error('bridge lock did not both commit and apply; no mint will be prepared');
  }
  const transaction = settlement.transaction;
  if (
    transaction.from !== input.wallet.accountId ||
    transaction.to !== to ||
    transaction.amount !== amount ||
    transaction.nonce !== nonce
  ) {
    throw new Error('committed bridge lock fields do not match the signed request');
  }
  pending.settlement = 'committed';
  persistPendingLock(pending);
  return pending;
}

function sleep(delayMs: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, delayMs));
}

/** Poll every explicitly configured validator; only not_found is retryable. */
export async function collectLockAttestations(input: {
  pending: PendingBridgeLock;
  validatorUrls: readonly string[];
  attempts?: number;
  delayMs?: number;
  requestTimeoutMs?: number;
}): Promise<NormalizedBridgeLockAttestation[]> {
  const attempts = input.attempts ?? 10;
  const delayMs = input.delayMs ?? 1_000;
  const requestTimeoutMs = input.requestTimeoutMs ?? 10_000;
  if (!Number.isInteger(attempts) || attempts < 1) throw new Error('attempts must be a positive integer');
  if (!Number.isFinite(delayMs) || delayMs < 0) throw new Error('delayMs must be non-negative');
  if (!Number.isFinite(requestTimeoutMs) || requestTimeoutMs <= 0) throw new Error('requestTimeoutMs must be positive');
  if (input.validatorUrls.length === 0) throw new Error('at least one explicit validator URL is required');

  const poll = async (endpoint: string): Promise<LockAttestation> => {
    for (let attempt = 1; attempt <= attempts; attempt++) {
      try {
        return await getLockAttestation(endpoint, input.pending.lockId, requestTimeoutMs);
      } catch (error) {
        if (!isNotFoundNodeError(error) || attempt === attempts) throw error;
        await sleep(delayMs);
      }
    }
    throw new Error('unreachable attestation polling state');
  };

  const outcomes = await Promise.allSettled(input.validatorUrls.map(poll));
  const attestations = outcomes
    .filter((outcome): outcome is PromiseFulfilledResult<LockAttestation> => outcome.status === 'fulfilled')
    .map((outcome) => outcome.value);
  if (attestations.length === 0) {
    const failures = outcomes
      .filter((outcome): outcome is PromiseRejectedResult => outcome.status === 'rejected')
      .map((outcome) => outcome.reason instanceof Error ? outcome.reason.message : String(outcome.reason));
    throw new Error(`no validator attestation was collected: ${failures.join('; ')}`);
  }
  // Endpoint failures are not retried unless they are not_found, but they do
  // not collapse the contract's m-of-n policy into all-of-n availability. The
  // contract adapter verifies registration and the live threshold below.
  const normalized = validateBridgeLockAttestations(attestations);
  const expectedAmount = BigInt(input.pending.nativeAmount);
  for (const attestation of normalized) {
    if (
      attestation.lockId !== input.pending.lockId.toLowerCase() ||
      getAddress(attestation.recipient) !== input.pending.recipient ||
      attestation.nativeAmount !== expectedAmount
    ) {
      throw new Error('validator attestation does not match the persisted pending lock');
    }
  }
  if (input.pending.settlement === 'submitted') {
    input.pending.settlement = 'committed';
    persistPendingLock(input.pending);
  }
  return normalized;

}