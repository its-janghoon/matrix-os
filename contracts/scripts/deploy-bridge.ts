import { ethers, network } from "hardhat";

/** Networks that are real (non-local) and therefore require strict guards. */
const REAL_NETWORKS = new Set(["mainnet", "sepolia"]);

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
/**
 * Resolves the attestor set and enforces the real-network guards.
 *
 * Exported and taking the network name and the available signer addresses as
 * arguments, for the same reason parseMinters is in deploy.ts: the strict branch
 * only runs on a real network, and a hardhat run cannot reach one without an RPC
 * URL, so the guards would otherwise be reachable only by attempting an actual
 * deploy. See test/DeployBridge.test.ts.
 */
export function resolveAttestors(
  networkName: string,
  signerAddresses: string[],
  threshold: number,
  env: { ATTESTORS?: string; ALLOW_SINGLE_ATTESTOR?: string } = process.env
): { attestors: string[]; singleAttestorWarning?: string } {
  const isReal = REAL_NETWORKS.has(networkName);

  let attestors: string[];
  const raw = (env.ATTESTORS ?? "").trim();
  if (raw !== "") {
    attestors = raw.split(",").map((a) => ethers.getAddress(a.trim()));
  } else if (isReal) {
    // NEVER fall back on a real chain. With `accounts: [PRIVATE_KEY]` the only
    // signer is the deployer, so this branch would have registered the DEPLOY
    // KEY as the sole attestor - a key exported into an env var for one
    // afternoon, holding unilateral authority to mint the whole cap. Silently,
    // and under a network name that reads as harmless.
    throw new Error(
      `refusing to deploy to '${networkName}': ATTESTORS is not set. Give the ` +
        `comma-separated attestor addresses the validators' own keys produce (each ` +
        `node prints its address from 'matrix bridge attestor-new'). There is no ` +
        `local fallback on a real network: it would make the deploy key the bridge's ` +
        `minting authority.`
    );
  } else {
    // Local / dry-run fallback: the first n local hardhat accounts.
    attestors = signerAddresses.slice(0, Math.max(threshold, 3));
  }

  if (attestors.length === 0) {
    throw new Error("attestor set is empty");
  }
  const unique = new Set(attestors.map((a) => a.toLowerCase()));
  if (unique.size !== attestors.length) {
    // The contract counts DISTINCT signers, so a duplicate in the registered set
    // silently lowers the real threshold: a 2-of-3 whose set is [A, A, B] is
    // reachable by A and B, or by A alone if the same key answers twice.
    throw new Error("ATTESTORS contains duplicate addresses");
  }
  if (threshold < 1 || threshold > attestors.length) {
    throw new Error(`THRESHOLD must be between 1 and ${attestors.length}`);
  }

  let singleAttestorWarning: string | undefined;
  if (isReal && attestors.length === 1) {
    // A 1-of-1 bridge on a real chain is one key away from unbacked wMATRIX,
    // which is the whole thing an m-of-n set exists to prevent. Allowed on a
    // local chain, where a rehearsal wants exactly one signer and nothing is at
    // stake. Refused here, with an override, because a THROWAWAY testnet
    // rehearsal is a legitimate reason to want one and being unable to say so
    // would just push the operator into editing the script.
    if (env.ALLOW_SINGLE_ATTESTOR !== "1") {
      throw new Error(
        `refusing to deploy to '${networkName}' with a single attestor: that one key ` +
          `can mint the entire cap on its own. Give at least two attestors and a ` +
          `THRESHOLD of 2 or more. If this is deliberately a throwaway rehearsal, set ` +
          `ALLOW_SINGLE_ATTESTOR=1 and treat the deployment as disposable.`
      );
    }
    singleAttestorWarning =
      `WARNING: deploying to '${networkName}' with ONE attestor, because ` +
      `ALLOW_SINGLE_ATTESTOR=1. That key alone can mint the whole cap. This ` +
      `deployment is a rehearsal and must never be treated as production.`;
  }
  return { attestors, singleAttestorWarning };
}

async function main() {
  const signers = await ethers.getSigners();
  const deployer = signers[0];

  const threshold = Number(process.env.THRESHOLD ?? "2");

  const { attestors, singleAttestorWarning } = resolveAttestors(
    network.name,
    signers.map((sg) => sg.address),
    threshold
  );
  if (singleAttestorWarning) console.warn(singleAttestorWarning);

  console.log("Deploying WrappedMatrix with account:", deployer.address);
  console.log("  attestors:", attestors);
  console.log("  threshold:", threshold);

  // MINT_CAP is an optional deploy-time policy cap in 18-decimal base units;
  // unset (or 0) selects the contract's documented DEFAULT_MINT_CAP.
  const rawCap = process.env.MINT_CAP;
  const mintCap = rawCap && rawCap.trim() !== "" ? BigInt(rawCap.trim()) : 0n;
  console.log("  mintCap (0=default):", mintCap.toString());

  const factory = await ethers.getContractFactory("WrappedMatrix");
  const wmatrix = await factory.deploy(attestors, threshold, mintCap);
  await wmatrix.waitForDeployment();

  const address = await wmatrix.getAddress();
  console.log("WrappedMatrix deployed to:", address);
  console.log("  name:              ", await wmatrix.name());
  console.log("  symbol:            ", await wmatrix.symbol());
  console.log("  decimals:          ", await wmatrix.decimals());
  console.log("  attestorCount:     ", (await wmatrix.attestorCount()).toString());
  console.log("  threshold:         ", (await wmatrix.threshold()).toString());
  console.log("  mintCap:           ", (await wmatrix.mintCap()).toString());
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
