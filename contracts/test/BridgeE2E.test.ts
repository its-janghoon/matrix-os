import { expect } from "chai";
import { ethers } from "hardhat";
import { execFileSync } from "node:child_process";
import { existsSync } from "node:fs";
import { resolve } from "node:path";
import type { WrappedMatrix } from "../typechain-types";
import type { HardhatEthersSigner } from "@nomicfoundation/hardhat-ethers/signers";

const ERC20_PER_NATIVE_UNIT = 10n ** 9n;

// Path to the Go module that produces real, on-chain-verifiable attestations.
const GO_CORE_DIR = resolve(__dirname, "../../services/core");

interface GoAttestation {
  attestors: string[];
  threshold: number;
  recipient: string;
  lockId: string;
  nativeAmount: number;
  wrappedAmount: string;
  signatures: string[];
  outstandingNative: number;
  outstandingWrapped: string;
}

// runGoBridge invokes the Go `bridge-attest` command, which locks native MATRIX,
// produces a validator-signed attestation over the canonical digest, and prints
// it as JSON. This is the REAL Go side of the bridge; its output must verify in
// the Solidity contract unchanged, which is what this test asserts.
function runGoBridge(args: Record<string, string>): GoAttestation {
  const argv = ["run", "./cmd/bridge-attest"];
  for (const [k, v] of Object.entries(args)) {
    argv.push(`-${k}`, v);
  }
  const out = execFileSync("go", argv, {
    cwd: GO_CORE_DIR,
    encoding: "utf8",
    env: { ...process.env },
  });
  return JSON.parse(out) as GoAttestation;
}

// goAvailable reports whether the Go toolchain and module are present so the
// end-to-end test can run. When absent (e.g. a contracts-only checkout) the test
// is skipped rather than failing.
function goAvailable(): boolean {
  if (!existsSync(GO_CORE_DIR)) return false;
  try {
    execFileSync("go", ["version"], { stdio: "ignore" });
    return true;
  } catch {
    return false;
  }
}

