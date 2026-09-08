import { expect } from "chai";
import { ethers } from "hardhat";
import { parseMinters, parseInitialSupply } from "../scripts/deploy";

/**
 * Guards on the MatrixToken deploy script.
 *
 * These matter because the contract's protection can be undone by
 * configuration: a 1-of-1 minter set satisfies the m-of-n code and is exactly
 * the single key the change removed. The strict branch needs a real network
 * name, which a hardhat run cannot reach without an RPC URL, so it is tested
 * here against parseMinters directly.
 */
describe("deploy.ts minter guards", () => {
  const a = ethers.getAddress("0x709fca731675b619cc24c284eeb5aef1d7527c77");
  const b = ethers.getAddress("0xf9c3c8f1389e7d319d8192283a1d7faf80e27849");
  const c = ethers.getAddress("0x13ac71e1c65e9691fbe86d2d558ce5a9e2ba7c9b");

  it("refuses a real network with no minter set: there is no single-owner fallback", () => {
    expect(() => parseMinters("mainnet", {})).to.throw(/MINTERS is required/);
  });

  it("refuses a real network with a 1-of-1 set, which is the key this replaced", () => {
    expect(() => parseMinters("mainnet", { MINTERS: a, THRESHOLD: "1" })).to.throw(/refusing to deploy/);
  });

  it("refuses a real network with a threshold of 1 over three minters", () => {
    expect(() =>
      parseMinters("sepolia", { MINTERS: [a, b, c].join(","), THRESHOLD: "1" })
    ).to.throw(/refusing to deploy/);
  });

  it("accepts 2-of-3 on a real network", () => {
    const got = parseMinters("mainnet", { MINTERS: [a, b, c].join(","), THRESHOLD: "2" });
    expect(got.threshold).to.equal(2);
    expect(got.minters).to.deep.equal([a, b, c]);
    expect(got.local).to.equal(false);
  });

  it("refuses a duplicate minter, which would fake distinctness", () => {
    expect(() => parseMinters("mainnet", { MINTERS: [a, a, b].join(","), THRESHOLD: "2" })).to.throw(
      /duplicate address/
    );
  });

  it("refuses a threshold above the minter count", () => {
    expect(() => parseMinters("mainnet", { MINTERS: [a, b].join(","), THRESHOLD: "3" })).to.throw(
      /THRESHOLD must be an integer/
    );
  });

  it("refuses a missing threshold", () => {
    expect(() => parseMinters("mainnet", { MINTERS: [a, b, c].join(",") })).to.throw(
      /THRESHOLD must be an integer/
    );
  });

  it("refuses a non-integer threshold", () => {
    expect(() =>
      parseMinters("mainnet", { MINTERS: [a, b, c].join(","), THRESHOLD: "1.5" })
    ).to.throw(/THRESHOLD must be an integer/);
  });

  it("refuses a malformed address rather than guessing", () => {
    expect(() => parseMinters("mainnet", { MINTERS: "not-an-address", THRESHOLD: "1" })).to.throw();
  });

  it("gives a local chain a working 2-of-3 default, and flags it as local", () => {
    const got = parseMinters("hardhat", {});
    expect(got.local).to.equal(true);
    expect(got.threshold).to.equal(2);
    expect(got.minters).to.have.lengthOf(3);
  });

  it("still applies the shape checks on a local chain", () => {
    expect(() => parseMinters("localhost", { MINTERS: [a, a].join(","), THRESHOLD: "2" })).to.throw(
      /duplicate address/
    );
  });
});

// The initial supply used to be a hardcoded 100,000,000 minted to the deploying
// key. MatrixToken is the STANDALONE mirror and is backed by nothing - only
// WrappedMatrix is backed by escrowed native - while the project describes its
// asset as 1:1 backed. A default that hands the deployer a hundred million
// unbacked tokens under the name MATRIX is the kind of thing nobody asked for
// and everybody would ask about.
describe("parseInitialSupply", () => {
  it("mints nothing unless someone types a number", () => {
    for (const net of ["localhost", "sepolia", "mainnet"]) {
      const { supply, explicit } = parseInitialSupply(net, {});
      expect(supply).to.equal(0n);
      expect(explicit).to.equal(false);
    }
  });

  it("treats an empty or whitespace value as unset rather than as zero-by-accident", () => {
    expect(parseInitialSupply("mainnet", { INITIAL_SUPPLY: "   " }).explicit).to.equal(false);
  });

  it("mints exactly what was asked for, in whole MATRIX at 18 decimals", () => {
    const { supply, explicit } = parseInitialSupply("localhost", { INITIAL_SUPPLY: "100" });
    expect(supply).to.equal(100n * 10n ** 18n);
    expect(explicit).to.equal(true);
  });

  it("refuses a value that is not a number, rather than minting something unintended", () => {
    for (const bad of ["1e8", "-5", "100_000", "abc", "0x64"]) {
      expect(() => parseInitialSupply("mainnet", { INITIAL_SUPPLY: bad })).to.throw(
        /INITIAL_SUPPLY must be/
      );
    }
  });

  it("accepts an explicit zero", () => {
    const { supply, explicit } = parseInitialSupply("mainnet", { INITIAL_SUPPLY: "0" });
    expect(supply).to.equal(0n);
    expect(explicit).to.equal(true);
  });
});
