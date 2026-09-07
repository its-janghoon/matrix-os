/**
 * The browser-held ed25519 key, presented as a Signer.
 *
 * This is the wallet from wallet.ts wearing the interface MetaMask also wears,
 * so the page has one code path for "sign this run" and "sign this payment"
 * rather than two branches that drift.
 *
 * The two are not interchangeable in what they cost the user, and the page says
 * so: this key lives in this browser and clearing site data destroys it, while
 * a MetaMask account survives anywhere that wallet is installed.
 */

import { paymentSigningBytes, runAuthorizationSigningBytes } from './signing';
import type { Message, PaymentFields, PaymentSignature, RunAuthorization, Signer } from './signer';
import type { Wallet } from './wallet';

export function browserSigner(wallet: Wallet): Signer {
  return {
    kind: 'browser',
    accountId: wallet.accountId,

    async signRunAuthorization(input: {
      provider: string;
      model: string;
      messages: Message[];
      timestamp: bigint;
    }): Promise<RunAuthorization> {
      const bytes = await runAuthorizationSigningBytes({
        fromPublicKey: wallet.publicKey,
        provider: input.provider,
        model: input.model,
        timestamp: input.timestamp,
        messages: input.messages,
      });
      return {
        publicKey: wallet.publicKey,
        timestamp: input.timestamp,
        signature: await wallet.sign(bytes),
      };
    },

    async signPayment(payment: PaymentFields): Promise<PaymentSignature> {
      const bytes = paymentSigningBytes({
        fromPublicKey: wallet.publicKey,
        to: payment.to,
        amount: payment.amount,
        nonce: payment.nonce,
        timestamp: payment.timestamp,
        prevHash: payment.prevHash,
      });
      return {
        fromPublicKey: wallet.publicKey,
        signature: await wallet.sign(bytes),
      };
    },
  };
}
