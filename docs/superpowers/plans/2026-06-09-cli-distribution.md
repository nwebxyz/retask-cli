# CLI Distribution Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Automate cross-platform binary releases for `retask` using GoReleaser + GitHub Actions, and provide a universal install script served at `https://retask.work/install.sh` (and `install.ps1` for Windows).

**Architecture:** GoReleaser builds 6 binaries (darwin/linux/windows × amd64/arm64) and publishes them as GitHub Release assets on every `v*.*.*` tag. Install scripts detect the user's OS/arch, download and verify the correct binary from GitHub Releases, and fall back to `go install` on failure.

**Tech Stack:** GoReleaser v2, GitHub Actions, POSIX shell, PowerShell 5+

---

## File Map

**`retask-cli` repo** (this repo):
- Create: `.goreleaser.yml` — GoReleaser build/archive/release config
- Create: `.github/workflows/release.yml` — GitHub Actions trigger on `v*.*.*` tags
- Modify: `README.md` — replace Homebrew install with script install

**`nweb-app-frontend` repo** (path: `../nweb-app-frontend/workspaces/retask-frontend/public/`):
- Create: `install.sh` — Unix install script (macOS + Linux)
- Create: `install.ps1` — Windows PowerShell install script

---

## Task 1: GoReleaser config

**Files:**
- Create: `.goreleaser.yml`

- [ ] **Step 1: Install GoReleaser if not present**

```bash
brew install goreleaser
```

Verify: `goreleaser --version` → prints version `2.x.x`

- [ ] **Step 2: Create `.goreleaser.yml`**

```yaml
version: 2

project_name: retask

before:
  hooks:
    - go mod tidy

builds:
  - id: retask
    main: ./cmd/retask/
    binary: retask
    ldflags:
      - -s -w -X nweb.xyz/retask-cli/internal/version.Version={{.Version}}
    goos:
      - darwin
      - linux
      - windows
    goarch:
      - amd64
      - arm64

archives:
  - id: retask
    format_overrides:
      - goos: windows
        format: zip
    name_template: >-
      {{ .ProjectName }}_
      {{- .Version }}_
      {{- .Os }}_
      {{- .Arch }}

checksum:
  name_template: "sha256sums.txt"
  algorithm: sha256

release:
  github:
    owner: nwebxyz
    name: retask-cli
  draft: false
  prerelease: auto

changelog:
  sort: asc
  filters:
    exclude:
      - "^docs:"
      - "^test:"
      - "^chore:"
```

- [ ] **Step 3: Validate config**

```bash
goreleaser check
```

Expected output: `• config is valid`

- [ ] **Step 4: Verify local snapshot build works**

```bash
goreleaser build --snapshot --clean
```

Expected: `dist/` directory created with 6 binaries:
```
dist/retask_darwin_amd64_v1/retask
dist/retask_darwin_arm64/retask
dist/retask_linux_amd64_v1/retask
dist/retask_linux_arm64/retask
dist/retask_windows_amd64_v1/retask.exe
dist/retask_windows_arm64/retask.exe
```

- [ ] **Step 5: Add `dist/` to `.gitignore`**

Open `.gitignore` (or create it) and add:
```
dist/
```

- [ ] **Step 6: Commit**

```bash
git add .goreleaser.yml .gitignore
git commit -m "build: add GoReleaser config for multi-platform binary releases"
```

---

## Task 2: GitHub Actions release workflow

**Files:**
- Create: `.github/workflows/release.yml`

- [ ] **Step 1: Create the workflows directory**

```bash
mkdir -p .github/workflows
```

- [ ] **Step 2: Create `.github/workflows/release.yml`**

```yaml
name: Release

on:
  push:
    tags:
      - 'v*.*.*'

permissions:
  contents: write

jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true

      - uses: goreleaser/goreleaser-action@v6
        with:
          distribution: goreleaser
          version: '~> v2'
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/release.yml
git commit -m "ci: add GitHub Actions release workflow via GoReleaser"
```

---

## Task 3: Unix install script (`install.sh`)

**Files:**
- Create: `../nweb-app-frontend/workspaces/retask-frontend/public/install.sh`

- [ ] **Step 1: Create `install.sh`**

