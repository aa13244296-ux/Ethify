#!/bin/sh
set -eu

if [ "$#" -ne 3 ]; then
    echo "Usage: $0 <client-binary> <version> <output-directory>" >&2
    exit 2
fi

CLIENT_BIN=$1
VERSION=$2
OUTPUT_DIR=$3

case "$VERSION" in
    *[!0-9A-Za-z.+~-]* | "")
        echo "Invalid Debian package version: $VERSION" >&2
        exit 2
        ;;
esac
if [ ! -f "$CLIENT_BIN" ]; then
    echo "Client binary not found: $CLIENT_BIN" >&2
    exit 1
fi
command -v dpkg-deb >/dev/null 2>&1 || {
    echo "dpkg-deb is required to build the Linux installer." >&2
    exit 1
}

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
STAGE=$(mktemp -d)
trap 'rm -rf "$STAGE"' EXIT HUP INT TERM

PACKAGE_DIR="$STAGE/ethify"
install -d "$PACKAGE_DIR/DEBIAN" "$PACKAGE_DIR/usr/bin" \
    "$PACKAGE_DIR/usr/share/applications" "$PACKAGE_DIR/usr/share/doc/ethify" \
    "$PACKAGE_DIR/usr/share/ethify/browser-extension"
install -m 0755 "$CLIENT_BIN" "$PACKAGE_DIR/usr/bin/ethify-client"
install -m 0644 "$REPO_ROOT/scripts/ethify.desktop" \
    "$PACKAGE_DIR/usr/share/applications/ethify.desktop"
install -m 0644 "$REPO_ROOT/README.md" "$PACKAGE_DIR/usr/share/doc/ethify/README.md"
install -m 0644 "$REPO_ROOT/LICENSE" "$PACKAGE_DIR/usr/share/doc/ethify/copyright"
install -m 0644 "$REPO_ROOT"/browser-extension/* "$PACKAGE_DIR/usr/share/ethify/browser-extension/"

cat > "$PACKAGE_DIR/DEBIAN/control" <<EOF
Package: ethify
Version: $VERSION
Section: net
Priority: optional
Architecture: amd64
Depends: xdg-utils
Maintainer: A2L <aa13244296-ux@users.noreply.github.com>
Description: Multi-adapter download and network manager
 Ethify can bind separate download connections to selected physical network
 adapters. The system-wide Wintun router is available on Windows only.
EOF

mkdir -p "$OUTPUT_DIR"
dpkg-deb --build --root-owner-group "$PACKAGE_DIR" "$OUTPUT_DIR/ethify_${VERSION}_amd64.deb"
echo "Built $OUTPUT_DIR/ethify_${VERSION}_amd64.deb"
