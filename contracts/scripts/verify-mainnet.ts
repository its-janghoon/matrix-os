import { run, network } from "hardhat";
import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { ethers } from "hardhat";

/**
 * Etherscan source-verification flow for a deployed WrappedMatrix.
 *
 * Reads ETHERSCAN_API_KEY from the environment (wired into the `etherscan`
 * block in hardhat.config.ts). The contract address + constructor args are
 * taken from either:
 *   1. the JSON deployment record written by scripts/deploy-mainnet.ts
 *      (deployments/wrapped-matrix.<network>.json), or
 *   2. the ADDRESS env var plus ATTESTORS / THRESHOLD env vars.
 *
 * Usage (after deploy-mainnet.ts wrote the record):
 *   npx hardhat run scripts/verify-mainnet.ts --network mainnet
 *
 * Or explicitly:
 *   ADDRESS=0x... ATTESTORS=0x..,0x.. THRESHOLD=2 \
 *     npx hardhat run scripts/verify-mainnet.ts --network mainnet
 *
 * Equivalent one-liner (constructor args must match exactly):
 *   npx hardhat verify --network mainnet <address> '["0x..","0x.."]' <threshold>
 */
interface VerifyInput {
  address: string;
  attestors: string[];
  threshold: number;
  // The resolved mint cap constructor arg (18-decimal base units). It must be
  // the value the contract was actually deployed with (the deploy record stores
  // the resolved cap, not the raw 0-means-default input), or Etherscan rejects
  // the verification for mismatched constructor args.
  mintCap: string;
}

function resolveVerifyInput(): VerifyInput {
  if (process.env.ADDRESS && process.env.ATTESTORS) {
    const attestors = process.env.ATTESTORS.split(",").map((a) => ethers.getAddress(a.trim()));
    const threshold = Number(process.env.THRESHOLD ?? "2");
    const mintCap = (process.env.MINT_CAP ?? "0").trim();
    return { address: ethers.getAddress(process.env.ADDRESS), attestors, threshold, mintCap };
  }

  const recordPath = resolve(__dirname, `../deployments/wrapped-matrix.${network.name}.json`);
  if (!existsSync(recordPath)) {
    throw new Error(
      `no deployment record at ${recordPath} and ADDRESS/ATTESTORS env not set. ` +
        `Run scripts/deploy-mainnet.ts first, or pass ADDRESS + ATTESTORS + THRESHOLD.`
    );
  }
  const record = JSON.parse(readFileSync(recordPath, "utf8"));
  return {
    address: record.address,
    attestors: record.args.attestors,
    threshold: record.args.threshold,
    // The CONSTRUCTOR ARGUMENT, never the resolved cap.
    //
    // These differ, and preferring the wrong one made verification impossible
    // for the common case. MINT_CAP unset means the constructor was called with
    // 0, and the contract then substitutes DEFAULT_MINT_CAP (1e27) internally.
    // The record stores both: args.mintCap is what was passed, mintCap is what
    // the contract reports. Etherscan re-encodes the arguments and compares them
    // to the deploy transaction's calldata, so submitting 1e27 for a deploy that
    // passed 0 fails every time - on a step whose whole purpose is to prove the
    // deployed bytecode matches the source.
    mintCap: (record.args?.mintCap ?? record.mintCap ?? "0").toString(),
  };
}

async function main() {
  // One etherscan.io key covers every chain through the Etherscan V2 API,
  // including Base and Base Sepolia. This used to demand BASESCAN_API_KEY for
  // the Base networks, matching a hardhat.config.ts that pinned them to the
  // per-explorer V1 endpoints; those endpoints now reject every request with
  // "You are using a deprecated V1 endpoint". BASESCAN_API_KEY is still
  // accepted as a fallback so an existing .env keeps working.
  const explorerKey = process.env.ETHERSCAN_API_KEY ?? process.env.BASESCAN_API_KEY;
  if (!explorerKey) {
    throw new Error(
      "ETHERSCAN_API_KEY is not set; cannot verify. One etherscan.io key serves " +
        "every chain via the Etherscan V2 API - get it from https://etherscan.io/myapikey"
    );
  }

  const input = resolveVerifyInput();
  const code = await ethers.provider.getCode(input.address);
  if (code === "0x") {
    throw new Error(`address ${input.address} has no contract code on ${network.name}`);
  }
  console.log("Verifying WrappedMatrix on", network.name);
  console.log("  address:  ", input.address);
  console.log("  attestors:", input.attestors);
  console.log("  threshold:", input.threshold);
  console.log("  mintCap:  ", input.mintCap);

  await run("verify:verify", {
    address: input.address,
    constructorArguments: [input.attestors, input.threshold, input.mintCap],
  });

  console.log("Verification submitted.");
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
