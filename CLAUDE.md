# CLAUDE.md

This file provides guidance to Claude Code when working with code in this repository.

## Overview

`retask-cli` is a public Go CLI (`github.com/nwebxyz/retask-cli`) for interacting with NWEB Retask APIs over gRPC. It authenticates via PAT → JWT exchange, supports multi-profile config, and is designed for both human operators and AI agents.

## Repository Structure

```
cmd/retask/main.go              # Entry point — wires all service commands
internal/
  auth/token.go                 # JWT resolver: env → cached → PAT exchange
  client/grpc.go                # gRPC connection factory (TLS, JWT interceptor)
  config/profile.go             # Load/save ~/.config/retask/config.yaml
  flags/flags.go                # Global flags struct
  output/output.go              # JSON and --pretty table renderer
  version/version.go            # Version var (set via ldflags)
  cmd/
    auth/                       # retask auth ...
    workspace/                  # retask workspace ...
    customer/                   # retask customer ...
    project/                    # retask project ...
    file/                       # retask file ...
    integration/                # retask integration ...
    task/                       # retask task ...
    projectconfig/              # retask project-config ...
    sandbox/                    # retask sandbox ...
    agent/                      # retask agent ...
    helpcmd/                    # retask help-llm
    skillcmd/                   # retask skill (prints embedded skill markdown)
embed.go                        # go:embed of skills/retask-cli.md (root package)
proto/                          # Protobuf sources (approved services only)
proto-gen/                      # Generated Go code — never edit by hand
.bin/
  build_proto.sh                # Regenerate proto-gen/ from proto/
  sync_proto.sh                 # Copy approved protos from local api-contracts/
buf.yaml                        # Buf config (source: proto/)
buf.gen.yaml                    # Code gen config (output: proto-gen/)
skills/retask-cli.md            # Claude Code skill file
```

## Commands

### Build

```bash
go build -ldflags "-X github.com/nwebxyz/retask-cli/internal/version.Version=0.1.0" -o retask ./cmd/retask/
```

### Run tests

```bash
go test ./...
```

### Regenerate proto code

```bash
./.bin/build_proto.sh
```

Requires [`buf`](https://buf.build/docs/installation) and [`protoc-go-inject-tag`](https://github.com/favadi/protoc-go-inject-tag).

## Security invariants — never break these

- **PAT (`NWEB_API_KEY`) is never stored.** It is read from env only when calling `auth.ExchangePat`. It is never written to the profile file or any other storage.
- **If `NWEB_API_TOKEN` is set, skip all PAT logic.** The token is used directly with no validation or exchange.
- **`--no-save` / `RETASK_NO_PERSIST` suppresses all file writes.** Commands must print `export NWEB_API_TOKEN=...` / `export NWEB_WORKSPACE_ID=...` lines instead.

## Code conventions

### Adding a new service command

1. Create `internal/cmd/<service>/command.go` with package `<service>`
2. Export `func NewCommand(gf *flags.Global) *cobra.Command`
3. Add one line to `cmd/retask/main.go`: `root.AddCommand(<service>cmd.NewCommand(gf))`
4. Add entries to `internal/cmd/helpcmd/command.go` manifest

### connect() pattern

Every service package uses the same pattern:

```go
func connect(gf *flags.Global) (servicev1.ServiceClient, func(), error) {
    path := gf.ConfigPath
    if path == "" { path = config.DefaultConfigPath() }
    cfg, err := config.Load(path)
    if err != nil { return nil, nil, err }
    profile := cfg.ActiveProfileData(gf.Profile)
    resolver := auth.NewResolver(profile, gf.Profile, gf.WorkspaceID, path, gf.NoSave, gf.Insecure)
    jwt, err := resolver.Token(context.Background())
    if err != nil { return nil, nil, err }
    conn, err := client.New(profile.Endpoint, jwt, gf.Insecure)
    if err != nil { return nil, nil, err }
    return servicev1.NewServiceClient(conn), func() { conn.Close() }, nil
}
```

### Partial updates

`task update` and `sandbox session update` use `commonv1.PartialData`. Use `cmd.Flags().Changed("flag-name")` to detect which flags were explicitly set:

```go
data := make(map[string]string)
if cmd.Flags().Changed("title") { data["title"] = title }
if cmd.Flags().Changed("status") { data["status"] = status }
svc.SetPartialTask(ctx, &commonv1.PartialData{Id: args[0], Data: data})
```

### Named return parameters

Functions with multiple return values must use named return parameters for clarity:

```go
// ✅ correct
func (b *SessionBootstrap) Run(ctx context.Context, conn *websocket.Conn) (sessionDir string, env []string, err error)

// ❌ wrong
func (b *SessionBootstrap) Run(ctx context.Context, conn *websocket.Conn) (string, []string, error)
```

### Help text template

Every command's `Long` description follows:

```
One-line summary.

Usage example:
  retask <command> <args> --flag value

Flags:
  --flag string  Description. Values: VALUE_A, VALUE_B
```

## Proto conventions

- Sources: `proto/` — organized by `<service>/v1/<service>.proto`
- Generated: `proto-gen/` — Go package prefix `github.com/nwebxyz/retask-cli/proto-gen`
- Never edit files in `proto-gen/` by hand
- To add a new approved service: add to the `APPROVED` array in `.bin/sync_proto.sh` and `APPROVED_SERVICES` in `.bin/build_proto.sh`, then re-run both scripts

### Approved proto services

| Service | Import path |
|---|---|
| auth | `proto-gen/auth/v1` |
| common | `proto-gen/common/v1` |
| customer | `proto-gen/customer/v1` |
| file | `proto-gen/file/v1` |
| integration | `proto-gen/integration/v1` |
| project | `proto-gen/project/v1` |
| quota | `proto-gen/quota/v1` (transitive dep of sandbox) |
| retask/agent | `proto-gen/retask/agent/v1` |
| retask/common | `proto-gen/retask/common/v1` |
| retask/project | `proto-gen/retask/project/v1` |
| retask/sandbox | `proto-gen/retask/sandbox/v1` |
| retask/task | `proto-gen/retask/task/v1` |
| workspace | `proto-gen/workspace/v1` |
