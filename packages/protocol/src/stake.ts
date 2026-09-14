/**
 * The recipient strings a stake operation is addressed to.
 *
 * A bond is not a special message type. It is an ordinary signed transfer whose
 * RECIPIENT names a consensus operation instead of an account, which is why a
 * browser can post one with the signing it already does: the same EIP-712
 * transfer a wallet signs to pay a provider, pointed at a different string.
 *
 * THE RULE THAT MAKES THIS SAFE is that a stake operation must name its own
 * sender. Bonding into someone else's account would be a gift of voting power
 * and withdrawing from theirs would be theft, so the node refuses any stake
 * transfer whose recipient names an account other than the signer. Building the
 * recipient from the caller's own id is therefore not a convenience - a
 * recipient built from anything else is rejected.
 */

/** Every stake recipient lives under this prefix, as the node spells it. */
export const STAKE_PREFIX = 'consensus/stake/';

/**
 * The recipient that bonds the value sent, from an account, for that account.
 *
 * `accountId` must be the SIGNER's own id - a 64-character hex id for an
 * ed25519 account, or `eth:0x...` for one controlled by a wallet.
 */
export function bondRecipient(accountId: string): string {
  return `${STAKE_PREFIX}bond/${accountId}`;
}

/**
 * The recipient that returns an account's whole bond.
 *
 * A withdrawal carries NO amount: the sum is not the caller's to choose, it is
 * whatever is bonded. A transfer to this recipient with a non-zero value is
 * refused rather than partially honoured.
 */
export function withdrawBondRecipient(accountId: string): string {
  return `${STAKE_PREFIX}withdraw/${accountId}`;
}

/** Whether a recipient string names a stake operation rather than an account. */
export function isStakeRecipient(recipient: string): boolean {
  return recipient.startsWith(STAKE_PREFIX);
}
