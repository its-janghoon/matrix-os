import {
  createPublicClient,
  createWalletClient,
  custom,
  encodePacked,
  getAddress,
  keccak256,
  recoverAddress,
  stringToHex,
  toHex,
  type Address,
  type Hash,
  type Hex,
} from 'viem';
import { base, baseSepolia } from 'viem/chains';
import {
  ERC20PerNativeUnit,
  MinBridgeLockAmount,
  validateBridgeLockAttestations,
  type BridgeLockAttestation,
  type NormalizedBridgeLockAttestation,
} from '@matrix-os/protocol';
import type { BridgeConfig } from './config';
import { wrappedMatrixAbi } from './abi';
import type { EvmSigner } from '@/lib/wallet/metamask';
import {
  getBridgeReadiness,
  isBridgeReadinessTransportError,
  type BridgeReadiness,
} from '@/lib/wallet/node';

export interface PreparedMint {
  recipient: Address;
  amount: bigint;
  lockId: Hash;
  signatures: readonly Hex[];
  signers: readonly Address[];
  threshold: bigint;
}

function chainFor(config: BridgeConfig) {
  return config.chain.id === 8453 ? base : baseSepolia;
}

function clients(wallet: EvmSigner, config: BridgeConfig) {
  const transport = custom(wallet.provider);
  const chain = chainFor(config);
  return {
    publicClient: createPublicClient({ chain, transport }),
    walletClient: createWalletClient({ account: wallet.address, chain, transport }),
  };
}

async function requireConfiguredChain(wallet: EvmSigner, config: BridgeConfig): Promise<void> {
  const actual = await wallet.getChainId();
  if (actual !== config.chain.id) {
    throw new Error(`wrong wallet chain ${actual}; switch to ${config.chain.name} (${config.chain.id}) first`);
  }
}

export function bridgeAttestationDigest(input: {
  recipient: Address;
  amount: bigint;
  lockId: Hash;
  chainId: number;
  contract: Address;
}): Hash {
  return keccak256(encodePacked(
    ['address', 'uint256', 'bytes32', 'uint256', 'address'],
    [input.recipient, input.amount, input.lockId, BigInt(input.chainId), input.contract],
  ));
}

export const BRIDGE_READINESS_DOMAIN = 'MATRIX_BRIDGE_READINESS_V1';

export interface BridgeContractReadiness {
  code?: Hex;
  threshold: bigint;
  attestorCount: bigint;
  mintCap: bigint;
  totalSupply: bigint;
  erc20PerNativeUnit: bigint;
}

export function bridgeReadinessDigest(input: {
  challenge: Hash;
  chainId: bigint;
  contract: Address;
  attestor: Address;
  minLockNative: bigint;
}): Hash {
  return keccak256(encodePacked(
    ['bytes', 'bytes32', 'uint256', 'address', 'address', 'uint256'],
    [stringToHex(BRIDGE_READINESS_DOMAIN), input.challenge, input.chainId, input.contract, input.attestor, input.minLockNative],
  ));
}

export function assertBridgeContractReadiness(snapshot: BridgeContractReadiness, nativeAmount: bigint): void {
  if (!snapshot.code || snapshot.code === '0x') throw new Error('configured WrappedMatrix address has no deployed code');
  if (snapshot.threshold <= 0n) throw new Error('WrappedMatrix threshold must be positive');
  if (snapshot.attestorCount < snapshot.threshold) {
    throw new Error(`WrappedMatrix attestor count ${snapshot.attestorCount} is below threshold ${snapshot.threshold}`);
  }
  if (snapshot.erc20PerNativeUnit !== ERC20PerNativeUnit) {
    throw new Error(`WrappedMatrix conversion is ${snapshot.erc20PerNativeUnit}; expected ${ERC20PerNativeUnit}`);
  }
  const expectedMint = nativeAmount * ERC20PerNativeUnit;
  if (snapshot.totalSupply > snapshot.mintCap || expectedMint > snapshot.mintCap - snapshot.totalSupply) {
    throw new Error('WrappedMatrix mint cap has insufficient headroom for this lock');
  }
}

