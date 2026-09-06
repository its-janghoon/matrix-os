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
}

function resolveVerifyInput(): VerifyInput {
  if (process.env.ADDRESS && process.env.ATTESTORS) {
    const attestors = process.env.ATTESTORS.split(",").map((a) => ethers.getAddress(a.trim()));
    const threshold = Number(process.env.THRESHOLD ?? "2");
    return { address: ethers.getAddress(process.env.ADDRESS), attestors, threshold };
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
  };
}

async function main() {
  if (!process.env.ETHERSCAN_API_KEY) {
    throw new Error("ETHERSCAN_API_KEY is not set; cannot verify on Etherscan.");
  }

  const input = resolveVerifyInput();
  console.log("Verifying WrappedMatrix on", network.name);
  console.log("  address:  ", input.address);
  console.log("  attestors:", input.attestors);
  console.log("  threshold:", input.threshold);

  await run("verify:verify", {
    address: input.address,
    constructorArguments: [input.attestors, input.threshold],
  });

  console.log("Verification submitted.");
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
