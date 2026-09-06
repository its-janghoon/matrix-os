import { ethers } from "hardhat";

/**
 * Deploys the WrappedMatrix bridge token against the configured network.
 *
 * The validator/attestor set is taken from the ATTESTORS env var (a
 * comma-separated list of 0x addresses) when set; otherwise it falls back to the
 * first `n` local hardhat accounts so a local deploy works with no secrets. The
 * mint threshold comes from THRESHOLD (default 2). NEVER hardcode a real private
 * key: signer keys come from hardhat's local accounts or the PRIVATE_KEY env var
 * wired in hardhat.config.ts.
 *
 * Usage against a local node:
 *   npx hardhat node &            # in one shell
 *   sleep 4
 *   npx hardhat run scripts/deploy-bridge.ts --network localhost
 */
async function main() {
  const signers = await ethers.getSigners();
  const deployer = signers[0];

  const threshold = Number(process.env.THRESHOLD ?? "2");

  let attestors: string[];
  if (process.env.ATTESTORS && process.env.ATTESTORS.trim() !== "") {
    attestors = process.env.ATTESTORS.split(",").map((a) => ethers.getAddress(a.trim()));
  } else {
    // Fall back to local hardhat accounts as a default validator set.
    const n = Math.max(threshold, 3);
    attestors = signers.slice(0, n).map((s) => s.address);
  }

  if (threshold < 1 || threshold > attestors.length) {
    throw new Error(`THRESHOLD must be between 1 and ${attestors.length}`);
  }

  console.log("Deploying WrappedMatrix with account:", deployer.address);
  console.log("  attestors:", attestors);
  console.log("  threshold:", threshold);

  const factory = await ethers.getContractFactory("WrappedMatrix");
  const wmatrix = await factory.deploy(attestors, threshold);
  await wmatrix.waitForDeployment();

  const address = await wmatrix.getAddress();
  console.log("WrappedMatrix deployed to:", address);
  console.log("  name:              ", await wmatrix.name());
  console.log("  symbol:            ", await wmatrix.symbol());
  console.log("  decimals:          ", await wmatrix.decimals());
  console.log("  attestorCount:     ", (await wmatrix.attestorCount()).toString());
  console.log("  threshold:         ", (await wmatrix.threshold()).toString());
  console.log("  ERC20_PER_NATIVE:  ", (await wmatrix.ERC20_PER_NATIVE_UNIT()).toString());
  console.log("  initial totalSupply:", (await wmatrix.totalSupply()).toString());
  console.log(
    "\nRegister these attestor addresses in internal/bridge (they must match the",
    "\nsecp256k1 keys the validators sign with) so Go-produced attestations verify here."
  );
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
