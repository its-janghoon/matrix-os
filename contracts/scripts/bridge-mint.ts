import { ethers, network } from "hardhat";
import { readFileSync } from "node:fs";

/**
 * Mints wMATRIX from attestations produced by running Matrix OS validators.
 *
 * This is the step between `matrix bridge lock` and wMATRIX existing, and it had
 * no script. Every rehearsal wrote its own, which is how a mint that reverts
 * after the gas is spent happens: the contract's requirements are exacting and
 * none of them are obvious from the call site.
 *
 * WHAT IT REFUSES TO GET WRONG.
 *
 *  - SIGNATURE ORDER. WrappedMatrix requires signatures ordered by ASCENDING
 *    recovered signer address, because that is how it enforces distinct signers
 *    without a mapping. Attestations are gathered from n nodes in whatever order
 *    they answered, so an m-of-n mint submitted in arrival order reverts
 *    InvalidSignature and the caller has no idea why. This sorts them, the same
 *    way bridge.Attestation.SortSignaturesForChain does on the Go side.
 *  - A FOREIGN SIGNER. Every recovered address is checked against the contract's
 *    registered attestor set before anything is sent, so an unregistered
 *    validator is named here rather than counted as "threshold not met" on chain.
 *  - DISAGREEING ATTESTATIONS. All of them must be about the same lock: same
 *    recipient, same amount, same lock id. Two nodes disagreeing means one of
 *    them is on a different chain or a different fork, which is worth stopping
 *    for rather than papering over.
 *  - A REPLAY. mintedLockId is read first: a lock already minted reverts
 *    LockAlreadyMinted, and finding that out locally costs nothing.
 *
 * It then simulates the call before broadcasting, so a revert costs no gas at
 * all. Only after the simulation passes does it send.
 *
 * INPUT is the GetLockAttestation response verbatim, one per validator, as a
 * JSON array in a file:
 *
 *   [{"recipient":"0x..","erc20Amount":"4000000000000000000",
 *     "lockId":"5b5a..","signature":"de76..","attestor":"0x..",
 *     "nativeAmount":"4000000000"}]
 *
 * Usage:
 *   CONTRACT=0x... ATTESTATIONS=./atts.json \
 *     npx hardhat run scripts/bridge-mint.ts --network sepolia
 *
 * The sending key pays gas and NOTHING ELSE: mint is permissionless and the
 * recipient is fixed inside the signed digest, so whoever broadcasts cannot
 * redirect the mint. Any funded key will do.
 */

interface Attestation {
  recipient: string;
  erc20Amount: string;
  lockId: string;
  signature: string;
  attestor: string;
  nativeAmount?: string;
}

function hex0x(s: string): string {
  return s.startsWith("0x") || s.startsWith("0X") ? s : "0x" + s;
}

/**
 * Reads and validates the attestation set. Exported so the guards are unit
 * tested rather than only reachable by attempting a real mint, which needs a
 * funded key and a deployed contract.
 */
export function parseAttestations(raw: string): {
  recipient: string;
  amount: bigint;
  lockId: string;
  signatures: string[];
} {
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch (e) {
    throw new Error(`attestations must be JSON: ${(e as Error).message}`);
  }
  const list = (Array.isArray(parsed) ? parsed : [parsed]) as Attestation[];
  if (list.length === 0) throw new Error("attestation set is empty");

  const first = list[0];
  for (const [i, a] of list.entries()) {
    for (const field of ["recipient", "erc20Amount", "lockId", "signature"] as const) {
      if (!a[field]) throw new Error(`attestation ${i} is missing "${field}"`);
    }
    // Disagreement means the validators are not describing the same lock. That
    // is a fork or a misconfiguration, never something to submit anyway.
    if (ethers.getAddress(hex0x(a.recipient)) !== ethers.getAddress(hex0x(first.recipient))) {
      throw new Error(
        `attestation ${i} names recipient ${a.recipient} but attestation 0 names ` +
          `${first.recipient}: the validators do not agree on this lock`
      );
    }
    if (BigInt(a.erc20Amount) !== BigInt(first.erc20Amount)) {
      throw new Error(
        `attestation ${i} names amount ${a.erc20Amount} but attestation 0 names ` +
          `${first.erc20Amount}: the validators do not agree on this lock`
      );
    }
    if (hex0x(a.lockId).toLowerCase() !== hex0x(first.lockId).toLowerCase()) {
      throw new Error(
        `attestation ${i} names lock ${a.lockId} but attestation 0 names ${first.lockId}`
      );
    }
    if (ethers.dataLength(hex0x(a.signature)) !== 65) {
      throw new Error(
        `attestation ${i} signature is ${ethers.dataLength(hex0x(a.signature))} bytes, want 65`
      );
    }
  }
  const lockId = hex0x(first.lockId).toLowerCase();
  if (ethers.dataLength(lockId) !== 32) {
    throw new Error(`lockId is ${ethers.dataLength(lockId)} bytes, want 32`);
  }
  return {
    recipient: ethers.getAddress(hex0x(first.recipient)),
    amount: BigInt(first.erc20Amount),
    lockId,
    signatures: list.map((a) => hex0x(a.signature)),
  };
}

