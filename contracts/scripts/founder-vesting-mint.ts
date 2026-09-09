import { ethers, network } from "hardhat";
import { readFileSync } from "node:fs";
import { mintAttestations, parseAttestations } from "./bridge-mint";
import { assertExpectedChain } from "./deploy-mainnet";
import {
  assertFounderCeremonyMatches,
  loadFounderCeremonyInput,
} from "./founder-ceremony";

/**
 * Founder-only mint entry point. It binds the attested mint to the reviewed,
 * frozen ceremony input and the deployed vault's immutable getters before the
 * generic bridge mint workflow is allowed to simulate or broadcast.
 */
async function main() {
  const ceremonyPath = process.env.FOUNDER_CEREMONY;
  if (!ceremonyPath) {
    throw new Error("FOUNDER_CEREMONY is required: path to the reviewed frozen ceremony JSON");
  }
  const vaultRaw = process.env.VESTING_ADDRESS;
  if (!vaultRaw) throw new Error("VESTING_ADDRESS is required");
  const contractRaw = process.env.CONTRACT;
  if (!contractRaw) throw new Error("CONTRACT is required: the WrappedMatrix address");
  const attestationsPath = process.env.ATTESTATIONS;
  if (!attestationsPath) throw new Error("ATTESTATIONS is required");

  const frozen = loadFounderCeremonyInput(ceremonyPath);
  const vaultAddress = ethers.getAddress(vaultRaw);
  const contractAddress = ethers.getAddress(contractRaw);
  const attested = parseAttestations(readFileSync(attestationsPath, "utf8"));
  const net = await ethers.provider.getNetwork();
  assertExpectedChain(network.name, net.chainId);

  if ((await ethers.provider.getCode(vaultAddress)) === "0x") {
    throw new Error(`VESTING_ADDRESS ${vaultAddress} has no contract code on chain ${net.chainId}`);
  }
  const vault = await ethers.getContractAt("FounderVestingVault", vaultAddress);
  const [token, beneficiary, start, cliffDuration, duration, allocation, released] =
    await Promise.all([
      vault.token(),
      vault.beneficiary(),
      vault.start(),
      vault.cliffDuration(),
      vault.duration(),
      vault.totalAllocation(),
      vault.released(),
    ]);

  assertFounderCeremonyMatches(frozen, {
    network: network.name,
    chainId: net.chainId,
    token,
    beneficiary,
    start: Number(start),
    cliffDuration: Number(cliffDuration),
    duration: Number(duration),
    allocation,
  }, "on-chain vault");

  if (contractAddress !== ethers.getAddress(token)) {
    throw new Error(`CONTRACT ${contractAddress} does not match frozen/on-chain token ${token}`);
  }
  if (attested.recipient !== vaultAddress) {
    throw new Error(
      `founder mint recipient ${attested.recipient} does not match reviewed vault ${vaultAddress}`
    );
  }
  if (attested.amount !== allocation) {
    throw new Error(
      `founder mint amount ${attested.amount} does not match frozen allocation ${allocation}`
    );
  }

  const latest = await ethers.provider.getBlock("latest");
  if (!latest) throw new Error("could not read latest block timestamp");
  if (BigInt(latest.timestamp) >= start + cliffDuration) {
    throw new Error(
      `founder cliff has elapsed at ${start + cliffDuration}; refusing to mint against a stale ceremony`
    );
  }
  if (released !== 0n) {
    throw new Error(`founder vault has already released ${released}; refusing initial allocation mint`);
  }
  const wrapped = await ethers.getContractAt("WrappedMatrix", token);
  const balance = await wrapped.balanceOf(vaultAddress);
  if (balance !== 0n) {
    throw new Error(`founder vault already holds ${balance} wMATRIX base units; expected zero before mint`);
  }

  console.log(`founder ceremony matches frozen input: ${ceremonyPath}`);
  await mintAttestations(attested);
}

if (require.main === module) {
  main().catch((error) => {
    console.error(String(error instanceof Error ? error.message : error));
    process.exitCode = 1;
  });
}