/** Verify challenge echo, deployment fields, key possession, uniqueness, and on-chain registration. */
export async function verifyBridgeReadinessResponders(input: {
  responses: readonly BridgeReadiness[];
  challenge: Uint8Array;
  chainId: number;
  contract: Address;
  threshold: bigint;
  isRegistered: (address: Address) => Promise<boolean>;
}): Promise<readonly Address[]> {
  if (input.challenge.length !== 32) throw new Error('bridge readiness challenge must be exactly 32 bytes');
  const challenge = toHex(input.challenge) as Hash;
  const expectedContract = getAddress(input.contract);
  const seen = new Set<string>();
  const signers: Address[] = [];
  for (const response of input.responses) {
    if (response.challenge.length !== 32 || toHex(response.challenge).toLowerCase() !== challenge.toLowerCase()) {
      throw new Error('validator readiness response did not echo the challenge');
    }
    if (response.chainId !== BigInt(input.chainId)) {
      throw new Error(`validator readiness chain ${response.chainId} does not match configured chain ${input.chainId}`);
    }
    let contract: Address;
    let claimed: Address;
    try {
      contract = getAddress(response.contract);
      claimed = getAddress(response.attestor);
    } catch {
      throw new Error('validator readiness response contains a malformed contract or attestor address');
    }
    if (contract !== expectedContract) throw new Error(`validator readiness contract ${contract} does not match ${expectedContract}`);
    if (response.minLockNative !== MinBridgeLockAmount) {
      throw new Error(`validator readiness minimum ${response.minLockNative} does not match protocol minimum ${MinBridgeLockAmount}`);
    }
    if (response.signature.length !== 65) throw new Error('validator readiness signature must be 65 bytes');
    const digest = bridgeReadinessDigest({
      challenge,
      chainId: response.chainId,
      contract,
      attestor: claimed,
      minLockNative: response.minLockNative,
    });
    let recovered: Address;
    try {
      recovered = getAddress(await recoverAddress({ hash: digest, signature: toHex(response.signature) }));
    } catch {
      throw new Error('validator readiness signature is malformed or invalid');
    }
    if (recovered !== claimed) {
      throw new Error(`validator readiness claimed attestor ${claimed} does not match recovered signer ${recovered}`);
    }
    const key = recovered.toLowerCase();
    if (seen.has(key)) throw new Error(`duplicate readiness signer ${recovered}`);
    seen.add(key);
    if (!(await input.isRegistered(recovered))) {
      throw new Error(`readiness signer ${recovered} is not a registered attestor`);
    }
    signers.push(recovered);
  }
  requireAttestorThreshold(signers.length, input.threshold);
  return signers;
}

export function collectBridgeReadinessResponses(
  outcomes: readonly PromiseSettledResult<BridgeReadiness>[],
  endpoints: readonly string[],
): BridgeReadiness[] {
  const responses: BridgeReadiness[] = [];
  outcomes.forEach((outcome, index) => {
    if (outcome.status === 'fulfilled') {
      responses.push(outcome.value);
      return;
    }
    if (!isBridgeReadinessTransportError(outcome.reason)) {
      const endpoint = endpoints[index] ?? 'unknown endpoint';
      const detail = outcome.reason instanceof Error ? outcome.reason.message : String(outcome.reason);
      throw new Error(`validator readiness failed at ${endpoint}: ${detail}`);
    }
  });
  return responses;
}

