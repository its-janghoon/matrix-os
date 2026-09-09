import { expect } from "chai";
import { ethers } from "hardhat";
import { time } from "@nomicfoundation/hardhat-network-helpers";
import {
  CLIFF_SECONDS,
  DURATION_SECONDS,
  FOUNDER_ALLOCATION_WHOLE,
  MAX_VESTING_START_FUTURE_SECONDS,
  MAX_VESTING_START_PAST_SKEW_SECONDS,
  resolveFounderVestingConfig,
} from "../scripts/deploy-founder-vesting";
import {
  assertFounderCeremonyMatches,
  parseFounderCeremonyInput,
} from "../scripts/founder-ceremony";

const ALLOCATION = 50_000_000n * 10n ** 18n;

async function fixture() {
  const [, beneficiary] = await ethers.getSigners();
  const attestor = ethers.Wallet.createRandom();
  const Wrapped = await ethers.getContractFactory("WrappedMatrix");
  const token = await Wrapped.deploy([attestor.address], 1, ALLOCATION);
  await token.waitForDeployment();

  const start = (await time.latest()) + 100;
  const Vault = await ethers.getContractFactory("FounderVestingVault");
  const vault = await Vault.deploy(
    await token.getAddress(),
    beneficiary.address,
    start,
    CLIFF_SECONDS,
    DURATION_SECONDS,
    ALLOCATION
  );
  await vault.waitForDeployment();

  const lockId = ethers.id("founder-backed-allocation");
  const digest = await token.attestationDigest(await vault.getAddress(), ALLOCATION, lockId);
  const signature = ethers.Signature.from(attestor.signingKey.sign(digest)).serialized;
  await token.mint(await vault.getAddress(), ALLOCATION, lockId, [signature]);
  return { token, vault, beneficiary, start };
}

describe("FounderVestingVault", () => {
  it("releases nothing before the one-year cliff and 20% at the cliff", async () => {
    const { token, vault, beneficiary, start } = await fixture();
    await time.increaseTo(start + CLIFF_SECONDS - 2);
    expect(await vault.releasable()).to.equal(0n);
    await expect(vault.release()).to.be.revertedWithCustomError(vault, "NothingReleasable");

    await time.increaseTo(start + CLIFF_SECONDS);
    const cliffVested = (ALLOCATION * BigInt(CLIFF_SECONDS)) / BigInt(DURATION_SECONDS);
    expect(await vault.releasable()).to.equal(cliffVested);
    await expect(vault.release()).to.emit(vault, "Released");
    const released = await vault.released();
    expect(released).to.be.greaterThanOrEqual(cliffVested);
    expect(await token.balanceOf(beneficiary.address)).to.equal(released);
  });

  it("supports partial releases and reaches exactly 100% after five years", async () => {
    const { token, vault, beneficiary, start } = await fixture();
    await time.increaseTo(start + 2 * 365 * 24 * 60 * 60);
    await vault.release();
    const first = await vault.released();
    expect(first).to.be.greaterThanOrEqual((ALLOCATION * 2n) / 5n);
    expect(await token.balanceOf(beneficiary.address)).to.equal(first);

    await time.increaseTo(start + 3 * 365 * 24 * 60 * 60);
    await vault.release();
    const second = await vault.released();
    expect(second).to.be.greaterThanOrEqual((ALLOCATION * 3n) / 5n);
    expect(await token.balanceOf(beneficiary.address)).to.equal(second);

    await time.increaseTo(start + DURATION_SECONDS);
    await vault.release();
    expect(await token.balanceOf(beneficiary.address)).to.equal(ALLOCATION);
    expect(await vault.released()).to.equal(ALLOCATION);
    expect(await vault.releasable()).to.equal(0n);
  });

  it("always pays the immutable beneficiary even when a stranger triggers release", async () => {
    const { token, vault, beneficiary, start } = await fixture();
    const [, , stranger] = await ethers.getSigners();
    await time.increaseTo(start + CLIFF_SECONDS);
    await vault.connect(stranger).release();
    const released = await vault.released();
    expect(await token.balanceOf(stranger.address)).to.equal(0n);
    expect(await token.balanceOf(beneficiary.address)).to.equal(released);
    expect(released).to.be.greaterThanOrEqual(ALLOCATION / 5n);
  });

  it("rejects zero addresses, empty allocations, and impossible schedules", async () => {
    const { token, beneficiary, start } = await fixture();
    const Vault = await ethers.getContractFactory("FounderVestingVault");
    await expect(Vault.deploy(ethers.ZeroAddress, beneficiary.address, start, 1, 2, 1))
      .to.be.revertedWithCustomError(Vault, "ZeroAddress");
    await expect(Vault.deploy(await token.getAddress(), ethers.ZeroAddress, start, 1, 2, 1))
      .to.be.revertedWithCustomError(Vault, "ZeroAddress");
    await expect(Vault.deploy(await token.getAddress(), beneficiary.address, start, 3, 2, 1))
      .to.be.revertedWithCustomError(Vault, "InvalidSchedule");
    await expect(Vault.deploy(await token.getAddress(), beneficiary.address, start, 1, 2, 0))
      .to.be.revertedWithCustomError(Vault, "ZeroAllocation");
  });
});

