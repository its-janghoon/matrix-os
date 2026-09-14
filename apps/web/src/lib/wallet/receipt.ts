import { verifyReceipt, type Receipt, type ReceiptCheck } from '@matrix-os/protocol';

import type { Settled } from './node';
import type { Message } from './signing';

export type { ReceiptCheck };

/**
 * Checks the seller's signed account of what it just charged.
 *
 * DONE IN THIS PAGE, ON PURPOSE. A receipt the seller's own node vouches for is
 * not evidence of anything - the whole value of one is that the person holding
 * it can check it without asking the seller, or us, for anything. So this reads
 * the bytes the node signed, recomputes the digest over the prompt that was
 * actually sent and the answer that came back, and verifies the signature here.
 *
 * WHAT A PASS MEANS, and the UI has to say this rather than showing a tick: the
 * seller MADE this claim. Not that the claim is true. No signature can tell a
 * buyer that the model named is the model that ran - nothing can, which is why
 * the claim being undeniable is the thing worth having.
 */
export async function checkReceipt(
  settled: Settled,
  messages: Message[],
  buyer: string,
): Promise<ReceiptCheck | undefined> {
  if (settled.receipt === '') return undefined;

  let receipt: Receipt;
  try {
    receipt = JSON.parse(settled.receipt) as Receipt;
  } catch {
    return { signatureValid: false, arithmeticValid: false, problem: 'the receipt is not readable' };
  }
  return verifyReceipt(receipt, { messages, completion: settled.completion, buyer });
}

/** The one-line verdict to put in front of a reader. */
export function receiptVerdict(check: ReceiptCheck | undefined): string {
  if (!check) return 'This seller issued no receipt, so there is nothing to hold them to.';
  if (check.problem) return `Receipt problem: ${check.problem}`;
  if (check.boundToExchange === false) return 'This receipt was issued over a different exchange.';
  return 'Receipt verified: the seller signed this bill, for this exact exchange.';
}
