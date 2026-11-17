# Install PMQ

PMQ is one static Go binary (pure-Go SQLite, no CGO) and a multi-arch container image. Pick one of the paths below; the full platform matrix, checksum verification, GHCR tag policy, and upgrade guidance live in [Releases & Distribution](/reference/distribution).

::: tip Prerequisites
PMQ needs no database server, message queue, or cloud account. Local evaluation runs entirely on loopback with one environment variable (`PMQ_DEPLOY_TOKEN`).
:::

## Release binary

Download the archive for your platform from the [releases page](https://github.com/pedro-stanaka/pmq/releases), verify it against `checksums.txt`, and put `pmq` on your `PATH`:

```sh
tar xzf pmq_linux_amd64.tar.gz
install -m 0755 pmq /usr/local/bin/pmq
pmq version
```

## Docker / Podman

```sh
docker run --rm -p 8080:8080 -v pmq-data:/data \
  -e PMQ_DOMAIN=apps.example.com -e PMQ_DEPLOY_TOKEN="$(openssl rand -hex 32)" \
  ghcr.io/pedro-stanaka/pmq:latest
```

The image is multi-arch (`linux/amd64`, `linux/arm64`), runs as a non-root user, and health-checks itself. For production use the [Compose example](https://github.com/pedro-stanaka/pmq/blob/main/compose.yaml) behind your reverse proxy.

## go install

With Go 1.27 or later:

```sh
go install github.com/pmq-dev/pmq/cmd/pmq@latest
```

Note the module path (`github.com/pmq-dev/pmq`) differs from the repository URL; only `go install` uses it.

## Arch Linux

Build from the pinned, checksum-verified source archive (no prebuilt binary):

```sh
cd packaging/arch
makepkg -si
pmq version
```

## Next

Continue with [Deploy Your First App](/getting-started/first-app).
