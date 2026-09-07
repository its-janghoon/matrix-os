import { ethers, network } from "hardhat";

/**
 * Deploys MatrixToken to the configured network and logs the deployed address
 * and initial supply.
 *
 * The deployer signer is provided by Hardhat. On a real network you supply a
 * key via the PRIVATE_KEY env var (wired in hardhat.config.ts); against the
 * local node / in-process network Hardhat's built-in funded accounts are used.
 * NEVER hardcode a real private key here.
 *
 * MINTING IS m-of-n AND TIMELOCKED, so this script needs a minter set and a
 * threshold rather than leaving the deployer in charge. It used to deploy a
 * token whose `onlyOwner` mint was held by whatever key happened to run it -
 * one key able to issue up to the 1e27 cap in a single transaction.
 *
 * On a REAL network the guards below are hard: a minter set must be given
 * explicitly, and a threshold of 1 (or a set of 1) is refused. A local chain
 * gets a convenient default so `npx hardhat run scripts/deploy.ts` still works
 * for development, and it says out loud that the configuration it used is not
 * one to ship.
 */

/** Networks that are real (non-local) and therefore require strict guards. */
const REAL_NETWORKS = new Set(["mainnet", "sepolia"]);

/**
 * Local development minter keys. Deterministic, published here, and worthless:
 * they exist so a dev chain has a working 2-of-3 without an operator inventing
 * keys. A real network never reaches this branch.
 */
const LOCAL_MINTER_KEYS = [
  "0xa111111111111111111111111111111111111111111111111111111111111111",
  "0xa222222222222222222222222222222222222222222222222222222222222222",
  "0xa333333333333333333333333333333333333333333333333333333333333333",
];

/**
 * Resolves the minter set and threshold from env vars.
 *
 * Exported and taking the network name as an argument so the guards are unit
 * tested rather than only reachable by attempting a real deploy - which needs an
 * RPC URL, so the strict branch would otherwise never be exercised. See
 * test/DeployMatrixToken.test.ts.
 */
export function parseMinters(
  networkName: string,
  env: { MINTERS?: string; THRESHOLD?: string } = process.env
): { minters: string[]; threshold: number; local: boolean } {
  const isReal = REAL_NETWORKS.has(networkName);
  const raw = (env.MINTERS ?? "").trim();
  const thresholdRaw = (env.THRESHOLD ?? "").trim();

  if (raw === "") {
    if (isReal) {
      throw new Error(
        `MINTERS is required on network "${networkName}". Minting is m-of-n: give a ` +
          `comma-separated list of minter addresses and a THRESHOLD, e.g. ` +
          `MINTERS=0xA,0xB,0xC THRESHOLD=2. There is no single-owner mode to fall back to.`
      );
    }
    return {
      minters: LOCAL_MINTER_KEYS.map((k) => ethers.computeAddress(new ethers.SigningKey(k).publicKey)),
      threshold: 2,
      local: true,
    };
  }

  const minters = raw.split(",").map((a) => ethers.getAddress(a.trim()));
  if (new Set(minters.map((a) => a.toLowerCase())).size !== minters.length) {
    throw new Error("MINTERS contains a duplicate address; duplicates would fake distinctness");
  }
  const threshold = thresholdRaw === "" ? 0 : Number(thresholdRaw);
  if (!Number.isInteger(threshold) || threshold < 1 || threshold > minters.length) {
    throw new Error(
      `THRESHOLD must be an integer in 1..${minters.length} (got ${thresholdRaw || "unset"})`
    );
  }
  if (isReal && (threshold < 2 || minters.length < 2)) {
    throw new Error(
      `refusing to deploy on "${networkName}" with a threshold of ${threshold} over ` +
        `${minters.length} minter(s): that is a single key able to mint up to the supply ` +
        `cap, which is the configuration this contract exists to prevent. Use at least 2-of-3.`
    );
  }
  return { minters, threshold, local: false };
}

async function main() {
  // Initial supply minted at genesis: 100,000,000 MATRIX (18 decimals).
  const initialSupply = ethers.parseUnits("100000000", 18);

  const [deployer] = await ethers.getSigners();
  const { minters, threshold, local } = parseMinters(network.name);

  // Who holds the initial float. It is a parameter on the contract precisely so
  // it need not be the deploying key; INITIAL_HOLDER overrides it.
  const initialHolder = ethers.getAddress((process.env.INITIAL_HOLDER ?? deployer.address).trim());

  console.log("Deploying MatrixToken with account:", deployer.address);
  console.log("  network:       ", network.name);
  console.log("  minters:       ", minters.join(", "));
  console.log("  threshold:     ", `${threshold}-of-${minters.length}`);
  console.log("  initialHolder: ", initialHolder);
  if (local) {
    console.log(
      "  NOTE: using the published local development minter keys. They are not secrets " +
        "and this configuration must never be used on a real network."
    );
  }

  const MatrixToken = await ethers.getContractFactory("MatrixToken");
  const token = await MatrixToken.deploy(initialSupply, initialHolder, minters, threshold);
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
  console.log("  mintDelay:  ", `${await token.MINT_DELAY()}s`);
  console.log("  mintWindow: ", `${await token.MINT_WINDOW()}s`);
  console.log(
    "  initial holder balance:",
    ethers.formatUnits(await token.balanceOf(initialHolder), decimals),
    symbol
  );
}

// Only run when invoked as a script, not when a test imports parseMinters.
if (require.main === module) {
  main().catch((error) => {
    console.error(error);
    process.exitCode = 1;
  });
}
