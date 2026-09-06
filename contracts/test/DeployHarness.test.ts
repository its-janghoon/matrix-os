import { expect } from "chai";
import { ethers, network } from "hardhat";
import {
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

  it("runs the production deploy path and deploys WrappedMatrix with expected params", async () => {
    if (network.name === "mainnet" || network.name === "sepolia") {
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
    // Bridge token starts with zero supply — every token is minted via attestation.
    expect(result.totalSupply).to.equal("0");

    // The harness must report deployment gas.
    expect(result.deploymentGas, "deployment gas must be reported").to.not.equal(null);
    expect(BigInt(result.deploymentGas as string)).to.be.greaterThan(0n);
    // eslint-disable-next-line no-console
    console.log(`      deployment gas used: ${result.deploymentGas}`);
  });

  it("exercises a representative bridge mint path on the deployed contract", async () => {
    if (network.name === "mainnet" || network.name === "sepolia") {
      this.skip();
    }

    // Deploy a fresh instance with a single known attestor so we can sign an
    // attestation locally and drive the real mint code path.
    const attestor = ethers.Wallet.createRandom();
    const cfg = { attestors: [attestor.address], threshold: 1 };
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
    delete process.env.PRIVATE_KEY;
    delete process.env.ATTESTORS;
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
    }
  });
});
