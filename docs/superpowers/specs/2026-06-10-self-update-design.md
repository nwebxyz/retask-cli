# Self-Update: `retask upgrade`

**Date:** 2026-06-10
**Status:** Approved

## Overview

Add a `retask upgrade` subcommand that fetches the latest release from GitHub, verifies its checksum, and atomically replaces the running binary in place.

## Architecture & Flow

**New files:**
- `internal/cmd/upgrade/command.go` (package `upgrade`)
- `internal/cmd/upgrade/command_test.go`

**Registration:** one line in `cmd/retask/main.go`, one entry in `internal/cmd/helpcmd/command.go`.

**New dependency:** `github.com/inconshreveable/go-update` — handles atomic binary replacement, rollback on failure, and platform differences (Unix rename-swap, Windows file locking).

### Execution steps

1. If `version.Version == "dev"`, print `upgrade: cannot upgrade a dev build` to stderr and exit non-zero. No network call.
2. `GET https://api.github.com/repos/nwebxyz/retask-cli/releases/latest` — parse JSON for `tag_name` and `assets[]`.
3. Strip leading `v` from `tag_name`. Compare with `version.Version`.
   - If equal: print `retask v{version} is already up to date` and exit 0.
4. Print `retask v{current} → v{latest}` to stdout.
5. Build asset filename from `runtime.GOOS` / `runtime.GOARCH`:
   - Linux/macOS: `retask_{version}_{os}_{arch}.tar.gz`
   - Windows: `retask_{version}_{os}_{arch}.zip`
6. Find the matching tarball asset URL and the `sha256sums.txt` asset URL in the release assets list. If either is missing, print `upgrade: no release asset found for {goos}/{goarch}` and exit non-zero.
7. Download `sha256sums.txt`, parse it to extract the expected hex digest for the tarball filename.
8. Download the tarball via a progress-reporting reader that updates stderr in place:
   ```
   Downloading retask_0.2.0_darwin_arm64.tar.gz... 1.2 MB / 3.4 MB (35%)
   ```
   - Uses `Content-Length` response header for percentage; omits percentage if header is absent.
   - Skips progress output if stderr is not a TTY (no garbled output in scripts).
   - Simultaneously streams through `sha256.New()` and `archive/tar` (or `archive/zip` on Windows) to extract the `retask` (or `retask.exe`) binary entry.
9. Call `update.Apply(binaryReader, update.Options{Hash: crypto.SHA256, Checksum: expectedBytes})`. This atomically replaces the running binary and rolls back on failure.
10. Print `Upgraded to v{version}` and exit 0.

## Error Handling

All errors print to stderr and exit non-zero.

| Scenario | Message |
|---|---|
| Dev build | `upgrade: cannot upgrade a dev build` |
| Network failure | `upgrade: failed to fetch latest release: <err>` |
| No matching asset | `upgrade: no release asset found for darwin/arm64` |
| SHA256 mismatch | `upgrade: checksum verification failed` |
| Insufficient permissions | `upgrade: <err> (try running with sudo)` |

## Testing

**`internal/cmd/upgrade/command_test.go`** covers two pure functions:

1. `assetName(version, goos, goarch string) string` — table-driven tests across OS/arch combos:
   - `darwin/amd64` → `retask_0.2.0_darwin_amd64.tar.gz`
   - `linux/arm64` → `retask_0.2.0_linux_arm64.tar.gz`
   - `windows/amd64` → `retask_0.2.0_windows_amd64.zip`

2. `parseChecksum(data []byte, filename string) ([]byte, error)` — tests with fixture data:
   - Matching line found → returns correct bytes
   - Filename not in file → returns error
   - Malformed line → returns error

HTTP fetch, download, and binary swap are not unit tested. Integration coverage comes from running `retask upgrade` in a real environment against a published release.

## What Is Not In Scope

- `--check` flag (check-only without applying)
- Rollback / downgrade to a specific version
- Auto-update check on every command invocation
- Support for users who installed via `go install` (they should use `go install` to upgrade)
