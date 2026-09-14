import { afterEach, describe, expect, it, vi } from 'vitest';

import { bond, withdrawBond } from './stake';
import type { PaymentFields, Signer } from './signer';

function response(body: unknown, ok = true, status = 200): Response {
  return { ok, status, text: async () => JSON.stringify(body) } as Response;
}

afterEach(() => vi.unstubAllGlobals());

const SETTLED = {
  transaction: { index: '1', from: 'x', to: 'y', amount: '0', nonce: '1', blockHeight: '1' },
  committed: true,
  applied: true,
};

/** A signer that records exactly what it was asked to sign. */
function recordingSigner(accountId: string): { signer: Signer; signed: PaymentFields[] } {
  const signed: PaymentFields[] = [];
  const signer: Signer = {
    accountId,
    kind: 'metamask',
    signRunAuthorization: async () => {
      throw new Error('not used');
    },
    signPayment: async (payment) => {
      signed.push(payment);
      return { fromPublicKey: new Uint8Array(20), signature: new Uint8Array(64) };
    },
  };
  return { signer, signed };
}

function bodyOf(fetchMock: ReturnType<typeof vi.fn>): Record<string, unknown> {
  const call = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
  return JSON.parse(call[1].body as string) as Record<string, unknown>;
}

describe('staking from a browser', () => {
  /**
   * The recipient is built from the SIGNER's own id, and this is the property
   * that matters rather than a formatting detail: the node refuses a stake
   * transfer whose recipient names an account other than the signer, because
   * bonding into somebody else's account would be a gift of voting power. A page
   * that built the string from a form field would produce transfers that are
   * always rejected, or - if the check were ever relaxed - a way to stake on a
   * stranger's behalf.
   */
  it('bonds to the signer"s own account and nothing else', async () => {
    const fetchMock = vi.fn(async () => response(SETTLED));
    vi.stubGlobal('fetch', fetchMock);

    const { signer, signed } = recordingSigner('eth:0x00000000000000000000000000000000000000aa');
    await bond('http://node:9093', signer, 500n);

    const expected = 'consensus/stake/bond/eth:0x00000000000000000000000000000000000000aa';
    expect(signed[0].to).toBe(expected);
    expect(signed[0].amount).toBe(500n);
    expect(bodyOf(fetchMock).to).toBe(expected);
    expect(bodyOf(fetchMock).amount).toBe('500');
  });

  it('works the same for a browser-held ed25519 account', async () => {
    const fetchMock = vi.fn(async () => response(SETTLED));
    vi.stubGlobal('fetch', fetchMock);

    const id = 'ab'.repeat(32);
    const { signer, signed } = recordingSigner(id);
    await bond('http://node:9093', signer, 1n);

    expect(signed[0].to).toBe(`consensus/stake/bond/${id}`);
  });

  /**
   * A withdrawal carries no amount, and that is the protocol's rule rather than
   * a default this picked: the sum is not the caller's to choose, it is whatever
   * is bonded, and a transfer to that recipient with a value is refused rather
   * than partially honoured.
   */
  it('withdraws the whole bond, carrying no amount', async () => {
    const fetchMock = vi.fn(async () => response(SETTLED));
    vi.stubGlobal('fetch', fetchMock);

    const { signer, signed } = recordingSigner('eth:0xabc');
    await withdrawBond('http://node:9093', signer);

    expect(signed[0].to).toBe('consensus/stake/withdraw/eth:0xabc');
    expect(signed[0].amount).toBe(0n);
    expect(bodyOf(fetchMock).amount).toBe('0');
  });

  it('refuses to sign a bond of nothing', async () => {
    const fetchMock = vi.fn(async () => response(SETTLED));
    vi.stubGlobal('fetch', fetchMock);

    const { signer, signed } = recordingSigner('eth:0xabc');
    await expect(bond('http://node:9093', signer, 0n)).rejects.toThrow(/amount/);
    // Refused before the wallet was ever asked, so a user is not shown a prompt
    // for a transaction that cannot succeed.
    expect(signed).toHaveLength(0);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('picks a fresh nonce each time, so two bonds are two transfers', async () => {
    const fetchMock = vi.fn(async () => response(SETTLED));
    vi.stubGlobal('fetch', fetchMock);

    const { signer, signed } = recordingSigner('eth:0xabc');
    await bond('http://node:9093', signer, 10n);
    await bond('http://node:9093', signer, 10n);

    // Identical amounts to the identical recipient: if the nonce repeated, the
    // second would be the first replayed and the chain would drop it.
    expect(signed[0].nonce).not.toBe(signed[1].nonce);
  });
});
