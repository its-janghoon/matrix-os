import { ethers, network } from "hardhat";
import { mkdirSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { assertExpectedChain } from "./deploy-mainnet";
import {
  assertFounderCeremonyMatches,
  loadFounderCeremonyInput,
} from "./founder-ceremony";

const PRODUCTION_NETWORKS = new Set(["mainnet", "base"]);
const CEREMONY_NETWORKS = new Set(["mainnet", "base", "sepolia", "baseSepolia"]);
export const FOUNDER_ALLOCATION_WHOLE = "50000000"; // 5% of 1,000,000,000 MATRIX
export const CLIFF_SECONDS = 365 * 24 * 60 * 60;
export const DURATION_SECONDS = 5 * 365 * 24 * 60 * 60;
/** Inclusive production allowance for RPC/host clock skew around the reviewed start. */
export const MAX_VESTING_START_PAST_SKEW_SECONDS = 5 * 60;
/** Inclusive future window in which the reviewed deployment ceremony may begin. */
export const MAX_VESTING_START_FUTURE_SECONDS = 5 * 60;

export interface FounderVestingConfig {
  token: string;
  beneficiary: string;
  start: number;
  allocation: bigint;
}

/** Resolve immutable vesting parameters and refuse implicit production policy. */
export function resolveFounderVestingConfig(
  networkName: string,
  now: number,
  env: NodeJS.ProcessEnv = process.env
): FounderVestingConfig {
  if (!env.WMATRIX_ADDRESS) throw new Error("WMATRIX_ADDRESS is required");
  if (!env.FOUNDER_BENEFICIARY) {
    throw new Error("FOUNDER_BENEFICIARY is required (prefer a Safe address)");
  }

  const rawStart = (env.VESTING_START ?? "").trim();
  if (PRODUCTION_NETWORKS.has(networkName) && rawStart === "") {
    throw new Error(`VESTING_START is required on production network '${networkName}'`);
  }
  const start = rawStart === "" ? now + 300 : Number(rawStart);
  if (!Number.isSafeInteger(start) || start <= 0) {
    throw new Error(`VESTING_START must be a positive Unix timestamp (got '${rawStart}')`);
  }
  if (PRODUCTION_NETWORKS.has(networkName)) {
    const earliest = now - MAX_VESTING_START_PAST_SKEW_SECONDS;
    const latest = now + MAX_VESTING_START_FUTURE_SECONDS;
    if (start < earliest) {
      throw new Error(
        `VESTING_START ${start} is stale on '${networkName}'; earliest allowed is ${earliest} ` +
          `(${MAX_VESTING_START_PAST_SKEW_SECONDS}s clock-skew allowance)`
      );
    }
    if (start > latest) {
      throw new Error(
        `VESTING_START ${start} exceeds the ${MAX_VESTING_START_FUTURE_SECONDS}s ` +
          `future ceremony window; latest allowed is ${latest}`
      );
    }
  }

  const rawAllocation = (env.FOUNDER_ALLOCATION ?? "").trim();
  if (PRODUCTION_NETWORKS.has(networkName) && rawAllocation === "") {
    throw new Error(
      `FOUNDER_ALLOCATION is required on production network '${networkName}'; ` +
        `the recommended 5% allocation is ${FOUNDER_ALLOCATION_WHOLE}`
    );
  }
  const allocationText = rawAllocation || FOUNDER_ALLOCATION_WHOLE;
  if (!/^\d+(\.\d+)?$/.test(allocationText)) {
    throw new Error(`FOUNDER_ALLOCATION must be a non-negative decimal amount (got '${allocationText}')`);
  }
  const allocation = ethers.parseUnits(allocationText, 18);
  if (allocation <= 0n) throw new Error("FOUNDER_ALLOCATION must be greater than zero");

  return {
    token: ethers.getAddress(env.WMATRIX_ADDRESS),
    beneficiary: ethers.getAddress(env.FOUNDER_BENEFICIARY),
    start,
    allocation,
  };
}

async function main() {
  const net = await ethers.provider.getNetwork();
  assertExpectedChain(network.name, net.chainId);
  const latest = await ethers.provider.getBlock("latest");
  if (!latest) throw new Error("could not read latest block timestamp");
  const cfg = resolveFounderVestingConfig(network.name, latest.timestamp);

  if (CEREMONY_NETWORKS.has(network.name)) {
    const ceremonyPath = process.env.FOUNDER_CEREMONY;
    if (!ceremonyPath) {
      throw new Error(
        "FOUNDER_CEREMONY is required on real networks: freeze and review the constructor input JSON before deployment"
      );
    }
    const frozen = loadFounderCeremonyInput(ceremonyPath);
    assertFounderCeremonyMatches(frozen, {
      network: network.name,
      chainId: net.chainId,
      token: cfg.token,
      beneficiary: cfg.beneficiary,
      start: cfg.start,
      cliffDuration: CLIFF_SECONDS,
      duration: DURATION_SECONDS,
      allocation: cfg.allocation,
    }, "deployment inputs");
  }

  if ((await ethers.provider.getCode(cfg.token)) === "0x") {
    throw new Error(`WMATRIX_ADDRESS ${cfg.token} has no contract code on chain ${net.chainId}`);
  }
  const wrapped = await ethers.getContractAt("WrappedMatrix", cfg.token);
  if ((await wrapped.symbol()) !== "wMATRIX") {
    throw new Error(`WMATRIX_ADDRESS ${cfg.token} does not report symbol wMATRIX`);
  }
  const cap = await wrapped.mintCap();
  const supply = await wrapped.totalSupply();
  if (supply + cfg.allocation > cap) {
    throw new Error(
      `founder allocation would take wMATRIX supply to ${supply + cfg.allocation}, over mint cap ${cap}`
    );
  }

  const [deployer] = await ethers.getSigners();
  console.log("Deploying immutable FounderVestingVault");
  console.log("  network:     ", network.name, `(${net.chainId})`);
  console.log("  deployer:    ", deployer.address, "(has no vault authority)");
  console.log("  token:       ", cfg.token);
  console.log("  beneficiary: ", cfg.beneficiary);
  console.log("  allocation:  ", cfg.allocation.toString());
  console.log("  start:       ", cfg.start);
  console.log("  cliff:       ", CLIFF_SECONDS, "seconds");
  console.log("  duration:    ", DURATION_SECONDS, "seconds");

  const factory = await ethers.getContractFactory("FounderVestingVault");
  const vault = await factory.deploy(
    cfg.token,
    cfg.beneficiary,
    cfg.start,
    CLIFF_SECONDS,
    DURATION_SECONDS,
    cfg.allocation
  );
  await vault.waitForDeployment();
  const address = await vault.getAddress();
  if ((await ethers.provider.getCode(address)) === "0x") {
    throw new Error(`vesting deployment at ${address} has no code`);
  }
  const receipt = await vault.deploymentTransaction()?.wait();

  const record = {
    network: network.name,
    chainId: net.chainId.toString(),
    address,
    deployer: deployer.address,
    token: cfg.token,
    beneficiary: cfg.beneficiary,
    allocation: cfg.allocation.toString(),
    start: cfg.start,
    cliffDuration: CLIFF_SECONDS,
    duration: DURATION_SECONDS,
    block: receipt?.blockNumber ?? null,
    gasUsed: receipt?.gasUsed?.toString() ?? null,
  };
  const dir = resolve(__dirname, "../deployments");
  mkdirSync(dir, { recursive: true });
  const file = resolve(dir, `founder-vesting.${network.name}.json`);
  writeFileSync(file, JSON.stringify(record, null, 2) + "\n", "utf8");

  console.log("FounderVestingVault deployed to:", address);
  console.log("Deployment record written to:", file);
  console.log("\nBACKING CEREMONY — deployment alone creates no founder tokens:");
  console.log(`  1. Lock exactly ${cfg.allocation / 1_000_000_000n} native base units on Matrix OS.`);
  console.log(`  2. Set the Base mint recipient to this vault: ${address}`);
  console.log("  3. Gather threshold lock attestations and run mint:founder-vesting:* with this same frozen ceremony file.");
  console.log(`  4. Confirm vault wMATRIX balance == ${cfg.allocation} and publish bridge reconciliation.`);
  console.log("  Never mint this allocation before the equal native amount is in bridge escrow.");
  console.log("\nVerify source with:");
  console.log(
    `  npx hardhat verify --network ${network.name} ${address} ${cfg.token} ${cfg.beneficiary} ` +
      `${cfg.start} ${CLIFF_SECONDS} ${DURATION_SECONDS} ${cfg.allocation}`
  );
}

if (require.main === module) {
  main().catch((error) => {
    console.error(error);
    process.exitCode = 1;
  });
}