```bash
#!/bin/sh
set -e

REPO="nwebxyz/retask-cli"
BINARY="retask"
FALLBACK="Install via Go instead: go install nweb.xyz/retask-cli/cmd/retask@latest"

# Parse optional --version flag
VERSION=""
while [ $# -gt 0 ]; do
  case "$1" in
    --version) VERSION="$2"; shift 2 ;;
    *) shift ;;
  esac
done

# Detect OS
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$OS" in
  darwin|linux) ;;
  *)
    echo "Unsupported OS: $OS"
    echo "$FALLBACK"
    exit 1
    ;;
esac

# Detect arch
ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *)
    echo "Unsupported architecture: $ARCH"
    echo "$FALLBACK"
    exit 1
    ;;
esac

# Fetch latest version if not specified
if [ -z "$VERSION" ]; then
  VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name"' \
    | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')
  if [ -z "$VERSION" ]; then
    echo "Failed to fetch latest version from GitHub API"
    echo "$FALLBACK"
    exit 1
  fi
fi

VERSION_NUM="${VERSION#v}"
ARCHIVE="${BINARY}_${VERSION_NUM}_${OS}_${ARCH}.tar.gz"
DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${VERSION}/${ARCHIVE}"
CHECKSUM_URL="https://github.com/${REPO}/releases/download/${VERSION}/sha256sums.txt"

# Install dir
if [ "$(id -u)" = "0" ]; then
  INSTALL_DIR="/usr/local/bin"
else
  INSTALL_DIR="${HOME}/.local/bin"
  mkdir -p "$INSTALL_DIR"
fi

# Download to temp dir
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

echo "Downloading retask ${VERSION} (${OS}/${ARCH})..."
if ! curl -fsSL "$DOWNLOAD_URL" -o "${TMP}/${ARCHIVE}"; then
  echo "Download failed: $DOWNLOAD_URL"
  echo "$FALLBACK"
  exit 1
fi
if ! curl -fsSL "$CHECKSUM_URL" -o "${TMP}/sha256sums.txt"; then
  echo "Failed to download checksums"
  echo "$FALLBACK"
  exit 1
fi

# Verify checksum
cd "$TMP"
if command -v sha256sum >/dev/null 2>&1; then
  grep "  ${ARCHIVE}$\| ${ARCHIVE}$" sha256sums.txt | sha256sum -c -
elif command -v shasum >/dev/null 2>&1; then
  grep "  ${ARCHIVE}$\| ${ARCHIVE}$" sha256sums.txt | shasum -a 256 -c -
else
  echo "Warning: sha256sum/shasum not found — skipping checksum verification"
fi
cd - >/dev/null

# Extract and install
tar -xzf "${TMP}/${ARCHIVE}" -C "$TMP"
install -m 755 "${TMP}/${BINARY}" "${INSTALL_DIR}/${BINARY}"

echo ""
echo "Installed retask ${VERSION} → ${INSTALL_DIR}/${BINARY}"

# PATH guidance
case ":${PATH}:" in
  *":${INSTALL_DIR}:"*) ;;
  *)
    echo ""
    echo "  ${INSTALL_DIR} is not in your PATH. Add it:"
    echo "    export PATH=\"\$PATH:${INSTALL_DIR}\""
    echo "  (add to ~/.bashrc or ~/.zshrc to persist)"
    ;;
esac
```

- [ ] **Step 2: Make executable**

```bash
chmod +x ../nweb-app-frontend/workspaces/retask-frontend/public/install.sh
```

- [ ] **Step 3: Lint the script**

```bash
shellcheck ../nweb-app-frontend/workspaces/retask-frontend/public/install.sh
```

If `shellcheck` is not installed: `brew install shellcheck`. Expected: no warnings or only style suggestions (SC2317 etc. acceptable).

- [ ] **Step 4: Smoke-test locally**

This requires at least one published GitHub Release to exist. If none exist yet, skip to Task 6 and return here after the first release.

```bash
sh /Users/tan/nweb/nweb-app-frontend/workspaces/retask-frontend/public/install.sh
```

Expected: downloads and installs the binary, prints `Installed retask vX.Y.Z → ~/.local/bin/retask`

```bash
~/.local/bin/retask version
```

Expected: prints the version string.

- [ ] **Step 5: Commit (in the frontend repo)**

```bash
git -C /Users/tan/nweb/nweb-app-frontend add workspaces/retask-frontend/public/install.sh
git -C /Users/tan/nweb/nweb-app-frontend commit -m "feat: add retask CLI unix install script"
```

---

## Task 4: Windows install script (`install.ps1`)

**Files:**
- Create: `../nweb-app-frontend/workspaces/retask-frontend/public/install.ps1`

- [ ] **Step 1: Create `install.ps1`**

