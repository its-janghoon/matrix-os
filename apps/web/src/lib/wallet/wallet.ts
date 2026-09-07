/**
 * A browser wallet for the Matrix chain.
 *
 * The chain's accounts are plain ed25519 keypairs and the account id IS the
 * public key in hex, so a browser can hold one without any extension, bridge or
 * dependency. WebCrypto has done Ed25519 since 2023.
 *
 * WHAT PROTECTS THE KEY, precisely, because this is the part it would be easy to
 * overclaim: the private key is generated NON-EXTRACTABLE and stored as a
 * `CryptoKey` in IndexedDB. Script on this origin can ask it to sign, but cannot
 * read the key material - `exportKey` refuses, and there is no moment where the
 * bytes exist as reachable JavaScript. That is a genuinely stronger position
 * than the usual "private key in localStorage", which any script on the origin
 * can read and exfiltrate.
 *
 * What it does NOT protect against: script on this origin signing whatever it
 * likes while the page is open. Non-extractability means a key cannot be stolen
 * and used elsewhere; it does not mean it cannot be used here. A hardware
 * signer is the answer to that, and this is not one.
 *
 * Passkeys are deliberately not used. A passkey signs WebAuthn's own challenge
 * structure with its own key, so it cannot produce the ed25519 signature over
 * our canonical bytes that the chain verifies. Claiming a passkey "seals" this
 * key would be describing something that is not happening.
 */

const DB_NAME = 'matrix-wallet';
const DB_VERSION = 1;
const STORE = 'keys';
const KEY_ID = 'account';

/** A wallet: the account id, the public key bytes, and a signer. */
export interface Wallet {
  /** The account id, which is the hex of the public key. */
  accountId: string;
  publicKey: Uint8Array;
  /** Signs a message with the non-extractable private key. */
  sign(message: Uint8Array): Promise<Uint8Array>;
}

interface StoredKey {
  id: string;
  privateKey: CryptoKey;
  publicKeyRaw: ArrayBuffer;
}

/** True when this context can hold a wallet at all. */
export function walletSupported(): boolean {
  return (
    typeof indexedDB !== 'undefined' &&
    typeof crypto !== 'undefined' &&
    typeof crypto.subtle !== 'undefined'
  );
}

function openDb(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(DB_NAME, DB_VERSION);
    req.onupgradeneeded = () => {
      const db = req.result;
      if (!db.objectStoreNames.contains(STORE)) db.createObjectStore(STORE, { keyPath: 'id' });
    };
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error ?? new Error('could not open the wallet database'));
  });
}

function withStore<T>(mode: IDBTransactionMode, fn: (store: IDBObjectStore) => IDBRequest<T>): Promise<T> {
  return openDb().then(
    (db) =>
      new Promise<T>((resolve, reject) => {
        const tx = db.transaction(STORE, mode);
        const req = fn(tx.objectStore(STORE));
        req.onsuccess = () => resolve(req.result);
        req.onerror = () => reject(req.error ?? new Error('wallet storage failed'));
        tx.oncomplete = () => db.close();
      }),
  );
}

function toHex(bytes: Uint8Array): string {
  return Array.from(bytes)
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('');
}

function walletFrom(stored: StoredKey): Wallet {
  const publicKey = new Uint8Array(stored.publicKeyRaw);
  return {
    accountId: toHex(publicKey),
    publicKey,
    async sign(message: Uint8Array): Promise<Uint8Array> {
      const sig = await crypto.subtle.sign('Ed25519', stored.privateKey, message as BufferSource);
      return new Uint8Array(sig);
    },
  };
}

/** Loads the wallet held in this browser, or null when there is none. */
export async function loadWallet(): Promise<Wallet | null> {
  if (!walletSupported()) return null;
  const stored = await withStore<StoredKey | undefined>(
    'readonly',
    (s) => s.get(KEY_ID) as IDBRequest<StoredKey | undefined>,
  );
  if (!stored) return null;
  return walletFrom(stored);
}

/**
 * Creates a wallet, refusing to overwrite one that already exists. Overwriting
 * would silently destroy the key to an account that may hold a balance, and
 * there is no recovery: a non-extractable key cannot have been backed up.
 */
export async function createWallet(): Promise<Wallet> {
  if (!walletSupported()) {
    throw new Error(
      'this browser cannot hold a wallet: it needs IndexedDB and WebCrypto, and WebCrypto ' +
        'requires a secure context (https, or localhost)',
    );
  }
  if (await loadWallet()) {
    throw new Error('a wallet already exists in this browser, and overwriting it would destroy the key');
  }

  // extractable: false is the whole point. See the file comment.
  const pair = (await crypto.subtle.generateKey({ name: 'Ed25519' }, false, ['sign', 'verify'])) as CryptoKeyPair;
  const publicKeyRaw = await crypto.subtle.exportKey('raw', pair.publicKey);

  const stored: StoredKey = { id: KEY_ID, privateKey: pair.privateKey, publicKeyRaw };
  await withStore<IDBValidKey>('readwrite', (s) => s.put(stored));
  return walletFrom(stored);
}

/**
 * Deletes the wallet. Irreversible, for the reason above: the key was never
 * extractable, so it cannot have been written down.
 */
export async function forgetWallet(): Promise<void> {
  if (!walletSupported()) return;
  await withStore<undefined>('readwrite', (s) => s.delete(KEY_ID) as IDBRequest<undefined>);
}
