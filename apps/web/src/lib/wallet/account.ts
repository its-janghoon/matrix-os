import { isAddress } from 'viem';

/**
 * One account, one id.
 *
 * THE TRAP THIS CLOSES. An Ethereum-controlled account is keyed by
 * `eth:0x<40 lowercase hex>`. But lowercase is not the form anyone ever SEES:
 * MetaMask, Etherscan, every explorer and every block of documentation show the
 * EIP-55 mixed-case form, because that is the form carrying a checksum. So the
 * string a person copies is not the string the ledger keys their account by.
 *
 * Sending to the copied form is not an error anywhere. The recipient is an
 * opaque string in the signed payload and the ledger credits whatever it is
 * handed, so the money arrives at a key no private key controls, the transfer
 * reports success, and nobody can tell from the outside. It cannot be undone by
 * anyone, because there is nothing to sign with.
 *
 * On the bridge that is the worst version of it: the recipient rides along with
 * a BURN on Base, which is irreversible. The wrapped token is destroyed and the
 * native MATRIX lands somewhere unreachable, so the loss is on both chains at
 * once and there is no retry.
 *
 * WHY THIS IS CHECKED BEFORE SIGNING. The recipient is inside the signature, so
 * nothing downstream can correct it without invalidating the signature it just
 * verified. The one moment it can be fixed is before the signing starts.
 *
 * This is the same rule as token.CanonicalAccountID in the node, deliberately
 * character for character: a client and a chain that disagree about which
 * account a string names is the bug, not the fix.
 */

/** An ethereum-controlled id, with or without its 0x. */
const ETH_ID = /^eth:(?:0[xX])?([0-9a-fA-F]{40})$/;

export class UnreachableAccountError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'UnreachableAccountError';
  }
}

/**
 * Returns the single id the ledger keys an account by.
 *
 * Anything that is not an ethereum id comes back trimmed and otherwise
 * untouched: an ed25519 id has a fixed form already, and a reserved recipient -
 * `bridge/escrow`, `spend/budget/...` - carries its terms in the name, where
 * case is meaning rather than notation.
 *
 * Throws UnreachableAccountError when the id names an account nobody could
 * control, which is the only safe thing to do with it.
 */
export function canonicalAccountId(id: string): string {
  const trimmed = id.trim();
  if (!trimmed.startsWith('eth:')) return trimmed;

  const match = ETH_ID.exec(trimmed);
  if (!match) {
    throw new UnreachableAccountError(
      `"${trimmed}" is not an ethereum account id. It should be eth: followed by an ` +
        `0x address of 40 hex characters.`,
    );
  }
  const hex = match[1]!;

  // Only a mixed-case address carries a checksum, so only a mixed-case address
  // is held to one. An all-lowercase address has nothing to check and is the
  // form every machine sends, so refusing it would break them all to guard
  // against a spelling nobody types by hand.
  if (hex !== hex.toLowerCase()) {
    // isAddress and NOT getAddress. viem's getAddress does not reject a bad
    // checksum - it returns the correctly-checksummed form of whatever hex it
    // was given, so a single mistyped character comes back as a DIFFERENT valid
    // address, and the money reaches somebody else rather than nobody. That is
    // worse than the bug this function exists to fix, and it is silent.
    if (!isAddress(`0x${hex}`)) {
      throw new UnreachableAccountError(
        `"${trimmed}" will not reach anybody: its checksum does not match, which means a ` +
          `character was mistyped. Paste the address again from your wallet.`,
      );
    }
  }
  return `eth:0x${hex.toLowerCase()}`;
}

/**
 * The same rule, as a message to put under a field rather than an exception.
 *
 * Returns null when the id is fine. A form wants to say what is wrong while
 * someone is still typing, not throw at them when they press the button.
 */
export function accountIdProblem(id: string): string | null {
  if (id.trim() === '') return null;
  try {
    canonicalAccountId(id);
    return null;
  } catch (error) {
    return error instanceof Error ? error.message : String(error);
  }
}
