// Step 11 of the rehearsal - burn wMATRIX naming a native recipient - scripted
// so the local rehearsal can run end to end.
//
// The runbook says to do this from MetaMask or Etherscan's Write Contract tab,
// which is right on a real chain, where the holder is a wallet you drive by
// hand. Against a local node there is no wallet UI, so this does the same call
// with a local signer.
//
// The contract's `burn(uint256, string)` does not validate `nativeRecipient` -
// it cannot, since a native account id means nothing to an EVM - so whatever
// the burner typed is what gets emitted and the tokens are destroyed either
// way. A `0x` prefix used to mean the escrow was never released.
//
// The node now normalizes a recipient that differs only in spelling (a `0x`
// prefix, uppercase hex), so that case releases correctly. The warning below
// stays because the contract still accepts anything: a recipient that is not an
// account id after normalizing is refused, and those tokens are gone.
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
      "NOTE: nativeRecipient carries an 0x prefix. A native account id is bare hex.\n" +
        "      The contract does not check this; the node normalizes it, so the escrow will\n" +
        "      release to the account named after the prefix. Anything that is NOT an account\n" +
        "      id once normalized is refused, and those tokens are destroyed with no recovery."
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
