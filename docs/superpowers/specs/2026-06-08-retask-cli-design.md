# Retask CLI — Design Spec

**Date:** 2026-06-08
**Status:** Approved
**Version:** 0.1.0

---

## Overview

`retask-cli` is a public Go CLI for interacting with NWEB APIs for Retask products. It connects via gRPC, authenticates using PAT → JWT exchange, and is designed to work for both human operators and AI agents (Claude Code, Codex, Gemini, etc.).

---

## Repository Structure

```
retask-cli/
├── cmd/
│   └── retask/
│       └── main.go                  # entry point, wires all service commands
├── internal/
│   ├── auth/
│   │   └── token.go                 # GetOrRefreshToken(), ExchangePat logic
│   ├── client/
│   │   └── grpc.go                  # gRPC connection factory (TLS, interceptors)
│   ├── config/
│   │   ├── profile.go               # load/save profiles, --no-save / RETASK_NO_PERSIST
│   │   └── workspace.go             # workspace resolution
│   ├── output/
│   │   └── output.go                # JSON vs --pretty table renderer
│   ├── version/
│   │   └── version.go               # Version constant, overridden via ldflags
│   └── cmd/
│       ├── auth/                    # retask auth ...
│       ├── customer/                # retask customer ...
│       ├── file/                    # retask file ...
│       ├── integration/             # retask integration ...
│       ├── project/                 # retask project ...
│       ├── projectconfig/           # retask project-config ...
│       ├── sandbox/                 # retask sandbox ...
│       ├── agent/                   # retask agent ...
│       ├── task/                    # retask task ...
│       └── workspace/               # retask workspace ...
├── proto/                           # approved proto sources (public-facing only)
│   ├── auth/v1/
│   ├── common/v1/
│   ├── customer/v1/
│   ├── file/v1/
│   ├── integration/v1/
│   ├── project/v1/
│   ├── retask/
│   │   ├── common/v1/
│   │   ├── agent/v1/
│   │   ├── project/v1/
│   │   ├── sandbox/v1/
│   │   └── task/v1/
│   └── workspace/v1/
├── proto-gen/                       # generated Go code (never edit by hand)
├── .bin/
│   ├── build_proto.sh               # generates Go from proto/ into proto-gen/
│   └── sync_proto.sh                # copies approved protos from local api-contracts/
├── buf.yaml                         # proto source: proto/
├── buf.gen.yaml                     # output: proto-gen/, prefix: nweb.xyz/retask-cli/proto-gen
├── go.mod
└── .gitignore                       # api-contracts/ is gitignored
```

`api-contracts/` is gitignored and local-dev only — used as source when running `sync_proto.sh`. It is scrubbed from git history before the repo goes public.

---

## Proto Scope

Only these services are included in `proto/` and `proto-gen/`. All others are excluded from the public repo.

| Service | Proto path |
|---|---|
| auth | `auth/v1/` |
| common (shared types) | `common/v1/` |
| customer | `customer/v1/` |
| file | `file/v1/` |
| integration | `integration/v1/` |
| project | `project/v1/` |
| retask/agent | `retask/agent/v1/` |
| retask/common | `retask/common/v1/` |
| retask/project | `retask/project/v1/` |
| retask/sandbox | `retask/sandbox/v1/` |
| retask/task | `retask/task/v1/` |
| workspace | `workspace/v1/` |

`event/`, `command/`, and `cron/` subdirectories are excluded — internal pubsub contracts not needed by the CLI.

---

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `NWEB_API_KEY` | Yes (unless `NWEB_API_TOKEN` set) | PAT (`nweb_pat_...`). Never stored in profile. Never sent to any API except `auth.ExchangePat`. |
| `NWEB_API_TOKEN` | No | Ready-to-use JWT. If set, skips all PAT exchange logic. |
| `NWEB_API_ENDPOINT` | No | Default: `api.dev.nweb.app:443` |
| `NWEB_WORKSPACE_ID` | No | Workspace scope. Required if not in profile and no `--workspace-id` flag. |
| `RETASK_PROFILE` | No | Active profile name. Default: `default`. |
| `RETASK_NO_PERSIST` | No | If set, suppress all file writes (shared sandbox mode). |

---

## Authentication & Token Lifecycle

### Token Resolution Flow

```
GetOrRefreshToken(ctx) → string (JWT)

1. If NWEB_API_TOKEN set → return directly (no expiry check — caller owns it)
2. Load active profile from ~/.config/retask/config.yaml
3. If profile.cached_jwt set AND expires_at > now+5min → return cached JWT
4. Require NWEB_API_KEY (PAT) — hard error if missing
5. Resolve workspace_id:
   a. --workspace-id flag
   b. NWEB_WORKSPACE_ID env
   c. profile.workspace_id
   d. Interactive TTY prompt: "Enter workspace ID:"
   e. Non-TTY → hard error: "workspace ID required. Set NWEB_WORKSPACE_ID,
      use --workspace-id, or run: retask auth login"
6. Call auth.ExchangePat(pat, workspace_id) → AccessToken
7. Unless RETASK_NO_PERSIST: save JWT + expires_at + workspace_id to profile
8. Return JWT
```

