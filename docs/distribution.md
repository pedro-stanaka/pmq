# Distribution and releases

PMQ ships as one static Go binary, an OCI container image, and an Arch Linux source package. This document explains how to install each one, how tagged releases are produced, and how to upgrade or roll back.

The Go module path is `github.com/pmq-dev/pmq`; the repository lives at `https://github.com/pedro-stanaka/pmq`. Both URLs point at this project; only `go install` uses the module path.

## Supported platforms

Binaries are cross-built with `CGO_ENABLED=0` (the SQLite driver is pure Go, so no C toolchain is needed on any target):

| OS      | amd64 | arm64 |
|---------|-------|-------|
| Linux   | built and tested in CI | built and smoke-tested in CI; runtime-tested on container builds |
| macOS   | built | built |
| Windows | built | built |

CI build-tests every target and runs the Linux amd64 binary's `version`/`help` commands. macOS and Windows binaries are not executed in CI; report issues for runtime problems. Container images are published for `linux/amd64` and `linux/arm64`; arm64 is built (and in CI, smoke-tested) via emulation — see [Limitations](#limitations).

## Install

### Release binary

1. Download the archive for your platform from the [releases page](https://github.com/pedro-stanaka/pmq/releases), e.g. `pmq_linux_amd64.tar.gz` (Windows: `pmq_windows_amd64.zip`).

2. Verify the download against the published `checksums.txt`:

   ```sh
   sha256sum -c --ignore-missing checksums.txt
   ```

3. Release artifacts carry GitHub artifact attestations. To verify provenance with the `gh` CLI:

   ```sh
   gh attestation verify ./pmq_linux_amd64.tar.gz --repo pedro-stanaka/pmq
   ```

4. Unpack and put the `pmq` binary on your `PATH`:

   ```sh
   tar xzf pmq_linux_amd64.tar.gz
   install -m 0755 pmq /usr/local/bin/pmq
   pmq version
   ```

### go install

With Go 1.27+ installed:

```sh
go install github.com/pmq-dev/pmq/cmd/pmq@v0.1.0
```

The binary is installed into `$(go env GOPATH)/bin`. `@latest` resolves to the newest tag. `go install` binaries report only the version (`pmq 0.1.0`), not commit or build date.

### Docker / Podman

Images are published to GHCR as multi-architecture images (`linux/amd64`, `linux/arm64`):

```sh
docker run --rm -p 8080:8080 -v pmq-data:/data \
  -e PMQ_DOMAIN=apps.example.com -e PMQ_DEPLOY_TOKEN="$(openssl rand -hex 32)" \
  ghcr.io/pedro-stanaka/pmq:latest
```

for a production setup with a reverse proxy, see the [compose example](https://github.com/pedro-stanaka/pmq/blob/main/compose.yaml) and [production and recovery](https://github.com/pedro-stanaka/pmq/blob/main/docs/proxy/production.md).

Image tag policy:

| Tag | Meaning |
|-----|---------|
| `vX.Y.Z` | Exact release; treat as immutable (enable tag immutability in the GHCR package settings for enforcement). |
| `vX.Y` | Newest patch of that minor version. |
| `latest` | Newest tagged release. |
| `edge` | Last successful build of `main`; not a release. |

The image runs as the non-root `pmq` user, stores all state under `/data` (mount it as a volume), exposes port 8080, and has a container health check. The binary inside reports the image's release version.

### Arch Linux (makepkg)

A source package lives in `packaging/arch/`. It builds PMQ from the pinned, checksum-verified release source archive using the Go toolchain — no prebuilt binary — and installs `/usr/bin/pmq`:

```sh
cd packaging/arch
makepkg -si            # build, run tests, install
pmq version
```

To rebuild cleanly, remove leftover build artifacts first: `rm -rf src pkg *.pkg.tar.*`.

When a new release is tagged, update the package with the helper (requires Arch Linux):

```sh
./packaging/arch/update.sh 0.2.0   # bumps pkgver, pins the new tarball checksum, regenerates .SRCINFO
makepkg -si
```

`scripts/test-arch-package.sh` validates the PKGBUILD end to end inside an `archlinux:base-devel` container using a checksum-verified source snapshot of the current checkout; CI runs it on every pull request. The PKGBUILD is kept suitable for a future AUR `pmq` source package, but it will not be published to AUR without explicit authorization.

## Release process

Releases are produced by two workflows; both use pinned, least-privilege GitHub Actions steps:

- **`release.yml`** runs when a `vX.Y.Z` tag is pushed. GoReleaser cross-builds the full platform matrix, packs archives plus `checksums.txt`, creates checksum and provenance attestations, and opens a **draft** GitHub release. Nothing is public until a maintainer reviews the draft and publishes it — publishing is the explicit authorization step.
- **`images.yml`** builds the multi-arch image only when it is published: on tags (release tags plus `latest`) and on `main` (`edge`). Pull requests do not build images — the QEMU arm64 build takes ~15–20 minutes and adds no signal beyond the containerized `arch-package` job, which builds the same Dockerfile.

Draft releases keep a changelog generated from merged pull requests (docs/test/chore commits excluded). Release archives contain only the binary, `README.md`, and `LICENSE` — never secrets, `.env` files, certificates, app databases, or local data directories (`.dockerignore` and `.gitignore` keep those out of images and checkouts).

To validate the pipeline without publishing:

```sh
goreleaser check                                   # validate configuration
goreleaser build --snapshot --clean                # build the full matrix locally
./scripts/test-arch-package.sh                     # PKGBUILD end-to-end in a container
```

## Upgrade and rollback

PMQ keeps all persistent state (SQLite databases, object storage, app releases) under `--data-dir` (default `./data`; `/data` in the container). Upgrades replace only the binary or image — persistent app data is never touched by a deployment or upgrade.

- **Binary:** replace the `pmq` binary and restart the process. `pmq version` confirms the new version.
- **Container:** change the image tag (e.g. `v0.2.0`) and recreate the container; the mounted `/data` volume keeps app data and releases.
- **Rollback a binary:** reinstall the previous version's archive. Revisions are never downgraded automatically; the on-disk layout is forward-compatible only, so roll back before any newer process has started if possible, and keep backups.
- **Rollback an app release:** use `pmq rollback <app>` — see the README. This is independent of server upgrades.

Back up the data directory (including app release directories and SQLite files) with the file system at rest — stop PMQ or snapshot the volume. There is no built-in backup command.

## Compatibility and support policy

- Breaking changes to configuration, on-disk data layout, or the `/_pmq/*` API bump the minor version and are called out in the release notes.
- Patch releases are drop-in compatible: same config, same data, same API.
- The newest release and `main` are the only actively supported versions; report issues against a current version.

## Limitations

- macOS and Windows binaries are built and archived but not executed in CI; runtime verification is manual.
- The `linux/arm64` image is built under QEMU emulation in CI. It starts and serves requests in emulated smoke tests, but it is not benchmarked or tested on native arm64 hardware in CI.
- Artifact attestations are generated by the release workflow; verification requires `gh attestation verify` and GitHub as the trust root.
- Homebrew and Scoop packages are intentionally not provided yet; they would be added only if someone commits to maintaining them.
- Releases are created as drafts first; GitHub does not compute a stable checksum for auto-generated source archives until a draft is published, so link the published release page rather than pinning `archive/refs/tags/...` checksums externally.
