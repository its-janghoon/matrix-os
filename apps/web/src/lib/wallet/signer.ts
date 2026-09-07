/**
 * What a wallet has to be able to do, for both kinds of wallet.
 *
 * The obvious interface - `sign(bytes)` - does not work, and the reason shapes
 * this file. MetaMask will not sign arbitrary bytes: it signs EIP-712 typed data
 * or EIP-191 messages, and nothing else. So a signer cannot be handed a byte
 * string; it has to be told WHAT it is signing and produce the right thing for
 * its own scheme.
 *
 * That turns out to be the better abstraction anyway. The knowledge of "this is
 * a payment for 26 base units to gpu-1" belongs where it can be shown to a
 * person, and with MetaMask it literally is: EIP-712 is what makes its prompt
 * readable rather than a hex blob.
 */

/** The chat roles, in the wire spelling the node's prompt digest uses. */
export type Role = 'system' | 'user' | 'assistant';

export interface Message {
  role: Role;
  content: string;
}

/** What a node needs to accept a run: the key, the moment, the signature. */
export interface RunAuthorization {
  publicKey: Uint8Array;
  timestamp: bigint;
  signature: Uint8Array;
}

/** The invoice fields a payment signature covers. */
export interface PaymentFields {
  to: string;
  amount: bigint;
  nonce: bigint;
  timestamp: bigint;
  prevHash: Uint8Array;
}

/** What a node needs to accept a payment. */
export interface PaymentSignature {
  fromPublicKey: Uint8Array;
  signature: Uint8Array;
}

export interface Signer {
  /** The native account id this signer controls. */
  readonly accountId: string;
  /**
   * How to describe this signer to a person: "this browser" or "MetaMask". The
   * distinction is not cosmetic - one of them can be lost by clearing site data
   * and the other cannot - so a UI has to be able to say which is in use.
   */
  readonly kind: 'browser' | 'metamask';
  /** Signs the buyer's request to have this exact work done. */
  signRunAuthorization(input: {
    provider: string;
    model: string;
    messages: Message[];
    timestamp: bigint;
  }): Promise<RunAuthorization>;
  /** Signs the invoice the node returned. */
  signPayment(payment: PaymentFields): Promise<PaymentSignature>;
}