PAT is **never stored** in the profile. It lives only in `NWEB_API_KEY` in the shell environment.

### Shared Sandbox Mode (`--no-save` / `RETASK_NO_PERSIST`)

For shared sandbox environments where multiple users share a filesystem but need isolated sessions:

```bash
eval $(retask auth login --no-save)
```

Instead of writing to disk, the CLI prints:

```bash
export NWEB_API_TOKEN="eyJhbGci..."
export NWEB_WORKSPACE_ID="ws_abc123"
# To apply: eval $(retask auth login --no-save)
```

Each shell session is fully isolated. Nothing persists to disk.

### Profile File (`~/.config/retask/config.yaml`)

```yaml
active_profile: default
profiles:
  default:
    endpoint: api.dev.nweb.app:443
    workspace_id: ws_abc123
    cached_jwt: eyJhbGci...
    jwt_expires_at: 2026-06-08T14:00:00Z
  staging:
    endpoint: api.staging.nweb.app:443
    workspace_id: ws_xyz789
```

Selected via `--profile` flag or `RETASK_PROFILE` env. PAT is never stored here.

---

## gRPC Client

- **TLS**: `credentials.NewClientTLSFromCert(nil, "")` — uses OS cert pool, works on macOS/Linux/Windows with no bundled certs.
- **Auth interceptor**: injects `Authorization: Bearer <jwt>` on every unary RPC.
- **`--insecure` flag**: skips TLS for local dev against `localhost`.
- One connection per command invocation, no global state.

---

## Output

- **Default**: JSON to stdout.
- **`--pretty` flag**: human-readable tables to stdout.
- **Errors**: always to stderr, never mixed with output.

Global flags available on every command:

| Flag | Env | Description |
|---|---|---|
| `--profile` | `RETASK_PROFILE` | Active profile |
| `--workspace-id` | `NWEB_WORKSPACE_ID` | Override workspace |
| `--pretty` | — | Human-readable table output |
| `--insecure` | — | Skip TLS (local dev) |
| `--no-save` | `RETASK_NO_PERSIST` | Don't write to config file |

---

## Command Tree

```
retask
├── auth
│   ├── login                        # interactive PAT → JWT exchange, save profile
│   ├── logout                       # clear cached JWT from profile
│   ├── whoami                       # print current token claims (workspace, expiry)
│   ├── pat list
│   ├── pat create [--name] [--workspace-id] [--expires-at]
│   └── pat revoke <id>
├── workspace
│   ├── list
│   ├── get <id>
│   ├── create [--name] [--description] [--color]
│   ├── update <id> [--name] [--description] [--color]
│   ├── delete <id>
│   ├── member list <workspace-id>
│   ├── member invite <workspace-id> [--email] [--role] [--display-name]
│   ├── member update <workspace-id> <member-id> [--role] [--display-name]
│   └── member remove <workspace-id> <member-id>
├── customer
│   ├── profile get
│   ├── profile set [--name] [--email] [--timezone] [--theme]
│   ├── list
│   └── get <id>
├── project
│   ├── list [--archived]
│   ├── get <id>
│   ├── create [--name] [--description] [--visibility] [--color] [--icon]
│   ├── update <id> [--name] [--description] [--visibility] [--color] [--icon]
│   ├── archive <id>
│   ├── unarchive <id>
│   ├── delete <id>
│   ├── member list <project-id>
│   ├── member add <project-id> [--member-id] [--role]
│   └── member remove <project-id> <member-id>
├── file
│   ├── list [--project-id]
│   ├── get <id>
│   ├── delete <id>
│   └── signed-url <id> [--expires-in]
├── integration
│   ├── provider list
│   ├── provider get <id>
│   ├── list [--provider-id]
│   ├── get <id>
│   ├── set [--provider-id] [--level] [--access-token]
│   ├── delete <id>
│   └── github repos [--level]
├── task
│   ├── list [--project-id] [--status] [--assignee] [--priority]
│   ├── get <id>
│   ├── get-by-key <key>
│   ├── create [--project-id] [--title] [--description] [--status] [--priority] [--assignee] [--due-at]
│   ├── update <id> [--title] [--description] [--status] [--priority] [--assignee] [--due-at]
│   ├── delete <id>
│   ├── attachment add <task-id> <file-id>
│   └── attachment remove <task-id> <file-id>
├── project-config
│   ├── get <project-id>
│   └── set <project-id> [--task-statuses] [--task-types] [--default-view]
├── sandbox
│   ├── list [--status] [--type]
│   ├── get <id>
│   ├── create [--name] [--template-id] [--workspace-id]
│   ├── update <id> [--name]
│   ├── stop <id>
│   ├── delete <id>
│   ├── session list [--sandbox-id] [--status]
│   ├── session get <id>
│   ├── session create [--sandbox-id] [--task-id]
│   ├── session update <id> [--status]
│   ├── session stop <id>
│   └── session delete <id>
└── agent
    ├── list [--role]
    ├── get <id>
    ├── create [--name] [--role] [--description] [--sandbox-template-id]
    ├── update <id> [--name] [--role] [--description] [--sandbox-template-id]
    └── delete <id>
```

