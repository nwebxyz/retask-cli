package sandbox

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/coder/websocket"
	agentfleet "github.com/hoaitan/agentfleet"
	"google.golang.org/protobuf/encoding/protojson"

	sandboxv1 "github.com/nwebxyz/retask-cli/proto-gen/retask/sandbox/v1"
)

// sessionDrainTimeout bounds how long teardown waits for a session's PTY to
// exit after SIGTERM before deleting its folder anyway.
const sessionDrainTimeout = 5 * time.Second

// stoppableRunner is the slice of *agentfleet.Runner that teardown needs, so
// drain can be tested without a real PTY.
type stoppableRunner interface {
	Stop() error
	Done() <-chan struct{}
}

// drain sends SIGTERM and waits for the process to actually exit, up to
// timeout. agentfleet's Stop returns as soon as the signal is delivered, not
// when the process has exited — so deleting a session folder straight after it
// races the agent's own shutdown, destroying the working directory while the
// agent is still flushing into it. SIGTERM exists to grant that grace period.
//
// A process that ignores SIGTERM is never escalated to SIGKILL by agentfleet,
// so the timeout is the only backstop against a hung session blocking teardown.
func drain(r stoppableRunner, timeout time.Duration) {
	if r == nil {
		return
	}
	r.Stop() //nolint:errcheck
	select {
	case <-r.Done():
	case <-time.After(timeout):
	}
}

// wsDialer dials a WebSocket URL. It is a field on SessionManager so tests can
// inject a fake in place of the real network dial.
type wsDialer func(ctx context.Context, url string) (*websocket.Conn, error)

func defaultWSDial(ctx context.Context, url string) (*websocket.Conn, error) {
	c, _, err := websocket.Dial(ctx, url, nil)
	// A dial error quotes the URL it tried, JWT and all. Scrub it here so no
	// caller can leak the token into the log panel or retask.log.
	return c, redactErr(err)
}

// SessionManager creates and tracks one agentfleet Runner per active sandbox session.
type SessionManager struct {
	sandboxID   string
	wsBase      string
	fleet       *agentfleet.Fleet
	fleetCfg    agentfleet.FleetConfig
	agentCfg    agentfleet.AgentConfig
	log         *slog.Logger
	workspaceID string
	sandboxName string
	baseDir     string
	endpoint    string
	sessionLog  *sessionLog // records session start times for retention
	autoRespond bool        // auto-accept known agent prompts (e.g. folder-trust)

	// sessionBufBytes is the per-session outbound buffer retained across a
	// session-lane drop (drop-oldest). 0 disables buffering.
	sessionBufBytes int
	// dial establishes a session-lane WebSocket; swappable for tests.
	dial wsDialer
	// Reconnect backoff bounds (fields, not consts, so tests can shrink them).
	reconnectInitial time.Duration
	reconnectMax     time.Duration

	mu       sync.Mutex
	sessions map[string]*sessionEntry // keyed by session_id
	creating map[string]struct{}      // session ids with a create() in flight
}

func newSessionManager(
	sandboxID, wsBase string,
	fleet *agentfleet.Fleet,
	fleetCfg agentfleet.FleetConfig,
	agentCfg agentfleet.AgentConfig,
	log *slog.Logger,
	workspaceID, sandboxName, baseDir, endpoint string,
	sessLog *sessionLog,
	autoRespond bool,
	sessionBufBytes int,
) *SessionManager {
	return &SessionManager{
		sandboxID:        sandboxID,
		wsBase:           wsBase,
		fleet:            fleet,
		fleetCfg:         fleetCfg,
		agentCfg:         agentCfg,
		log:              log,
		workspaceID:      workspaceID,
		sandboxName:      sandboxName,
		baseDir:          baseDir,
		endpoint:         endpoint,
		sessionLog:       sessLog,
		autoRespond:      autoRespond,
		sessionBufBytes:  sessionBufBytes,
		dial:             defaultWSDial,
		reconnectInitial: reconnectInitialBackoff,
		reconnectMax:     reconnectMaxBackoff,
		sessions:         make(map[string]*sessionEntry),
		creating:         make(map[string]struct{}),
	}
}

