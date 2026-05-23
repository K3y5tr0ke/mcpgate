#!/usr/bin/env bash
# generate-certs.sh — generate dev TLS and SAML SP key pairs using openssl
# Output: certs/ directory (gitignored)
# NOT for production use.

set -euo pipefail

CERTS_DIR="$(dirname "$0")/../certs"
mkdir -p "$CERTS_DIR"
cd "$CERTS_DIR"

echo "[certs] Generating dev TLS certificate (self-signed, 825-day validity)..."
openssl req -x509 -newkey rsa:2048 -nodes \
    -keyout tls.key \
    -out tls.crt \
    -days 825 \
    -subj "/CN=localhost/O=mcpgate-dev" \
    -addext "subjectAltName=DNS:localhost,IP:127.0.0.1" \
    2>/dev/null
echo "[certs]   tls.key  (private key)"
echo "[certs]   tls.crt  (certificate)"

echo "[certs] Generating SAML SP key pair..."
openssl req -x509 -newkey rsa:2048 -nodes \
    -keyout saml.key \
    -out saml.crt \
    -days 3650 \
    -subj "/CN=mcpgate-saml-sp/O=mcpgate-dev" \
    2>/dev/null
echo "[certs]   saml.key  (SP private key)"
echo "[certs]   saml.crt  (SP certificate — share this with your IdP)"

echo "[certs] Generating JWT RS256 signing key..."
openssl genrsa -out jwt.key 2048 2>/dev/null
openssl rsa -in jwt.key -pubout -out jwt.pub 2>/dev/null
echo "[certs]   jwt.key  (private signing key — keep secret)"
echo "[certs]   jwt.pub  (public key — safe to share for JWKS endpoint)"

chmod 600 tls.key saml.key jwt.key

echo "[certs] Done. Files written to certs/"
