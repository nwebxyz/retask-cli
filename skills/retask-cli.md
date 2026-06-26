# retask CLI

Use `retask help-llm` to get a full JSON manifest of all commands, flags, and examples.

## When to use
Use the `retask` CLI whenever you're working with **Retask** (https://app.retask.work) — reading or changing workspaces, projects, tasks, sandboxes, sessions, agents, files, or integrations. Reach for it instead of guessing IDs or asking the user to copy data: the CLI can look it up.

**Task URLs → look up the task by key.** When the user mentions or pastes a Retask task URL of the form:

```
https://app.retask.work/w/<workspaceSlug>:<workspaceId>/tasks/<taskKey>
```

extract `<taskKey>` (e.g. `ENG-42`) and run:

```bash
retask task get-by-key ENG-42
```

The `<workspaceId>` segment of the URL is the workspace ID — pass it as `--workspace-id` (or set `NWEB_WORKSPACE_ID`) when a command needs workspace context, e.g. `retask task create`.

## Quick start

```bash
export NWEB_API_KEY="nweb_pat_..."
export NWEB_WORKSPACE_ID="ws_..."
retask auth login

# Or for session-isolated credentials (shared sandboxes):
eval $(retask auth login --no-save)
```

## Auth
retask resolves a JWT in priority order:
1. **`NWEB_API_TOKEN`** — if set, this ready-to-use JWT is used directly and PAT exchange is skipped entirely.
2. Otherwise **`NWEB_API_KEY`** (PAT, starts with `nweb_pat_`) is exchanged for a JWT, using a workspace ID from **`NWEB_WORKSPACE_ID`** (or `--workspace-id`, or the active profile). The PAT is never stored.

## Optional env
- `NWEB_API_ENDPOINT` — API endpoint (default: `api.nweb.app:443`)
- `NWEB_API_TRANSPORT` — Transport override (advanced)
- `RETASK_PROFILE` — Config profile name (default: `default`)
- `RETASK_NO_PERSIST` — Don't write credentials to disk

## Output
All output is JSON by default. Add `--pretty` for human-readable tables.

## Discovery
```bash
retask skill              # this onboarding guide (Markdown)
retask help-llm           # full command manifest (JSON)
retask <command> --help   # flags and examples (works at any nesting level,
                          #   e.g. `retask sandbox session create --help`)
```