// sessionLaneURL builds the session-lane WebSocket URL for a (session, token).
func (sm *SessionManager) sessionLaneURL(sessionID, token string) string {
	return fmt.Sprintf("%s/ws/session-lane?sandbox_id=%s&session_id=%s&token=%s",
		sm.wsBase, sm.sandboxID, sessionID, token)
}

// recordSessionStart logs when a session started, before bootstrap runs.
// Bootstrap creates the folder early but can fail afterwards; under the
// log-only retention policy a folder with no entry could never be reaped, so
// the entry must exist as soon as the folder can.
func (sm *SessionManager) recordSessionStart(sessionID, name string, at time.Time) {
	if sm.sessionLog == nil {
		return
	}
	if err := sm.sessionLog.record(sessionID, name, "session-"+sessionID, at); err != nil {
		sm.logError("session_log_record_failed", "session_id", sessionID, "error", err)
	}
}

// isActive reports whether a session currently has a live runner. The retention
// sweeper uses it so a long-running session is never reaped.
func (sm *SessionManager) isActive(sessionID string) bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	_, ok := sm.sessions[sessionID]
	return ok
}

// Start handles a new_session event. It is IDEMPOTENT: if session_id already
// has a live runner (the relay lost its state, or the VM data-lane reconnected
// and the relay re-sent new_session), re-bind a fresh session-lane to the
// existing PTY instead of creating a second one.
func (sm *SessionManager) Start(ctx context.Context, sessionID, token, name string, configJSON json.RawMessage, systemPrompt, seedPrompt string, cols, rows int) {
	sm.mu.Lock()
	entry := sm.sessions[sessionID]
	if entry != nil {
		sm.mu.Unlock()
		sm.attach(ctx, entry, sessionID, token, cols, rows)
		return
	}
	if _, inflight := sm.creating[sessionID]; inflight {
		// A create() for this session is already bootstrapping. A duplicate
		// new_session must not spawn a second PTY — drop it.
		sm.mu.Unlock()
		sm.logInfo("new_session_ignored_creating", "session_id", sessionID)
		return
	}
	sm.creating[sessionID] = struct{}{}
	sm.mu.Unlock()
	sm.create(ctx, sessionID, token, name, configJSON, systemPrompt, seedPrompt, cols, rows)
}

func (sm *SessionManager) clearCreating(sessionID string) {
	sm.mu.Lock()
	delete(sm.creating, sessionID)
	sm.mu.Unlock()
}

// Reattach answers the relay's reconnect_session: re-bind a session lane to a
// PTY this process is already running. It NEVER bootstraps or spawns a PTY —
// that is what new_session is for.
//
// It reports whether this process has the session. false means the session is
// gone from here (this is a restarted `retask sandbox connect`, or the PTY has
// exited), and the caller must tell the relay so, rather than letting a stale
// session id quietly acquire a brand-new PTY.
//
// A session still inside create() counts as present: its own lane dial is
// already on the way, so reporting it gone would tell the viewer a session that
// is merely still starting has died.
//
// The frame carries no token, and none is needed: a session-lane token is
// minted once, at new_session, and this process stored it then. We re-dial with
// our own copy — the relay holds the matching one for the life of the session,
// across its own restarts.
func (sm *SessionManager) Reattach(ctx context.Context, sessionID string, cols, rows int) bool {
	sm.mu.Lock()
	entry := sm.sessions[sessionID]
	_, creating := sm.creating[sessionID]
	sm.mu.Unlock()

	if entry == nil {
		return creating
	}
	token, _, _ := entry.reconnectParams()
	sm.attach(ctx, entry, sessionID, token, cols, rows)
	return true
}

// attach re-binds a fresh session-lane to an already-running runner. It never
// bootstraps or spawns a new PTY. The token is the long-lived session-lane
// token carried by the new_session / reconnect_session frame.
func (sm *SessionManager) attach(ctx context.Context, entry *sessionEntry, sessionID, token string, cols, rows int) {
	// Record the freshest token/geometry so a later CLI-driven re-dial uses them.
	entry.setReconnectParams(token, cols, rows)
	wsConn, err := sm.dial(ctx, sm.sessionLaneURL(sessionID, token))
	if err != nil {
		sm.logError("session_reattach_error", "session_id", sessionID, "error", err)
		return
	}
	sm.logInfo("session_lane_reattached", "sandbox_id", sm.sandboxID, "session_id", sessionID)
	if !sm.bindReattach(ctx, entry, sessionID, wsConn, cols, rows) {
		wsConn.CloseNow() //nolint:errcheck
	}
}

