/**
 * A budget: one wallet signature that lets the key in this page spend a bounded
 * amount, for a bounded time, on inference and nothing else.
 *
 * WHY THIS EXISTS. Holding your own key used to cost two wallet prompts per
 * message - one to authorise the run before a seller spends GPU time, one to pay
 * for it after, because the price does not exist until the work is done. The
 * only escape the protocol offered was custody, a node holding the buyer's
 * private key, which buys a quiet interface with the worst risk there is.
 *
 * A budget is an ACCOUNT whose name carries the terms its owner signed. Because
 * `to` is part of what a transfer signs, depositing into it IS signing the
 * terms - so one signature opens it, and afterwards the delegate named in that
 * name settles every message with no prompt at all. The delegate here is the
 * non-extractable key this page already generates: it stops being a second
 * account that needs its own funding and becomes a bounded hand on the account
 * the reader already has.
 *
 * THE NAME IS THE ONLY WAY BACK. Clearing this site's data loses the browser key
 * and the record below, and the budget account is not memorable. It is
 * recoverable - the deposit appears in the owner's own transaction history like
 * any other payment - but that is a search, so the UI shows the name and offers
 * to copy it.
 */

import { randomUint64 } from '@/lib/bridge/lock';
import { submitSignedTransfer } from './node';
import type { Message, PaymentFields, RunAuthorization, Signer } from './signer';

export interface Budget {
  /** The account the money came from and the account a close returns it to. */
  buyer: string;
  /** Lowercase hex ed25519 public key allowed to draw. */
  delegate: string;
  /** The most any one job may take, in base units. */
  perJobCap: bigint;
  /** Refuse a seller dearer than this, in base units per unit. */
  maxPricePerUnit: bigint;
  /** Unix SECONDS at and after which nothing may be drawn. */
  expiry: bigint;
  /** Distinguishes two budgets with otherwise identical terms. */
  nonce: bigint;
}

const ESCROW_PREFIX = 'spend/escrow/';
const CLOSE_PREFIX = 'spend/close/';

/**
 * The terms as the single path segment every budget recipient carries.
 *
 * THIS MUST MATCH THE GO EXACTLY, byte for byte. It is an account name, so a
 * difference of one character is money in an account nothing here can close.
 * There is a test on each side asserting the same literal string for the same
 * terms, which is what makes a change to either one fail loudly.
 */
function terms(b: Budget): string {
  return [
    b.buyer,
    b.delegate,
    b.perJobCap.toString(),
    b.maxPricePerUnit.toString(),
    b.expiry.toString(),
    b.nonce.toString(),
  ].join('.');
}

/** The ledger account holding this budget. Its balance is what is left. */
export function budgetAccount(b: Budget): string {
  return ESCROW_PREFIX + terms(b);
}

/** The recipient that returns whatever is left to the owner. */
export function budgetCloseRecipient(b: Budget): string {
  return CLOSE_PREFIX + terms(b);
}

/** True once the chain will refuse any further draw on this budget. */
export function budgetExpired(b: Budget, now: Date = new Date()): boolean {
  return BigInt(Math.floor(now.getTime() / 1000)) >= b.expiry;
}

/**
 * Reads a budget's terms back OUT of its account id.
 *
 * The terms are not stored anywhere else that matters: the account NAME is the
 * authorisation, which is why depositing into it is signing them. So anything
 * that needs to respect a bound - and the per-job cap is one a reservation has to
 * respect before it is signed - reads them from here rather than from a copy kept
 * beside it. A copy is how one side starts believing a cap the chain does not.
 *
 * Returns null for anything that is not a budget account, which is the normal
 * answer when a wallet is paying directly.
 */
export function budgetFromAccount(id: string): Budget | null {
  if (!id.startsWith(ESCROW_PREFIX)) return null;
  const parts = id.slice(ESCROW_PREFIX.length).split('.');
  if (parts.length !== 6) return null;
  try {
    return {
      buyer: parts[0],
      delegate: parts[1],
      perJobCap: BigInt(parts[2]),
      maxPricePerUnit: BigInt(parts[3]),
      expiry: BigInt(parts[4]),
      nonce: BigInt(parts[5]),
    };
  } catch {
    // A malformed name is not a budget this code can reason about, and guessing
    // at one would mean reasoning about a cap nobody signed.
    return null;
  }
}

/**
 * Opens a budget: ONE wallet signature, and the only one this flow needs.
 *
 * The amount deposited is the budget. Nothing else about the terms is sent
 * anywhere - they are in the recipient, which the signature covers.
 */
export async function openBudget(input: {
  endpoint: string;
  wallet: Signer;
  /** The delegate's account id: the browser key that will do the spending. */
  delegate: string;
  amount: bigint;
  perJobCap: bigint;
  maxPricePerUnit: bigint;
  /** How long the budget may be drawn on, in seconds. */
  ttlSeconds: number;
}): Promise<Budget> {
  if (input.amount <= 0n) throw new Error('a budget needs something in it');
  if (input.perJobCap <= 0n || input.maxPricePerUnit <= 0n) {
    throw new Error('a budget with an unset bound is a blank cheque; set a per-job cap and a price ceiling');
  }
  if (input.perJobCap > input.amount) {
    throw new Error('a per-job cap larger than the budget bounds nothing');
  }

  const budget: Budget = {
    buyer: input.wallet.accountId,
    delegate: input.delegate,
    perJobCap: input.perJobCap,
    maxPricePerUnit: input.maxPricePerUnit,
    expiry: BigInt(Math.floor(Date.now() / 1000) + input.ttlSeconds),
    nonce: 0n,
  };

  await submitBudgetTransfer(input.endpoint, input.wallet, budgetAccount(budget), input.amount);
  return budget;
}

