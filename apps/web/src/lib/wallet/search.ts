import type { Seller } from './node';

/**
 * Searching a directory, and grouping it by model.
 *
 * Kept out of the page because it is the part with rules rather than markup: a
 * search that quietly matches the wrong field, or a sort that puts a seller
 * first for the wrong reason, is a routing decision made on someone's behalf.
 * Here it can be tested against the cases that matter without a browser.
 *
 * WHAT IS SEARCHABLE, and why it is not "everything". A reader looking for a
 * seller knows one of four things: the model they want, the address they were
 * given, the node somebody named, or the account they are paying. Those are the
 * fields matched. Prices and stakes are NOT matched as text - typing 5 would
 * otherwise hit every seller whose stake merely contains a 5, which looks like a
 * filter working and is noise.
 */

export type SortKey = 'price' | 'staked' | 'paid' | 'payers';

export interface SellerQuery {
  /** Free text, matched case-insensitively against model, address, node, account. */
  text?: string;
  /** Refuse sellers staking less than this. */
  minBond?: bigint;
  /** Hide sellers with no HTTP address: they cannot take a prompt. */
  reachableOnly?: boolean;
  /** Hide sellers with no capacity left to reserve. */
  withCapacityOnly?: boolean;
  sort?: SortKey;
}

function matchesText(seller: Seller, needle: string): boolean {
  if (needle === '') return true;
  const hay = [seller.id, seller.nodeId, seller.endpoint, ...seller.models].join(' ').toLowerCase();
  // Every term must match, so adding a word narrows rather than widens - which
  // is what a reader typing a second word means by it.
  return needle
    .toLowerCase()
    .split(/\s+/)
    .filter((t) => t !== '')
    .every((term) => hay.includes(term));
}

function compare(a: Seller, b: Seller, sort: SortKey): number {
  const desc = (x: bigint, y: bigint) => (x === y ? 0 : x > y ? -1 : 1);
  switch (sort) {
    case 'staked':
      return desc(a.bonded, b.bonded);
    case 'paid':
      return desc(a.settledPayments, b.settledPayments);
    case 'payers':
      return desc(a.settledPayers, b.settledPayers);
    case 'price':
    default:
      // Cheapest first, which is the one sort where LOW wins.
      return a.pricePerUnit === b.pricePerUnit ? 0 : a.pricePerUnit < b.pricePerUnit ? -1 : 1;
  }
}

/**
 * Applies a query to a directory.
 *
 * Ties always break on the pair that identifies an offer, so the order is
 * total: a table that reshuffled equal rows between refreshes would make a
 * reader doubt what they were reading, and two readers comparing screens would
 * see different things.
 */
export function searchSellers(sellers: Seller[], query: SellerQuery = {}): Seller[] {
  const text = (query.text ?? '').trim();
  const minBond = query.minBond ?? 0n;
  const sort = query.sort ?? 'price';

  const kept = sellers.filter((s) => {
    if (!matchesText(s, text)) return false;
    if (s.bonded < minBond) return false;
    if (query.reachableOnly && s.endpoint === '') return false;
    if (query.withCapacityOnly && s.available === 0n) return false;
    return true;
  });

  return kept.sort((a, b) => {
    const primary = compare(a, b, sort);
    if (primary !== 0) return primary;
    if (a.nodeId !== b.nodeId) return a.nodeId < b.nodeId ? -1 : 1;
    return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
  });
}

/** What a network offers for one model, summarised across its sellers. */
export interface ModelRow {
  model: string;
  sellers: number;
  cheapest: bigint;
  dearest: bigint;
  /** The largest stake behind any seller of it, and the total settled payments. */
  topStake: bigint;
  settledPayments: bigint;
}

/**
 * Groups a directory by model.
 *
 * A buyer usually arrives knowing the MODEL, not the seller, so this is the view
 * that answers their first question - who has it, and what does it cost - before
 * they care which box serves it.
 *
 * Only reachable sellers are counted. One that publishes no address cannot take
 * a prompt, so counting it would inflate "3 sellers" into a number a reader
 * cannot act on, and the cheapest price might belong to a seller nobody can
 * reach.
 */
export function sellersByModel(sellers: Seller[]): ModelRow[] {
  const rows = new Map<string, ModelRow>();
  for (const s of sellers) {
    if (s.endpoint === '') continue;
    for (const model of s.models) {
      const row = rows.get(model);
      if (!row) {
        rows.set(model, {
          model,
          sellers: 1,
          cheapest: s.pricePerUnit,
          dearest: s.pricePerUnit,
          topStake: s.bonded,
          settledPayments: s.settledPayments,
        });
        continue;
      }
      row.sellers += 1;
      if (s.pricePerUnit < row.cheapest) row.cheapest = s.pricePerUnit;
      if (s.pricePerUnit > row.dearest) row.dearest = s.pricePerUnit;
      if (s.bonded > row.topStake) row.topStake = s.bonded;
      row.settledPayments += s.settledPayments;
    }
  }
  return [...rows.values()].sort((a, b) => a.model.localeCompare(b.model));
}