// bindReattach flushes buffered output to wsConn, adopts it as the session
// lane, and starts a read pump — the shared tail of both the relay-driven
// attach and the CLI reconnect loop. It returns false (and leaves wsConn for the
// caller to close) if the entry was reaped or the buffer flush failed.
func (sm *SessionManager) bindReattach(ctx context.Context, entry *sessionEntry, sessionID string, wsConn *websocket.Conn, cols, rows int) bool {
	old, ok, err := entry.reattach(wsConn)
	if err != nil {
		// The fresh conn couldn't take the buffered backlog; caller retries.
		sm.logError("session_reattach_flush_failed", "session_id", sessionID, "error", err)
		return false
	}
	if !ok {
		// The entry was reaped (its PTY exited). Do not bind onto a dead runner;
		// the client will reconnect and get a fresh session via create.
		sm.logInfo("session_reattach_reaped", "session_id", sessionID)
		return false
	}
	if old != nil {
		old.CloseNow() //nolint:errcheck
	}
	if cols > 0 && rows > 0 {
		entry.runner.Resize(rows, cols) //nolint:errcheck
	}
	go sm.readPump(ctx, entry, sessionID, wsConn)
	return true
}

// create bootstraps a brand-new session: connects a session lane, runs
// bootstrap, launches a new PTY, and bridges it. Called only when session_id is
// not already tracked.
func (sm *SessionManager) create(ctx context.Context, sessionID, token, name string, configJSON json.RawMessage, systemPrompt, seedPrompt string, cols, rows int) {
	defer sm.clearCreating(sessionID)
	if name == "" {
		name = sessionID
	}

	// Parse Sandbox_Config from proto JSON (camelCase field names).
	var cfg sandboxv1.Sandbox_Config
	if len(configJSON) > 0 {
		if err := protojson.Unmarshal(configJSON, &cfg); err != nil {
			sm.logError("session_config_parse_error", "session_id", sessionID, "error", err)
			return
		}
	}

	// Connect session lane first so bootstrap can stream logs to the FE.
	wsConn, err := sm.dial(ctx, sm.sessionLaneURL(sessionID, token))
	if err != nil {
		sm.logError("session_lane_error", "session_id", sessionID, "error", err)
		return
	}
	sm.logInfo("session_lane_connected", "sandbox_id", sm.sandboxID, "session_id", sessionID)

	// Record before bootstrap: setupFolder creates the folder early but Run can
	// fail later, and an unlogged folder can never be reaped.
	sm.recordSessionStart(sessionID, name, time.Now())

	// pending latches window sizes that arrive before the PTY exists: the
	// initial geometry reported in the new_session frame, or a resize that
	// races bootstrap. It is flushed once the PTY is up.
	pending := &pendingSize{}
	if cols > 0 && rows > 0 {
		pending.store(rows, cols)
	}

	// Run bootstrap — writes files, clones repos, builds env.
	sb := &SessionBootstrap{
		SessionID:    sessionID,
		SessionName:  name,
		SandboxID:    sm.sandboxID,
		SandboxName:  sm.sandboxName,
		WorkspaceID:  sm.workspaceID,
		Config:       &cfg,
		SystemPrompt: systemPrompt,
		SeedPrompt:   seedPrompt,
		Endpoint:     sm.endpoint,
		BaseDir:      sm.baseDir,
		Log:          sm.log,
		Pending:      pending,
	}
	sessionDir, env, err := sb.Run(ctx, wsConn)
	if err != nil {
		sm.logError("session_bootstrap_failed", "session_id", sessionID, "error", err)
		wsConn.CloseNow() //nolint:errcheck
		return
	}

	initCommand := cfg.GetSessionInitCommand()
	if initCommand == "" {
		initCommand = "bash"
	}
	// cd into session folder before running the init command.
	shellCmd := fmt.Sprintf("cd '%s' && %s", sessionDir, initCommand)

	agCfg := sm.agentCfg
	agCfg.Env = env
	// Start the PTY at the browser's real geometry when the client reported
	// one, so the session never renders at a default the user cannot see.
	if cols > 0 && rows > 0 {
		agCfg.PTYCols = cols
		agCfg.PTYRows = rows
	}

	sm.logInfo("session_starting", "session_id", sessionID, "name", name, "init_command", initCommand)
	ag := agentfleet.NewPtyAgent([]string{"sh", "-c", shellCmd}, agCfg)
	task := &agentfleet.BasicTask{TaskID: sessionID, TaskName: name, Cmd: initCommand}
	r := agentfleet.NewRunner(task, ag, sm.fleetCfg, agCfg)
	r.Start()

	if err := sm.fleet.Add(ctx, r); err != nil {
		r.Stop()          //nolint:errcheck
		wsConn.CloseNow() //nolint:errcheck
		return
	}

	// The runner's output is a PERMANENT sessionWriter, set once here and never
	// re-pointed: it sends to the current session-lane while attached and buffers
	// (drop-oldest, up to sessionBufBytes) while detached, flushing the backlog on
	// re-attach. It starts already attached to this conn. Set output BEFORE
	// publishing the entry so a concurrent attach can never find the entry before
	// its output exists.
	writer := newSessionWriter(ctx, wsConn, sm.sessionBufBytes)
	entry := newSessionEntry(r, writer, wsConn, token, cols, rows)
	r.SetOutput(writer)
	sm.mu.Lock()
	sm.sessions[sessionID] = entry
	sm.mu.Unlock()

	// Apply any geometry latched before the PTY existed (from the new_session
	// frame or a resize during bootstrap). Must run after SetOutput and before
	// the read pump starts, so a buffered resize can only refine the geometry.
	if pRows, pCols, ok := pending.take(); ok {
		if err := applyResize(r, pRows, pCols, 20, 50*time.Millisecond); err != nil {
			pending.store(pRows, pCols)
			sm.logError("session_initial_resize_failed", "session_id", sessionID, "error", err)
		} else {
			sm.logInfo("session_initial_resize", "session_id", sessionID, "rows", pRows, "cols", pCols)
		}
	}

	if sm.autoRespond {
		// Auto-accept known startup prompts (e.g. the agent's folder-trust
		// dialog) so unattended sessions don't stall; stops once every rule has
		// fired or the watch window ends.
		go newPromptWatcher(r.Lines, r.StdinWriter(), defaultPromptRules(), defaultPollInterval, defaultPromptWindow, sm.log).Run(ctx)
	}

	// Read pump: bridge the session-lane socket to the PTY stdin. A socket
	// error detaches (see readPump) rather than stopping the agent.
	go sm.readPump(ctx, entry, sessionID, wsConn)

	// PTY-exit watcher: reaps the entry (so a racing re-attach is refused),
	// closes whichever socket is currently attached, and removes the session.
	go func() {
		<-r.Done()
		// Mark reaped + take the current conn atomically, so a re-attach racing
		// this delete gets ok=false from reattach instead of binding a dead runner.
		if c := entry.reap(); c != nil {
			c.Close(websocket.StatusNormalClosure, "session ended") //nolint:errcheck
		}
		sm.mu.Lock()
		delete(sm.sessions, sessionID)
		sm.mu.Unlock()
		sm.fleet.Remove(sessionID)
		sm.logInfo("session_stopped", "session_id", sessionID)
	}()
}

