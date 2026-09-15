#!/usr/bin/env python3
"""Answer "which of these accounts do I actually hold the key for" from a laptop.

WHY IT EXISTS. An account id on this chain is a public key, so a genesis
allocation names an account without saying who can spend it. Finding out meant
opening wallet files by hand and comparing 64 hex characters by eye, which is
the kind of check that gets skipped and then gets assumed - and the assumption
is expensive: a bond funded from an account nobody holds is money that cannot
move, and this project has already caught one genesis draft that returned three
validator bonds to accounts whose keys died with the old store.

IT NEVER TOUCHES A SECRET. It does not decrypt, does not ask for a passphrase,
and prints no key material. Both wallet shapes carry the PUBLIC key in the
clear - the plaintext one as `public_key`, the encrypted keystore as
`public_key` and `account_id` beside the ciphertext - and the public key IS the
account id, so the whole question is answerable from the parts that are not
secret. A tool that asked for a passphrase to tell you your own address would be
teaching the reflex that a phishing page relies on.

usage:
  scripts/wallet-sweep.py [DIR ...] [--want ACCOUNT ...] [--want-file FILE]

  DIR          where to look. Defaults to ~/.matrix, ~/Downloads and the
               current directory. Searched one level deep, then recursively
               under any .matrix it finds.
  --want       an account you are looking for. Repeatable. Matches are marked
               and a summary says which wanted accounts were NOT found.
  --want-file  a file of accounts, one per line, # for comments.
"""

import argparse
import json
import os
import sys
from pathlib import Path


def account_of(path):
    """The account id a wallet file is for, and which shape it is.

    Returns (account_id, shape) or (None, reason). Nothing is decrypted: both
    shapes state the public key outright.
    """
    try:
        raw = path.read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError) as err:
        return None, f"unreadable ({err.__class__.__name__})"
    try:
        doc = json.loads(raw)
    except json.JSONDecodeError:
        return None, "not json"
    if not isinstance(doc, dict):
        return None, "not a wallet"

    pub = str(doc.get("public_key") or doc.get("account_id") or "").strip().lower()
    if len(pub) != 64 or any(c not in "0123456789abcdef" for c in pub):
        return None, "no account id"

    if "ciphertext" in doc:
        shape = "keystore"
        if not doc.get("mnemonic_backed_up", False):
            shape = "keystore (NO recovery phrase)"
    elif "private_key" in doc:
        shape = "plaintext"
    else:
        shape = "public only"
    return pub, shape


def candidates(dirs):
    """Wallet-shaped files under the given directories, without descending into
    the whole disk: one level of each directory, and all of any .matrix."""
    seen = set()
    for d in dirs:
        d = Path(d).expanduser()
        if not d.is_dir():
            continue
        paths = d.rglob("*.json") if d.name == ".matrix" else d.glob("*.json")
        for p in paths:
            rp = p.resolve()
            if rp not in seen:
                seen.add(rp)
                yield p


def main():
    ap = argparse.ArgumentParser(add_help=False)
    ap.add_argument("dirs", nargs="*")
    ap.add_argument("--want", action="append", default=[])
    ap.add_argument("--want-file")
    ap.add_argument("-h", "--help", action="store_true")
    args = ap.parse_args()

    if args.help:
        print(__doc__)
        return 0

    home = Path.home()
    dirs = args.dirs or [home / ".matrix", home / "Downloads", Path.cwd()]

    wanted = {w.strip().lower() for w in args.want if w.strip()}
    if args.want_file:
        for line in Path(args.want_file).read_text(encoding="utf-8").splitlines():
            line = line.split("#", 1)[0].strip().lower()
            if line:
                wanted.add(line)

    held, skipped = {}, []
    for path in candidates(dirs):
        acct, shape = account_of(path)
        if acct is None:
            skipped.append((path, shape))
            continue
        held.setdefault(acct, []).append((path, shape))

    if not held:
        print("No wallet found in: " + ", ".join(str(Path(d).expanduser()) for d in dirs))
        print("Pass the directories to search, or run `matrix wallet show` where one lives.")
        return 1

    print("accounts this machine holds a key for")
    print()
    for acct, files in sorted(held.items()):
        mark = "  <== WANTED" if acct in wanted else ""
        print(f"  {acct}{mark}")
        for path, shape in files:
            print(f"      {shape:<30} {path}")
    print()

    if wanted:
        missing = sorted(wanted - set(held))
        if missing:
            print("wanted, and NOT on this machine:")
            for acct in missing:
                print(f"  {acct}")
            print()
            print("The key is somewhere else, or it does not exist. An account whose key")
            print("nobody holds can receive and can never spend, so treat a balance there")
            print("as gone until a wallet for it turns up.")
        else:
            print("every wanted account is held here.")
        print()

    if skipped:
        print(f"({len(skipped)} file(s) looked at and were not wallets)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
