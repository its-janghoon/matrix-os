/**
 * The EIP-712 typed data a MetaMask user signs, in the exact shape the node
 * verifies.
 *
 * THIS FILE USED TO BE THE IMPLEMENTATION - the last of the four canonical
 * signed payloads still living in this app after the two ed25519 layouts moved
 * to `@matrix-os/protocol`. The blocker was the domain salt: it is a keccak256
 * value, the shared package takes no dependency, and precomputing the hash
 * here was the workaround. The package has its own keccak now, so this is a
 * re-export and there is nothing here to drift.
 *
 * The test beside this file stays, and stays pinned to ethers-generated
 * constants: only agreeing with a real Ethereum library proves a wallet will
 * produce a signature the node accepts, and that check now guards the shared
 * package's computed salt through this app's own import of it.
 */

export {
  DOMAIN_SALT,
  EIP712_DOMAIN,
  RUN_AUTHORIZATION_TYPES,
  TRANSFER_TYPES,
  bytes32,
  fromHex,
  toHex,
} from '@matrix-os/protocol';
