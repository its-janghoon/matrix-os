import { expect } from "chai";
import { ethers } from "hardhat";
import { mintAttestations, parseAttestations, orderSignatures } from "../scripts/bridge-mint";

/**
 * Guards on the mint script.
 *
 * The reason they exist: WrappedMatrix.mint is exacting in ways that are
 * invisible at the call site, and every one of them fails as a bare revert
 * after the gas is already spent. Each test below is a mistake a real operator
 * gathering attestations from n nodes will make.
 */
describe("bridge-mint.ts attestation guards", () => {
  const RECIPIENT = ethers.getAddress("0x70997970c51812dc3a010c7d01b50e0d17dc79c8");
  const LOCK = "0x" + "ab".repeat(32);
  const AMOUNT = "4000000000000000000";

  function att(over: Record<string, unknown> = {}) {
    return {
      recipient: RECIPIENT,
      erc20Amount: AMOUNT,
      lockId: LOCK.slice(2), // the node returns it unprefixed, which is the point
      signature: "00".repeat(65),
      attestor: "0x" + "11".repeat(20),
      ...over,
    };
  }

  it("accepts the node's unprefixed lock id, because that is what it returns", () => {
    const got = parseAttestations(JSON.stringify([att()]));
    expect(got.lockId).to.equal(LOCK);
    expect(got.amount).to.equal(BigInt(AMOUNT));
    expect(got.recipient).to.equal(RECIPIENT);
  });

  it("refuses attestations that disagree on the recipient, which means a fork", () => {
    const other = ethers.getAddress("0x3c44cdddb6a900fa2b585dd299e03d12fa4293bc");
    expect(() =>
      parseAttestations(JSON.stringify([att(), att({ recipient: other })]))
    ).to.throw(/do not agree on this lock/);
  });

  it("refuses attestations that disagree on the amount", () => {
    expect(() =>
      parseAttestations(JSON.stringify([att(), att({ erc20Amount: "1" })]))
    ).to.throw(/do not agree on this lock/);
  });

  it("refuses a signature that is not 65 bytes, before it reaches ecrecover", () => {
    expect(() => parseAttestations(JSON.stringify([att({ signature: "0011" })]))).to.throw(
      /signature is 2 bytes, want 65/
    );
  });

  it("refuses an empty set rather than sending a mint with no signatures", () => {
    expect(() => parseAttestations("[]")).to.throw(/empty/);
  });

  it("mints the validated snapshot without rereading the attestation path", async () => {
    const attestor = ethers.Wallet.createRandom();
    const Wrapped = await ethers.getContractFactory("WrappedMatrix");
    const token = await Wrapped.deploy([attestor.address], 1, BigInt(AMOUNT));
    await token.waitForDeployment();
    const digest = await token.attestationDigest(RECIPIENT, BigInt(AMOUNT), LOCK);
    const signature = ethers.Signature.from(attestor.signingKey.sign(digest)).serialized;
    const snapshot = parseAttestations(JSON.stringify([att({ signature })]));

    const previousContract = process.env.CONTRACT;
    const previousAttestations = process.env.ATTESTATIONS;
    process.env.CONTRACT = await token.getAddress();
    process.env.ATTESTATIONS = "/path/replaced-after-founder-preflight.json";
    try {
      await mintAttestations(snapshot);
    } finally {
      if (previousContract === undefined) delete process.env.CONTRACT;
      else process.env.CONTRACT = previousContract;
      if (previousAttestations === undefined) delete process.env.ATTESTATIONS;
      else process.env.ATTESTATIONS = previousAttestations;
    }

    expect(await token.balanceOf(RECIPIENT)).to.equal(BigInt(AMOUNT));
  });

  /**
   * The ordering rule is the one that bites hardest: the contract counts
   * distinct signers by requiring strictly ascending recovered addresses, so a
   * threshold's worth of perfectly valid signatures submitted in the order the
   * nodes answered reverts InvalidSignature.
   */
  it("sorts signatures by ascending signer, whatever order the nodes answered in", async () => {
    const digest = ethers.keccak256(ethers.toUtf8Bytes("a digest to sign"));
    const wallets = [
      new ethers.Wallet("0x" + "a1".repeat(32)),
      new ethers.Wallet("0x" + "b2".repeat(32)),
      new ethers.Wallet("0x" + "c3".repeat(32)),
    ];
    const sigs = wallets.map((w) => w.signingKey.sign(digest).serialized);

    // Feed them in an order deliberately unrelated to the signer addresses.
    const ordered = orderSignatures(digest, [sigs[2], sigs[0], sigs[1]]);
    expect(ordered).to.have.length(3);
    for (let i = 1; i < ordered.length; i++) {
      expect(BigInt(ordered[i].signer) > BigInt(ordered[i - 1].signer)).to.equal(
        true,
        `signer ${ordered[i].signer} must sort after ${ordered[i - 1].signer}`
      );
    }
    // Every input signature survives the sort: sorting must not drop one.
    expect(new Set(ordered.map((o) => o.signature))).to.deep.equal(new Set(sigs));
  });

  it("refuses a duplicate signer, which is one short of the threshold and not one over", async () => {
    const digest = ethers.keccak256(ethers.toUtf8Bytes("a digest to sign"));
    const w = new ethers.Wallet("0x" + "a1".repeat(32));
    const sig = w.signingKey.sign(digest).serialized;
    expect(() => orderSignatures(digest, [sig, sig])).to.throw(/signed twice/);
  });
});
