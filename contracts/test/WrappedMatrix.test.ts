import { expect } from "chai";
import { ethers } from "hardhat";
import type { WrappedMatrix } from "../typechain-types";
import type { HardhatEthersSigner } from "@nomicfoundation/hardhat-ethers/signers";

const ERC20_PER_NATIVE_UNIT = 10n ** 9n; // matches supply.go ERC20PerNativeUnit

// Deterministic local test attestor keys (NOT secrets, never used for funds).
// They mirror how internal/bridge derives local test keys.
const ATTESTOR_KEYS = [
  "0x1111111111111111111111111111111111111111111111111111111111111111",
  "0x2222222222222222222222222222222222222222222222222222222222222222",
  "0x3333333333333333333333333333333333333333333333333333333333333333",
];

// buildDigest recomputes the canonical attestation digest exactly as
// WrappedMatrix.attestationDigest and internal/bridge.AttestationDigest do:
// keccak256(abi.encodePacked(recipient, amount, lockId, chainId, contract)).
function buildDigest(
  recipient: string,
  amount: bigint,
  lockId: string,
  chainId: bigint,
  contract: string
): string {
  return ethers.solidityPackedKeccak256(
    ["address", "uint256", "bytes32", "uint256", "address"],
    [recipient, amount, lockId, chainId, contract]
  );
}

// signDigest signs the raw 32-byte digest with a secp256k1 key and returns the
// 65-byte {r,s,v} signature that ecrecover accepts (NO EIP-191 prefix), matching
// internal/bridge.Attestor.SignDigest.
function signDigest(privKey: string, digest: string): string {
  const sk = new ethers.SigningKey(privKey);
  const sig = sk.sign(digest);
  return ethers.concat([sig.r, sig.s, ethers.toBeHex(sig.v, 1)]);
}

// sortByCosigner orders {signer, sig} pairs by ascending signer address, which
// WrappedMatrix.mint requires (ascending order enforces distinct signers).
function orderedSignatures(
  keys: string[],
  digest: string
): string[] {
  const items = keys.map((k) => {
    const addr = ethers.computeAddress(new ethers.SigningKey(k).publicKey);
    return { addr: BigInt(addr), sig: signDigest(k, digest) };
  });
  items.sort((a, b) => (a.addr < b.addr ? -1 : a.addr > b.addr ? 1 : 0));
  return items.map((i) => i.sig);
}