/** Complete fail-closed deployment preflight run before any native lock signature. */
export async function verifyBridgeReadiness(
  wallet: EvmSigner,
  config: BridgeConfig,
  nativeAmount: bigint,
): Promise<void> {
  await requireConfiguredChain(wallet, config);
  const { publicClient } = clients(wallet, config);
  const common = { address: config.wrappedMatrixAddress, abi: wrappedMatrixAbi } as const;
  const code = await publicClient.getCode({ address: config.wrappedMatrixAddress });
  if (!code || code === '0x') throw new Error('configured WrappedMatrix address has no deployed code');
  const [threshold, attestorCount, mintCap, totalSupply, erc20PerNativeUnit] = await Promise.all([
    publicClient.readContract({ ...common, functionName: 'threshold' }),
    publicClient.readContract({ ...common, functionName: 'attestorCount' }),
    publicClient.readContract({ ...common, functionName: 'mintCap' }),
    publicClient.readContract({ ...common, functionName: 'totalSupply' }),
    publicClient.readContract({ ...common, functionName: 'ERC20_PER_NATIVE_UNIT' }),
  ]);
  assertBridgeContractReadiness({ code, threshold, attestorCount, mintCap, totalSupply, erc20PerNativeUnit }, nativeAmount);

  const challenge = crypto.getRandomValues(new Uint8Array(32));
  const outcomes = await Promise.allSettled(
    config.validatorUrls.map((endpoint) => getBridgeReadiness(endpoint, challenge)),
  );
  const responses = collectBridgeReadinessResponses(outcomes, config.validatorUrls);
  await verifyBridgeReadinessResponders({
    responses,
    challenge,
    chainId: config.chain.id,
    contract: config.wrappedMatrixAddress,
    threshold,
    isRegistered: async (address) => publicClient.readContract({ ...common, functionName: 'isAttestor', args: [address] }),
  });
}

/** Recover every signer from the exact contract digest and validate claimed identity/order inputs. */
export async function verifyAttestationSigners(
  input: {
    attestations: readonly NormalizedBridgeLockAttestation[];
    chainId: number;
    contract: Address;
  },
  isRegistered: (address: Address) => Promise<boolean>,
): Promise<Array<{ signer: Address; signature: Hex }>> {
  const first = input.attestations[0];
  if (!first) throw new Error('at least one lock attestation is required');
  const digest = bridgeAttestationDigest({
    recipient: getAddress(first.recipient),
    amount: first.erc20Amount,
    lockId: first.lockId as Hash,
    chainId: input.chainId,
    contract: input.contract,
  });
  const seen = new Set<string>();
  const recovered: Array<{ signer: Address; signature: Hex }> = [];
  for (const attestation of input.attestations) {
    const signer = getAddress(await recoverAddress({ hash: digest, signature: attestation.signature as Hex }));
    const claimed = getAddress(attestation.attestor);
    if (signer !== claimed) {
      throw new Error(`claimed attestor ${claimed} does not match recovered signer ${signer}`);
    }
    const key = signer.toLowerCase();
    if (seen.has(key)) throw new Error(`duplicate recovered signer ${signer}`);
    seen.add(key);
    if (!(await isRegistered(signer))) throw new Error(`recovered signer ${signer} is not a registered attestor`);
    recovered.push({ signer, signature: attestation.signature as Hex });
  }
  recovered.sort((a, b) => {
    const left = BigInt(a.signer);
    const right = BigInt(b.signer);
    return left < right ? -1 : left > right ? 1 : 0;
  });
  return recovered;
}

export function requireAttestorThreshold(count: number, threshold: bigint): void {
  if (BigInt(count) < threshold) {
    throw new Error(`attestor threshold not met: got ${count}, need ${threshold}`);
  }
}

