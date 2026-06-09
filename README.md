# retask-cli

A command-line interface for [Retask.work](https://retask.work) — manage workspaces, projects, tasks, sandboxes, agents, and more directly from your terminal or scripts.

## Features

- **Full API coverage** — workspaces, projects, tasks, sandboxes, sessions, agents, files, integrations, customer profiles
- **JSON-first output** — pipe to `jq`, use in scripts, or add `--pretty` for human-readable tables
- **AI-agent friendly** — `retask help-llm` prints a complete JSON manifest for LLM injection; every command has structured `--help`
- **Shared sandbox support** — `--no-save` / `RETASK_NO_PERSIST` prints `export` lines instead of writing to disk, keeping sessions isolated per shell
- **Multi-profile config** — manage multiple endpoints and workspaces via `~/.config/retask/config.yaml`

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

## Quick start

```bash
# Set your Personal Access Token and workspace
export NWEB_API_KEY="nweb_pat_..."
export NWEB_WORKSPACE_ID="ws_..."

# Exchange PAT for JWT (saved to ~/.config/retask/config.yaml)
retask auth login

# List your projects
retask project list

# Create a task
retask task create --project-id proj_abc --title "Fix login bug" --priority HIGH

# Update just one field (partial update)
retask task update task_xyz --status STATUS_DONE
```

### Shared sandboxes (session-isolated credentials)

For shared environments where multiple users share a filesystem:

```bash
eval $(retask auth login --no-save)
# Exports NWEB_API_TOKEN and NWEB_WORKSPACE_ID into the current shell only
```

Nothing is written to disk. Each shell session is fully isolated.

## Authentication

| Variable | Required | Description |
|---|---|---|
| `NWEB_API_KEY` | Yes* | Personal Access Token (`nweb_pat_...`). Never stored. |
| `NWEB_API_TOKEN` | No | Ready-to-use JWT. If set, skips PAT exchange entirely. |
| `NWEB_API_ENDPOINT` | No | Default: `api.nweb.app:443` |
| `NWEB_WORKSPACE_ID` | Yes* | Workspace scope. Required for most commands. |
| `RETASK_PROFILE` | No | Active profile name. Default: `default`. |
| `RETASK_NO_PERSIST` | No | Suppress all credential writes to disk. |

*Required unless `NWEB_API_TOKEN` is already set.

## Global flags

Available on every command:

| Flag | Env | Description |
|---|---|---|
| `--profile` | `RETASK_PROFILE` | Active config profile |
| `--workspace-id` | `NWEB_WORKSPACE_ID` | Override workspace ID |
| `--pretty` | — | Human-readable table output |
| `--insecure` | — | Skip TLS (local dev only) |
| `--no-save` | `RETASK_NO_PERSIST` | Don't write to config file |
| `--config` | — | Config file path |

## Commands

```
retask
├── auth
│   ├── login                  # PAT → JWT exchange, save to profile
│   ├── logout                 # Clear cached JWT
│   ├── whoami                 # Show current token info
│   └── pat list / create / revoke
├── workspace
│   ├── list / get / create / update / delete
│   └── member list / invite / update / remove
├── customer
│   ├── profile get / set
│   ├── list
│   └── get <id>
├── project
│   ├── list / get / create / update / delete
│   ├── archive / unarchive
│   └── member list / add / remove
├── file
│   └── list / get / delete / signed-url
├── integration
│   ├── provider list / get
│   ├── list / get / set / delete
│   └── github repos
├── task
│   ├── list / get / get-by-key
│   ├── create / update / delete
│   └── attachment add / remove
├── project-config
│   └── get / set
├── sandbox
│   ├── list / get / create / update / stop / delete
│   └── session list / get / create / update / stop / delete
└── agent
    └── list / get / create / update / delete
```

## AI agent support

Run `retask help-llm` to get a complete JSON manifest of all commands, flags, and examples — designed to be injected into an LLM system prompt:

```bash
# Full manifest
retask help-llm

# Filter to task-related commands
retask help-llm | jq '.commands[] | select(.command | contains("task"))'
```

See [`skills/retask-cli.md`](skills/retask-cli.md) for the Claude Code skill file.

## Multi-profile config

`~/.config/retask/config.yaml`:

```yaml
active_profile: default
profiles:
  default:
    endpoint: api.nweb.app:443
    workspace_id: ws_abc123
  staging:
    endpoint: api.staging.nweb.app:443
    workspace_id: ws_xyz789
```

Switch profiles with `--profile staging` or `RETASK_PROFILE=staging`.

## Development

### Proto code generation

The proto sources live in `proto/` and generated Go code in `proto-gen/`. To regenerate after updating protos:

```bash
./.bin/build_proto.sh
```

Requires [`buf`](https://buf.build/docs/installation) and [`protoc-go-inject-tag`](https://github.com/favadi/protoc-go-inject-tag).

### Run tests

```bash
go test ./...
```

### Build

```bash
go build -ldflags "-X nweb.xyz/retask-cli/internal/version.Version=0.1.0" -o retask ./cmd/retask/
```

## License

MIT
