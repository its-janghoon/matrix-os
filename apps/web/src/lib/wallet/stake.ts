import { bondRecipient, withdrawBondRecipient } from '@matrix-os/protocol';

import { submitSignedTransfer, type SettledTransfer } from './node';
import type { Signer } from './signer';

/**
 * Staking from a browser, with the wallet the page already has.
 *
 * A bond is not a special message. It is an ORDINARY signed transfer whose
 * recipient names a consensus operation instead of an account, so the same
 * EIP-712 signature a wallet makes to pay a provider does this too - pointed at
 * a different string. Nothing new had to be added to a wallet to make a GPU
 * owner able to stake from a web page.
 *
 * WHAT A BOND IS FOR, since the UI has to say it and saying it wrong is worse
 * than saying nothing. It does not make a seller honest and it cannot be slashed
 * for bad service: no protocol can judge whether a completion was really the
 * model advertised, a buyer-complaint slash would be a weapon competitors point
 * at each other, and validators voting on service quality is not something
 * consensus can do. What a bond does is make a LISTING cost capital, which is
 * what stops one attacker from filling a directory with cheap fake sellers.
 *
 * AND IT ONLY WORKS IF THE CAPITAL STAYS PUT. The chain enforces a minimum
 * residency - a bond cannot be withdrawn for a configured number of blocks after
 * it is posted - so this is not a deposit to be posted and pulled in the same
 * breath. A page asking someone to stake has to be plain about that before they
 * sign, not after.
 */

/** 2^64 - 1, the largest nonce a transfer can carry. */
const UINT64_MAX = 18_446_744_073_709_551_615n;

function randomNonce(): bigint {
  const bytes = new Uint8Array(8);
  crypto.getRandomValues(bytes);
  let out = 0n;
  for (const b of bytes) out = (out << 8n) | BigInt(b);
  return out > UINT64_MAX ? UINT64_MAX : out;
}

/**
 * Posts a bond from the signer's own account.
 *
 * The recipient is built from the SIGNER's id and cannot be anything else: the
 * node refuses a stake transfer whose recipient names an account other than the
 * signer, because bonding into somebody else's account would be a gift of voting
 * power. So there is no "bond on behalf of" to get wrong here.
 */
export async function bond(endpoint: string, signer: Signer, amount: bigint): Promise<SettledTransfer> {
  if (amount <= 0n) throw new Error('a bond has to carry an amount');

  const to = bondRecipient(signer.accountId);
  const nonce = randomNonce();
  const timestamp = BigInt(Date.now()) * 1_000_000n;
  const prevHash = new Uint8Array();

  const signed = await signer.signPayment({ to, amount, nonce, timestamp, prevHash });
  return submitSignedTransfer(endpoint, {
    fromPublicKey: signed.fromPublicKey,
    to,
    amount,
    nonce,
    timestamp,
    prevHash,
    signature: signed.signature,
  });
}

/**
 * Asks for the whole bond back.
 *
 * It carries NO amount, and that is the protocol's rule rather than a default
 * this picked: the sum is not the caller's to choose, it is whatever is bonded,
 * and a transfer to this recipient with a value is refused rather than partially
 * honoured. The chain decides whether the residency has elapsed; a request made
 * too early is refused with the height it becomes possible.
 */
export async function withdrawBond(endpoint: string, signer: Signer): Promise<SettledTransfer> {
  const to = withdrawBondRecipient(signer.accountId);
  const nonce = randomNonce();
  const timestamp = BigInt(Date.now()) * 1_000_000n;
  const prevHash = new Uint8Array();

  const signed = await signer.signPayment({ to, amount: 0n, nonce, timestamp, prevHash });
  return submitSignedTransfer(endpoint, {
    fromPublicKey: signed.fromPublicKey,
    to,
    amount: 0n,
    nonce,
    timestamp,
    prevHash,
    signature: signed.signature,
  });
}

/** What an account currently has staked, read from the node's chain. */
export async function bondedFor(endpoint: string, accountId: string): Promise<bigint> {
  const { getBalance } = await import('./node');
  return getBalance(endpoint, bondRecipient(accountId));
}
