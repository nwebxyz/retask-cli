# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

`retask-cli` is a Go module (`nweb.xyz/retask-cli`) that houses the **API contract definitions** (Protobuf) and their **generated Go code** for the NWEB platform, with a focus on the Retask.work services.

## Repository Structure

```
api-contracts/          # Git subtree — source of truth for all .proto files
  proto/                # Protobuf definitions organized by service
  docs/architecture/    # Architecture design docs
api-contracts-gen/      # Generated Go code (never edit by hand)
buf.yaml                # Buf module config (proto source: api-contracts/proto)
buf.gen.yaml            # Code generation config (Go + gRPC + grpc-gateway)
.bin/build_proto.sh     # Build script
```

`api-contracts/` is embedded as a git subtree from `git@github.com:nwebxyz/api-contracts.git`.

## Commands

### Generate protobuf code

```bash
./.bin/build_proto.sh
```

This runs `buf generate .` then `protoc-go-inject-tag` on all generated `.pb.go` files. Requires `buf` and `protoc-go-inject-tag` to be installed.

### Sync api-contracts subtree

```bash
# Pull latest from api-contracts repo
git subtree pull --prefix api-contracts git@github.com:nwebxyz/api-contracts.git main --squash

# Push changes back to api-contracts repo
git subtree push --prefix api-contracts git@github.com:nwebxyz/api-contracts.git main
```

## Proto Conventions (enforced by api-contracts/CLAUDE.md)

**Never edit files in `api-contracts-gen/`** — they are always regenerated from proto sources.

### HTTP URL conventions
- Nested resources: `GET /v1/workspaces/{workspace_id}/members`
- Custom methods use `:` separator per AIP-136: `POST /retask/v1/projects/{id}:archive`
- Path param is `{id}` for `common.v1.Id`, or the actual field name otherwise

### RPC naming
- List: `rpc GetCustomers(CustomersRequest) returns (CustomersResponse)` — plural for both
- Single by ID: `rpc GetCustomer(common.v1.Id) returns (Customer)` — entity directly
- Never prefix message types with `Get` (verb belongs on the RPC, not the message)

### Message conventions
- Audit fields (`created_by_nrn`, `updated_by_nrn`, `created_at`, `updated_at`) required on all stored messages
- Soft delete required by default: `is_deleted` + `deletion_info` fields; soft-deleted items invisible to API
- Filter fields use `repeated` for multi-select (exception: `workspace_id` stays singular)
- Nested types: define inside the message that directly uses them; don't repeat parent name in nested type name
- Enum values must be prefixed with enum name in UPPER_SNAKE_CASE (e.g., `THEME_PREFERENCE_LIGHT`)

### Proto service domains
- `retask/` — Retask.work: `common/v1`, `project/v1`, `task/v1`, `sandbox/v1`, `agent/v1`
- `ai/` — NWEB.AI: chat, credit, payment, project, report, ocr, agent
- Root-level services: auth, workspace, subscription, quota, cron, file, message, secret, customer