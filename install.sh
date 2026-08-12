#!/bin/bash
set -euo pipefail

OS=$(uname -s | tr 'A-Z' 'a-z')

ARCH=$(uname -m)
case "$ARCH" in
	x86_64)
		ARCH=amd64
		;;
	aarch64|arm64)
		ARCH=arm64
		;;
	*)
		echo "Unsupported architecture: $ARCH" >&2
		exit 1
		;;
esac

echo "Resolving latest version..."

RELEASE=$(curl --fail --silent --show-error --location https://api.github.com/repos/coalaura/mksvc/releases/latest)
VERSION=$(printf '%s\n' "$RELEASE" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | sed -n '1p')

if ! printf '%s\n' "$VERSION" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
	echo "Error: '$VERSION' is not in vMAJOR.MINOR.PATCH format" >&2
	exit 1
fi

BIN="mksvc_${VERSION}_${OS}_${ARCH}"
URL="https://github.com/coalaura/mksvc/releases/download/${VERSION}/${BIN}"
CHECKSUM_URL="https://github.com/coalaura/mksvc/releases/download/${VERSION}/checksums.txt"
TMP_DIR=$(mktemp -d)

trap 'rm -rf "$TMP_DIR"' EXIT

echo "Downloading ${BIN}..."

if ! curl --fail --silent --show-error --location "$URL" -o "$TMP_DIR/$BIN"; then
	echo "Error: failed to download $URL" >&2
	exit 1
fi

if ! curl --fail --silent --show-error --location "$CHECKSUM_URL" -o "$TMP_DIR/checksums.txt"; then
	echo "Error: failed to download release checksums" >&2
	exit 1
fi

if ! command -v sha256sum >/dev/null 2>&1; then
	echo "Error: sha256sum is required to verify the download" >&2
	exit 1
fi

if ! (cd "$TMP_DIR" && sha256sum --check --ignore-missing checksums.txt && grep -Fq "  $BIN" checksums.txt); then
	echo "Error: checksum verification failed" >&2
	exit 1
fi

chmod +x "$TMP_DIR/$BIN"

echo "Installing to /usr/local/bin/mksvc requires sudo"

if ! sudo install -o root -g root -m 0755 "$TMP_DIR/$BIN" /usr/local/bin/mksvc; then
	echo "Error: install failed" >&2
	exit 1
fi

echo "mksvc $VERSION installed to /usr/local/bin/mksvc"
