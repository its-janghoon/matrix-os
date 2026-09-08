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

// parseInitialSupply decides how many tokens the constructor mints, and to whom.
//
// WHAT THIS CHANGED, and why. It used to be a hardcoded 100,000,000 minted to
// the deploying key. MatrixToken is ERC20("Matrix Compute Token", "MATRIX") and
// is backed by NOTHING - it is the standalone mirror, not the bridge token -
// while the project describes its asset as 1:1 backed by native MATRIX locked in
// L1 escrow. Shipping a script whose default hands the deployer a hundred
// million unbacked tokens under that name is a thing that ends badly whatever
// the intent was, and nothing in the repo asked for it.
//
// The default is now ZERO. A real supply has to be typed, by someone who meant
// it, into INITIAL_SUPPLY. Zero is also the honest genesis for a mirror: the
// only supply that should exist is the supply that was locked for it.
export function parseInitialSupply(
  networkName: string,
  env: { INITIAL_SUPPLY?: string } = process.env
): { supply: bigint; explicit: boolean } {
  const isReal = REAL_NETWORKS.has(networkName);
  const raw = (env.INITIAL_SUPPLY ?? "").trim();

  if (raw === "") {
    return { supply: 0n, explicit: false };
  }
  if (!/^\d+(\.\d+)?$/.test(raw)) {
    throw new Error(`INITIAL_SUPPLY must be a non-negative decimal number of whole MATRIX (got "${raw}")`);
  }
  const supply = ethers.parseUnits(raw, 18);
  if (isReal && supply > 0n) {
    // Not refused - an operator may have a reason - but it must be a decision
    // taken in the open rather than a default nobody looked at.
    console.warn(
      `  WARNING: minting ${raw} unbacked MATRIX on "${networkName}". MatrixToken is the ` +
        `standalone mirror and is NOT backed by locked native MATRIX; only WrappedMatrix is. ` +
        `Anything minted here exists against nothing.`
    );
  }
  return { supply, explicit: true };
}

async function main() {
  const { supply: initialSupply, explicit } = parseInitialSupply(network.name);

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
  console.log(
    "  initialSupply: ",
    explicit
      ? `${ethers.formatUnits(initialSupply, 18)} MATRIX (from INITIAL_SUPPLY)`
      : "0 (default; set INITIAL_SUPPLY to mint an unbacked initial float)"
  );
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