describe("WrappedMatrix", () => {
  let wmatrix: WrappedMatrix;
  let deployer: HardhatEthersSigner;
  let recipient: HardhatEthersSigner;
  let chainId: bigint;
  let attestorAddrs: string[];

  const THRESHOLD = 2;

  beforeEach(async () => {
    [deployer, recipient] = await ethers.getSigners();
    attestorAddrs = ATTESTOR_KEYS.map((k) =>
      ethers.computeAddress(new ethers.SigningKey(k).publicKey)
    );
    const factory = await ethers.getContractFactory("WrappedMatrix");
    wmatrix = (await factory.deploy(attestorAddrs, THRESHOLD)) as unknown as WrappedMatrix;
    await wmatrix.waitForDeployment();
    chainId = (await ethers.provider.getNetwork()).chainId;
  });

  describe("construction", () => {
    it("registers attestors and threshold", async () => {
      expect(await wmatrix.attestorCount()).to.equal(ATTESTOR_KEYS.length);
      expect(await wmatrix.threshold()).to.equal(THRESHOLD);
      for (const a of attestorAddrs) {
        expect(await wmatrix.isAttestor(a)).to.equal(true);
      }
      expect(await wmatrix.ERC20_PER_NATIVE_UNIT()).to.equal(ERC20_PER_NATIVE_UNIT);
    });

    it("has wrapped metadata", async () => {
      expect(await wmatrix.name()).to.equal("Wrapped Matrix");
      expect(await wmatrix.symbol()).to.equal("wMATRIX");
      expect(await wmatrix.decimals()).to.equal(18);
    });

    it("reverts on empty attestor set", async () => {
      const factory = await ethers.getContractFactory("WrappedMatrix");
      await expect(factory.deploy([], 1)).to.be.revertedWithCustomError(
        factory,
        "InvalidAttestorSet"
      );
    });

    it("reverts when threshold exceeds attestor count", async () => {
      const factory = await ethers.getContractFactory("WrappedMatrix");
      await expect(
        factory.deploy(attestorAddrs, ATTESTOR_KEYS.length + 1)
      ).to.be.revertedWithCustomError(factory, "InvalidAttestorSet");
    });

    it("reverts on duplicate attestor", async () => {
      const factory = await ethers.getContractFactory("WrappedMatrix");
      await expect(
        factory.deploy([attestorAddrs[0], attestorAddrs[0]], 1)
      ).to.be.revertedWithCustomError(factory, "InvalidAttestor");
    });
  });

  describe("mint on valid attestation", () => {
    const nativeAmount = 4n; // 4 native base units
    const amount = nativeAmount * ERC20_PER_NATIVE_UNIT; // wrapped amount
    const lockId = ethers.zeroPadValue("0xabcd01", 32);

    it("mints when a threshold of valid signatures is supplied", async () => {
      const contract = await wmatrix.getAddress();
      const digest = buildDigest(recipient.address, amount, lockId, chainId, contract);
      const sigs = orderedSignatures(ATTESTOR_KEYS.slice(0, 2), digest);

      await expect(wmatrix.mint(recipient.address, amount, lockId, sigs))
        .to.emit(wmatrix, "Minted")
        .withArgs(lockId, recipient.address, amount);

      expect(await wmatrix.balanceOf(recipient.address)).to.equal(amount);
      expect(await wmatrix.totalSupply()).to.equal(amount);
      expect(await wmatrix.mintedLockId(lockId)).to.equal(true);
    });

    it("agrees with the contract's own attestationDigest", async () => {
      const contract = await wmatrix.getAddress();
      const local = buildDigest(recipient.address, amount, lockId, chainId, contract);
      const onchain = await wmatrix.attestationDigest(recipient.address, amount, lockId);
      expect(onchain).to.equal(local);
    });

    it("rejects insufficient threshold", async () => {
      const contract = await wmatrix.getAddress();
      const digest = buildDigest(recipient.address, amount, lockId, chainId, contract);
      const sigs = orderedSignatures(ATTESTOR_KEYS.slice(0, 1), digest);
      await expect(
        wmatrix.mint(recipient.address, amount, lockId, sigs)
      ).to.be.revertedWithCustomError(wmatrix, "ThresholdNotMet");
    });

    it("rejects a forged signature from a non-attestor", async () => {
      const contract = await wmatrix.getAddress();
      const digest = buildDigest(recipient.address, amount, lockId, chainId, contract);
      const forgedKey =
        "0x9999999999999999999999999999999999999999999999999999999999999999";
      const sigs = orderedSignatures([ATTESTOR_KEYS[0], forgedKey], digest);
      await expect(
        wmatrix.mint(recipient.address, amount, lockId, sigs)
      ).to.be.revertedWithCustomError(wmatrix, "InvalidSignature");
    });

    it("rejects duplicate signer (non-ascending order)", async () => {
      const contract = await wmatrix.getAddress();
      const digest = buildDigest(recipient.address, amount, lockId, chainId, contract);
      const sig = signDigest(ATTESTOR_KEYS[0], digest);
      await expect(
        wmatrix.mint(recipient.address, amount, lockId, [sig, sig])
      ).to.be.revertedWithCustomError(wmatrix, "InvalidSignature");
    });

    it("rejects signatures over a tampered amount", async () => {
      const contract = await wmatrix.getAddress();
      const digest = buildDigest(recipient.address, amount, lockId, chainId, contract);
      const sigs = orderedSignatures(ATTESTOR_KEYS.slice(0, 2), digest);
      // Submit with a different amount than what was signed.
      await expect(
        wmatrix.mint(recipient.address, amount + 1n, lockId, sigs)
      ).to.be.revertedWithCustomError(wmatrix, "InvalidSignature");
    });

    it("rejects a replayed lockId", async () => {
      const contract = await wmatrix.getAddress();
      const digest = buildDigest(recipient.address, amount, lockId, chainId, contract);
      const sigs = orderedSignatures(ATTESTOR_KEYS.slice(0, 2), digest);
      await wmatrix.mint(recipient.address, amount, lockId, sigs);
      await expect(
        wmatrix.mint(recipient.address, amount, lockId, sigs)
      ).to.be.revertedWithCustomError(wmatrix, "LockAlreadyMinted");
    });

    it("rejects a zero amount", async () => {
      const contract = await wmatrix.getAddress();
      const digest = buildDigest(recipient.address, 0n, lockId, chainId, contract);
      const sigs = orderedSignatures(ATTESTOR_KEYS.slice(0, 2), digest);
      await expect(
        wmatrix.mint(recipient.address, 0n, lockId, sigs)
      ).to.be.revertedWithCustomError(wmatrix, "ZeroAmount");
    });
  });

  describe("burn to unlock", () => {
    const nativeAmount = 10n;
    const amount = nativeAmount * ERC20_PER_NATIVE_UNIT;
    const lockId = ethers.zeroPadValue("0x01", 32);
    const nativeRecipient = "a".repeat(64); // an L1 ed25519 account id

    beforeEach(async () => {
      const contract = await wmatrix.getAddress();
      const digest = buildDigest(recipient.address, amount, lockId, chainId, contract);
      const sigs = orderedSignatures(ATTESTOR_KEYS.slice(0, 2), digest);
      await wmatrix.mint(recipient.address, amount, lockId, sigs);
    });

    it("burns and emits the native recipient", async () => {
      const burnAmount = 3n * ERC20_PER_NATIVE_UNIT;
      await expect(wmatrix.connect(recipient).burn(burnAmount, nativeRecipient))
        .to.emit(wmatrix, "Burned")
        .withArgs(recipient.address, nativeRecipient, burnAmount);

      expect(await wmatrix.balanceOf(recipient.address)).to.equal(amount - burnAmount);
      expect(await wmatrix.totalSupply()).to.equal(amount - burnAmount);
    });

    it("rejects a burn that is not a multiple of the conversion factor", async () => {
      await expect(
        wmatrix.connect(recipient).burn(ERC20_PER_NATIVE_UNIT + 1n, nativeRecipient)
      ).to.be.revertedWithCustomError(wmatrix, "NonMultipleBurn");
    });

    it("rejects a zero burn", async () => {
      await expect(
        wmatrix.connect(recipient).burn(0n, nativeRecipient)
      ).to.be.revertedWithCustomError(wmatrix, "ZeroAmount");
    });

    it("reverts burning more than the balance", async () => {
      await expect(
        wmatrix.connect(recipient).burn(amount + ERC20_PER_NATIVE_UNIT, nativeRecipient)
      ).to.be.revertedWithCustomError(wmatrix, "ERC20InsufficientBalance");
    });
  });

  describe("supply invariant tracks net locked", () => {
    it("total wrapped supply equals net minted (locked) amount", async () => {
      const contract = await wmatrix.getAddress();
      let expectedSupply = 0n;

      // Three locks -> mints.
      for (let i = 0; i < 3; i++) {
        const nativeAmount = BigInt((i + 1) * 5);
        const amount = nativeAmount * ERC20_PER_NATIVE_UNIT;
        const lockId = ethers.zeroPadValue(ethers.toBeHex(100 + i), 32);
        const digest = buildDigest(recipient.address, amount, lockId, chainId, contract);
        const sigs = orderedSignatures(ATTESTOR_KEYS.slice(0, 2), digest);
        await wmatrix.mint(recipient.address, amount, lockId, sigs);
        expectedSupply += amount;
      }
      expect(await wmatrix.totalSupply()).to.equal(expectedSupply);

      // A burn (unlock) reduces the net locked/wrapped supply 1:1.
      const burnAmount = 4n * ERC20_PER_NATIVE_UNIT;
      await wmatrix.connect(recipient).burn(burnAmount, "b".repeat(64));
      expectedSupply -= burnAmount;
      expect(await wmatrix.totalSupply()).to.equal(expectedSupply);
    });
  });
});