/** Contract-verify an agreed attestation set and prepare an ascending-signature mint. */
export async function prepareMint(
  wallet: EvmSigner,
  config: BridgeConfig,
  attestations: readonly BridgeLockAttestation[],
): Promise<PreparedMint> {
  await requireConfiguredChain(wallet, config);
  const normalized = validateBridgeLockAttestations(attestations);
  const first = normalized[0];
  if (!first) throw new Error('at least one lock attestation is required');
  if (getAddress(first.recipient) !== wallet.address) {
    throw new Error(`attestation recipient ${first.recipient} is not connected wallet ${wallet.address}`);
  }

  const { publicClient } = clients(wallet, config);
  const common = { address: config.wrappedMatrixAddress, abi: wrappedMatrixAbi } as const;
  const [threshold, replayed, mintCap, totalSupply] = await Promise.all([
    publicClient.readContract({ ...common, functionName: 'threshold' }),
    publicClient.readContract({ ...common, functionName: 'mintedLockId', args: [first.lockId as Hash] }),
    publicClient.readContract({ ...common, functionName: 'mintCap' }),
    publicClient.readContract({ ...common, functionName: 'totalSupply' }),
  ]);
  if (replayed) throw new Error(`lock ${first.lockId} has already been minted`);
  if (totalSupply + first.erc20Amount > mintCap) throw new Error('mint would exceed the WrappedMatrix mint cap');

  const verified = await verifyAttestationSigners(
    { attestations: normalized, chainId: config.chain.id, contract: config.wrappedMatrixAddress },
    async (address) => publicClient.readContract({ ...common, functionName: 'isAttestor', args: [address] }),
  );
  requireAttestorThreshold(verified.length, threshold);
  return {
    recipient: getAddress(first.recipient),
    amount: first.erc20Amount,
    lockId: first.lockId as Hash,
    signatures: verified.map((entry) => entry.signature),
    signers: verified.map((entry) => entry.signer),
    threshold,
  };
}

export async function simulateAndWriteMint(
  wallet: EvmSigner,
  config: BridgeConfig,
  mint: PreparedMint,
): Promise<Hash> {
  await requireConfiguredChain(wallet, config);
  const { publicClient, walletClient } = clients(wallet, config);
  const { request } = await publicClient.simulateContract({
    account: wallet.address,
    address: config.wrappedMatrixAddress,
    abi: wrappedMatrixAbi,
    functionName: 'mint',
    args: [mint.recipient, mint.amount, mint.lockId, [...mint.signatures]],
  });
  // Re-check immediately before the write so a chainChanged event cannot turn a valid simulation into a wrong-chain write.
  await requireConfiguredChain(wallet, config);
  const hash = await walletClient.writeContract(request);
  const receipt = await publicClient.waitForTransactionReceipt({ hash });
  if (receipt.status !== 'success') throw new Error(`mint transaction ${hash} reverted`);
  const minted = await publicClient.readContract({
    address: config.wrappedMatrixAddress,
    abi: wrappedMatrixAbi,
    functionName: 'mintedLockId',
    args: [mint.lockId],
  });
  if (!minted) throw new Error(`mint transaction ${hash} confirmed without marking the lock minted`);
  return hash;
}

export function validateBurnAmount(amount: bigint): bigint {
  if (amount <= 0n) throw new Error('burn amount must be positive');
  if (amount % ERC20PerNativeUnit !== 0n) {
    throw new Error(`burn amount must be an exact multiple of ${ERC20PerNativeUnit} wMATRIX base units`);
  }
  return amount;
}

export async function simulateAndWriteBurn(
  wallet: EvmSigner,
  config: BridgeConfig,
  amount: bigint,
  nativeRecipient: string,
): Promise<Hash> {
  await requireConfiguredChain(wallet, config);
  const checkedAmount = validateBurnAmount(amount);
  const recipient = nativeRecipient.trim();
  if (!recipient) throw new Error('native recipient is required');
  if (recipient.length > 256) throw new Error('native recipient is too long');
  const { publicClient, walletClient } = clients(wallet, config);
  const { request } = await publicClient.simulateContract({
    account: wallet.address,
    address: config.wrappedMatrixAddress,
    abi: wrappedMatrixAbi,
    functionName: 'burn',
    args: [checkedAmount, recipient],
  });
  await requireConfiguredChain(wallet, config);
  return walletClient.writeContract(request);
}
