/**
 * MetaMask as the wallet for a native account.
 *
 * A native account is normally an ed25519 keypair, which MetaMask cannot sign
 * for. So the node accepts a second kind of account - `eth:0x<address>` -
 * verified by recovering an Ethereum signature over EIP-712 typed data. This
 * file is the browser end of that: connect, read the address, and sign the two
 * payloads.
 *
 * WHY THIS IS WORTH THE SECOND ACCOUNT KIND: the alternative is a user holding
 * MetaMask for wMATRIX on Ethereum and something of ours for native MATRIX, and
 * being told those are the same money.
 *
 * A note on what the user sees. EIP-712 is not just a signing format, it is the
 * reason MetaMask can show "Transfer: 26 to gpu-1" instead of a hex blob. That
 * is most of the value: a wallet prompt nobody can read is a wallet prompt
 * everybody approves.
 */

import { EIP712_DOMAIN, RUN_AUTHORIZATION_TYPES, TRANSFER_TYPES, bytes32, fromHex, toHex } from './eip712';
import { messagesDigest } from './signing';
import type { Message, PaymentFields, PaymentSignature, RunAuthorization, Signer } from './signer';

/** The subset of EIP-1193 this needs. */
interface Eip1193Provider {
  request(args: { method: string; params?: unknown[] }): Promise<unknown>;
}

declare global {
  interface Window {
    ethereum?: Eip1193Provider;
  }
}

/** True when a browser wallet is injected on this page. */
export function metamaskAvailable(): boolean {
  return typeof window !== 'undefined' && typeof window.ethereum !== 'undefined';
}

/** The native account id an Ethereum address controls. Must match
 * token.EthAccountID on the node, including the lowercasing. */
export function ethAccountID(address: string): string {
  return 'eth:' + address.toLowerCase();
}

/**
 * Connects, prompting the user to choose an account, and returns a signer.
 *
 * `eth_requestAccounts` is what shows the connect prompt; `eth_accounts` would
 * silently return nothing when the site is not yet authorised, which looks to a
 * user like a broken button.
 */
export async function connectMetamask(): Promise<Signer> {
  if (!metamaskAvailable()) {
    throw new Error('no browser wallet is installed on this page');
  }
  const provider = window.ethereum as Eip1193Provider;

  const accounts = (await provider.request({ method: 'eth_requestAccounts' })) as string[];
  const address = accounts?.[0];
  if (!address) {
    throw new Error('the wallet returned no account');
  }
  return new MetamaskSigner(provider, address);
}

class MetamaskSigner implements Signer {
  readonly kind = 'metamask' as const;
  readonly accountId: string;

  constructor(
    private readonly provider: Eip1193Provider,
    private readonly address: string,
  ) {
    this.accountId = ethAccountID(address);
  }

  async signRunAuthorization(input: {
    provider: string;
    model: string;
    messages: Message[];
    timestamp: bigint;
  }): Promise<RunAuthorization> {
    const promptDigest = await messagesDigest(input.messages);
    const signature = await this.signTypedData(RUN_AUTHORIZATION_TYPES, {
      buyer: this.address,
      provider: input.provider,
      model: input.model,
      promptDigest: bytes32(promptDigest),
      timestamp: input.timestamp.toString(),
    });
    return {
      // The node reads a 20-byte "public key" as an address; that length IS the
      // discriminator between the two account kinds.
      publicKey: fromHex(this.address),
      timestamp: input.timestamp,
      signature,
    };
  }

  async signPayment(payment: PaymentFields): Promise<PaymentSignature> {
    const signature = await this.signTypedData(TRANSFER_TYPES, {
      from: this.address,
      to: payment.to,
      amount: payment.amount.toString(),
      nonce: payment.nonce.toString(),
      timestamp: payment.timestamp.toString(),
      prevHash: bytes32(payment.prevHash),
    });
    return { fromPublicKey: fromHex(this.address), signature };
  }

  /**
   * eth_signTypedData_v4 takes the whole payload as a JSON STRING, with the
   * domain types spelled out in `types`. Passing an object, or omitting
   * EIP712Domain, is rejected by some wallets and silently mis-hashed by others.
   */
  private async signTypedData(types: object, message: Record<string, string>): Promise<Uint8Array> {
    const payload = JSON.stringify({
      domain: EIP712_DOMAIN,
      types: {
        EIP712Domain: [
          { name: 'name', type: 'string' },
          { name: 'version', type: 'string' },
          { name: 'salt', type: 'bytes32' },
        ],
        ...types,
      },
      primaryType: Object.keys(types)[0],
      message,
    });

    const signature = (await this.provider.request({
      method: 'eth_signTypedData_v4',
      params: [this.address, payload],
    })) as string;

    const bytes = fromHex(signature);
    if (bytes.length !== 65) {
      throw new Error(`the wallet returned a ${bytes.length}-byte signature, expected 65`);
    }
    return bytes;
  }
}

/** Exported for the tests, which check the payload a wallet is handed. */
export const _internal = { toHex };
