#!/usr/bin/env python3
"""
gen_update_key.py

Generate an ed25519 keypair for signing rustdesk-update.json metadata.

Usage:
    python scripts/gen_update_key.py --out update_signing_key.pem

The private key is written in PEM PKCS8 format. Use it with:
    RUSTDESK_API_RUSTDESK_UPDATE_KEY_FILE=/data/update_signing_key.pem

The public key (base64 raw) is printed for adding to TRUSTED_UPDATE_KEYS
in the rustdesk client.
"""

import argparse
import base64
import sys
from pathlib import Path

try:
    from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
    from cryptography.hazmat.primitives.serialization import (
        Encoding, PrivateFormat, NoEncryption, PublicFormat,
    )
except ImportError:
    print("Install cryptography: pip install cryptography", file=sys.stderr)
    sys.exit(1)


def main() -> None:
    parser = argparse.ArgumentParser(description="Generate ed25519 update signing key")
    parser.add_argument("--out", required=True, help="Output path for private key PEM")
    args = parser.parse_args()

    priv = Ed25519PrivateKey.generate()
    pub = priv.public_key()

    # Private key PEM (PKCS8, unencrypted) — keep secret, use on build/signing server only
    priv_pem = priv.private_bytes(Encoding.PEM, PrivateFormat.PKCS8, NoEncryption())
    Path(args.out).write_bytes(priv_pem)
    print(f"Private key written to {args.out}")

    # Public key raw bytes -> base64 for TRUSTED_UPDATE_KEYS
    raw_pub = pub.public_bytes(Encoding.Raw, PublicFormat.Raw)
    b64_pub = base64.b64encode(raw_pub).decode()
    print()
    print("PUBLIC_KEY_BASE64 (for TRUSTED_UPDATE_KEYS):")
    print(b64_pub)
    print()
    print("Add to your docker-compose environment:")
    print(f'  - RUSTDESK_API_RUSTDESK_UPDATE_KEY_FILE={args.out}')


if __name__ == "__main__":
    main()
