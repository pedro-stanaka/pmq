# Upgrades & Rollback

PMQ keeps all persistent state — SQLite databases, object files, app releases, and active pointers — under one data directory (`--data-dir`, `/data` in the container). Upgrades replace only the binary or image; app data is never touched by a deployment or upgrade.

## Upgrading the server

- **Binary:** replace the `pmq` binary and restart the process. `pmq version` confirms the new version.
- **Container:** change the image tag (pin an immutable `vX.Y.Z` or digest) and recreate the container. The mounted `/data` volume keeps app releases and documents.
- Verify after upgrading: container health, proxy identity, a deployed app's static files and API, and denial of direct or forged requests.

## Compatibility

- Patch releases are drop-in compatible: same configuration, same data layout, same API.
- Breaking changes to configuration, on-disk layout, or `/_pmq/*` bump the minor version and are called out in the release notes.
- The on-disk data layout is **forward-compatible only**: never assume an older binary can read a newer data layout.

## Rolling back a server

Keep a pinned previous image digest (or archive) and a verified backup taken when PMQ was stopped. To roll back: stop PMQ, restore the matching data backup **if the newer version changed the data layout**, then start the earlier version and verify apps and documents. See the backup drill in [Production & Recovery](/guides/production).

## Rolling back an app

Independent of server upgrades: `pmq rollback APP` activates the previous release; `pmq rollback APP --release ID` selects a specific one. Releases are immutable and retained until pruned. Do not prune a release you may need to roll back to.

## Version policy

The newest release and `main` are the only actively supported versions; report issues against a current version. Release channels and the full download/verification procedure are in [Releases & Distribution](/reference/distribution).
