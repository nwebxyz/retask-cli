---
title: retask-cli Distribution Design
date: 2026-06-09
status: approved
---

# retask-cli Distribution Design

## Goal

Ship pre-built `retask` binaries for all major platforms with a single install command, and automate binary publishing on every tagged release.

## Approach

GoReleaser + GitHub Actions for binary builds and GitHub Releases. Custom install scripts served from `https://retask.work` for user-facing installation. No Homebrew tap.

## Components

| Component | Location | Purpose |
|---|---|---|
| `.goreleaser.yml` | `retask-cli` root | Build config: targets, archives, checksums |
| `.github/workflows/release.yml` | `retask-cli` | Trigger GoReleaser on `v*` tag push |
| `install.sh` | `retask-frontend/public/install.sh` | Unix install script (macOS + Linux) |
| `install.ps1` | `retask-frontend/public/install.ps1` | Windows PowerShell install script |

## Release Flow

```
git tag v0.1.0 && git push origin v0.1.0
  → GitHub Actions triggers release.yml
  → GoReleaser cross-compiles 6 targets:
      darwin/amd64, darwin/arm64
      linux/amd64,  linux/arm64
      windows/amd64, windows/arm64
  → Creates GitHub Release with:
      retask_0.1.0_darwin_arm64.tar.gz
      retask_0.1.0_linux_amd64.tar.gz
      retask_0.1.0_windows_amd64.zip  (etc.)
      checksums.txt (sha256)
```

The `GORELEASER_TOKEN` secret in `nwebxyz/retask-cli` is the default `GITHUB_TOKEN` — no extra PAT needed since GoReleaser only writes to the same repo's releases.

## GoReleaser Config (`.goreleaser.yml`)

Key settings:
- **builds**: single binary `retask` from `./cmd/retask/`, ldflags inject `version.Version` from the git tag
- **targets**: `darwin_amd64`, `darwin_arm64`, `linux_amd64`, `linux_arm64`, `windows_amd64`, `windows_arm64`
- **archives**: `.tar.gz` for unix targets, `.zip` for windows
- **checksum**: `sha256sums.txt`
- **release**: GitHub release, draft mode off

## GitHub Actions Workflow (`.github/workflows/release.yml`)

- Triggers on: `push: tags: ['v*.*.*']`
- Steps: checkout (fetch-tags), setup-go, run GoReleaser
- Uses `GITHUB_TOKEN` (no extra secret required)

## Install Script: `install.sh`

Served at `https://retask.work/install.sh`.

```
Usage:
  curl -fsSL https://retask.work/install.sh | sh
  curl -fsSL https://retask.work/install.sh | sh -s -- --version v0.2.0
```

Logic:
1. Detect OS via `uname -s` → `darwin` or `linux`
2. Detect arch via `uname -m` → `amd64` (x86_64) or `arm64` (aarch64)
3. Fetch latest version tag from GitHub API (`/repos/nwebxyz/retask-cli/releases/latest`)
4. Construct archive URL: `https://github.com/nwebxyz/retask-cli/releases/download/v{VERSION}/retask_{VERSION}_{OS}_{ARCH}.tar.gz`
5. Download archive + `sha256sums.txt` to a temp dir
6. Verify sha256 checksum; abort on mismatch
7. Extract binary; install to `~/.local/bin` (non-root) or `/usr/local/bin` (root)
8. Print PATH guidance if install dir is not in `$PATH`
9. On any failure: print fallback instructions: `go install nweb.xyz/retask-cli/cmd/retask@latest`

## Install Script: `install.ps1`

Served at `https://retask.work/install.ps1`.

```
Usage:
  irm https://retask.work/install.ps1 | iex
```

Logic:
1. Detect arch via `$env:PROCESSOR_ARCHITECTURE` → `amd64` or `arm64`
2. Fetch latest version from GitHub API
3. Construct `.zip` URL: `retask_{VERSION}_windows_{ARCH}.zip`
4. Download to temp path, verify sha256
5. Extract `retask.exe` to `$env:LOCALAPPDATA\retask\bin\`
6. Add install dir to user PATH (via registry) if not already present
7. On failure: print fallback: `go install nweb.xyz/retask-cli/cmd/retask@latest`

## README Changes

Replace:
```
brew install nweb/tap/retask
```

With:
```bash
# macOS / Linux
curl -fsSL https://retask.work/install.sh | sh

# Windows (PowerShell)
irm https://retask.work/install.ps1 | iex

# Go install (requires Go)
go install nweb.xyz/retask-cli/cmd/retask@latest
```

## Out of Scope

- Code signing / macOS notarization / Windows Authenticode
- Docker images
- apt/deb, rpm, AUR packages
- Homebrew tap
- Snapshot / nightly builds
