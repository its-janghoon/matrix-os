import { ethers, network } from "hardhat";
import { mkdirSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import type { WrappedMatrix } from "../typechain-types";

/**
 * Production deploy harness for the Ethereum-side WRAPPED MATRIX (wMATRIX)
 * token + validator-attestation bridge.
 *
 * IMPORTANT — native-vs-wrapped model: what is deployed to Ethereum mainnet by
 * this script is the WRAPPED ERC-20 (`WrappedMatrix`) + its lock-and-mint
 * bridge, NOT "the" token. The canonical MATRIX coin is NATIVE to the Matrix OS
 * consensus L1; wMATRIX only mirrors native MATRIX 1:1 (backed by native locked
 * in L1 escrow) so it can be represented on Ethereum (e.g. for an exchange
 * listing). See contracts/README.md and WrappedMatrix.sol.
 *
 * This script NEVER holds or hardcodes a private key. The deployer key comes
 * from the PRIVATE_KEY env var (wired into hardhat.config.ts). The attestor set
 * and threshold come from ATTESTORS / THRESHOLD env vars.
 *
 * The real deploy logic lives in `deployWrappedMatrix`, exported so the dry-run
 * / fork simulation test (test/DeployHarness.test.ts) exercises the exact same
 * code path rather than a copy.
 */

/** Networks that are real (non-local) and therefore require strict guards. */
const REAL_NETWORKS = new Set(["mainnet", "sepolia"]);

export interface DeployConfig {
  /** Validator secp256k1 attestor addresses (the n in m-of-n). */
  attestors: string[];
  /** Distinct signatures required to authorize a mint (the m in m-of-n). */
  threshold: number;
  /**
   * Maximum total wrapped supply that may ever be minted (18-decimal base
   * units). It is a deploy-time policy knob: 0n selects the contract's
   * documented DEFAULT_MINT_CAP (1e27, the full wrapped supply ceiling), and a
   * tighter value bounds minting below the whole supply per the token-and-bridge
   * policy proposal. It is never a hardcoded literal here; it comes from the
   * MINT_CAP env var and defaults to 0n (the contract default).
   */
  mintCap: bigint;
}

export interface DeployResult {
  address: string;
  network: string;
  chainId: string;
  deployer: string;
  args: {
    attestors: string[];
    threshold: number;
    mintCap: string;
  };
  mintCap: string;
  block: number | null;
  deploymentGas: string | null;
  name: string;
  symbol: string;
  decimals: number;
  attestorCount: string;
  erc20PerNativeUnit: string;
  totalSupply: string;
}

/**
 * Resolve the deploy configuration from the environment.
 *
 * On a REAL network (mainnet/sepolia) this refuses footguns:
 *   - PRIVATE_KEY must be set (no unsigned/local-account deploys to real chains)
 *   - ATTESTORS must be explicitly provided (NO silent fallback to local
 *     hardhat accounts, which would register throwaway keys as validators)
 * On local networks it falls back to the first local hardhat accounts so a
 * dry-run works with no secrets.
 */
export async function resolveDeployConfig(networkName: string): Promise<DeployConfig> {
  const isReal = REAL_NETWORKS.has(networkName);
  const threshold = Number(process.env.THRESHOLD ?? "2");

  if (!Number.isInteger(threshold) || threshold < 1) {
    throw new Error(`THRESHOLD must be a positive integer (got '${process.env.THRESHOLD}')`);
  }

  // MINT_CAP is the deploy-time policy cap in 18-decimal base units. Unset (or
  // "0") selects the contract's documented DEFAULT_MINT_CAP; any other value
  // must be a positive integer that the contract further bounds at <= its
  // default (the full wrapped supply ceiling).
  const rawCap = process.env.MINT_CAP;
  let mintCap = 0n;
  if (rawCap && rawCap.trim() !== "") {
    try {
      mintCap = BigInt(rawCap.trim());
    } catch {
      throw new Error(`MINT_CAP must be an integer number of base units (got '${rawCap}')`);
    }
    if (mintCap < 0n) {
      throw new Error(`MINT_CAP must not be negative (got '${rawCap}')`);
    }
  }

  if (isReal && !process.env.PRIVATE_KEY) {
    throw new Error(
      `refusing to deploy to '${networkName}': PRIVATE_KEY is not set. ` +
        `Provide the deployer key via the PRIVATE_KEY env var (never hardcode it).`
    );
  }

  let attestors: string[];
  const rawAttestors = process.env.ATTESTORS;
  if (rawAttestors && rawAttestors.trim() !== "") {
    attestors = rawAttestors.split(",").map((a) => ethers.getAddress(a.trim()));
  } else if (isReal) {
    // NEVER silently use local hardhat accounts as validators on a real chain.
    throw new Error(
      `refusing to deploy to '${networkName}': ATTESTORS is not set. ` +
        `Set ATTESTORS to the comma-separated secp256k1 attestor addresses that ` +
        `match the Go validator keys (internal/bridge). Local fallback is only ` +
        `allowed on local/dry-run networks.`
    );
  } else {
    // Local / dry-run fallback: use the first n local hardhat accounts.
    const signers = await ethers.getSigners();
    const n = Math.max(threshold, 3);
    attestors = signers.slice(0, n).map((s) => s.address);
  }

  if (attestors.length === 0) {
    throw new Error("attestor set is empty");
  }
  if (isReal && attestors.length === 1) {
    // The same rule deploy.ts applies to MatrixToken's minters, applied here
    // because this is the side that holds real collateral. A 1-of-1 bridge is
    // one key able to mint the entire cap against escrow it does not own, which
    // is the whole thing an m-of-n set exists to prevent. Escapable only by
    // saying out loud that the deployment is disposable.
    if (process.env.ALLOW_SINGLE_ATTESTOR !== "1") {
      throw new Error(
        `refusing to deploy to '${networkName}' with a single attestor: that one key can ` +
          `mint the entire cap on its own. Give at least two attestors and a THRESHOLD of ` +
          `2 or more. If this is deliberately a throwaway rehearsal, set ` +
          `ALLOW_SINGLE_ATTESTOR=1 and treat the deployment as disposable.`
      );
    }
    console.warn(
      `WARNING: deploying to '${networkName}' with ONE attestor, because ` +
        `ALLOW_SINGLE_ATTESTOR=1. That key alone can mint the whole cap. This deployment ` +
        `is a rehearsal and must never be treated as production.`
    );
  }
  const unique = new Set(attestors.map((a) => a.toLowerCase()));
  if (unique.size !== attestors.length) {
    throw new Error("ATTESTORS contains duplicate addresses");
  }
  if (threshold > attestors.length) {
    throw new Error(
      `THRESHOLD (${threshold}) must be <= the number of attestors (${attestors.length})`
    );
  }

  return { attestors, threshold, mintCap };
}

/**
 * Deploy WrappedMatrix with the given attestor set + threshold and return a
 * full deployment record. This is the single, reusable deploy path used by both
 * the CLI entrypoint below and the dry-run/fork test.
 */
export async function deployWrappedMatrix(cfg: DeployConfig): Promise<DeployResult> {
  const [deployer] = await ethers.getSigners();
  const net = await ethers.provider.getNetwork();

  const factory = await ethers.getContractFactory("WrappedMatrix");

  // Report the estimated deployment gas before broadcasting.
  const deployTx = await factory.getDeployTransaction(cfg.attestors, cfg.threshold, cfg.mintCap);
  let deploymentGas: string | null = null;
  try {
    deploymentGas = (await ethers.provider.estimateGas(deployTx)).toString();
  } catch {
    // Estimation can fail on some providers; not fatal for the record.
    deploymentGas = null;
  }

  const wmatrix = (await factory.deploy(cfg.attestors, cfg.threshold, cfg.mintCap)) as unknown as WrappedMatrix;
  await wmatrix.waitForDeployment();

  const address = await wmatrix.getAddress();
  const deployReceipt = await wmatrix.deploymentTransaction()?.wait();

  return {
    address,
    network: network.name,
    chainId: net.chainId.toString(),
    deployer: deployer.address,
    args: {
      attestors: cfg.attestors,
      threshold: cfg.threshold,
      mintCap: cfg.mintCap.toString(),
    },
    block: deployReceipt?.blockNumber ?? null,
    deploymentGas: deployReceipt?.gasUsed?.toString() ?? deploymentGas,
    name: await wmatrix.name(),
    symbol: await wmatrix.symbol(),
    decimals: Number(await wmatrix.decimals()),
    attestorCount: (await wmatrix.attestorCount()).toString(),
    erc20PerNativeUnit: (await wmatrix.ERC20_PER_NATIVE_UNIT()).toString(),
    // The resolved on-chain cap (the contract turns 0 into DEFAULT_MINT_CAP), so
    // the record reflects what will actually be enforced, not the raw input.
    mintCap: (await wmatrix.mintCap()).toString(),
    totalSupply: (await wmatrix.totalSupply()).toString(),
  };
}

/** Write a JSON deployment record to the gitignored deployments/ dir. */
export function writeDeploymentRecord(result: DeployResult): string {
  const dir = resolve(__dirname, "../deployments");
  mkdirSync(dir, { recursive: true });
  const file = resolve(dir, `wrapped-matrix.${result.network}.json`);
  writeFileSync(file, JSON.stringify(result, null, 2) + "\n", "utf8");
  return file;
}

async function main() {
  const cfg = await resolveDeployConfig(network.name);
  const [deployer] = await ethers.getSigners();

  console.log("Deploying WrappedMatrix (wrapped ERC-20 + attestation bridge)");
  console.log("  network:   ", network.name);
  console.log("  deployer:  ", deployer.address);
  console.log("  attestors: ", cfg.attestors);
  console.log("  threshold: ", cfg.threshold);
  console.log("  mintCap:   ", cfg.mintCap === 0n ? "0 (contract DEFAULT_MINT_CAP)" : cfg.mintCap.toString());

  const result = await deployWrappedMatrix(cfg);

  console.log("\nWrappedMatrix deployed to:", result.address);
  console.log("  name:               ", result.name);
  console.log("  symbol:             ", result.symbol);
  console.log("  decimals:           ", result.decimals);
  console.log("  attestorCount:      ", result.attestorCount);
  console.log("  threshold:          ", result.args.threshold);
  console.log("  mintCap:            ", result.mintCap);
  console.log("  ERC20_PER_NATIVE:   ", result.erc20PerNativeUnit);
  console.log("  initial totalSupply:", result.totalSupply);
  console.log("  chainId:            ", result.chainId);
  console.log("  block:              ", result.block);
  console.log("  deploymentGas:      ", result.deploymentGas);

  const file = writeDeploymentRecord(result);
  console.log("\nDeployment record written to:", file);

  // Emit the exact verify command for the recorded constructor args.
  const argsCsv = result.args.attestors.join(",");
  console.log("\nTo verify on Etherscan (ETHERSCAN_API_KEY must be set):");
  console.log(
    // args.mintCap, not result.mintCap: verification re-encodes the CONSTRUCTOR
    // ARGUMENT and compares it to the deploy calldata, and those differ whenever
    // MINT_CAP was unset (0 passed, 1e27 resolved).
    `  ATTESTORS='${argsCsv}' THRESHOLD=${result.args.threshold} MINT_CAP=${result.args.mintCap} \\`
  );
  console.log(
    `    npx hardhat run scripts/verify-mainnet.ts --network ${result.network}`
  );
  console.log("  # or directly:");
  console.log(
    `  npx hardhat verify --network ${result.network} ${result.address} ` +
      `'[${result.args.attestors.map((a) => `"${a}"`).join(",")}]' ${result.args.threshold} ${result.args.mintCap}`
  );

  console.log(
    "\nRegister these attestor addresses in internal/bridge — they MUST match the",
    "\nsecp256k1 keys the Go validators sign with, or Go-produced attestations will",
    "\nnot verify on-chain."
  );
}

// Only run when invoked directly (e.g. `hardhat run scripts/deploy-mainnet.ts`),
// so importing the deploy function from tests does not trigger a deploy.
if (require.main === module) {
  main().catch((error) => {
    console.error(error);
    process.exitCode = 1;
  });
}