describe("founder vesting deployment policy", () => {
  const token = "0x0000000000000000000000000000000000000001";
  const beneficiary = "0x0000000000000000000000000000000000000002";
  const now = 1_800_000_000;
  const productionEnv = (start: number): NodeJS.ProcessEnv => ({
    WMATRIX_ADDRESS: token,
    FOUNDER_BENEFICIARY: beneficiary,
    VESTING_START: String(start),
    FOUNDER_ALLOCATION: FOUNDER_ALLOCATION_WHOLE,
  });

  it("requires explicit start and allocation on Base production", () => {
    expect(() => resolveFounderVestingConfig("base", now, {
      WMATRIX_ADDRESS: token,
      FOUNDER_BENEFICIARY: beneficiary,
    })).to.throw(/VESTING_START/);
    expect(() => resolveFounderVestingConfig("base", now, {
      WMATRIX_ADDRESS: token,
      FOUNDER_BENEFICIARY: beneficiary,
      VESTING_START: String(now),
    })).to.throw(/FOUNDER_ALLOCATION/);
  });

  it("rejects an elapsed cliff and starts older than the clock-skew allowance", () => {
    expect(() => resolveFounderVestingConfig("base", now, productionEnv(now - CLIFF_SECONDS)))
      .to.throw(/stale/);
    expect(() => resolveFounderVestingConfig(
      "base",
      now,
      productionEnv(now - MAX_VESTING_START_PAST_SKEW_SECONDS - 1)
    )).to.throw(/stale/);
  });

  it("accepts the inclusive past-skew and future-ceremony boundaries", () => {
    expect(resolveFounderVestingConfig(
      "base",
      now,
      productionEnv(now - MAX_VESTING_START_PAST_SKEW_SECONDS)
    ).start).to.equal(now - MAX_VESTING_START_PAST_SKEW_SECONDS);
    expect(resolveFounderVestingConfig(
      "base",
      now,
      productionEnv(now + MAX_VESTING_START_FUTURE_SECONDS)
    ).start).to.equal(now + MAX_VESTING_START_FUTURE_SECONDS);
  });

  it("rejects a start beyond the future ceremony window", () => {
    expect(() => resolveFounderVestingConfig(
      "base",
      now,
      productionEnv(now + MAX_VESTING_START_FUTURE_SECONDS + 1)
    )).to.throw(/future ceremony window/);
  });

  it("rejects a start that ages out before the deployment transaction is mined", async () => {
    const reviewedAt = await time.latest();
    const acceptedAtPreflight = reviewedAt - MAX_VESTING_START_PAST_SKEW_SECONDS;
    expect(resolveFounderVestingConfig(
      "base",
      reviewedAt,
      productionEnv(acceptedAtPreflight)
    ).start).to.equal(acceptedAtPreflight);

    const Vault = await ethers.getContractFactory("FounderVestingVault");
    await time.setNextBlockTimestamp(reviewedAt + 1);
    await expect(Vault.deploy(
      token,
      beneficiary,
      acceptedAtPreflight,
      CLIFF_SECONDS,
      DURATION_SECONDS,
      ALLOCATION
    )).to.be.revertedWithCustomError(Vault, "InvalidStart");
  });

  it("parses and exactly compares the frozen ceremony start", () => {
    const frozen = parseFounderCeremonyInput(JSON.stringify({
      network: "base",
      chainId: "8453",
      token,
      beneficiary,
      start: now,
      cliffDuration: CLIFF_SECONDS,
      duration: DURATION_SECONDS,
      allocation: ALLOCATION.toString(),
    }));
    expect(() => assertFounderCeremonyMatches(frozen, frozen, "on-chain vault")).not.to.throw();
    expect(() => assertFounderCeremonyMatches(
      frozen,
      { ...frozen, start: now + 1 },
      "on-chain vault"
    )).to.throw(/start mismatch/);
  });

  it("uses the documented 5% allocation for local rehearsal", () => {
    const got = resolveFounderVestingConfig("hardhat", now, {
      WMATRIX_ADDRESS: token,
      FOUNDER_BENEFICIARY: beneficiary,
    });
    expect(got.start).to.equal(now + 300);
    expect(got.allocation).to.equal(ethers.parseUnits(FOUNDER_ALLOCATION_WHOLE, 18));
  });
});
