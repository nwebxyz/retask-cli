# retask CLI

Use `retask help-llm` to get a full JSON manifest of all commands, flags, and examples.

## Quick start

```bash
export NWEB_API_KEY="nweb_pat_..."
export NWEB_WORKSPACE_ID="ws_..."
retask auth login

# Or for session-isolated credentials (shared sandboxes):
eval $(retask auth login --no-save)
```

## Required env
- `NWEB_API_KEY` — PAT (Personal Access Token, starts with `nweb_pat_`)
- `NWEB_WORKSPACE_ID` — Workspace ID

## Optional env
- `NWEB_API_TOKEN` — Ready-to-use JWT (skips PAT exchange)
- `NWEB_API_ENDPOINT` — API endpoint (default: `api.nweb.app:443`)
- `RETASK_PROFILE` — Config profile name (default: `default`)
- `RETASK_NO_PERSIST` — Don't write credentials to disk

## Output
All output is JSON by default. Add `--pretty` for human-readable tables.

## Discovery
```bash
retask help-llm           # full command manifest
retask <command> --help   # flags and examples for a specific command
```
