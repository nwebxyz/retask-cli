# Private VM Session Setup Design

**Date:** 2026-06-19
**Scope:** `retask sandbox connect` — session setup for Private VM sandboxes

---

## Background

`retask sandbox connect <sandboxID>` maintains a persistent WebSocket (data lane) to
`sandbox-proxy` and manages sessions as local PTY processes via `agentfleet`. Until now,
session setup (git repos, system prompt, env vars) was only implemented for Cloud sandboxes
inside `sandbox-proxy`'s `setupSession` / `lib.sh`. Private VM sessions received a bare
`init_command` with no bootstrapping.

PR #18 in `sandbox-proxy` changed the `new_session` message format — replacing
`init_command` + `env` with a full `Sandbox_Config` JSON object — and introduced the
session-folder approach (`/workspace/session-<id>/`) for Cloud. This design extends
equivalent bootstrapping to Private VM sessions, implemented entirely in Go inside
`retask-cli`.

---

## Goals

1. Parse the updated `new_session` message (`config` field replacing `init_command`/`env`).
2. Create an isolated `session-<id>/` working folder per session.
3. Clone configured git repos (shallow, with retry and graceful degradation).
4. Write the system prompt (CLAUDE.md / AGENTS.md) built by sandbox-proxy.
5. Set per-session env vars (host env + user config + injected session vars).
6. Stream setup progress to the FE terminal so users see what is happening.
7. Clean up the session folder on delete.

---

## Protocol Changes

### `new_session` message (sandbox-proxy → CLI)

**Before (stale):**
```json
{
  "type": "new_session",
  "session_id": "...",
  "token": "...",
  "new_session": {
    "name": "...",
    "init_command": "...",
    "env": { "KEY": "value" }
  }
}
```

**After:**
```json
{
  "type": "new_session",
  "session_id": "...",
  "token": "...",
  "new_session": {
    "name": "...",
    "config": { /* Sandbox_Config proto JSON (camelCase) */ },
    "system_prompt": "# Current context\n...",
    "seed_prompt": "..."
  }
}
```

