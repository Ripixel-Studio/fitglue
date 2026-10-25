#!/usr/bin/env bash
# Install the pinned Caddy static binary to ~/.local/bin/caddy (no sudo, not in the repo).
# Verifies the tarball against the SHA-512 in the official release checksums file.
set -euo pipefail

CADDY_VERSION="${CADDY_VERSION:-2.11.7}"
BASE="https://github.com/caddyserver/caddy/releases/download/v${CADDY_VERSION}"
TARBALL="caddy_${CADDY_VERSION}_linux_amd64.tar.gz"
DEST="${HOME}/.local/bin/caddy"

if [ -x "$DEST" ] && "$DEST" version | grep -q "^v${CADDY_VERSION} "; then
	echo "caddy v${CADDY_VERSION} already installed at ${DEST}"
	exit 0
fi

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"
curl -fsSLo "$TARBALL" "${BASE}/${TARBALL}"
curl -fsSLo checksums.txt "${BASE}/caddy_${CADDY_VERSION}_checksums.txt"
grep " ${TARBALL}\$" checksums.txt | sha512sum -c -
mkdir -p ex && tar -xzf "$TARBALL" -C ex caddy
install -D -m 0755 ex/caddy "$DEST"
"$DEST" version
