import { expect } from "chai";
import { ethers } from "hardhat";
import { time } from "@nomicfoundation/hardhat-network-helpers";
import type { MatrixToken } from "../typechain-types";
import type { HardhatEthersSigner } from "@nomicfoundation/hardhat-ethers/signers";

const INITIAL_SUPPLY = ethers.parseUnits("100000000", 18); // 100M MATRIX
const MAX_SUPPLY = ethers.parseUnits("1000000000", 18); // 1B MATRIX
const MINT_DELAY = 2 * 24 * 60 * 60;
const MINT_WINDOW = 7 * 24 * 60 * 60;

// Deterministic local minter keys (NOT secrets, never used for funds), the same
// shape WrappedMatrix.test.ts uses - one signing convention across both
// contracts means one thing for an off-chain signer to get right.
const MINTER_KEYS = [
  "0xa111111111111111111111111111111111111111111111111111111111111111",
  "0xa222222222222222222222222222222222222222222222222222222222222222",
  "0xa333333333333333333333333333333333333333333333333333333333333333",
];
const OUTSIDER_KEY = "0xb999999999999999999999999999999999999999999999999999999999999999";

const addressOf = (key: string) => ethers.computeAddress(new ethers.SigningKey(key).publicKey);

/**
 * Signs the raw 32-byte digest and returns the 65-byte {r,s,v} signature
 * ecrecover accepts, with NO EIP-191 prefix - matching what the contract
 * recomputes.
 */
function signDigest(privKey: string, digest: string): string {
  const sig = new ethers.SigningKey(privKey).sign(digest);
  return ethers.concat([sig.r, sig.s, ethers.toBeHex(sig.v, 1)]);
}

/**
 * Signs a mint authorization with each key and returns the signatures in
 * strictly ascending signer-address order, which is what the contract requires
 * to prove distinctness without an auxiliary mapping.
 */
async function signMint(
  token: MatrixToken,
  keys: string[],
  to: string,
  amount: bigint,
  nonce: bigint
): Promise<string[]> {
  const digest = await token.mintDigest(to, amount, nonce);
  const items = keys.map((k) => ({ addr: BigInt(addressOf(k)), sig: signDigest(k, digest) }));
  items.sort((a, b) => (a.addr < b.addr ? -1 : a.addr > b.addr ? 1 : 0));
  return items.map((i) => i.sig);
}

