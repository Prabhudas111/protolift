#!/bin/sh
# Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
# SPDX-License-Identifier: Apache-2.0
#
# Installs the latest protolift release for macOS or Linux.
#   curl -fsSL https://raw.githubusercontent.com/Prabhudas111/protolift/main/install.sh | sh
# Set PROTOLIFT_VERSION=v0.1.0 to pick a version, PROTOLIFT_BIN=/path to pick the folder.
set -eu

REPO="Prabhudas111/protolift"
fail() { echo "protolift install: $1" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || fail "needs $1"; }
need curl; need tar; need uname
# GitHub sometimes answers a download with a short-lived 5xx. curl retries
# those (and timeouts) a few times with a pause. The checksum still decides.
get() { curl -fsSL --retry 4 --retry-delay 3 --connect-timeout 20 "$@"; }

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in darwin|linux) ;; *) fail "unsupported system $os (on Windows use install.ps1)";; esac
arch=$(uname -m)
case "$arch" in x86_64|amd64) arch=amd64 ;; arm64|aarch64) arch=arm64 ;; *) fail "unsupported CPU $arch" ;; esac

version="${PROTOLIFT_VERSION:-}"
if [ -z "$version" ]; then
  version=$(get --max-time 30 "https://api.github.com/repos/$REPO/releases/latest" \
    | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1)
  [ -n "$version" ] || fail "could not find the latest release"
fi
file="protolift_${version#v}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$version"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
echo "Downloading protolift $version for $os/$arch..."
get --max-time 300 -o "$tmp/$file" "$base/$file" || fail "download failed: $base/$file (check your connection and run the installer again)"
get --max-time 60 -o "$tmp/checksums.txt" "$base/checksums.txt" || fail "checksum download failed"

want=$(grep " $file\$" "$tmp/checksums.txt" | cut -d' ' -f1)
[ -n "$want" ] || fail "no checksum listed for $file"
if command -v sha256sum >/dev/null 2>&1; then got=$(sha256sum "$tmp/$file" | cut -d' ' -f1)
else got=$(shasum -a 256 "$tmp/$file" | cut -d' ' -f1); fi
[ "$want" = "$got" ] || fail "checksum mismatch: the download was not used"

tar -xzf "$tmp/$file" -C "$tmp" protolift

dest="${PROTOLIFT_BIN:-}"
if [ -z "$dest" ]; then
  if [ -w /usr/local/bin ]; then dest=/usr/local/bin; else dest="$HOME/.local/bin"; fi
fi
mkdir -p "$dest"
install -m 0755 "$tmp/protolift" "$dest/protolift"
echo "Installed $dest/protolift"
case ":$PATH:" in *":$dest:"*) ;; *) echo "Add $dest to your PATH to run protolift." ;; esac
echo "Next: protolift init   ·   Built by Proto.Engineer: https://www.proto-engineer.com"
