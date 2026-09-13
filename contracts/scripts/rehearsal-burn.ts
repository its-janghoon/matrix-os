// Step 11 of the rehearsal - burn wMATRIX naming a native recipient - scripted
// so the local rehearsal can run end to end.
//
// The runbook says to do this from MetaMask or Etherscan's Write Contract tab,
// which is right on a real chain, where the holder is a wallet you drive by
// hand. Against a local node there is no wallet UI, so this does the same call
// with a local signer.
//
// It is ALSO the reproduction for a fund-loss bug found by running it: the
// contract's `burn(uint256, string)` does not validate `nativeRecipient`. Pass
// a native account id with an `0x` prefix - the natural thing for anyone used
// to Ethereum - and the tokens burn, the escrow is never released, and the
// node's watcher logs the rejection once and never retries. Run it with
// NATIVE_RECIPIENT set to a 0x-prefixed id to see that; run it with a bare
// 64-hex id to see the path that works.
//
//   CONTRACT=0x... HOLDER=0x... AMOUNT=100000000000000000000 \
//     NATIVE_RECIPIENT=<64 hex, no 0x> \
//     npx hardhat run scripts/rehearsal-burn.ts --network localhost
import { ethers } from "hardhat";

function required(name: string): string {
  const value = process.env[name];
  if (!value) throw new Error(`${name} is required`);
  return value;
}

async function main() {
  const contract = required("CONTRACT");
  const nativeRecipient = required("NATIVE_RECIPIENT");
  const amount = BigInt(required("AMOUNT"));
  const holder = await ethers.getSigner(required("HOLDER"));

  // Said out loud rather than silently corrected. The contract accepts either,
  // and only one of them can ever be released.
  if (nativeRecipient.startsWith("0x")) {
    console.warn(
      "WARNING: nativeRecipient carries an 0x prefix. A native account id is bare hex.\n" +
        "         The contract does not check this. The burn will succeed, the tokens will be\n" +
        "         destroyed, and the escrow will never be released."
    );
  }

  const wrapped = await ethers.getContractAt("WrappedMatrix", contract, holder);
  console.log(`burning ${amount} from ${holder.address} to native ${nativeRecipient}`);
  const receipt = await (await wrapped.burn(amount, nativeRecipient)).wait();
  console.log(`burned in block ${receipt!.blockNumber}, gas ${receipt!.gasUsed}`);
  console.log(`totalSupply now ${await wrapped.totalSupply()}`);
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});
