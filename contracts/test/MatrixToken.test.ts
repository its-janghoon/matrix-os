import { expect } from "chai";
import { ethers } from "hardhat";
import type { MatrixToken } from "../typechain-types";
import type { HardhatEthersSigner } from "@nomicfoundation/hardhat-ethers/signers";

const INITIAL_SUPPLY = ethers.parseUnits("100000000", 18); // 100M MATRIX
const MAX_SUPPLY = ethers.parseUnits("1000000000", 18); // 1B MATRIX

describe("MatrixToken", () => {
  let token: MatrixToken;
  let owner: HardhatEthersSigner;
  let alice: HardhatEthersSigner;
  let bob: HardhatEthersSigner;

  beforeEach(async () => {
    [owner, alice, bob] = await ethers.getSigners();
    const factory = await ethers.getContractFactory("MatrixToken");
    token = (await factory.deploy(INITIAL_SUPPLY)) as unknown as MatrixToken;
    await token.waitForDeployment();
  });

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

    it("sets the deployer as owner", async () => {
      expect(await token.owner()).to.equal(owner.address);
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

  describe("mint access control and supply cap", () => {
    it("lets the owner mint", async () => {
      const amount = ethers.parseUnits("1000", 18);
      await expect(token.mint(alice.address, amount))
        .to.emit(token, "Transfer")
        .withArgs(ethers.ZeroAddress, alice.address, amount);
      expect(await token.balanceOf(alice.address)).to.equal(amount);
      expect(await token.totalSupply()).to.equal(INITIAL_SUPPLY + amount);
    });

    it("reverts when a non-owner tries to mint", async () => {
      await expect(
        token.connect(alice).mint(alice.address, ethers.parseUnits("1", 18))
      ).to.be.revertedWithCustomError(token, "OwnableUnauthorizedAccount");
    });

    it("reverts when a mint would exceed max supply", async () => {
      const remaining = MAX_SUPPLY - INITIAL_SUPPLY;
      const tooMuch = remaining + 1n;
      await expect(
        token.mint(owner.address, tooMuch)
      ).to.be.revertedWithCustomError(token, "MaxSupplyExceeded");
    });

    it("allows minting up to exactly the max supply", async () => {
      const remaining = MAX_SUPPLY - INITIAL_SUPPLY;
      await token.mint(owner.address, remaining);
      expect(await token.totalSupply()).to.equal(MAX_SUPPLY);
    });

    it("rejects a constructor initial supply above the cap", async () => {
      const factory = await ethers.getContractFactory("MatrixToken");
      await expect(
        factory.deploy(MAX_SUPPLY + 1n)
      ).to.be.revertedWithCustomError(factory, "MaxSupplyExceeded");
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