// readPump bridges the session-lane socket to the runner's stdin until the
// socket errors. A socket error means the viewer/relay went away — the PTY is
// left RUNNING (detached) and re-bound on the next new_session. The runner is
// NEVER stopped here; only an explicit Stop/Remove ends it (teardown contract).
func (sm *SessionManager) readPump(ctx context.Context, entry *sessionEntry, sessionID string, conn *websocket.Conn) {
	// Release this session-lane's socket FD when the pump exits. coder/websocket
	// does not close the socket on a read error, and the read uses the long-lived
	// process ctx (no deadline), so without this an ungraceful drop (1006, RST,
	// DO recycle) would hold the FD until GC. CloseNow is idempotent, so a
	// concurrent attach/Done-watcher close is safe.
	defer conn.CloseNow() //nolint:errcheck
	err := sm.readLoop(ctx, conn, entry, sessionID)
	sm.logInfo("session_lane_detached", "session_id", sessionID, "error", err)
	// Detach + switch the writer to buffering atomically under the entry lock, so
	// this never clobbers the output a concurrent re-attach installed. The PTY
	// keeps running either way. detachIfCurrent returns false for a stale conn
	// (a newer conn is already live) — only the current conn's drop starts a
	// reconnect, so we never spin up a redundant loop.
	if entry.detachIfCurrent(conn, entry.writer.detach) {
		sm.startReconnect(ctx, entry, sessionID)
	}
}