describe("Bridge end-to-end (Go attestation -> Solidity mint)", function () {
  // The Go `go run` compile+run can take a while on a cold cache.
  this.timeout(120_000);

  let wmatrix: WrappedMatrix;
  let recipient: HardhatEthersSigner;

  const THRESHOLD = 2;
  const VALIDATORS = 3;
  const SEED = "matrix-e2e-test";
  const NATIVE_AMOUNT = 4_000_000_000; // 4 native base units (4 * 1e9)

  before(function () {
    if (!goAvailable()) {
      // eslint-disable-next-line no-console
      console.warn("skipping bridge e2e: Go toolchain or services/core not available");
      this.skip();
    }
  });

  beforeEach(async () => {
    [, recipient] = await ethers.getSigners();
    const chainId = (await ethers.provider.getNetwork()).chainId;

    // First derive the attestor addresses from Go using a placeholder contract
    // (only the attestor addresses depend on the seed, not the contract), so we
    // can deploy WrappedMatrix registering exactly those addresses.
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
    wmatrix = (await factory.deploy(bootstrap.attestors, THRESHOLD, 0)) as unknown as WrappedMatrix;
    await wmatrix.waitForDeployment();
  });

  it("mints wrapped tokens from a Go-produced attestation, then burns to unlock", async () => {
    const chainId = (await ethers.provider.getNetwork()).chainId;
    const contract = await wmatrix.getAddress();

    // Produce the real attestation now bound to the DEPLOYED contract address.
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
    expect(wrappedAmount).to.equal(BigInt(NATIVE_AMOUNT) * ERC20_PER_NATIVE_UNIT);
    expect(att.signatures.length).to.equal(THRESHOLD);

    // The Go signatures verify on-chain unchanged.
    await expect(wmatrix.mint(att.recipient, wrappedAmount, att.lockId, att.signatures))
      .to.emit(wmatrix, "Minted")
      .withArgs(att.lockId, recipient.address, wrappedAmount);

    // 1:1 backing: wrapped supply == locked native converted, and matches the
    // Go side's own reconciliation of outstanding wrapped supply.
    expect(await wmatrix.totalSupply()).to.equal(wrappedAmount);
    expect(await wmatrix.totalSupply()).to.equal(BigInt(att.outstandingWrapped));

    // Burn part of it to authorize a native unlock; the event carries the L1
    // recipient the Go side would release escrow to.
    const burnAmount = 2n * ERC20_PER_NATIVE_UNIT;
    const nativeRecipient = "cafe".repeat(16); // an L1 account id
    const burnTx = await wmatrix.connect(recipient).burn(burnAmount, nativeRecipient);
    await expect(burnTx)
      .to.emit(wmatrix, "Burned")
      .withArgs(recipient.address, nativeRecipient, burnAmount);

    expect(await wmatrix.totalSupply()).to.equal(wrappedAmount - burnAmount);

    // Cross-check the RAW emitted log against the layout the Go burn-log decoder
    // (internal/bridge.DecodeBurnedLog) parses, so the on-chain event bytes and
    // the Go parser are proven byte-compatible. This is the payload an operator
    // (or a future log subscription) hands to ProcessBurn to unlock native.
    const rcpt = await burnTx.wait();
    const burnedTopic = ethers.id("Burned(address,string,uint256)");
    const log = rcpt!.logs.find((l) => l.topics[0] === burnedTopic);
    expect(log, "a Burned log must be emitted").to.not.equal(undefined);

    // topics[0] is keccak256 of the exact signature the Go decoder keys on.
    expect(log!.topics[0]).to.equal(burnedTopic);
    // topics[1] is the indexed burner address, left-padded to 32 bytes.
    expect(log!.topics[1]).to.equal(ethers.zeroPadValue(recipient.address, 32));
    // data is abi.encode(string nativeRecipient, uint256 amount) in the head/tail
    // layout the Go decoder inverts: offset word (0x40), amount, length, bytes.
    const [decodedRecipient, decodedAmount] = ethers.AbiCoder.defaultAbiCoder().decode(
      ["string", "uint256"],
      log!.data
    );
    expect(decodedRecipient).to.equal(nativeRecipient);
    expect(decodedAmount).to.equal(burnAmount);
    // The first data word is the string offset 0x40 the Go decoder expects.
    expect(log!.data.slice(0, 66)).to.equal("0x" + "40".padStart(64, "0"));
  });

  it("rejects a replay of the same Go attestation (same lockId)", async () => {
    const chainId = (await ethers.provider.getNetwork()).chainId;
    const contract = await wmatrix.getAddress();
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
    await wmatrix.mint(att.recipient, wrappedAmount, att.lockId, att.signatures);
    await expect(
      wmatrix.mint(att.recipient, wrappedAmount, att.lockId, att.signatures)
    ).to.be.revertedWithCustomError(wmatrix, "LockAlreadyMinted");
  });

  // The CONSENSUS lock path, end to end.
  //
  // Everything above drives bridge.Lock: a direct ledger write with a per-node
  // counter, which has no production caller. A real node locks through a
  // committed transaction and derives the id from it (nonce, sender, recipient,
  // amount). That derivation and the attestation it produces had never been fed
  // to WrappedMatrix.mint - the on-ramp was built and never shown to mint.
  //
  // This is that proof. Same contract, same threshold, same verification; only
  // the lock half differs.
  it("mints from an attestation produced by the consensus lock path", async function () {
    if (!goAvailable()) this.skip();

    const chainId = (await ethers.provider.getNetwork()).chainId;
    const contract = await wmatrix.getAddress();
    const att = runGoBridge({
      recipient: recipient.address,
      native: String(NATIVE_AMOUNT),
      "chain-id": String(chainId),
      contract,
      threshold: String(THRESHOLD),
      validators: String(VALIDATORS),
      seed: SEED,
      consensus: "true",
      nonce: "7",
    });

    const wrappedAmount = BigInt(att.wrappedAmount);
    const before = await wmatrix.balanceOf(recipient.address);

    await expect(wmatrix.mint(att.recipient, wrappedAmount, att.lockId, att.signatures))
      .to.emit(wmatrix, "Minted");

    expect(await wmatrix.balanceOf(recipient.address)).to.equal(before + wrappedAmount);
    // Backing holds: wrapped supply tracks what the Go side says is escrowed.
    expect(BigInt(att.outstandingWrapped)).to.equal(wrappedAmount);

    // And the lock id is consumed, so a committed lock cannot mint twice even
    // though its id now comes from a transaction rather than a counter.
    await expect(
      wmatrix.mint(att.recipient, wrappedAmount, att.lockId, att.signatures)
    ).to.be.reverted;
  });
});
