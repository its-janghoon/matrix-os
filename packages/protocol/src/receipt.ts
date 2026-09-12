import { lp, u64, utf8, concat, type Message } from './index';

/**
 * Checking a seller's signed account of what it charged, in the browser.
 *
 * WHY THIS EXISTS IN TYPESCRIPT AT ALL. A receipt is only evidence if the person
 * holding it can check it without asking the seller, or us, for anything. A
 * buyer in a browser is exactly that person, and they have no node - so the
 * layout the node signed has to be reproduced here.
 *
 * WHICH MAKES DRIFT THE DANGER. A second transcription of a signed layout does
 * not fail loudly when it is wrong. It fails as "invalid signature", which reads
 * as a key problem and sends a reader looking somewhere else entirely while
 * every receipt the network issues quietly stops verifying. So this is pinned to
 * the Go implementation by generated vectors rather than to three examples:
 * `receipt-vectors.json` carries the digest the node computes for inputs chosen
 * where two implementations diverge - empty strings, multi-byte text, and
 * numbers above 2^53 - and the test here must reproduce every one.
 *
 * WHAT A VALID SIGNATURE MEANS, and it is worth being exact because the whole
 * design rests on it: the seller made this claim. Not that the claim is true. No
 * signature can tell a buyer that the model named is the model that ran.
 */

/** The domain separating a receipt signature from every other signed structure. */
export const RECEIPT_DOMAIN = 'matrix/inference/receipt/v1';

/** A seller's signed account of one billed inference, as the node emits it. */
export interface Receipt {
  job_id: string;
  buyer: string;
  provider: string;
  node_id: string;
  model: string;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  /** Base units. Strings or numbers on the wire; both are read as BigInt here. */
  units: number | string;
  price_per_unit: number | string;
  total: number | string;
  /** Base64. */
  exchange_digest: string;
  issued_at: number | string;
  /** Base64. */
  public_key: string;
  /** Base64. */
  signature: string;
}

function big(value: number | string): bigint {
  return typeof value === 'bigint' ? value : BigInt(value);
}

function fromBase64(value: string): Uint8Array {
  const binary = typeof atob === 'function' ? atob(value) : Buffer.from(value, 'base64').toString('binary');
  const out = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i += 1) out[i] = binary.charCodeAt(i);
  return out;
}

async function sha256(bytes: Uint8Array): Promise<Uint8Array> {
  const digest = await crypto.subtle.digest('SHA-256', bytes as BufferSource);
  return new Uint8Array(digest);
}

/**
 * Hashes the request and completion into the value a receipt commits to.
 *
 * Length-prefixed throughout, so no two distinct exchanges serialise the same
 * way: without it a prompt ending in text the completion begins with could be
 * re-split into a different pair with an identical digest.
 */
export async function exchangeDigest(messages: Message[], completion: string): Promise<Uint8Array> {
  return sha256(
    concat([
      lp(utf8(RECEIPT_DOMAIN)),
      // The COUNT is a bare u64, not length-prefixed, matching the node.
      u64(BigInt(messages.length)),
      ...messages.flatMap((m) => [lp(utf8(m.role)), lp(utf8(m.content))]),
      lp(utf8(completion)),
    ]),
  );
}

/**
 * The 32-byte digest a receipt's signature covers: every field except the
 * signature itself.
 *
 * Every number is a BigInt. Reading `units` or `issued_at` as a JavaScript
 * number would be exact for almost every receipt and silently wrong above 2^53,
 * which is the kind of bug that only appears once real money is moving.
 */
export async function receiptSigningDigest(receipt: Receipt): Promise<Uint8Array> {
  return sha256(
    concat([
      lp(utf8(RECEIPT_DOMAIN)),
      lp(utf8(receipt.job_id)),
      lp(utf8(receipt.buyer)),
      lp(utf8(receipt.provider)),
      lp(utf8(receipt.node_id)),
      lp(utf8(receipt.model)),
      u64(BigInt(receipt.prompt_tokens)),
      u64(BigInt(receipt.completion_tokens)),
      u64(BigInt(receipt.total_tokens)),
      u64(big(receipt.units)),
      u64(big(receipt.price_per_unit)),
      u64(big(receipt.total)),
      u64(big(receipt.issued_at)),
      lp(fromBase64(receipt.exchange_digest)),
      lp(fromBase64(receipt.public_key)),
    ]),
  );
}

/** What a check concluded, and why, in terms a person can be shown. */
export interface ReceiptCheck {
  /** The signature verifies and the arithmetic holds. */
  signatureValid: boolean;
  /** units * price_per_unit equals total. */
  arithmeticValid: boolean;
  /**
   * The receipt was issued over the exchange it was checked against. Undefined
   * when no exchange was supplied, which is a different answer from false: a
   * receipt nobody bound to a request is a valid claim about SOME exchange.
   */
  boundToExchange?: boolean;
  /** A reason, when something did not hold. */
  problem?: string;
}

/**
 * Verifies a receipt, and - when given the exchange - that it is about THAT one.
 *
 * Binding matters more than it looks. A signed statement about an unnamed
 * request proves nothing about any particular one, so an honest receipt could be
 * handed out as a reference for every job after it. Bound to a digest of the
 * prompt and the completion, a receipt belongs to one request, and the buyer
 * holding that text is the one who can prove which.
 */
export async function verifyReceipt(
  receipt: Receipt,
  exchange?: { messages: Message[]; completion: string; buyer?: string },
): Promise<ReceiptCheck> {
  const publicKey = fromBase64(receipt.public_key);
  const signature = fromBase64(receipt.signature);
  if (publicKey.length !== 32) {
    return { signatureValid: false, arithmeticValid: false, problem: 'the public key is not 32 bytes' };
  }
  if (signature.length !== 64) {
    return { signatureValid: false, arithmeticValid: false, problem: 'the signature is not 64 bytes' };
  }

  const arithmeticValid = big(receipt.units) * big(receipt.price_per_unit) === big(receipt.total);

  let signatureValid = false;
  try {
    const key = await crypto.subtle.importKey('raw', publicKey as BufferSource, { name: 'Ed25519' }, false, ['verify']);
    const digest = await receiptSigningDigest(receipt);
    signatureValid = await crypto.subtle.verify(
      'Ed25519',
      key,
      signature as BufferSource,
      digest as BufferSource,
    );
  } catch (err) {
    return {
      signatureValid: false,
      arithmeticValid,
      problem: `this browser cannot check an Ed25519 signature: ${String(err)}`,
    };
  }

  if (!signatureValid) {
    return { signatureValid: false, arithmeticValid, problem: 'the signature does not verify' };
  }
  if (!arithmeticValid) {
    return {
      signatureValid,
      arithmeticValid,
      problem: `the total ${receipt.total} does not follow from ${receipt.units} units at ${receipt.price_per_unit} each`,
    };
  }

  if (!exchange) return { signatureValid, arithmeticValid };

  if (exchange.buyer !== undefined && exchange.buyer !== '' && receipt.buyer !== exchange.buyer) {
    return {
      signatureValid,
      arithmeticValid,
      boundToExchange: false,
      problem: `this receipt is for ${receipt.buyer}, not ${exchange.buyer}`,
    };
  }

  const want = await exchangeDigest(exchange.messages, exchange.completion);
  const got = fromBase64(receipt.exchange_digest);
  const boundToExchange =
    want.length === got.length && want.every((b, i) => b === got[i]);
  return {
    signatureValid,
    arithmeticValid,
    boundToExchange,
    problem: boundToExchange ? undefined : 'this receipt was issued over a different prompt or completion',
  };
}