/**
 * Returns whatever is left to the owner.
 *
 * Before the expiry only the owner may, and that is what revoking the delegate
 * IS - the key keeps working until the money is gone. At and after the expiry
 * anyone may, which is what stops a balance being stranded.
 *
 * It carries no amount: the amount is the whole remaining balance and is not the
 * caller's to choose.
 */
export async function closeBudget(endpoint: string, wallet: Signer, budget: Budget): Promise<void> {
  await submitBudgetTransfer(endpoint, wallet, budgetCloseRecipient(budget), 0n);
}

async function submitBudgetTransfer(endpoint: string, wallet: Signer, to: string, amount: bigint): Promise<void> {
  // A random nonce, for the reason the bridge lock's is random: a budget
  // operation is a reserved recipient, so it is not in the counted history an
  // ordinary nonce derivation reads, and a counter that never advanced would
  // sign every operation at the same nonce.
  const payment: PaymentFields = {
    to,
    amount,
    nonce: randomUint64(),
    timestamp: BigInt(Date.now()) * 1_000_000n,
    prevHash: new Uint8Array(),
  };
  const signed = await wallet.signPayment(payment);
  await submitSignedTransfer(endpoint, {
    fromPublicKey: signed.fromPublicKey,
    to: payment.to,
    amount: payment.amount,
    nonce: payment.nonce,
    prevHash: payment.prevHash,
    signature: signed.signature,
    timestamp: payment.timestamp,
  });
}

/**
 * The signer a purchase uses once a budget is open.
 *
 * It is the browser key doing every signature, presented under the BUDGET's
 * account id - which is exactly what the node needs to see. Naming a budget as
 * the buyer is how a caller says "pay from this", and the node then addresses
 * the invoice to the delegate the budget's own name authorises. So `chat` needs
 * no knowledge of budgets at all: it asks this signer who it is and to sign,
 * and both answers are already right.
 */
export function budgetSigner(browser: Signer, budget: Budget): Signer {
  const account = budgetAccount(budget);
  return {
    kind: browser.kind,
    accountId: account,
    signRunAuthorization(input: {
      provider: string;
      model: string;
      messages: Message[];
      timestamp: bigint;
    }): Promise<RunAuthorization> {
      return browser.signRunAuthorization(input);
    },
    signPayment(payment: PaymentFields) {
      return browser.signPayment(payment);
    },
  };
}

const STORAGE_KEY = 'matrix.budget.v1';

interface StoredBudget {
  buyer: string;
  delegate: string;
  perJobCap: string;
  maxPricePerUnit: string;
  expiry: string;
  nonce: string;
}

/**
 * Remembers the open budget for this browser.
 *
 * Per-viewer convenience and nothing more: losing it costs a lookup in the
 * owner's own transaction history, not the money. Every read and write is
 * wrapped, because storage throws in a private window and comes back empty when
 * site data is cleared.
 */
export function rememberBudget(budget: Budget): void {
  try {
    const stored: StoredBudget = {
      buyer: budget.buyer,
      delegate: budget.delegate,
      perJobCap: budget.perJobCap.toString(),
      maxPricePerUnit: budget.maxPricePerUnit.toString(),
      expiry: budget.expiry.toString(),
      nonce: budget.nonce.toString(),
    };
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(stored));
  } catch {
    // A budget that is open but not remembered still works for this session.
  }
}

/**
 * Reads back the remembered budget WITHOUT knowing whose it is.
 *
 * A page reloads holding no wallet: MetaMask needs an explicit connect, because
 * silently reading an account nobody authorised is what a wallet prompt exists
 * to stop. So on the first render there is no owner to check a budget against -
 * and a page that showed nothing until the reader reconnected showed nothing
 * about an account with their money in it. The Close button simply vanished.
 *
 * The terms are not a secret. They are an account NAME: they appear in the
 * owner's own transaction history, and anyone who can read this browser's
 * storage can already read the delegate key that spends the budget. What is
 * secret is the WALLET, and closing still needs it - so this only makes the
 * budget visible and namable, never spendable.
 */
export function recallAnyBudget(): Budget | null {
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (!raw) return null;
    const stored = JSON.parse(raw) as StoredBudget;
    return {
      buyer: stored.buyer,
      delegate: stored.delegate,
      perJobCap: BigInt(stored.perJobCap),
      maxPricePerUnit: BigInt(stored.maxPricePerUnit),
      expiry: BigInt(stored.expiry),
      nonce: BigInt(stored.nonce),
    };
  } catch {
    return null;
  }
}

/** Reads back the remembered budget for an owner, if it is theirs. */
export function recallBudget(buyer: string): Budget | null {
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (!raw) return null;
    const stored = JSON.parse(raw) as StoredBudget;
    // A budget belongs to one account. Switching wallet must not hand the new
    // one somebody else's budget, which it could not spend anyway - the
    // delegate and the terms would not match - but which would read as theirs.
    if (stored.buyer !== buyer) return null;
    return {
      buyer: stored.buyer,
      delegate: stored.delegate,
      perJobCap: BigInt(stored.perJobCap),
      maxPricePerUnit: BigInt(stored.maxPricePerUnit),
      expiry: BigInt(stored.expiry),
      nonce: BigInt(stored.nonce),
    };
  } catch {
    return null;
  }
}

export function forgetBudget(): void {
  try {
    window.localStorage.removeItem(STORAGE_KEY);
  } catch {
    // Nothing to do: the caller is clearing local state, not the chain's.
  }
}
