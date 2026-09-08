import { expect } from "chai";
import { ethers } from "hardhat";

/**
 * A gas budget for the bridge, because these numbers get quoted to an operator
 * deciding what to spend, and the deploy figure is quoted about a transaction
 * that happens ONCE against a contract with no owner and an immutable cap.
 *
 * MINT HAS THREE TIERS, NOT TWO. Getting this wrong understates the recurring
 * cost of running a bridge by about a fifth, and the first version of this file
 * got it wrong: it minted twice to the SAME recipient, saw 109,769 then 75,589,
 * and concluded the second number was what every later user costs. It is not.
 * Measured at threshold 2:
 *
 *   109,769  the first mint the contract EVER performs
 *    92,670  the first mint to a recipient that has never held wMATRIX
 *    75,590  a repeat mint to an address that already holds some
 *
 * The 34,200 gap between the outer two is 2 x 17,100 - two cold zero-to-nonzero
 * SSTORE premiums - and they are NOT the same kind of event. One is the
 * recipient's balance slot, paid once per new user, forever. The other is
 * OpenZeppelin's `_totalSupply` slot, which is zero only until the first mint
 * and never again. So the number that matters for "what does each new bridge
 * user cost me" is the MIDDLE one, and it is the tier a naive test never sees.
 *
 * BURN IS NOT A CONSTANT EITHER. It scales with the native recipient string,
 * which is unbounded calldata and unbounded LOG data at roughly 24 gas per
 * character: 36,975 for a 3-character id, 38,097 for the 64-character id a real
 * Matrix account uses, 42,741 for 256. Burning a balance to zero is CHEAPER
 * (30,488) because the storage reset refunds. The 64-character case is the one
 * budgeted below, because that is what an actual account id is.
 *
 * Other inputs that move the numbers, all small but none of them zero: a real
 * MINT_CAP costs ~106 gas more to deploy than the 0 sentinel; attestor address
 * byte patterns move the deploy by up to ~672 gas (zero bytes are cheaper
 * calldata); signature and lockId bytes move a mint by tens of gas run to run.
 * Which is why these are CEILINGS about 15% above measured, not exact matches:
 * a test pinning the last digit fails on a toolchain bump while telling nobody
 * anything, whereas these pass through drift and fail on a change of shape - a
 * loop added to the constructor, a storage write added to mint, a per-attestor
 * cost that stops being one cold SSTORE.
 *
 * Environment: solc 0.8.24, optimizer 200, solc evmVersion cancun; the hardhat
 * NETWORK runs its default fork (osaka in hardhat 2.29.1). Gas is identical
 * under cancun, prague and osaka for every transaction here, so these transfer
 * to mainnet as gas UNITS and only the gas PRICE is unknown.
 */