// startReconnect launches the single CLI-driven reconnect loop for a session
// whose lane just dropped. beginReconnect guarantees at most one loop, and none
// once the entry is reaped.
func (sm *SessionManager) startReconnect(ctx context.Context, entry *sessionEntry, sessionID string) {
	if !entry.beginReconnect() {
		return
	}
	go sm.reconnectLoop(ctx, entry, sessionID)
}

// reconnectLoop re-establishes a dropped session lane with the same long-lived
// token, using the shared exponential backoff. On success the buffered output
// flushes and live streaming resumes; the runner is never touched. It exits when
// the session is reaped/stopped, another path (a relay new_session) re-attached
// first, or the process context is cancelled.
func (sm *SessionManager) reconnectLoop(ctx context.Context, entry *sessionEntry, sessionID string) {
	defer entry.endReconnect()
	backoff := sm.reconnectInitial
	for {
		if entry.isReaped() || entry.currentConn() != nil || ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if entry.isReaped() || entry.currentConn() != nil {
			return
		}
		token, cols, rows := entry.reconnectParams()
		wsConn, err := sm.dial(ctx, sm.sessionLaneURL(sessionID, token))
		if err != nil {
			sm.logWarn("session_lane_reconnect_error", "session_id", sessionID, "error", err, "retrying_in", backoff.String())
			backoff = min(backoff*2, sm.reconnectMax)
			continue
		}
		if sm.bindReattach(ctx, entry, sessionID, wsConn, cols, rows) {
			sm.logInfo("session_lane_reconnected", "sandbox_id", sm.sandboxID, "session_id", sessionID)
			return
		}
		// Reaped → give up; flush error → discard the conn and retry with backoff.
		wsConn.CloseNow() //nolint:errcheck
		if entry.isReaped() {
			return
		}
		backoff = min(backoff*2, sm.reconnectMax)
	}
}

func (sm *SessionManager) readLoop(ctx context.Context, conn *websocket.Conn, entry *sessionEntry, sessionID string) error {
	r := entry.runner
	for {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		var msg struct {
			Type string `json:"type"`
			Data string `json:"data"`
			Rows int    `json:"rows"`
			Cols int    `json:"cols"`
		}
		if json.Unmarshal(raw, &msg) != nil {
			continue
		}
		switch msg.Type {
		case "data":
			b, err := base64.StdEncoding.DecodeString(msg.Data)
			if err != nil {
				continue
			}
			r.StdinWriter().Write(b) //nolint:errcheck
		case "resize":
			sm.logInfo("session_resize", "session_id", sessionID, "rows", msg.Rows, "cols", msg.Cols)
			// Values <= 0 are ignored; values above maxPTYDimension are
			// clamped down to it rather than ignored (see clampDimension).
			if msg.Rows > 0 && msg.Cols > 0 {
				rows, cols := clampDimension(msg.Rows), clampDimension(msg.Cols)
				if err := applyResize(r, rows, cols, 5, 50*time.Millisecond); err != nil {
					sm.logError("session_resize_failed", "session_id", sessionID, "error", err)
				}
				// Record the size the browser is actually showing now, not just
				// the live PTY. Without this, the NEXT reattach — the CLI's own
				// reconnectLoop self-healing a dropped lane, or a relay-driven
				// Reattach — replays whatever geometry this entry was last
				// (re)dialed at (session start, or the last reattach) and
				// silently resizes the PTY back down to it in bindReattach,
				// even though nothing about the browser's window changed. See
				// bindReattach's `entry.runner.Resize(rows, cols)`.
				entry.updateSize(cols, rows)
			}
		}
	}
}

