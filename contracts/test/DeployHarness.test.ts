import { expect } from "chai";
import { ethers, network } from "hardhat";
import {
  assertExpectedChain,
  deployWrappedMatrix,
  resolveDeployConfig,
} from "../scripts/deploy-mainnet";
import type { WrappedMatrix } from "../typechain-types";

const ERC20_PER_NATIVE_UNIT = 10n ** 9n;

/**
 * Dry-run / fork simulation of the production mainnet deploy harness.
 *
 * This test runs the REAL deploy path from scripts/deploy-mainnet.ts
 * (`resolveDeployConfig` + `deployWrappedMatrix`) against the in-process Hardhat
 * network. When MAINNET_FORK_RPC_URL is set, hardhat.config.ts forks mainnet so
 * this same test executes against forked mainnet state — proving the harness
 * before the operator ever broadcasts a real transaction. No key is ever held
 * here; local dry-runs use hardhat's built-in funded accounts.
 */
describe("Deploy harness (dry-run / fork simulation)", function () {
  // Forked runs pull remote state and can be slow.
  this.timeout(120_000);

  it("checks Base chain IDs before signing", () => {
    expect(() => assertExpectedChain("base", 8453n)).not.to.throw();
    expect(() => assertExpectedChain("baseSepolia", 84532n)).not.to.throw();
    expect(() => assertExpectedChain("base", 1n)).to.throw(/expected 8453/);
  });

  it("runs the production deploy path and deploys WrappedMatrix with expected params", async () => {
    if (["mainnet", "sepolia", "base", "baseSepolia"].includes(network.name)) {
      // Guard: this simulation must never run against a real network.
      this.skip();
    }

    // Exercise the real config resolution (local fallback to hardhat accounts).
    const cfg = await resolveDeployConfig(network.name);
    expect(cfg.attestors.length).to.be.greaterThanOrEqual(cfg.threshold);
    expect(cfg.threshold).to.be.greaterThanOrEqual(1);

    // Run the exact deploy function used by scripts/deploy-mainnet.ts.
    const result = await deployWrappedMatrix(cfg);

    expect(ethers.isAddress(result.address)).to.equal(true);
    expect(result.name).to.equal("Wrapped Matrix");
    expect(result.symbol).to.equal("wMATRIX");
    expect(result.decimals).to.equal(18);
    expect(result.attestorCount).to.equal(String(cfg.attestors.length));
    expect(result.args.threshold).to.equal(cfg.threshold);
    expect(result.erc20PerNativeUnit).to.equal(ERC20_PER_NATIVE_UNIT.toString());
    // With no MINT_CAP set, the deploy resolves cap_ == 0 to the contract's
    // documented DEFAULT_MINT_CAP (1e27, the full wrapped supply ceiling).
    expect(result.mintCap).to.equal((10n ** 27n).toString());
    // Bridge token starts with zero supply, every token is minted via attestation.
    expect(result.totalSupply).to.equal("0");

    // The harness must report deployment gas.
    expect(result.deploymentGas, "deployment gas must be reported").to.not.equal(null);
    expect(BigInt(result.deploymentGas as string)).to.be.greaterThan(0n);
    // eslint-disable-next-line no-console
    console.log(`      deployment gas used: ${result.deploymentGas}`);
  });

  it("exercises a representative bridge mint path on the deployed contract", async () => {
    if (["mainnet", "sepolia", "base", "baseSepolia"].includes(network.name)) {
      this.skip();
    }

    // Deploy a fresh instance with a single known attestor so we can sign an
    // attestation locally and drive the real mint code path.
    const attestor = ethers.Wallet.createRandom();
    const cfg = { attestors: [attestor.address], threshold: 1, mintCap: 0n };
    const result = await deployWrappedMatrix(cfg);

    const factory = await ethers.getContractFactory("WrappedMatrix");
    const wmatrix = factory.attach(result.address) as unknown as WrappedMatrix;

    const [, recipientSigner] = await ethers.getSigners();
    const recipient = recipientSigner.address;
    const nativeAmount = 5n; // native base units
    const amount = nativeAmount * ERC20_PER_NATIVE_UNIT;
    const lockId = ethers.id("deploy-harness-lock-1");

    // Recreate the canonical attestation digest the contract verifies.
    const chainId = (await ethers.provider.getNetwork()).chainId;
    const digest = ethers.solidityPackedKeccak256(
      ["address", "uint256", "bytes32", "uint256", "address"],
      [recipient, amount, lockId, chainId, result.address]
    );

    // Sign the raw digest (no EIP-191 prefix), matching the contract's ecrecover.
    const sig = attestor.signingKey.sign(digest);
    const signature = ethers.Signature.from(sig).serialized;

    await expect(wmatrix.mint(recipient, amount, lockId, [signature]))
      .to.emit(wmatrix, "Minted")
      .withArgs(lockId, recipient, amount);

    expect(await wmatrix.totalSupply()).to.equal(amount);
    expect(await wmatrix.balanceOf(recipient)).to.equal(amount);
  });

  it("refuses an unsafe real-network config (missing PRIVATE_KEY / ATTESTORS)", async () => {
    // Simulate resolving config as if targeting mainnet, without secrets set.
    const savedKey = process.env.PRIVATE_KEY;
    const savedAttestors = process.env.ATTESTORS;
    const savedMintCap = process.env.MINT_CAP;
    delete process.env.PRIVATE_KEY;
    delete process.env.ATTESTORS;
    process.env.MINT_CAP = "60000000000000000000000000";
    try {
      await expect(resolveDeployConfig("mainnet")).to.be.rejectedWith(/PRIVATE_KEY/);

      // With a key present but no attestors, it must still refuse on a real net.
      process.env.PRIVATE_KEY = "0x" + "1".repeat(64);
      await expect(resolveDeployConfig("mainnet")).to.be.rejectedWith(/ATTESTORS/);
    } finally {
      if (savedKey === undefined) delete process.env.PRIVATE_KEY;
      else process.env.PRIVATE_KEY = savedKey;
      if (savedAttestors === undefined) delete process.env.ATTESTORS;
      else process.env.ATTESTORS = savedAttestors;
      if (savedMintCap === undefined) delete process.env.MINT_CAP;
      else process.env.MINT_CAP = savedMintCap;
    }
  });

  it("enforces a strict real-network attestor quorum", async () => {
    const names = ["PRIVATE_KEY", "ATTESTORS", "THRESHOLD", "MINT_CAP", "ALLOW_SINGLE_ATTESTOR"] as const;
    const saved = Object.fromEntries(names.map((name) => [name, process.env[name]]));
    const attestors = [
      "0xad42459bdc6d4e2761461788239fcc995e42cce7",
      "0x709fca731675b619cc24c284eeb5aef1d7527c77",
      "0x90f79bf6eb2c4f870365e785982e1f101e93b906",
    ];
    process.env.PRIVATE_KEY = "0x" + "1".repeat(64);
    process.env.ATTESTORS = attestors.join(",");
    process.env.MINT_CAP = "60000000000000000000000000";
    delete process.env.ALLOW_SINGLE_ATTESTOR;
    try {
      process.env.THRESHOLD = "1";
      await expect(resolveDeployConfig("mainnet")).to.be.rejectedWith(/strictly greater than two thirds/);

      process.env.THRESHOLD = "2";
      await expect(resolveDeployConfig("mainnet")).to.be.rejectedWith(/strictly greater than two thirds/);

      process.env.THRESHOLD = "3";
      const cfg = await resolveDeployConfig("mainnet");
      expect(cfg.attestors).to.have.length(3);
      expect(cfg.threshold).to.equal(3);

      process.env.ATTESTORS = attestors[0];
      process.env.THRESHOLD = "1";
      process.env.ALLOW_SINGLE_ATTESTOR = "1";
      await expect(resolveDeployConfig("base")).to.be.rejectedWith(/fewer than two attestors/);
    } finally {
      for (const name of names) {
        if (saved[name] === undefined) delete process.env[name];
        else process.env[name] = saved[name];
      }
    }
  });

  it("refuses Base mainnet without an explicit non-zero mint cap", async () => {
    const savedKey = process.env.PRIVATE_KEY;
    const savedAttestors = process.env.ATTESTORS;
    const savedThreshold = process.env.THRESHOLD;
    const savedCap = process.env.MINT_CAP;
    process.env.PRIVATE_KEY = "0x" + "1".repeat(64);
    process.env.ATTESTORS = [
      "0xad42459bdc6d4e2761461788239fcc995e42cce7",
      "0x709fca731675b619cc24c284eeb5aef1d7527c77",
    ].join(",");
    process.env.THRESHOLD = "2";
    delete process.env.MINT_CAP;
    try {
      await expect(resolveDeployConfig("base")).to.be.rejectedWith(/explicit non-zero MINT_CAP/);
    } finally {
      if (savedKey === undefined) delete process.env.PRIVATE_KEY; else process.env.PRIVATE_KEY = savedKey;
      if (savedAttestors === undefined) delete process.env.ATTESTORS; else process.env.ATTESTORS = savedAttestors;
      if (savedThreshold === undefined) delete process.env.THRESHOLD; else process.env.THRESHOLD = savedThreshold;
      if (savedCap === undefined) delete process.env.MINT_CAP; else process.env.MINT_CAP = savedCap;
    }
  });
});