describe("bridge gas budget", () => {
  const BUDGET = {
    deploy2of3: 1_750_000,
    perExtraAttestor: 30_000,
    mintFirstEver: 130_000,
    mintNewRecipient: 110_000,
    mintRepeat: 90_000,
    burn64CharRecipient: 45_000,
  };

  function attestors(n: number) {
    return Array.from({ length: n }, (_, i) =>
      new ethers.Wallet("0x" + String(i + 1).repeat(64).slice(0, 64))
    ).sort((a, b) => (BigInt(a.address) < BigInt(b.address) ? -1 : 1));
  }

  async function bridge(threshold = 2) {
    const set = attestors(3);
    const c = await (await ethers.getContractFactory("WrappedMatrix")).deploy(
      set.map((w) => w.address),
      threshold,
      0
    );
    await c.waitForDeployment();
    return { c: c as any, set };
  }

  async function mintTo(c: any, set: ethers.Wallet[], to: string, threshold = 2) {
    const amount = ethers.parseUnits("4", 18);
    // A random lockId, because a low-entropy one is cheaper calldata and would
    // flatter the measurement by a few hundred gas.
    const lockId = ethers.hexlify(ethers.randomBytes(32));
    const digest = await c.attestationDigest(to, amount, lockId);
    const sigs = set
      .slice(0, threshold)
      .map((w) => ({ w, s: w.signingKey.sign(digest).serialized }))
      .sort((a, b) => (BigInt(a.w.address) < BigInt(b.w.address) ? -1 : 1))
      .map((x) => x.s);
    const rc = await (await c.mint(to, amount, lockId, sigs)).wait();
    return Number(rc.gasUsed);
  }

  it("deploys a 2-of-3 bridge within budget", async () => {
    const set = attestors(3).map((w) => w.address);
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
    const three = await (await F.deploy(attestors(3).map((w) => w.address), 2, 0))
      .deploymentTransaction()!
      .wait();
    const five = await (await F.deploy(attestors(5).map((w) => w.address), 2, 0))
      .deploymentTransaction()!
      .wait();
    const perAttestor = (Number(five!.gasUsed) - Number(three!.gasUsed)) / 2;
    expect(perAttestor).to.be.lessThan(
      BUDGET.perExtraAttestor,
      "each registered attestor costs more than one cold storage write, which means the " +
        "constructor is doing something per-attestor that it did not used to"
    );
  });

  /**
   * The tier that decides the operator's recurring bill. Every distinct person
   * who bridges in for the first time pays this one, forever, and the only way
   * to see it is to mint to an address that has never held the token.
   */
  it("mints to a NEW recipient within budget, which is the per-user cost", async () => {
    const { c, set } = await bridge();
    const firstEver = await mintTo(c, set, ethers.Wallet.createRandom().address);
    expect(firstEver).to.be.lessThan(BUDGET.mintFirstEver, "the contract's first mint ever");

    // Now supply is non-zero, so this is the steady-state cost of a new user.
    for (let i = 0; i < 3; i++) {
      const g = await mintTo(c, set, ethers.Wallet.createRandom().address);
      expect(g).to.be.lessThan(
        BUDGET.mintNewRecipient,
        "a first mint to a new recipient. This is what each new bridge user costs, and it " +
          "is ~17,100 gas above a repeat mint: the recipient's balance slot going from " +
          "zero to non-zero"
      );
      expect(g).to.be.greaterThan(
        BUDGET.mintRepeat,
        "a new recipient came in UNDER the repeat-mint budget, which means this test is no " +
          "longer measuring a cold balance slot and the per-user figure it guards is stale"
      );
    }
  });

  it("mints repeatedly to one holder within the cheaper budget", async () => {
    const { c, set } = await bridge();
    const holder = ethers.Wallet.createRandom().address;
    await mintTo(c, set, holder);
    await mintTo(c, set, holder);
    const repeat = await mintTo(c, set, holder);
    expect(repeat).to.be.lessThan(BUDGET.mintRepeat, "a repeat mint to an existing holder");
  });

  it("burns a real 64-character account id within budget", async () => {
    const signers = await ethers.getSigners();
    const { c, set } = await bridge();
    await mintTo(c, set, signers[1].address);
    const rc = await (
      await c.connect(signers[1]).burn(ethers.parseUnits("1", 18), "a".repeat(64))
    ).wait();
    expect(Number(rc.gasUsed)).to.be.lessThan(
      BUDGET.burn64CharRecipient,
      "burn with the 64-character account id a real Matrix account uses"
    );
  });

  /**
   * The native recipient is an unbounded string that lands in calldata and in
   * the Burned event. The burner pays for their own, so it is a cost note
   * rather than an attack, but a budget that quotes one number for burn is
   * quoting the length it happened to test.
   */
  it("charges the burner for a longer account id, so burn is a range not a constant", async () => {
    const signers = await ethers.getSigners();
    const { c, set } = await bridge();
    await mintTo(c, set, signers[1].address);
    const as1 = c.connect(signers[1]);
    const one = ethers.parseUnits("1", 18);
    const short = Number((await (await as1.burn(one, "a".repeat(3))).wait()).gasUsed);
    const long = Number((await (await as1.burn(one, "a".repeat(256))).wait()).gasUsed);
    expect(long).to.be.greaterThan(
      short,
      "a longer native recipient did not cost more, so either calldata pricing changed or " +
        "the string stopped reaching the event"
    );
    const perChar = (long - short) / 253;
    expect(perChar).to.be.lessThan(
      40,
      `burn costs ${perChar.toFixed(1)} gas per character of the account id, up from ~24. ` +
        "The string is unbounded, so a per-character cost is a per-character bill"
    );
  });
});