`config` is the proto JSON serialization of `Sandbox_Config` (camelCase field names, as
produced by `@bufbuild/protobuf`'s `toJson`). `system_prompt` is the fully-rendered
CLAUDE.md content built by `buildSystemPrompt(runtime)` in sandbox-proxy. `seed_prompt`
is `session.seedPrompt`.

### Go struct (`datalane.go`)

```go
type dataLaneMsgNewSession struct {
    Name         string          `json:"name,omitempty"`
    Config       json.RawMessage `json:"config,omitempty"`
    SystemPrompt string          `json:"system_prompt,omitempty"`
    SeedPrompt   string          `json:"seed_prompt,omitempty"`
}
```

`Config` is held as `json.RawMessage` and deserialized with `protojson.Unmarshal` into
`*sandboxv1.Sandbox_Config` to handle camelCase proto JSON correctly.

---

## sandbox-proxy Changes

### `src/index.ts` — PRIVATE terminal path

After `getSessionRuntime` succeeds for a `Sandbox_Type.PRIVATE` sandbox, build the system
prompt and seed prompt and forward them as headers to the `PrivateSandboxRelay` DO:

```ts
import { buildSystemPrompt } from './systemPrompt';

// existing: build configJson from resp.sandbox.config
const systemPrompt = buildSystemPrompt(resp!);
const seedPrompt   = resp?.session?.seedPrompt ?? '';

headers.set('x-system-prompt', systemPrompt);
headers.set('x-seed-prompt',   seedPrompt);
// x-sandbox-config header unchanged
```

The same `buildSystemPrompt(runtime)` function is used by both Cloud (written to container
in `setupSession`) and Private VM (forwarded to CLI). Both paths produce identical prompts.

### `src/privateSandboxRelay.ts` — `handleTerminal`

Read the new headers and include them in the `new_session` message sent to the Private VM:

```ts
const systemPrompt = request.headers.get('x-system-prompt') ?? '';
const seedPrompt   = request.headers.get('x-seed-prompt')   ?? '';

this.vmSocket.send(JSON.stringify({
  type: 'new_session',
  session_id: sessionId,
  token: sessionToken,
  new_session: {
    name: sessionName,
    config: sandboxConfig,
    system_prompt: systemPrompt,
    seed_prompt:   seedPrompt,
  },
}));
```

**Cloud path (`setupSession` / `setupClaudeCode`) — no changes.** Cloud continues to build
and write CLAUDE.md directly to the container.

---

## retask-cli Changes

### Session lane connect order

Currently: start PTY → connect session lane → bridge.

New order: **connect session lane → bootstrap (with log streaming) → start PTY → bridge**.

The session lane WebSocket is connected first so bootstrap can stream progress lines to
the FE as terminal output data frames, giving users real-time visibility into setup.

### New file: `internal/cmd/sandbox/sessionBootstrap.go`

`SessionBootstrap` performs all per-session setup steps before the PTY starts.

```go
type SessionBootstrap struct {
    SessionID    string
    SessionName  string
    SandboxID    string
    SandboxName  string
    WorkspaceID  string
    Config       *sandboxv1.Sandbox_Config
    SystemPrompt string
    SeedPrompt   string
    JWT          string
    Endpoint     string
    BaseDir      string  // cwd where `retask sandbox connect` was run
    Log          *slog.Logger
}

// Run performs setup and returns (sessionDir, envSlice, error).
// Streams progress to conn as terminal output data frames.
// On ErrAborted the caller must close the session lane without starting a PTY.
func (b *SessionBootstrap) Run(ctx context.Context, conn *websocket.Conn) (sessionDir string, env []string, err error)
```

#### Setup sequence inside `Run`

**Step 1 — Create session folder**
```
os.MkdirAll(filepath.Join(baseDir, "session-"+sessionID), 0755)
```

**Step 2 — Write agent configs**
- `session-<id>/CLAUDE.md` ← `SystemPrompt`
- `session-<id>/AGENTS.md` ← same content (copy, not symlink, for portability)
- `session-<id>/.claude/settings.json` ← project-scoped Claude Code config:
  ```json
  {
    "skipDangerousModePermissionPrompt": true,
    "enabledPlugins": {"superpowers@claude-plugins-official": true}
  }
  ```
  This is project-scoped (inside session folder) and does **not** touch
  `~/.claude/settings.json` on the user's machine.

**Step 3 — Clone git repos (with retry + interactive menu)**

```
outer:
for {
    err = setupGitRepos(ctx, conn, config.GitRepos, sessionDir)
    if err == nil { break }

    writeTerm(conn, "\nGit repo setup failed.\n  1) Retry\n  2) Continue\n  3) Exit\n")
    choice = readChoice(ctx, conn)
    switch choice {
    case 1: continue outer       // retry
    case 2: break outer          // proceed without repos
    case 3: return ErrAborted    // PTY never starts
    }
}
```

`setupGitRepos` retries each clone up to 3 times with 1 s backoff (mirroring
`git_repos.sh`'s `retry 3`):

```
for each repo in config.GitRepos:
    branch = repo.Branch or "main"
    dest   = filepath.Join(sessionDir, targetDir(repo))
    for attempt := 1; attempt <= 3; attempt++:
        writeTerm(conn, fmt.Sprintf("[repos] cloning %s (attempt %d/3)...", repo.URL, attempt))
        run: git clone --depth=1 -b branch url dest
        if ok: break
        if attempt < 3: sleep 1s
    if all 3 failed: return error
```

A GitHub token found in `config.EnvVars` (key `GITHUB_TOKEN` or `GH_TOKEN`) is injected
into the HTTPS URL before cloning so private repos work without requiring the user to have
the token in their host git config.

**Step 4 — Build env slice**

Three layers applied in order (later layers override earlier):

| Layer | Source |
|---|---|
| Host machine env | `os.Environ()` |
| User config vars | `config.EnvVars` — `Secret.Value` if set, else `Plain` |
| Injected session vars | See table below |

Injected session vars (always override):

| Var | Value |
|---|---|
| `SESSION_ID` | sessionID |
| `NWEB_API_TOKEN` | JWT from resolver |
| `NWEB_WORKSPACE_ID` | `sbResp.Msg.WorkspaceId` |
| `NWEB_API_ENDPOINT` | `profile.Endpoint` |
| `NWEB_API_TRANSPORT` | `http` |
| `RETASK_NO_PERSIST` | `true` |
| `IS_SANDBOX` | `1` |
| `CLAUDE_CODE_EFFORT_LEVEL` | `xhigh` |
| `SEED_PROMPT` | `SeedPrompt` |

**Step 5 — Success**
```
writeTerm(conn, "Session ready.\n")
return sessionDir, env, nil
```

#### Terminal I/O helpers

`writeTerm(conn, text)` encodes `text` as a base64 data frame and writes it to the session
lane WebSocket — identical in format to PTY output, so the FE terminal renders it directly.

`readChoice(ctx, conn)` reads data frames from the session lane (skipping resize frames),
decodes each keystroke, echoes it back, and returns an integer (1/2/3) when Enter is
pressed. Returns `2` (continue) on any read error so a disconnected client doesn't hang
the session forever.

### `internal/cmd/sandbox/sessionlane.go`

`SessionManager.Start()` signature change — replace `initCommand string, env map[string]string`
with `configJSON json.RawMessage, systemPrompt, seedPrompt string`:

```
old: Start(ctx, sessionID, token, name, initCommand, env)
new: Start(ctx, sessionID, token, name, configJSON, systemPrompt, seedPrompt)
```

New execution order inside `Start`:

1. Deserialize `configJSON` with `protojson.Unmarshal` → `*sandboxv1.Sandbox_Config`
2. Connect session lane WebSocket (using `token`)
3. Construct `SessionBootstrap` and call `Run(ctx, conn)`
   - On `ErrAborted`: close session lane, return
   - On other error: log and return (session lane already closed by defer)
4. Start PTY: `agentfleet.NewPtyAgent([]string{"sh", "-c", config.SessionInitCommand}, agCfg)`
   with `CWD = sessionDir`, `Env = envSlice`
5. Bridge PTY ↔ session lane (existing `readLoop` / `wsWriter` logic, unchanged)

### `internal/cmd/sandbox/connect.go`

Pass additional fields to `SessionManager` constructor:

- `workspaceID` ← `sbResp.Msg.WorkspaceId`
- `sandboxName` ← `sbResp.Msg.Name`
- `baseDir` ← `os.Getwd()` (captured once at connect time)
- `jwt`, `endpoint` ← from resolver / profile (already available)

### `internal/cmd/sandbox/datalane.go`

- Update `dataLaneMsgNewSession` struct (see Protocol Changes section above)
- Update `new_session` dispatch to call `sm.Start(..., msg.NewSession.Config, msg.NewSession.SystemPrompt, msg.NewSession.SeedPrompt)`

### Session folder cleanup

`SessionManager.Remove(sessionID)` (called on `delete_session`) — add after stopping PTY:
```go
os.RemoveAll(filepath.Join(sm.baseDir, "session-"+sessionID))
```

`StopAll` (called on `delete_sandbox`) calls `Stop` per session, not `Remove`, so folders
are left in place when the sandbox is merely stopped. `Remove` is only called on explicit
deletion, which permanently cleans up the session folder.

---

## File Change Summary

| Repo | File | Change |
|---|---|---|
| `sandbox-proxy` | `src/index.ts` | Build + forward `x-system-prompt`, `x-seed-prompt` headers for PRIVATE path |
| `sandbox-proxy` | `src/privateSandboxRelay.ts` | Read new headers, add `system_prompt` + `seed_prompt` to `new_session` message |
| `retask-cli` | `internal/cmd/sandbox/datalane.go` | Update `dataLaneMsgNewSession` struct; pass new fields to `Start()` |
| `retask-cli` | `internal/cmd/sandbox/sessionlane.go` | New `Start()` signature; connect session lane before bootstrap; call bootstrap |
| `retask-cli` | `internal/cmd/sandbox/sessionBootstrap.go` | **New file** — all bootstrap logic |
| `retask-cli` | `internal/cmd/sandbox/connect.go` | Pass `workspaceID`, `sandboxName`, `baseDir`, `jwt`, `endpoint` to `SessionManager` |

---

## Non-goals

- Startup command (`config.StartupCommand`) is not run on Private VM — users manage their
  own machine setup.
- `~/.claude.json` (global onboarding suppressor) is not written — the user's machine
  config is left untouched.
- Session replay (R2 storage) is Cloud-only and not replicated here.
- Seed prompt delivery to the agent (beyond setting `SEED_PROMPT` env var) is not in scope.
