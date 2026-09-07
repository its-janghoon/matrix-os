import { ethers } from "hardhat";
import { execFileSync } from "node:child_process";
import { resolve } from "node:path";

/**
 * bridge-burn-emit deploys WrappedMatrix on the connected network (a local
 * hardhat node via --network localhost), mints wMATRIX from a REAL Go-produced
 * attestation (cmd/bridge-attest, the exact mint path the BridgeE2E test uses),
 * then BURNS part of it to emit an on-chain `Burned` event naming a native L1
 * recipient.
 *
 * It prints a single JSON line describing the burn so the orchestration script
 * (scripts/bridge-watch-e2e.sh) can drive the Go watcher (cmd/bridge-watch)
 * against the same node and assert the native unlock. This is the emit half of
 * the burn->unlock loop; the Go watcher is the ingest+unlock half.
 *
 * Run against a local node:
 *   npx hardhat node &
 *   npx hardhat run scripts/bridge-burn-emit.ts --network localhost
 */

const GO_CORE_DIR = resolve(__dirname, "../../services/core");
const ERC20_PER_NATIVE_UNIT = 10n ** 9n;

interface GoAttestation {
  attestors: string[];
  recipient: string;
  lockId: string;
  wrappedAmount: string;
  signatures: string[];
}

function runGoBridge(args: Record<string, string>): GoAttestation {
  const argv = ["run", "./cmd/bridge-attest"];
  for (const [k, v] of Object.entries(args)) argv.push(`-${k}`, v);
  const out = execFileSync("go", argv, { cwd: GO_CORE_DIR, encoding: "utf8", env: { ...process.env } });
  return JSON.parse(out) as GoAttestation;
}

async function main() {
  const THRESHOLD = 2;
  const VALIDATORS = 3;
  const SEED = "matrix-bridge-watch-e2e";
  const NATIVE_AMOUNT = 8_000_000_000; // 8 native base units

  const signers = await ethers.getSigners();
  const recipient = signers[1];
  const chainId = (await ethers.provider.getNetwork()).chainId;

  // Derive attestor addresses (seed-dependent, not contract-dependent) so we can
  // deploy WrappedMatrix registering exactly those attestors.
  const bootstrap = runGoBridge({
    recipient: recipient.address,
    native: String(NATIVE_AMOUNT),
    "chain-id": String(chainId),
    contract: "0x0000000000000000000000000000000000000000",
    threshold: String(THRESHOLD),
    validators: String(VALIDATORS),
    seed: SEED,
  });

  const factory = await ethers.getContractFactory("WrappedMatrix");
  // 0 selects the contract's documented DEFAULT_MINT_CAP; this local demo does
  // not exercise the cap so it uses the default ceiling.
  const wmatrix = await factory.deploy(bootstrap.attestors, THRESHOLD, 0);
  await wmatrix.waitForDeployment();
  const contract = await wmatrix.getAddress();

  // Real attestation bound to the deployed contract, then mint.
  const att = runGoBridge({
    recipient: recipient.address,
    native: String(NATIVE_AMOUNT),
    "chain-id": String(chainId),
    contract,
    threshold: String(THRESHOLD),
    validators: String(VALIDATORS),
    seed: SEED,
  });
  const wrappedAmount = BigInt(att.wrappedAmount);
  const mintTx = await wmatrix.mint(att.recipient, wrappedAmount, att.lockId, att.signatures);
  await mintTx.wait();

  // Burn part of it, emitting a Burned event that names the native recipient.
  const burnNative = 3n; // native base units to unlock
  const burnAmount = burnNative * ERC20_PER_NATIVE_UNIT;
  const nativeRecipient = "beef".repeat(16); // a 64-hex L1 account id
  const burnTx = await wmatrix.connect(recipient).burn(burnAmount, nativeRecipient);
  const rcpt = await burnTx.wait();

  // Emit machine-readable coordinates for the Go watcher.
  const result = {
    contract,
    chainId: Number(chainId),
    nativeRecipient,
    burnNative: Number(burnNative),
    burnBlock: rcpt!.blockNumber,
    // Seed the watcher's escrow with the full minted native so the unlock has
    // backing to release (mirrors the outstanding escrow a real bridge holds).
    escrowFund: NATIVE_AMOUNT,
  };
  // A single clearly-delimited JSON line the shell can grep out.
  console.log("BRIDGE_BURN_JSON " + JSON.stringify(result));
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