describe("MatrixToken", () => {
  let token: MatrixToken;
  let owner: HardhatEthersSigner;
  let alice: HardhatEthersSigner;
  let bob: HardhatEthersSigner;
  // Three minters, threshold 2: enough to show that two authorize and one does
  // not, which is the property `onlyOwner` did not have.
  let minterAddrs: string[];

  beforeEach(async () => {
    [owner, alice, bob] = await ethers.getSigners();
    minterAddrs = MINTER_KEYS.map(addressOf);
    const factory = await ethers.getContractFactory("MatrixToken");
    token = (await factory.deploy(
      INITIAL_SUPPLY,
      owner.address,
      minterAddrs,
      2
    )) as unknown as MatrixToken;
    await token.waitForDeployment();
  });

  /** Proposes a mint with two minters and returns the nonce it consumed. */
  async function propose(to: string, amount: bigint): Promise<bigint> {
    const nonce = await token.mintNonce();
    const sigs = await signMint(token, MINTER_KEYS.slice(0, 2), to, amount, nonce);
    await token.proposeMint(to, amount, sigs);
    return nonce;
  }

  describe("metadata and initial supply", () => {
    it("has the expected name, symbol and decimals", async () => {
      expect(await token.name()).to.equal("Matrix Compute Token");
      expect(await token.symbol()).to.equal("MATRIX");
      expect(await token.decimals()).to.equal(18);
    });

    it("mints the initial supply to the deployer", async () => {
      expect(await token.totalSupply()).to.equal(INITIAL_SUPPLY);
      expect(await token.balanceOf(owner.address)).to.equal(INITIAL_SUPPLY);
    });

    it("exposes the documented max supply cap", async () => {
      expect(await token.MAX_SUPPLY()).to.equal(MAX_SUPPLY);
    });

    it("has no owner at all: there is no single minting key to compromise", async () => {
      // The whole point of the change. `owner()` is gone, so there is nothing
      // to transfer to an EOA later, by accident or otherwise.
      expect((token as unknown as { owner?: unknown }).owner).to.equal(undefined);
    });

    it("records the minter set and threshold", async () => {
      expect(await token.threshold()).to.equal(2);
      expect(await token.minterCount()).to.equal(3);
      expect(await token.isMinter(minterAddrs[0])).to.equal(true);
      expect(await token.isMinter(alice.address)).to.equal(false);
    });
  });

  describe("transfer", () => {
    it("moves balance and emits Transfer", async () => {
      const amount = ethers.parseUnits("1000", 18);
      await expect(token.transfer(alice.address, amount))
        .to.emit(token, "Transfer")
        .withArgs(owner.address, alice.address, amount);

      expect(await token.balanceOf(alice.address)).to.equal(amount);
      expect(await token.balanceOf(owner.address)).to.equal(INITIAL_SUPPLY - amount);
    });

    it("reverts on insufficient balance", async () => {
      const amount = ethers.parseUnits("1", 18);
      await expect(
        token.connect(alice).transfer(bob.address, amount)
      ).to.be.revertedWithCustomError(token, "ERC20InsufficientBalance");
    });
  });

  describe("approve / transferFrom / allowance", () => {
    it("approves an allowance and emits Approval", async () => {
      const amount = ethers.parseUnits("500", 18);
      await expect(token.approve(alice.address, amount))
        .to.emit(token, "Approval")
        .withArgs(owner.address, alice.address, amount);
      expect(await token.allowance(owner.address, alice.address)).to.equal(amount);
    });

    it("transfersFrom and decrements the allowance", async () => {
      const allowance = ethers.parseUnits("500", 18);
      const spend = ethers.parseUnits("200", 18);
      await token.approve(alice.address, allowance);

      await expect(
        token.connect(alice).transferFrom(owner.address, bob.address, spend)
      )
        .to.emit(token, "Transfer")
        .withArgs(owner.address, bob.address, spend);

      expect(await token.balanceOf(bob.address)).to.equal(spend);
      expect(await token.allowance(owner.address, alice.address)).to.equal(
        allowance - spend
      );
    });

    it("reverts transferFrom that over-spends the allowance", async () => {
      const allowance = ethers.parseUnits("100", 18);
      const spend = ethers.parseUnits("101", 18);
      await token.approve(alice.address, allowance);
      await expect(
        token.connect(alice).transferFrom(owner.address, bob.address, spend)
      ).to.be.revertedWithCustomError(token, "ERC20InsufficientAllowance");
    });
  });

  describe("mint requires a threshold and a timelock", () => {
    it("mints after a threshold proposal and the delay", async () => {
      const amount = ethers.parseUnits("1000", 18);
      const nonce = await propose(alice.address, amount);

      await time.increase(MINT_DELAY);
      await expect(token.executeMint(alice.address, amount, nonce))
        .to.emit(token, "Transfer")
        .withArgs(ethers.ZeroAddress, alice.address, amount);

      expect(await token.balanceOf(alice.address)).to.equal(amount);
      expect(await token.totalSupply()).to.equal(INITIAL_SUPPLY + amount);
    });

    it("refuses a proposal signed by ONE minter", async () => {
      const amount = ethers.parseUnits("1", 18);
      const nonce = await token.mintNonce();
      const sigs = await signMint(token, [MINTER_KEYS[0]], alice.address, amount, nonce);
      await expect(
        token.proposeMint(alice.address, amount, sigs)
      ).to.be.revertedWithCustomError(token, "ThresholdNotMet");
    });

    it("refuses a proposal signed by a non-minter", async () => {
      const amount = ethers.parseUnits("1", 18);
      const nonce = await token.mintNonce();
      const sigs = await signMint(token, [MINTER_KEYS[0], OUTSIDER_KEY], alice.address, amount, nonce);
      await expect(
        token.proposeMint(alice.address, amount, sigs)
      ).to.be.revertedWithCustomError(token, "InvalidSignature");
    });

    it("refuses one minter signing twice to reach the threshold", async () => {
      const amount = ethers.parseUnits("1", 18);
      const nonce = await token.mintNonce();
      const digest = await token.mintDigest(alice.address, amount, nonce);
      const sig = signDigest(MINTER_KEYS[0], digest);
      await expect(
        token.proposeMint(alice.address, amount, [sig, sig])
      ).to.be.revertedWithCustomError(token, "InvalidSignature");
    });

    it("refuses execution before the delay has elapsed", async () => {
      const amount = ethers.parseUnits("1000", 18);
      const nonce = await propose(alice.address, amount);
      await time.increase(MINT_DELAY - 60);
      await expect(
        token.executeMint(alice.address, amount, nonce)
      ).to.be.revertedWithCustomError(token, "MintNotReady");
      expect(await token.balanceOf(alice.address)).to.equal(0n);
    });

    it("refuses execution after the window has closed", async () => {
      const amount = ethers.parseUnits("1000", 18);
      const nonce = await propose(alice.address, amount);
      await time.increase(MINT_DELAY + MINT_WINDOW + 60);
      await expect(
        token.executeMint(alice.address, amount, nonce)
      ).to.be.revertedWithCustomError(token, "MintExpired");
    });

    it("refuses executing a mint nobody proposed", async () => {
      await expect(
        token.executeMint(alice.address, ethers.parseUnits("1", 18), 0)
      ).to.be.revertedWithCustomError(token, "MintNotProposed");
    });

    it("refuses executing the same proposal twice", async () => {
      const amount = ethers.parseUnits("1000", 18);
      const nonce = await propose(alice.address, amount);
      await time.increase(MINT_DELAY);
      await token.executeMint(alice.address, amount, nonce);
      await expect(
        token.executeMint(alice.address, amount, nonce)
      ).to.be.revertedWithCustomError(token, "MintNotProposed");
      expect(await token.balanceOf(alice.address)).to.equal(amount);
    });

    it("lets the threshold cancel a pending mint", async () => {
      const amount = ethers.parseUnits("1000", 18);
      const nonce = await propose(alice.address, amount);

      const sigs = await signMint(token, MINTER_KEYS.slice(1, 3), alice.address, amount, nonce);
      await expect(token.cancelMint(alice.address, amount, nonce, sigs))
        .to.emit(token, "MintCancelled");

      await time.increase(MINT_DELAY);
      await expect(
        token.executeMint(alice.address, amount, nonce)
      ).to.be.revertedWithCustomError(token, "MintNotProposed");
    });

    it("refuses a cancel signed by one minter", async () => {
      const amount = ethers.parseUnits("1000", 18);
      const nonce = await propose(alice.address, amount);
      const sigs = await signMint(token, [MINTER_KEYS[0]], alice.address, amount, nonce);
      await expect(
        token.cancelMint(alice.address, amount, nonce, sigs)
      ).to.be.revertedWithCustomError(token, "ThresholdNotMet");
    });

    it("will not let one signature set authorize two mints", async () => {
      const amount = ethers.parseUnits("1000", 18);
      const nonce = await token.mintNonce();
      const sigs = await signMint(token, MINTER_KEYS.slice(0, 2), alice.address, amount, nonce);
      await token.proposeMint(alice.address, amount, sigs);
      // The nonce advanced, so the same signatures no longer match the digest.
      await expect(
        token.proposeMint(alice.address, amount, sigs)
      ).to.be.revertedWithCustomError(token, "InvalidSignature");
    });

    it("reverts a proposal that would exceed max supply", async () => {
      const tooMuch = MAX_SUPPLY - INITIAL_SUPPLY + 1n;
      const nonce = await token.mintNonce();
      const sigs = await signMint(token, MINTER_KEYS.slice(0, 2), owner.address, tooMuch, nonce);
      await expect(
        token.proposeMint(owner.address, tooMuch, sigs)
      ).to.be.revertedWithCustomError(token, "MaxSupplyExceeded");
    });

    it("re-checks the cap at execution, not only at proposal", async () => {
      // Two proposals that each fit alone but not together. The second must
      // fail at EXECUTION: the cap check that protects holders is the one at
      // the moment of minting, and totalSupply moved in between.
      const half = (MAX_SUPPLY - INITIAL_SUPPLY) / 2n + 1n;
      const first = await propose(alice.address, half);
      const second = await propose(bob.address, half);

      await time.increase(MINT_DELAY);
      await token.executeMint(alice.address, half, first);
      await expect(
        token.executeMint(bob.address, half, second)
      ).to.be.revertedWithCustomError(token, "MaxSupplyExceeded");
    });

    it("mints up to exactly the max supply", async () => {
      const remaining = MAX_SUPPLY - INITIAL_SUPPLY;
      const nonce = await propose(owner.address, remaining);
      await time.increase(MINT_DELAY);
      await token.executeMint(owner.address, remaining, nonce);
      expect(await token.totalSupply()).to.equal(MAX_SUPPLY);
    });

    it("rejects a zero-amount proposal", async () => {
      const nonce = await token.mintNonce();
      const sigs = await signMint(token, MINTER_KEYS.slice(0, 2), alice.address, 0n, nonce);
      await expect(
        token.proposeMint(alice.address, 0n, sigs)
      ).to.be.revertedWithCustomError(token, "ZeroAmount");
    });

    it("binds a signature to THIS contract, so it cannot be replayed to a twin", async () => {
      const amount = ethers.parseUnits("1000", 18);
      const nonce = await token.mintNonce();
      const sigs = await signMint(token, MINTER_KEYS.slice(0, 2), alice.address, amount, nonce);

      const factory = await ethers.getContractFactory("MatrixToken");
      const twin = (await factory.deploy(
        INITIAL_SUPPLY,
        owner.address,
        minterAddrs,
        2
      )) as unknown as MatrixToken;
      await twin.waitForDeployment();

      await expect(
        twin.proposeMint(alice.address, amount, sigs)
      ).to.be.revertedWithCustomError(twin, "InvalidSignature");
    });
  });

  describe("constructor validation", () => {
    it("rejects an initial supply above the cap", async () => {
      const factory = await ethers.getContractFactory("MatrixToken");
      await expect(
        factory.deploy(MAX_SUPPLY + 1n, owner.address, minterAddrs, 2)
      ).to.be.revertedWithCustomError(factory, "MaxSupplyExceeded");
    });

    it("rejects an empty minter set", async () => {
      const factory = await ethers.getContractFactory("MatrixToken");
      await expect(
        factory.deploy(0n, owner.address, [], 1)
      ).to.be.revertedWithCustomError(factory, "InvalidMinterSet");
    });

    it("rejects a threshold above the minter count", async () => {
      const factory = await ethers.getContractFactory("MatrixToken");
      await expect(
        factory.deploy(0n, owner.address, minterAddrs.slice(0, 2), 3)
      ).to.be.revertedWithCustomError(factory, "InvalidMinterSet");
    });

    it("rejects a zero threshold", async () => {
      const factory = await ethers.getContractFactory("MatrixToken");
      await expect(
        factory.deploy(0n, owner.address, minterAddrs, 0)
      ).to.be.revertedWithCustomError(factory, "InvalidMinterSet");
    });

    it("rejects a duplicated minter, which would fake distinctness", async () => {
      const factory = await ethers.getContractFactory("MatrixToken");
      await expect(
        factory.deploy(0n, owner.address, [minterAddrs[0], minterAddrs[0]], 2)
      ).to.be.revertedWithCustomError(factory, "InvalidMinter");
    });

    it("rejects a zero-address minter", async () => {
      const factory = await ethers.getContractFactory("MatrixToken");
      await expect(
        factory.deploy(0n, owner.address, [ethers.ZeroAddress], 1)
      ).to.be.revertedWithCustomError(factory, "InvalidMinter");
    });
  });

  describe("permit (EIP-2612)", () => {
    it("sets an allowance via a signed permit", async () => {
      const value = ethers.parseUnits("123", 18);
      const deadline = ethers.MaxUint256;
      const nonce = await token.nonces(owner.address);

      const domain = {
        name: await token.name(),
        version: "1",
        chainId: (await ethers.provider.getNetwork()).chainId,
        verifyingContract: await token.getAddress(),
      };
      const types = {
        Permit: [
          { name: "owner", type: "address" },
          { name: "spender", type: "address" },
          { name: "value", type: "uint256" },
          { name: "nonce", type: "uint256" },
          { name: "deadline", type: "uint256" },
        ],
      };
      const message = {
        owner: owner.address,
        spender: alice.address,
        value,
        nonce,
        deadline,
      };

      const signature = await owner.signTypedData(domain, types, message);
      const { v, r, s } = ethers.Signature.from(signature);

      await token.permit(owner.address, alice.address, value, deadline, v, r, s);
      expect(await token.allowance(owner.address, alice.address)).to.equal(value);
      expect(await token.nonces(owner.address)).to.equal(nonce + 1n);
    });
  });
});
