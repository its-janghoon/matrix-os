/**
 * The canonical bytes the Matrix chain verifies, for the two signatures a
 * self-custody client produces.
 *
 * THIS FILE USED TO BE A COPY. Its own header said so: "WHY THIS IS DUPLICATED
 * ... three copies of a byte layout is exactly how a signature quietly stops
 * verifying." It is now a re-export of the one implementation in
 * `@matrix-os/protocol`, so there is nothing here to drift.
 *
 * The blocker was real, not laziness: a plain relative import across the app
 * root type-checks and then fails the build, because Turbopack refuses to
 * resolve a module outside its inferred root. What fixes it is a package
 * boundary plus two lines of next.config.js (`transpilePackages` and
 * `turbopack.root`), which is what this now uses.
 *
 * The base64 helpers stay here. They are not a signed layout - they are how
 * proto JSON carries a `bytes` field - and they use `btoa`/`atob`, which is a
 * browser assumption the shared package deliberately does not make.
 */

export {
  RUN_AUTH_DOMAIN,
  messagesDigest,
  paymentSigningBytes,
  runAuthorizationSigningBytes,
} from '@matrix-os/protocol';

// Role and Message come from signer.ts, so there is one definition of what a
// chat turn is in this app rather than two that can drift in their spelling of
// a role - and the role spelling is inside the signed digest. The shared
// package declares structurally identical types; signer.ts stays the app's
// name for them.
export type { Message, Role } from './signer';

/** Standard base64, which is how proto JSON carries a `bytes` field. */
export function toBase64(bytes: Uint8Array): string {
  let binary = '';
  for (const b of bytes) binary += String.fromCharCode(b);
  return btoa(binary);
}

/** Decodes standard base64. */
export function fromBase64(value: string): Uint8Array {
  const binary = atob(value);
  const out = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) out[i] = binary.charCodeAt(i);
  return out;
}