**Excluded from v0.1.0**: `ConnectSession`, `GetSessionRuntime`, `ReportSessionStatus`, `IssueSessionPat`, `IssueSessionJwt` (BE-BE only or streaming).

### Plug-and-Play Wiring

Each service package exports a single function:

```go
func NewCommand() *cobra.Command
```

`main.go` wires them all:

```go
root.AddCommand(
    auth.NewCommand(),
    workspace.NewCommand(),
    customer.NewCommand(),
    project.NewCommand(),
    file.NewCommand(),
    integration.NewCommand(),
    task.NewCommand(),
    projectconfig.NewCommand(),
    sandbox.NewCommand(),
    agent.NewCommand(),
)
```

Adding a new service = new package + one `AddCommand` line. Zero changes to existing packages.

### Partial Update Commands

`task update` and `sandbox session update` map to `SetPartialTask` / `SetPartialSession` (both use `common.v1.PartialData`). Named flags per field — only set flags are sent:

```bash
retask task update abc123 --status STATUS_DONE --priority HIGH
# Sends PartialData{id: "abc123", data: {"status": "STATUS_DONE", "priority": "HIGH"}}
```

---

## AI Agent Support

### JSON-first output

All output is JSON by default. Agents pipe directly to `jq` or parse in tool calls. `--pretty` is opt-in for humans.

### LLM-friendly `--help`

Every command's `Long` description follows a strict template:

```
Short one-line summary.

Usage example:
  retask task update <id> --status STATUS_DONE --priority HIGH

Flags:
  --status string    Task status. Values: STATUS_OPEN, STATUS_IN_PROGRESS, STATUS_DONE
  --priority string  Priority. Values: LOW, MEDIUM, HIGH, URGENT

Related commands:
  retask task get <id>
  retask task list --project-id <id>
```

Enum values and NRN formats are always listed explicitly in flag descriptions.

### `retask help --llm`

Prints a full JSON manifest of all commands, flags, and examples. Designed to be injected into an LLM system prompt or tool definition:

```json
{
  "cli": "retask",
  "version": "0.1.0",
  "auth": {
    "required_env": ["NWEB_API_KEY", "NWEB_WORKSPACE_ID"],
    "optional_env": ["NWEB_API_TOKEN", "NWEB_API_ENDPOINT"]
  },
  "commands": [
    {
      "command": "retask task list",
      "description": "List tasks in a project",
      "flags": ["--project-id", "--status", "--priority", "--assignee"],
      "example": "retask task list --project-id <id> --status STATUS_OPEN"
    }
  ]
}
```

### Skill file

`skills/retask-cli.md` (short by design — the CLI is self-describing):

```markdown
# retask CLI

Use `retask help --llm` to discover all available commands and flags.

Required env: NWEB_API_KEY, NWEB_WORKSPACE_ID
Optional env: NWEB_API_TOKEN (skip PAT exchange), NWEB_API_ENDPOINT

All output is JSON. Use --pretty for human-readable tables.
```

---

## Version

Defined in `internal/version/version.go`:

```go
var Version = "dev"
```

Overridden at build time:

```bash
go build -ldflags "-X nweb.xyz/retask-cli/internal/version.Version=0.1.0" ./cmd/retask
```

`retask --version` outputs `retask version 0.1.0`.

---

## Build Scripts

### `.bin/sync_proto.sh`

Copies approved proto files from local `api-contracts/` into `proto/`. Requires `api-contracts/` to be present locally (gitignored).

```bash
APPROVED_SERVICES=(
  "auth/v1" "common/v1" "customer/v1" "file/v1"
  "integration/v1" "project/v1"
  "retask/common/v1" "retask/agent/v1" "retask/project/v1"
  "retask/sandbox/v1" "retask/task/v1"
  "workspace/v1"
)
```

Only `*.proto` files at the service level are copied — no `event/`, `command/`, or `cron/` subdirs.

### `.bin/build_proto.sh`

Runs `buf generate .` then `protoc-go-inject-tag` on generated files. Uses the same `APPROVED_SERVICES` array as the single place to add new services.

---

## Proto Migration Steps

1. Run `git filter-repo --path api-contracts --invert-paths` to scrub `api-contracts/` from git history.
2. Run `.bin/sync_proto.sh` to populate `proto/` from local `api-contracts/`.
3. Update `buf.yaml`: `path: proto`.
4. Update `buf.gen.yaml`: `out: proto-gen`, `go_package_prefix: nweb.xyz/retask-cli/proto-gen`.
5. Run `.bin/build_proto.sh` to regenerate into `proto-gen/`.
6. Delete old `api-contracts-gen/` directory.
7. Verify `proto-gen/` has only the approved services.

---

## go.mod Dependencies

```
module nweb.xyz/retask-cli

go 1.23.4

require (
    github.com/spf13/cobra v1.8.0
    google.golang.org/grpc v1.63.0
    google.golang.org/protobuf v1.34.0
    gopkg.in/yaml.v3 v3.0.1
)
```
