import { expect } from "chai";
import { ethers } from "hardhat";

/**
 * A gas budget for the bridge, because these numbers are quoted to an operator
 * who is deciding what to spend and, for the deploy, they are quoted about a
 * transaction that happens ONCE against an immutable contract.
 *
 * CEILINGS, NOT EXACT MATCHES. The exact figure moves by a few gas with the
 * calldata - an attestor address or a signature with more zero bytes is
 * marginally cheaper - and by more across a solc or optimizer change, and a
 * test that pins the last digit fails on a toolchain bump while telling nobody
 * anything. The ceilings below sit roughly 15% above what is measured today, so
 * they pass through ordinary drift and fail on a change of shape: a loop added
 * to the constructor, a storage write added to mint, an unbounded array.
 *
 * Measured today (solc 0.8.24, optimizer 200, evmVersion cancun; identical
 * under the cancun, prague and osaka EVM rules, so these transfer to mainnet as
 * gas UNITS):
 *
 *   deploy, 2-of-3          1,519,899   (~23,112 more per additional attestor)
 *   mint, threshold 2         109,789 first to an address, 75,589 after
 *                                       (~7,671 more per additional signature)
 *   burn                       38,097   (independent of threshold)
 *
 * The first-vs-later mint gap is 34,200 gas: the recipient's balance slot going
 * from zero to non-zero.
 */
describe("bridge gas budget", () => {
  const BUDGET = {
    deploy2of3: 1_750_000,
    mintFirst: 130_000,
    mintRepeat: 90_000,
    burn: 45_000,
    perExtraAttestor: 30_000,
  };

  async function attestors(n: number) {
    const w = Array.from({ length: n }, (_, i) =>
      new ethers.Wallet("0x" + String(i + 1).repeat(64).slice(0, 64))
    );
    return w.sort((a, b) => (BigInt(a.address) < BigInt(b.address) ? -1 : 1));
  }

  it("deploys a 2-of-3 bridge within budget", async () => {
    const set = (await attestors(3)).map((w) => w.address);
    const c = await (await ethers.getContractFactory("WrappedMatrix")).deploy(set, 2, 0);
    const rc = await c.deploymentTransaction()!.wait();
    expect(Number(rc!.gasUsed)).to.be.lessThan(
      BUDGET.deploy2of3,
      "the one-time deploy got materially more expensive; it is an immutable contract, so " +
        "this is the only chance to notice"
    );
  });

  it("costs a bounded amount per additional attestor, so an n-of-m set stays predictable", async () => {
    const F = await ethers.getContractFactory("WrappedMatrix");
    const three = await (await F.deploy((await attestors(3)).map((w) => w.address), 2, 0))
      .deploymentTransaction()!
      .wait();
    const five = await (await F.deploy((await attestors(5)).map((w) => w.address), 2, 0))
      .deploymentTransaction()!
      .wait();
    const perAttestor = (Number(five!.gasUsed) - Number(three!.gasUsed)) / 2;
    expect(perAttestor).to.be.lessThan(
      BUDGET.perExtraAttestor,
      "each registered attestor costs more than one cold storage write, which means the " +
        "constructor is doing something per-attestor that it did not used to"
    );
  });

  it("mints and burns within budget", async () => {
    const signers = await ethers.getSigners();
    const set = await attestors(3);
    const c = await (await ethers.getContractFactory("WrappedMatrix")).deploy(
      set.map((w) => w.address),
      2,
      0
    );
    await c.waitForDeployment();
    const recipient = signers[1].address;
    const amount = ethers.parseUnits("4", 18);

    const gas: number[] = [];
    for (const round of [1, 2]) {
      const lockId = ethers.keccak256(ethers.toUtf8Bytes(`budget-${round}`));
      const digest = await (c as any).attestationDigest(recipient, amount, lockId);
      const sigs = set
        .slice(0, 2)
        .map((w) => ({ w, s: w.signingKey.sign(digest).serialized }))
        .sort((a, b) => (BigInt(a.w.address) < BigInt(b.w.address) ? -1 : 1))
        .map((x) => x.s);
      const rc = await (await (c as any).mint(recipient, amount, lockId, sigs)).wait();
      gas.push(Number(rc.gasUsed));
    }
    expect(gas[0]).to.be.lessThan(BUDGET.mintFirst, "the first mint to an address");
    expect(gas[1]).to.be.lessThan(BUDGET.mintRepeat, "a repeat mint to the same address");

    const brc = await (
      await (c as any).connect(signers[1]).burn(ethers.parseUnits("1", 18), "a".repeat(64))
    ).wait();
    expect(Number(brc.gasUsed)).to.be.lessThan(BUDGET.burn, "burn");
  });
});
