import { ethers } from "hardhat";

/**
 * Deploys MatrixToken to the configured network and logs the deployed address
 * and initial supply.
 *
 * The deployer signer is provided by Hardhat. On a real network you supply a
 * key via the PRIVATE_KEY env var (wired in hardhat.config.ts); against the
 * local node / in-process network Hardhat's built-in funded accounts are used.
 * NEVER hardcode a real private key here.
 */
async function main() {
  // Initial supply minted to the deployer: 100,000,000 MATRIX (18 decimals).
  const initialSupply = ethers.parseUnits("100000000", 18);

  const [deployer] = await ethers.getSigners();
  console.log("Deploying MatrixToken with account:", deployer.address);

  const MatrixToken = await ethers.getContractFactory("MatrixToken");
  const token = await MatrixToken.deploy(initialSupply);
  await token.waitForDeployment();

  const address = await token.getAddress();
  const name = await token.name();
  const symbol = await token.symbol();
  const decimals = await token.decimals();
  const totalSupply = await token.totalSupply();
  const maxSupply = await token.MAX_SUPPLY();

  console.log("MatrixToken deployed to:", address);
  console.log("  name:       ", name);
  console.log("  symbol:     ", symbol);
  console.log("  decimals:   ", decimals);
  console.log("  totalSupply:", ethers.formatUnits(totalSupply, decimals), symbol);
  console.log("  maxSupply:  ", ethers.formatUnits(maxSupply, decimals), symbol);
  console.log(
    "  deployer balance:",
    ethers.formatUnits(await token.balanceOf(deployer.address), decimals),
    symbol
  );
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
