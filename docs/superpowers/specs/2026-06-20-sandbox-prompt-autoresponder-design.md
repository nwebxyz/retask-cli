# Sandbox PTY prompt auto-responder

**Date:** 2026-06-20
**Status:** Approved — ready for implementation

## Problem

When a Private VM sandbox session starts, the bootstrap creates a fresh
`session-<id>` directory and launches the agent (e.g. Claude Code) inside it.
Claude Code shows a one-time **folder-trust dialog** on every session:

```
Quick safety check: Is this a project you created or one you trust? ...
Claude Code'll be able to read, edit, and execute files here.
```

This blocks unattended sessions. The trust acceptance is persisted per-directory
in the user-level `~/.claude.json`, not in the project's `.claude/settings.json`,
so the project-level `skipDangerousModePermissionPrompt` we already write cannot
suppress it. Because each session uses a brand-new directory, the prompt fires
every time.

The CLOUD path (`sandbox-proxy/src/setupAgents.ts`) solves this by seeding
`/root/.claude.json` with trust state. We **cannot** reuse that approach here:
`retask-cli` runs in many environments (real developer machines, different
agents), so mutating a shared global config is unacceptable.

## Approach

An "expect"-style auto-responder: watch the agent's PTY output stream for a
known prompt and inject the accept keystroke into the PTY's stdin. This touches
no global config and works for any agent, given a matching rule.

### Architecture

One new file `internal/cmd/sandbox/promptResponder.go`, plus wiring in
`sessionlane.go` and a flag/env in `connect.go`. No agentfleet changes.

```
PTY stdout ─▶ promptResponder.Write ─▶ wsWriter ─▶ WebSocket ─▶ user terminal
                     │  (pass-through, unaltered)
                     │  scans normalized rolling buffer for a rule match
                     ▼
              r.StdinWriter()  ◀── injects rule.send once, then drops the rule
                     ▲
              readLoop also writes here (real keystrokes) — existing path
```

### Components

1. **`promptResponder`** — an `io.Writer` wrapping the inner sink (`wsWriter`),
   holding `r.StdinWriter()`, a rolling buffer (cap ~8 KB), the active rule
   slice, a mutex, and a logger.

   `Write(p)` logic:
   ```
   if len(rules) == 0:          // quick bypass once all rules fired / none configured
       return inner.Write(p)
   n, err := inner.Write(p)     // always pass through, unaltered
   append p to rolling buffer (trim to cap); norm = normalize(buffer)
   for each active rule:
       if strings.Contains(norm, rule.match):
           stdin.Write(rule.send)   // best-effort; log on error, never fail Write
           remove rule from active slice   // auto-deregister
   return n, err
   ```

2. **`normalize(buf) string`** — robust matching against terminal noise (ANSI
   escapes, box-drawing `│─`, one-word-per-line wrapping). Lowercases, replaces
   every rune not in `[a-z0-9 ]` with a space, collapses whitespace runs. So
   `"…│ execute │\n│ files here │"` → `"execute files here"`.

3. **`rule` + `defaultPromptRules()`** — `rule{name, match, send string}`.
   Ships exactly one rule today; extending = append a struct:
   ```go
   {name: "claude-trust", match: "execute files here", send: "\r"}
   ```
   `match` is a normalized, distinctive anchor from the trust dialog text
   ("…read, edit, and execute files here").

### Disable switch (default: feature ON)

Mirrors the existing `--auto-open` / `RETASK_SANDBOX_AUTO_OPEN_SESSION` pattern.

- CLI flag on `retask sandbox connect`: `--no-auto-respond` (bool, default false)
- Env: `RETASK_SANDBOX_NO_AUTO_RESPOND=1`
- Combined: `disabled := noAutoRespond || os.Getenv("RETASK_SANDBOX_NO_AUTO_RESPOND") == "1"`
- Threaded into `newSessionManager` as a stored bool. In `Start()`: if disabled,
  set output to the plain `wsWriter` (current behavior, zero overhead); if
  enabled, wrap it in `promptResponder` with `defaultPromptRules()`.

### Risky assumption (verify during implementation)

`send: "\r"` accepts the **highlighted default** option of the trust dialog,
assumed to be "Yes, proceed". If the default is the safe/No option, Enter would
dismiss wrongly. `send` is a per-rule constant, trivially changed to `"1\r"`
(explicit option) or an arrow sequence. Verify against a live PTY output capture
before considering the feature done.

### Error handling & safety

- Fire-once per rule via auto-deregister, so dialog redraw animations don't
  re-trigger.
- Pass-through never alters or blocks user output.
- Injection errors are logged, never propagated — must never break a session.
- Concurrent stdin writes (injection vs. real keystrokes) only overlap at
  startup before the user can type; risk negligible. Mutex guards the
  responder's own mutable state.

## Testing (TDD)

- `normalize` strips ANSI + box chars + collapses wrapped text.
- Responder injects `rule.send` into stdin when the noisy, word-wrapped trust
  prompt flows through; passes output through byte-identical.
- A fired rule is removed; it does not fire twice across multiple `Write` calls /
  redraws.
- Empty rule list short-circuits to pure pass-through (no stdin writes).
- Does not fire on unrelated output.

## Out of scope

- Per-action permission prompts (already suppressed by
  `--dangerously-skip-permissions` / `skipDangerousModePermissionPrompt`).
- Seeding or mutating any agent's global config.
- Codex / other agent rules (the rule table is built to add them later).