/**
 * Orders signatures by ascending recovered signer, which is what the contract
 * requires, and reports who signed. Exported for the same reason as above.
 *
 * The digest is recomputed here rather than trusted from the node: recovering
 * against a digest the node supplied would verify nothing, since a node that
 * signed the wrong thing would also report the wrong digest.
 */
export function orderSignatures(
  digest: string,
  signatures: string[]
): { signature: string; signer: string }[] {
  const recovered = signatures.map((signature, i) => {
    let signer: string;
    try {
      signer = ethers.recoverAddress(digest, signature);
    } catch (e) {
      throw new Error(`signature ${i} does not recover: ${(e as Error).message}`);
    }
    return { signature, signer };
  });
  const seen = new Set<string>();
  for (const r of recovered) {
    const key = r.signer.toLowerCase();
    if (seen.has(key)) {
      throw new Error(
        `${r.signer} signed twice. The contract counts distinct signers, so a ` +
          `duplicate is one signature short of the threshold, not one over.`
      );
    }
    seen.add(key);
  }
  return recovered.sort((a, b) =>
    BigInt(a.signer) < BigInt(b.signer) ? -1 : BigInt(a.signer) > BigInt(b.signer) ? 1 : 0
  );
}

async function main() {
  const contractAddr = process.env.CONTRACT;
  if (!contractAddr) throw new Error("CONTRACT is required: the WrappedMatrix address");
  const source = process.env.ATTESTATIONS;
  if (!source) {
    throw new Error(
      "ATTESTATIONS is required: a path to a JSON array of GetLockAttestation responses, " +
        "one per validator"
    );
  }

  const { recipient, amount, lockId, signatures } = parseAttestations(readFileSync(source, "utf8"));
  const w = await ethers.getContractAt("WrappedMatrix", ethers.getAddress(contractAddr));
  const [sender] = await ethers.getSigners();

  console.log(`network:   ${network.name} (chainId ${(await ethers.provider.getNetwork()).chainId})`);
  console.log(`contract:  ${await w.getAddress()}`);
  console.log(`sender:    ${sender.address} (pays gas only)`);
  console.log(`lock:      ${lockId}`);
  console.log(`recipient: ${recipient}`);
  console.log(`amount:    ${amount} (${ethers.formatUnits(amount, 18)} wMATRIX)`);

  if (await w.mintedLockId(lockId)) {
    throw new Error(
      `lock ${lockId} has already been minted. Each lock mints exactly once; ` +
        `a second wMATRIX for it would be unbacked.`
    );
  }

  // The digest binds chain id and contract address, so an attestation for one
  // deployment cannot be replayed against another. Recomputing it locally is
  // also the only way to tell a signature for a DIFFERENT contract apart from a
  // corrupt one: both simply fail to recover to a registered attestor.
  const digest = await w.attestationDigest(recipient, amount, lockId);
  const ordered = orderSignatures(digest, signatures);

  const threshold = Number(await w.threshold());
  for (const { signer } of ordered) {
    const registered = await w.isAttestor(signer);
    console.log(`signer:    ${signer} ${registered ? "(registered)" : "(NOT REGISTERED)"}`);
    if (!registered) {
      throw new Error(
        `${signer} is not in the contract's attestor set. Either that validator's ` +
          `attestor address was never registered at deploy time, or it signed for a ` +
          `different chain id or contract address than this one.`
      );
    }
  }
  if (ordered.length < threshold) {
    throw new Error(
      `${ordered.length} valid signature(s), threshold is ${threshold}. Collect ` +
        `GetLockAttestation from ${threshold - ordered.length} more validator(s).`
    );
  }

  const supply = await w.totalSupply();
  const cap = await w.mintCap();
  if (supply + amount > cap) {
    throw new Error(
      `this mint would take total supply to ${supply + amount}, over the deploy-time ` +
        `cap of ${cap}. The cap is immutable: raising it is a redeploy.`
    );
  }

  const sigs = ordered.map((o) => o.signature);
  // Simulate first. A revert here costs nothing; the same revert after
  // broadcasting costs the whole gas limit.
  await w.mint.staticCall(recipient, amount, lockId, sigs);
  console.log("simulation passed, broadcasting");

  const tx = await w.mint(recipient, amount, lockId, sigs);
  console.log(`tx:        ${tx.hash}`);
  const rc = await tx.wait();
  console.log(`mined:     block ${rc?.blockNumber}, gas ${rc?.gasUsed}`);
  console.log(`supply:    ${await w.totalSupply()}`);
  console.log(`balance:   ${await w.balanceOf(recipient)} for ${recipient}`);
}

// Only run when invoked as a script; the exported guards above are imported by
// tests, which must not trigger a deploy.
if (require.main === module) {
  main().catch((e) => {
    console.error(String(e instanceof Error ? e.message : e));
    process.exitCode = 1;
  });
}
