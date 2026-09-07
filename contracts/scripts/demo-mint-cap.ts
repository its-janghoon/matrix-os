import { ethers } from "hardhat";

// demo-mint-cap.ts deploys WrappedMatrix against the running local hardhat node
// with a deliberately tight mint cap, mints right up to the cap (succeeds), then
// attempts one more mint over the cap and shows it revert with MintCapExceeded.
// It is the "real run against a local node" demonstration for the mint cap.
//
// It is NOT a policy statement: the cap here is a tiny test value chosen so the
// ceiling is reachable in a script; production picks the cap via MINT_CAP.
const ATTESTOR_KEY =
  "0x1111111111111111111111111111111111111111111111111111111111111111";
const ERC20_PER_NATIVE_UNIT = 10n ** 9n;

function signDigest(privKey: string, digest: string): string {
  const sk = new ethers.SigningKey(privKey);
  const sig = sk.sign(digest);
  return ethers.concat([sig.r, sig.s, ethers.toBeHex(sig.v, 1)]);
}

async function main() {
  const [, recipient] = await ethers.getSigners();
  const attestor = ethers.computeAddress(new ethers.SigningKey(ATTESTOR_KEY).publicKey);

  // Tight cap: 5 native units worth of wrapped supply.
  const cap = 5n * ERC20_PER_NATIVE_UNIT;
  const factory = await ethers.getContractFactory("WrappedMatrix");
  const wmatrix = await factory.deploy([attestor], 1, cap);
  await wmatrix.waitForDeployment();
  const contract = await wmatrix.getAddress();
  const chainId = (await ethers.provider.getNetwork()).chainId;
  console.log("Deployed WrappedMatrix at", contract, "with mintCap", (await wmatrix.mintCap()).toString());

  const mint = async (amount: bigint, lockId: string) => {
    const digest = ethers.solidityPackedKeccak256(
      ["address", "uint256", "bytes32", "uint256", "address"],
      [recipient.address, amount, lockId, chainId, contract]
    );
    const sig = signDigest(ATTESTOR_KEY, digest);
    return wmatrix.mint(recipient.address, amount, lockId, [sig]);
  };

  // Mint exactly up to the cap: succeeds.
  await mint(cap, ethers.zeroPadValue("0x01", 32));
  console.log("Minted up to the cap, totalSupply =", (await wmatrix.totalSupply()).toString());

  // One more base-unit-worth over the cap: must revert with MintCapExceeded.
  try {
    await mint(ERC20_PER_NATIVE_UNIT, ethers.zeroPadValue("0x02", 32));
    console.error("FAIL: over-cap mint did NOT revert");
    process.exitCode = 1;
  } catch (err: unknown) {
    const msg = (err as Error).message ?? String(err);
    if (msg.includes("MintCapExceeded")) {
      console.log("OK: over-cap mint reverted with MintCapExceeded");
      console.log("  revert detail:", msg.split("\n")[0]);
    } else {
      console.error("FAIL: reverted with an unexpected error:", msg);
      process.exitCode = 1;
    }
  }
  console.log("Final totalSupply (unchanged by the rejected mint) =", (await wmatrix.totalSupply()).toString());
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