// Stop sends SIGTERM to the session's PTY process (keeps the folder).
func (sm *SessionManager) Stop(sessionID string) {
	sm.logInfo("session_stopping", "session_id", sessionID)
	sm.mu.Lock()
	entry := sm.sessions[sessionID]
	sm.mu.Unlock()
	if entry != nil {
		entry.runner.Stop() //nolint:errcheck
	}
}

// Remove tears down one session for delete_session: stop the PTY, wait for it
// to exit, then delete its working folder and log entry. An explicit delete
// reclaims disk immediately rather than waiting for the retention window.
func (sm *SessionManager) Remove(sessionID string) {
	sm.logInfo("session_removing", "session_id", sessionID)
	sm.mu.Lock()
	entry := sm.sessions[sessionID]
	delete(sm.sessions, sessionID)
	sm.mu.Unlock()

	if entry != nil {
		drain(entry.runner, sessionDrainTimeout)
		sm.fleet.Remove(sessionID)
	}
	if err := os.RemoveAll(filepath.Join(sm.baseDir, "session-"+sessionID)); err != nil {
		sm.logError("session_dir_remove_failed", "session_id", sessionID, "error", err)
	}
	if sm.sessionLog != nil {
		if err := sm.sessionLog.remove(sessionID); err != nil {
			sm.logError("session_log_remove_failed", "session_id", sessionID, "error", err)
		}
	}
}

// RemoveAll tears everything down for a deleted sandbox: stop and drain every
// live session, delete every folder the log knows about, then delete the log
// file itself. Used for delete_sandbox, after which the CLI exits.
//
// Sessions drain concurrently, so teardown costs one drain timeout rather than
// one per session.
func (sm *SessionManager) RemoveAll() {
	sm.logInfo("sandbox_removing", "sandbox_id", sm.sandboxID)

	sm.mu.Lock()
	entries := make(map[string]*sessionEntry, len(sm.sessions))
	for id, entry := range sm.sessions {
		entries[id] = entry
	}
	sm.sessions = make(map[string]*sessionEntry)
	sm.mu.Unlock()

	var wg sync.WaitGroup
	for id, entry := range entries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			drain(entry.runner, sessionDrainTimeout)
			if sm.fleet != nil {
				sm.fleet.Remove(id)
			}
		}()
	}
	wg.Wait()

	if sm.sessionLog == nil {
		return
	}
	// Delete every folder the log knows about — including sessions from earlier
	// runs of this sandbox that are no longer live. Folders with no entry are
	// not ours to touch.
	logEntries, err := sm.sessionLog.entries()
	if err != nil {
		sm.logError("session_log_read_failed", "sandbox_id", sm.sandboxID, "error", err)
	}
	for id, e := range logEntries {
		if rmErr := os.RemoveAll(filepath.Join(sm.baseDir, e.Dir)); rmErr != nil {
			sm.logError("session_dir_remove_failed", "session_id", id, "error", rmErr)
		}
	}
	if err := sm.sessionLog.destroy(); err != nil {
		sm.logError("session_log_destroy_failed", "sandbox_id", sm.sandboxID, "error", err)
	}
}

// StopAll stops every active session.
func (sm *SessionManager) StopAll() {
	sm.mu.Lock()
	ids := make([]string, 0, len(sm.sessions))
	for id := range sm.sessions {
		ids = append(ids, id)
	}
	sm.mu.Unlock()
	for _, id := range ids {
		sm.Stop(id)
	}
}

func (sm *SessionManager) logInfo(msg string, args ...any) {
	if sm.log != nil {
		sm.log.Info(msg, args...)
	}
}

func (sm *SessionManager) logError(msg string, args ...any) {
	if sm.log != nil {
		sm.log.Error(msg, args...)
	}
}

func (sm *SessionManager) logWarn(msg string, args ...any) {
	if sm.log != nil {
		sm.log.Warn(msg, args...)
	}
}
