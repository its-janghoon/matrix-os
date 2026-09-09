import { ethers, network } from "hardhat";
import { assertExpectedChain } from "./deploy-mainnet";

async function main() {
  const raw = process.env.VESTING_ADDRESS;
  if (!raw) throw new Error("VESTING_ADDRESS is required");
  const address = ethers.getAddress(raw);
  const net = await ethers.provider.getNetwork();
  assertExpectedChain(network.name, net.chainId);
  if ((await ethers.provider.getCode(address)) === "0x") {
    throw new Error(`VESTING_ADDRESS ${address} has no code on chain ${net.chainId}`);
  }

  const vault = await ethers.getContractAt("FounderVestingVault", address);
  const tokenAddress = await vault.token();
  const token = await ethers.getContractAt("WrappedMatrix", tokenAddress);
  const latest = await ethers.provider.getBlock("latest");
  if (!latest) throw new Error("could not read latest block");

  const [beneficiary, start, cliff, duration, allocation, released, releasable, balance] =
    await Promise.all([
      vault.beneficiary(),
      vault.start(),
      vault.cliffDuration(),
      vault.duration(),
      vault.totalAllocation(),
      vault.released(),
      vault.releasable(),
      token.balanceOf(address),
    ]);

  const vested = await vault.vestedAmount(latest.timestamp);
  console.log(JSON.stringify({
    network: network.name,
    chainId: net.chainId.toString(),
    vault: address,
    token: tokenAddress,
    beneficiary,
    now: latest.timestamp,
    start: start.toString(),
    cliffDuration: cliff.toString(),
    duration: duration.toString(),
    totalAllocation: allocation.toString(),
    vaultBalance: balance.toString(),
    vested: vested.toString(),
    released: released.toString(),
    releasable: releasable.toString(),
    fullyFunded: balance + released >= allocation,
  }, null, 2));
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
