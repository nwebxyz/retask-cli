# Durable session lane: buffer + active reconnect

Design of record for the second iteration of PR "Durable private sessions (P1)".
It builds on the teardown contract (the agent PTY is stopped only by an explicit
`stop`/`delete`, never by a transport event) and adds two transport-layer
improvements to `retask sandbox connect`.

## Goals

1. **Active session-lane reconnect.** Both CLI↔sandbox-proxy lanes self-heal. The
   data lane already reconnects with exponential backoff; the session lane now
   does too, driven by the CLI, instead of passively waiting for the relay to
   re-send `new_session`.
2. **Durable outbound buffer.** While a session lane is down, PTY output is
   buffered (drop-oldest ring) instead of discarded, and flushed to the proxy on
   reconnect, so the viewer keeps the most recent output across the gap.

Both are transport-only: the runner is never stopped, started, or re-bootstrapped
by any of this.

## Key facts that shaped the design

- The session-lane **token is minted by sandbox-proxy and delivered over the data
  lane** in the `new_session` frame. It is **long-lived and reusable**, so the CLI
  can re-dial `/ws/session-lane` with the last token — no fresh token needed from
  the relay. This is what makes CLI-driven reconnect possible.
- PTY output is raw terminal bytes. Truncating mid-escape-sequence is acceptable:
  full-screen TUIs repaint on the re-attach `SIGWINCH` (a resize), and plain
  shells simply show limited scrollback.

## Components

### `sessionWriter` (`sessionwriter.go`)

The runner's **permanent** output sink, set once at `create()` and never
re-pointed (this replaces the old per-attach `SetOutput(wsWriter)` /
`SetOutput(io.Discard)` swapping). Behind one mutex it holds the current conn
(nil when detached) and a bounded ring buffer:

- **Write** — attached: frame + send to the conn; on a send error, close the conn
  (to wake the read pump), drop it, and buffer the chunk. Detached: buffer. Always
  returns success — a dead socket never stalls or errors PTY output.
- **attach(conn)** — flush the buffer to `conn` **in order** (chunked, bounded by a
  timeout), then adopt it, so buffered bytes precede any live write. On flush
  failure the remainder is re-staged and the conn is rejected.
- **detach()** — stop sending; subsequent writes buffer.

Buffering is **outbound only** (PTY → proxy). Inbound keystrokes are not buffered
because a down socket delivers none.

### `ringBuffer` (`ringbuffer.go`)

Bounded FIFO of raw bytes; **drop-oldest** so it always holds the most recent
`cap` bytes. `cap == 0` disables buffering (drop immediately). Not concurrency-
safe on its own; `sessionWriter` guards it.

### `sessionEntry` (`sessionentry.go`)

Owns the runner + writer and the authoritative current conn. Its lock-guarded
state machine is unchanged in spirit; `reattach` now flushes the buffer and swaps
the conn **atomically under the entry lock**, so a stale detach can never clobber a
fresh attach. Adds a single-slot `reconnecting` guard and the freshest
token/geometry for a CLI re-dial. `conn` is **not** cleared by a transient send
error — only by `detachIfCurrent` / `reap` / a successful `reattach` — so the read
side stays the source of truth for "is this lane live".

### Reconnect loop (`sessionlane.go`)

On a real drop, `readPump`'s `detachIfCurrent` returns true only for the current
conn (a stale conn's drop is a no-op), and starts **one** reconnect goroutine
(`beginReconnect` guarantees at most one, and none after reap). The loop re-dials
with the shared exponential backoff (2s → 30s, reset on success) using the stored
token; on success `bindReattach` flushes the buffer and resumes streaming. It
exits on reap/stop, on the process context cancelling, or when a relay
`new_session` re-attached first (both paths converge through `bindReattach`).

## Data flow (drop → recover)

```
socket drops
  → readPump detach → writer buffers (drop-oldest)
  → reconnect loop re-dials with backoff (same long-lived token)
  → on success: flush buffered tail in order, then live output resumes
     (full-screen TUIs also repaint via the re-attach SIGWINCH)
```

## Config

`--session-buffer` on `sandbox connect` (env `RETASK_SANDBOX_SESSION_BUFFER`,
flag wins). Default `10MB`, per session; `0` disables buffering. Accepts binary
sizes (`512KB`, `10MB`, `1GB`). Worst-case memory ≈ buffer × concurrently-detached
sessions. The reconnect cadence is not configurable — both lanes share the data
lane's backoff.

## Trade-offs / known limits

- The buffer flush runs under the entry lock (bounded by a 10s timeout), so a
  delete racing a very large flush to a slow peer can wait briefly. Chosen for
  correctness — flush + conn-swap must be atomic to preserve the anti-clobber
  invariant.
- Drop-oldest truncation can cut an escape sequence; acceptable per the TUI
  repaint / shell-scrollback reasoning above.
- Live end-to-end (real proxy + private sandbox) is still pending, as noted in the
  PR — the reconnect + flush path over a real WebSocket/PTY needs an integration
  pass. Unit tests cover the ring buffer, the writer (incl. live socket), the
  entry state machine, and the reconnect loop's control flow via an injected
  dialer.
```
