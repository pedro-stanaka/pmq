#!/usr/bin/env bash
# Regenerate packaging/arch/.SRCINFO and, when given a version, bump pkgver
# and fill in the release source archive checksum.
#
# Usage:
#   ./packaging/arch/update.sh            # regenerate .SRCINFO only
#   ./packaging/arch/update.sh 0.1.0      # bump to v0.1.0 + checksums + .SRCINFO
#
# Requires makepkg (Arch Linux). Run after a release tag exists; the tarball
# checksum can only be computed once the tag is published on GitHub.
set -euo pipefail
cd "$(dirname "$0")"

if [ $# -gt 1 ]; then
  echo "usage: $0 [version]" >&2
  exit 2
fi

if [ $# -eq 1 ]; then
  version="$1"
  [[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "invalid version: $version" >&2; exit 2; }
  url="https://github.com/pedro-stanaka/pmq/archive/refs/tags/v${version}.tar.gz"
  curl -fsSL "$url" -o /tmp/pmq-source.tar.gz
  sum=$(sha256sum /tmp/pmq-source.tar.gz | cut -d' ' -f1)
  sed -i "s/^pkgver=.*/pkgver=${version}/" PKGBUILD
  sed -i "s/^pkgrel=.*/pkgrel=1/" PKGBUILD
  sed -i "s/^sha256sums=.*/sha256sums=('${sum}')/" PKGBUILD
  rm -f /tmp/pmq-source.tar.gz
fi

makepkg --printsrcinfo > .SRCINFO
echo "packaging/arch updated"
