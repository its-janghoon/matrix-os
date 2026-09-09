import { expect } from "chai";
import { ethers } from "hardhat";
import { resolveAttestors } from "../scripts/deploy-bridge";

/**
 * Guards on the WrappedMatrix deploy script.
 *
 * These exist because the attestor set IS the bridge's security. The contract
 * is ownerless and its cap is immutable, so the only authority that can create
 * wrapped supply is a threshold of registered attestor signatures - which makes
 * a wrong set at deploy time unfixable without a redeploy, and a set of one
 * indistinguishable from no bridge security at all.
 *
 * The strict branch only runs on a real network name, which a hardhat run
 * cannot reach without an RPC URL, so it is tested against resolveAttestors
 * directly - the same shape as deploy.ts's parseMinters tests.
 */
describe("deploy-bridge.ts attestor guards", () => {
  const a = ethers.getAddress("0xad42459bdc6d4e2761461788239fcc995e42cce7");
  const b = ethers.getAddress("0x709fca731675b619cc24c284eeb5aef1d7527c77");
  const c = ethers.getAddress("0x90f79bf6eb2c4f870365e785982e1f101e93b906");
  const local = [
    ethers.getAddress("0xf39fd6e51aad88f6f4ce6ab8827279cfffb92266"),
    ethers.getAddress("0x70997970c51812dc3a010c7d01b50e0d17dc79c8"),
    ethers.getAddress("0x3c44cdddb6a900fa2b585dd299e03d12fa4293bc"),
  ];

  /**
   * The one that mattered. On a real network `accounts` is `[PRIVATE_KEY]`, so
   * the local fallback had exactly one signer to offer: the deployer. An
   * operator who forgot ATTESTORS would have registered the key they exported
   * into an env var that afternoon as the bridge's sole minting authority, and
   * the script would have printed it as success.
   */
  it("refuses a real network with no ATTESTORS, instead of registering the deploy key", () => {
    expect(() => resolveAttestors("sepolia", [local[0]], 1, {})).to.throw(
      /refusing to deploy to 'sepolia': ATTESTORS is not set/
    );
    expect(() => resolveAttestors("mainnet", [local[0]], 1, {})).to.throw(/ATTESTORS is not set/);
  });

  it("refuses a 1-of-1 on a real network: one key can mint the whole cap", () => {
    expect(() => resolveAttestors("sepolia", [], 1, { ATTESTORS: a })).to.throw(
      /single attestor/
    );
  });

  it("allows a throwaway 1-of-1 rehearsal when it is said out loud, and warns", () => {
    const got = resolveAttestors("sepolia", [], 1, { ATTESTORS: a, ALLOW_SINGLE_ATTESTOR: "1" });
    expect(got.attestors).to.deep.equal([a]);
    expect(got.singleAttestorWarning).to.match(/ONE attestor/);
    expect(got.singleAttestorWarning).to.match(/never be treated as production/);
  });

  it("accepts a real 2-of-2 with no warning", () => {
    const got = resolveAttestors("sepolia", [], 2, { ATTESTORS: `${a},${b}` });
    expect(got.attestors).to.deep.equal([a, b]);
    expect(got.singleAttestorWarning).to.equal(undefined);
  });

  it("rejects real-network 1-of-N and 2-of-3, but accepts 3-of-3", () => {
    const attestors = `${a},${b},${c}`;
    expect(() => resolveAttestors("baseSepolia", [], 1, { ATTESTORS: attestors })).to.throw(
      /strictly greater than two thirds/
    );
    expect(() => resolveAttestors("baseSepolia", [], 2, { ATTESTORS: attestors })).to.throw(
      /strictly greater than two thirds/
    );
    expect(resolveAttestors("baseSepolia", [], 3, { ATTESTORS: attestors }).attestors).to.deep.equal([
      a,
      b,
      c,
    ]);
  });

  it("never allows a production 1-of-1, even with the testnet override", () => {
    for (const production of ["mainnet", "base"]) {
      expect(() =>
        resolveAttestors(production, [], 1, { ATTESTORS: a, ALLOW_SINGLE_ATTESTOR: "1" })
      ).to.throw(/fewer than two attestors/);
    }
  });

  /**
   * A duplicate lowers the real threshold silently: the contract counts
   * distinct signers, so a 2-of-3 registered as [A, A, B] is reachable by A and
   * B - or by A alone, if the same node answers twice.
   */
  it("refuses a duplicate in the attestor set", () => {
    expect(() => resolveAttestors("sepolia", [], 2, { ATTESTORS: `${a},${a}` })).to.throw(
      /duplicate/
    );
  });

  it("refuses a threshold larger than the set, which could never mint", () => {
    expect(() => resolveAttestors("sepolia", [], 3, { ATTESTORS: `${a},${b}` })).to.throw(
      /THRESHOLD must be between 1 and 2/
    );
  });

  // The local convenience must survive: a dev chain has to work with no secrets
  // and no env, or every rehearsal starts by inventing keys.
  it("still falls back to local accounts on a local network", () => {
    const got = resolveAttestors("localhost", local, 2, {});
    expect(got.attestors).to.deep.equal(local);
    expect(got.singleAttestorWarning).to.equal(undefined);
  });

  it("allows a 1-of-1 on a local chain with no ceremony", () => {
    const got = resolveAttestors("localhost", local, 1, { ATTESTORS: a });
    expect(got.attestors).to.deep.equal([a]);
    expect(got.singleAttestorWarning).to.equal(undefined);
  });
});
