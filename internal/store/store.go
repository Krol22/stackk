// Package store keeps stackk's state in SQLite: tasks and their event log.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS tasks (
  id                  TEXT PRIMARY KEY,
  name                TEXT NOT NULL,
  cwd                 TEXT NOT NULL DEFAULT '',
  agent               TEXT NOT NULL,
  agent_session_id    TEXT,
  session_path        TEXT NOT NULL DEFAULT '',
  stack               TEXT NOT NULL,
  deferred            INTEGER NOT NULL DEFAULT 0,
  phase               TEXT NOT NULL,
  waiting_kind        TEXT NOT NULL DEFAULT '',
  waiting_tool_use_id TEXT NOT NULL DEFAULT '',
  phase_since         INTEGER NOT NULL,
  queued_at           INTEGER NOT NULL DEFAULT 0,
  last_msg            TEXT NOT NULL DEFAULT '',
  last_tool           TEXT NOT NULL DEFAULT '',
  last_tool_at        INTEGER NOT NULL DEFAULT 0,
  last_event_at       INTEGER NOT NULL DEFAULT 0,
  run_id              TEXT NOT NULL DEFAULT '',
  run_state           TEXT NOT NULL,
  run_started_at      INTEGER NOT NULL DEFAULT 0,
  tmux_pane           TEXT NOT NULL DEFAULT '',
  home_window         TEXT NOT NULL DEFAULT '',
  error               TEXT NOT NULL DEFAULT '',
  note                TEXT NOT NULL DEFAULT '',
  archived_at         INTEGER NOT NULL DEFAULT 0,
  paused_at           INTEGER NOT NULL DEFAULT 0,
  hidden_at           INTEGER NOT NULL DEFAULT 0,
  created_at          INTEGER NOT NULL,
  UNIQUE (agent, agent_session_id)
);
CREATE TABLE IF NOT EXISTS events (
  id      INTEGER PRIMARY KEY,
  ts      INTEGER NOT NULL,
  task_id TEXT NOT NULL,
  run_id  TEXT NOT NULL DEFAULT '',
  source  TEXT NOT NULL,
  event   TEXT NOT NULL,
  applied INTEGER NOT NULL,
  detail  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS events_task ON events(task_id, id);
CREATE TABLE IF NOT EXISTS session_index (
  path TEXT PRIMARY KEY,
  mod  INTEGER NOT NULL,
  size INTEGER NOT NULL,
  data TEXT NOT NULL
);
`

type Task struct {
	ID, Name, Cwd, Agent, AgentSessionID, SessionPath, Stack string
	Deferred                                                 bool
	Phase, WaitingKind, WaitingToolUseID                     string
	PhaseSince, QueuedAt                                     int64
	LastMsg, LastTool                                        string
	LastToolAt, LastEventAt                                  int64
	RunID, RunState                                          string
	RunStartedAt                                             int64
	TmuxPane, HomeWindow, Error, Note                        string
	ArchivedAt, CreatedAt                                    int64
	PausedAt                                                 int64 // paused: hidden until brought back, agent keeps running
	HiddenAt                                                 int64 // hidden: "not a task" (notes, an editor), out of every list
}

type EventRow struct {
	ID       int64
	TS       int64
	TaskID   string
	TaskName string
	RunID    string
	Source   string
	Event    string
	Applied  bool
	Detail   string
}

type DB struct{ *sql.DB }

var ErrNotFound = errors.New("not found")

func DefaultPath() string {
	if p := os.Getenv("STACKK_DB"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "stackk", "stackk.db")
}

func Open(path string) (*DB, error) {
	// The DB keeps excerpts of your prompts and the agents' replies (Claude's own transcripts are
	// 0600): the directory is private to you, which covers the DB, its WAL and errors.log.
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(1000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &DB{db}, nil
}

// schemaVersion is bumped with every schema change. Open runs the schema only when the stored
// version is older, so the common path (a hook opening the DB) costs one PRAGMA read.
const schemaVersion = 2

// Columns added after the first release, for databases created before them.
var addedColumns = []string{
	`ALTER TABLE tasks ADD COLUMN paused_at INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE tasks ADD COLUMN hidden_at INTEGER NOT NULL DEFAULT 0`,
}

func migrate(db *sql.DB) error {
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	if v >= schemaVersion {
		return nil
	}
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	for _, alter := range addedColumns {
		if _, err := db.Exec(alter); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	_, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion))
	return err
}

// taskFields are the task columns besides id and created_at, in the order of taskArgs and
// scanTask. agent_session_id is stored as NULL when empty so its unique index allows many.
var taskFields = []string{"name", "cwd", "agent", "agent_session_id", "session_path", "stack", "deferred",
	"phase", "waiting_kind", "waiting_tool_use_id", "phase_since", "queued_at", "last_msg", "last_tool", "last_tool_at",
	"last_event_at", "run_id", "run_state", "run_started_at", "tmux_pane", "home_window", "error", "note", "archived_at",
	"paused_at", "hidden_at"}

func taskArgs(t Task) []any {
	return []any{t.Name, t.Cwd, t.Agent, t.AgentSessionID, t.SessionPath, t.Stack, b2i(t.Deferred),
		t.Phase, t.WaitingKind, t.WaitingToolUseID, t.PhaseSince, t.QueuedAt, t.LastMsg, t.LastTool, t.LastToolAt,
		t.LastEventAt, t.RunID, t.RunState, t.RunStartedAt, t.TmuxPane, t.HomeWindow, t.Error, t.Note, t.ArchivedAt,
		t.PausedAt, t.HiddenAt}
}

func placeholder(col string) string {
	if col == "agent_session_id" {
		return "NULLIF(?,'')"
	}
	return "?"
}

var taskCols, insertTaskSQL, updateTaskSQL = func() (string, string, string) {
	sel := []string{"id", "created_at"}
	vals := []string{"?", "?"}
	var sets []string
	for _, c := range taskFields {
		if c == "agent_session_id" {
			sel = append(sel, "COALESCE(agent_session_id,'')")
		} else {
			sel = append(sel, c)
		}
		vals = append(vals, placeholder(c))
		sets = append(sets, c+"="+placeholder(c))
	}
	cols := strings.Join(append([]string{"id", "created_at"}, taskFields...), ", ")
	return strings.Join(sel, ", "),
		"INSERT INTO tasks (" + cols + ") VALUES (" + strings.Join(vals, ",") + ")",
		"UPDATE tasks SET " + strings.Join(sets, ", ") + " WHERE id=?"
}()

type scanner interface{ Scan(...any) error }

func scanTask(s scanner) (Task, error) {
	var t Task
	var deferred int
	err := s.Scan(&t.ID, &t.CreatedAt, &t.Name, &t.Cwd, &t.Agent, &t.AgentSessionID, &t.SessionPath, &t.Stack, &deferred,
		&t.Phase, &t.WaitingKind, &t.WaitingToolUseID, &t.PhaseSince, &t.QueuedAt, &t.LastMsg, &t.LastTool, &t.LastToolAt,
		&t.LastEventAt, &t.RunID, &t.RunState, &t.RunStartedAt, &t.TmuxPane, &t.HomeWindow, &t.Error, &t.Note, &t.ArchivedAt,
		&t.PausedAt, &t.HiddenAt)
	t.Deferred = deferred != 0
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

// Querier is satisfied by *sql.DB and *sql.Tx.
type Querier interface {
	Exec(string, ...any) (sql.Result, error)
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}

func GetTask(q Querier, id string) (Task, error) {
	return scanTask(q.QueryRow(`SELECT `+taskCols+` FROM tasks WHERE id = ?`, id))
}

func FindByAgentSession(q Querier, agent, sid string) (Task, error) {
	if sid == "" {
		return Task{}, ErrNotFound
	}
	return scanTask(q.QueryRow(`SELECT `+taskCols+` FROM tasks WHERE agent = ? AND agent_session_id = ?`, agent, sid))
}

func FindByPane(q Querier, pane string) (Task, error) {
	if pane == "" {
		return Task{}, ErrNotFound
	}
	return scanTask(q.QueryRow(`SELECT `+taskCols+` FROM tasks WHERE tmux_pane = ? AND archived_at = 0 ORDER BY created_at DESC LIMIT 1`, pane))
}

func InsertTask(q Querier, t Task) error {
	_, err := q.Exec(insertTaskSQL, append([]any{t.ID, t.CreatedAt}, taskArgs(t)...)...)
	return err
}

func UpdateTask(q Querier, t Task) error {
	// A conversation id belongs to one task; a resumed or re-adopted conversation moves here.
	if t.AgentSessionID != "" {
		if _, err := q.Exec(`UPDATE tasks SET agent_session_id = NULL WHERE agent = ? AND agent_session_id = ? AND id != ?`,
			t.Agent, t.AgentSessionID, t.ID); err != nil {
			return err
		}
	}
	_, err := q.Exec(updateTaskSQL, append(taskArgs(t), t.ID)...)
	return err
}

func LogEvent(q Querier, e EventRow) error {
	if e.TS == 0 {
		e.TS = time.Now().Unix()
	}
	_, err := q.Exec(`INSERT INTO events (ts, task_id, run_id, source, event, applied, detail) VALUES (?,?,?,?,?,?,?)`,
		e.TS, e.TaskID, e.RunID, e.Source, e.Event, b2i(e.Applied), e.Detail)
	return err
}

// LastHookEvent is the newest applied event reported by the agent itself (not by stackk).
func LastHookEvent(q Querier, taskID string) (string, error) {
	var ev string
	err := q.QueryRow(`SELECT event FROM events WHERE task_id = ? AND applied = 1 AND source IN ('claude','omp')
  AND event IN ('start','prompt','stop','stop_bg','fail','wait','end') ORDER BY id DESC LIMIT 1`, taskID).Scan(&ev)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return ev, err
}

// CachedSession returns the indexed data for a session file if it has not changed since.
func CachedSession(q Querier, path string, mod, size int64) (string, bool) {
	var data string
	err := q.QueryRow(`SELECT data FROM session_index WHERE path = ? AND mod = ? AND size = ?`, path, mod, size).Scan(&data)
	return data, err == nil
}

func CacheSession(q Querier, path string, mod, size int64, data string) error {
	_, err := q.Exec(`INSERT INTO session_index (path, mod, size, data) VALUES (?,?,?,?)
  ON CONFLICT(path) DO UPDATE SET mod=excluded.mod, size=excluded.size, data=excluded.data`, path, mod, size, data)
	return err
}

// Tasks returns non-archived tasks, all stacks when stack is empty.
func Tasks(q Querier, stack string) ([]Task, error) {
	query := `SELECT ` + taskCols + ` FROM tasks WHERE archived_at = 0`
	args := []any{}
	if stack != "" {
		query += ` AND stack = ?`
		args = append(args, stack)
	}
	rows, err := q.Query(query+` ORDER BY created_at`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Events returns the newest events, for one stack (empty = all) or one task.
func Events(q Querier, stack, taskID string, limit int) ([]EventRow, error) {
	query := `SELECT e.id, e.ts, e.task_id, COALESCE(t.name, e.task_id), e.run_id, e.source, e.event, e.applied, e.detail
  FROM events e LEFT JOIN tasks t ON t.id = e.task_id WHERE 1=1`
	args := []any{}
	if stack != "" {
		query += ` AND t.stack = ?`
		args = append(args, stack)
	}
	if taskID != "" {
		query += ` AND e.task_id = ?`
		args = append(args, taskID)
	}
	query += ` ORDER BY e.id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventRow
	for rows.Next() {
		var e EventRow
		var applied int
		if err := rows.Scan(&e.ID, &e.TS, &e.TaskID, &e.TaskName, &e.RunID, &e.Source, &e.Event, &applied, &e.Detail); err != nil {
			return nil, err
		}
		e.Applied = applied != 0
		out = append(out, e)
	}
	return out, rows.Err()
}

// Tx runs fn in a write transaction (BEGIN IMMEDIATE, so concurrent hooks queue on busy_timeout).
// The connection never goes back to the pool with a transaction open: with one connection that
// would block every later write until restart.
func (db *DB) Tx(fn func(q Querier) error) (err error) {
	conn, err := db.DB.Conn(ctxBG)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctxBG, `BEGIN IMMEDIATE`); err != nil {
		return err
	}
	done := false
	defer func() {
		if !done {
			conn.ExecContext(ctxBG, `ROLLBACK`)
		}
		if r := recover(); r != nil {
			err = fmt.Errorf("transaction panicked: %v", r)
		}
	}()
	if err := fn(connQ{conn}); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctxBG, `COMMIT`); err != nil {
		return err
	}
	done = true
	return nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