```powershell
$ErrorActionPreference = 'Stop'

$Repo    = "nwebxyz/retask-cli"
$Binary  = "retask"
$Fallback = "Install via Go instead: go install nweb.xyz/retask-cli/cmd/retask@latest"

try {
  # Detect arch
  $Arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    'AMD64' { 'amd64' }
    'ARM64' { 'arm64' }
    default  { throw "Unsupported architecture: $($env:PROCESSOR_ARCHITECTURE)" }
  }

  # Fetch latest version
  $ApiUrl  = "https://api.github.com/repos/$Repo/releases/latest"
  $Headers = @{ 'User-Agent' = 'retask-install' }
  $Release = Invoke-RestMethod -Uri $ApiUrl -Headers $Headers
  $Version    = $Release.tag_name
  $VersionNum = $Version.TrimStart('v')

  $Archive     = "${Binary}_${VersionNum}_windows_${Arch}.zip"
  $DownloadUrl = "https://github.com/$Repo/releases/download/$Version/$Archive"
  $ChecksumUrl = "https://github.com/$Repo/releases/download/$Version/sha256sums.txt"

  # Temp dir
  $Tmp = Join-Path $env:TEMP "retask-install-$(New-Guid)"
  New-Item -ItemType Directory -Path $Tmp | Out-Null

  Write-Host "Downloading retask $Version (windows/$Arch)..."
  Invoke-WebRequest -Uri $DownloadUrl  -OutFile "$Tmp\$Archive"       -UseBasicParsing
  Invoke-WebRequest -Uri $ChecksumUrl  -OutFile "$Tmp\sha256sums.txt" -UseBasicParsing

  # Verify checksum
  $ChecksumLine = Get-Content "$Tmp\sha256sums.txt" | Where-Object { $_ -match [regex]::Escape($Archive) }
  if ($ChecksumLine) {
    $Expected = ($ChecksumLine -split '\s+')[0].ToLower()
    $Actual   = (Get-FileHash "$Tmp\$Archive" -Algorithm SHA256).Hash.ToLower()
    if ($Expected -ne $Actual) {
      throw "Checksum mismatch!`n  Expected: $Expected`n  Got:      $Actual"
    }
  } else {
    Write-Warning "No checksum entry found for $Archive — skipping verification"
  }

  # Extract
  Expand-Archive -Path "$Tmp\$Archive" -DestinationPath $Tmp -Force

  # Install
  $InstallDir = "$env:LOCALAPPDATA\retask\bin"
  New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
  Copy-Item "$Tmp\$Binary.exe" "$InstallDir\$Binary.exe" -Force

  # Add to user PATH if not present
  $UserPath = [Environment]::GetEnvironmentVariable('PATH', 'User')
  if ($UserPath -notlike "*$InstallDir*") {
    [Environment]::SetEnvironmentVariable('PATH', "$UserPath;$InstallDir", 'User')
    Write-Host "Added $InstallDir to your user PATH (restart terminal to take effect)"
  }

  Write-Host ""
  Write-Host "Installed retask $Version → $InstallDir\$Binary.exe"

} catch {
  Write-Host "Installation failed: $_"
  Write-Host $Fallback
  exit 1
} finally {
  if ($Tmp -and (Test-Path $Tmp)) { Remove-Item -Recurse -Force $Tmp }
}
```

- [ ] **Step 2: Commit (in the frontend repo)**

```bash
git -C /Users/tan/nweb/nweb-app-frontend add workspaces/retask-frontend/public/install.ps1
git -C /Users/tan/nweb/nweb-app-frontend commit -m "feat: add retask CLI Windows PowerShell install script"
```

---

## Task 5: Update README

**Files:**
- Modify: `README.md` (in `retask-cli` repo)

- [ ] **Step 1: Replace the Installation section**

Find the current Installation section (lines 13–34) and replace with:

```markdown
## Installation

### macOS / Linux

```bash
curl -fsSL https://retask.work/install.sh | sh
```

### Windows (PowerShell)

```powershell
irm https://retask.work/install.ps1 | iex
```

### Go install

```bash
go install nweb.xyz/retask-cli/cmd/retask@latest
```

### Build from source

```bash
git clone https://github.com/nwebxyz/retask-cli
cd retask-cli
go build -ldflags "-X nweb.xyz/retask-cli/internal/version.Version=$(git describe --tags)" -o retask ./cmd/retask/
```
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: update installation instructions to use install script"
```

---

## Task 6: First release end-to-end test

This task verifies the full pipeline works. Do this after all other tasks are committed and pushed.

- [ ] **Step 1: Push all commits to `main`**

```bash
git push origin main
```

Also push the frontend repo changes:
```bash
git -C /Users/tan/nweb/nweb-app-frontend push origin main
```

- [ ] **Step 2: Create and push a pre-release tag**

Use a `v0.0.x` tag to avoid conflicting with real releases:

```bash
git tag v0.0.1-test
git push origin v0.0.1-test
```

- [ ] **Step 3: Watch the Actions run**

Go to `https://github.com/nwebxyz/retask-cli/actions` and watch the Release workflow.

Expected: all steps green, a new Release published at `https://github.com/nwebxyz/retask-cli/releases/tag/v0.0.1-test` with 6 archives and `sha256sums.txt`.

- [ ] **Step 4: Test `install.sh` against the published release**

```bash
sh /Users/tan/nweb/nweb-app-frontend/workspaces/retask-frontend/public/install.sh --version v0.0.1-test
```

Expected: downloads, verifies checksum, installs to `~/.local/bin/retask`, prints version.

```bash
~/.local/bin/retask version
```

Expected: `0.0.1-test` or similar.

- [ ] **Step 5: Clean up test release**

Delete the test tag and release via GitHub UI or:

```bash
git tag -d v0.0.1-test
git push origin :refs/tags/v0.0.1-test
```

Then delete the GitHub Release from the Releases page.
