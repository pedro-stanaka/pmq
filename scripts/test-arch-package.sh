#!/usr/bin/env bash
# Validate packaging/arch/PKGBUILD end to end inside an Arch Linux container:
#
#   1. Snapshot the current checkout as a git-archive tarball laid out exactly
#      like the GitHub release source archive (prefix "pmq-<version>/").
#   2. Point a copy of the PKGBUILD at that tarball and pin its checksum with
#      updpkgsums, so the tested package is checksum-verified like a release.
#   3. makepkg -si: build, run go tests (check()), install, and verify
#      `pmq version` reports the PKGBUILD version.
#
# Requires Docker or Podman and network access for Go modules.
# Used by CI (see .github/workflows/ci.yml) and runnable locally.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

pkgver=$(sed -n 's/^pkgver=//p' packaging/arch/PKGBUILD)
runner=$(command -v docker || command -v podman)
[ -n "$runner" ] || { echo "docker or podman required" >&2; exit 2; }

work=$(mktemp -d)
# The Arch container writes as its own uid; it relaxes permissions in its
# own EXIT trap so the host user (CI runners use a different uid) can clean up.
trap 'rm -rf "$work" 2>/dev/null || true' EXIT
git archive --format=tar.gz --prefix="pmq-$pkgver/" -o "$work/pmq-$pkgver.tar.gz" HEAD

"$runner" run --rm -i \
  -v "$PWD:/src:ro" \
  -v "$work:/work" \
  archlinux:base-devel \
  bash -euo pipefail -s -- "$pkgver" <<'EOF'
pkgver="$1"
trap 'chmod -R a+rwX /work 2>/dev/null || true' EXIT
pacman -Syu --noconfirm --needed pacman-contrib sudo >/dev/null
useradd -m builder
echo 'builder ALL=(ALL) NOPASSWD: ALL' > /etc/sudoers.d/builder
cp /src/packaging/arch/PKGBUILD /work/
chown -R builder:builder /work
cd /work
# Build from the verified local snapshot instead of the (not yet published) tag.
sed -i "s|::https://github.com/[^\"']*||" PKGBUILD
sudo -u builder updpkgsums
grep -q "SKIP" PKGBUILD && { echo "checksum not pinned"; exit 1; }
sudo -u builder makepkg --syncdeps --noconfirm
pacman -U --noconfirm pmq-"$pkgver"-*.pkg.tar.*
installed=$(pacman -Qi pmq | sed -n 's/^Version *: //p')
[ "$installed" = "$pkgver-1" ] || { echo "unexpected package version: $installed"; exit 1; }
out=$(pmq version)
[ "${out%% (commit*}" = "pmq $pkgver" ] || { echo "unexpected version output: $out"; exit 1; }
echo "arch package OK: pmq $pkgver built from a checksum-verified source snapshot"
EOF
