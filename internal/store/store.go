// Package store keeps stackk's state in SQLite: tasks and their event log.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(1000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	// Columns added after the first release.
	db.Exec(`ALTER TABLE tasks ADD COLUMN paused_at INTEGER NOT NULL DEFAULT 0`)
	db.Exec(`ALTER TABLE tasks ADD COLUMN hidden_at INTEGER NOT NULL DEFAULT 0`)
	return &DB{db}, nil
}

const taskCols = `id, name, cwd, agent, COALESCE(agent_session_id,''), session_path, stack, deferred,
  phase, waiting_kind, waiting_tool_use_id, phase_since, queued_at, last_msg, last_tool, last_tool_at,
  last_event_at, run_id, run_state, run_started_at, tmux_pane, home_window, error, note, archived_at, created_at, paused_at, hidden_at`

type scanner interface{ Scan(...any) error }

func scanTask(s scanner) (Task, error) {
	var t Task
	var deferred int
	err := s.Scan(&t.ID, &t.Name, &t.Cwd, &t.Agent, &t.AgentSessionID, &t.SessionPath, &t.Stack, &deferred,
		&t.Phase, &t.WaitingKind, &t.WaitingToolUseID, &t.PhaseSince, &t.QueuedAt, &t.LastMsg, &t.LastTool, &t.LastToolAt,
		&t.LastEventAt, &t.RunID, &t.RunState, &t.RunStartedAt, &t.TmuxPane, &t.HomeWindow, &t.Error, &t.Note, &t.ArchivedAt, &t.CreatedAt, &t.PausedAt, &t.HiddenAt)
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
	_, err := q.Exec(`INSERT INTO tasks (id, name, cwd, agent, agent_session_id, session_path, stack, deferred,
  phase, waiting_kind, waiting_tool_use_id, phase_since, queued_at, last_msg, last_tool, last_tool_at,
  last_event_at, run_id, run_state, run_started_at, tmux_pane, home_window, error, note, archived_at, created_at, paused_at, hidden_at)
  VALUES (?,?,?,?,NULLIF(?,''),?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.Name, t.Cwd, t.Agent, t.AgentSessionID, t.SessionPath, t.Stack, b2i(t.Deferred),
		t.Phase, t.WaitingKind, t.WaitingToolUseID, t.PhaseSince, t.QueuedAt, t.LastMsg, t.LastTool, t.LastToolAt,
		t.LastEventAt, t.RunID, t.RunState, t.RunStartedAt, t.TmuxPane, t.HomeWindow, t.Error, t.Note, t.ArchivedAt, t.CreatedAt, t.PausedAt, t.HiddenAt)
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
	_, err := q.Exec(`UPDATE tasks SET name=?, cwd=?, agent=?, agent_session_id=NULLIF(?,''), session_path=?, stack=?, deferred=?,
  phase=?, waiting_kind=?, waiting_tool_use_id=?, phase_since=?, queued_at=?, last_msg=?, last_tool=?, last_tool_at=?,
  last_event_at=?, run_id=?, run_state=?, run_started_at=?, tmux_pane=?, home_window=?, error=?, note=?, archived_at=?, paused_at=?, hidden_at=?
  WHERE id=?`,
		t.Name, t.Cwd, t.Agent, t.AgentSessionID, t.SessionPath, t.Stack, b2i(t.Deferred),
		t.Phase, t.WaitingKind, t.WaitingToolUseID, t.PhaseSince, t.QueuedAt, t.LastMsg, t.LastTool, t.LastToolAt,
		t.LastEventAt, t.RunID, t.RunState, t.RunStartedAt, t.TmuxPane, t.HomeWindow, t.Error, t.Note, t.ArchivedAt, t.PausedAt, t.HiddenAt, t.ID)
	return err
}

func DeleteTask(q Querier, id string) error {
	_, err := q.Exec(`DELETE FROM tasks WHERE id = ?`, id)
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
func (db *DB) Tx(fn func(q Querier) error) error {
	conn, err := db.DB.Conn(ctxBG)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctxBG, `BEGIN IMMEDIATE`); err != nil {
		return err
	}
	if err := fn(connQ{conn}); err != nil {
		conn.ExecContext(ctxBG, `ROLLBACK`)
		return err
	}
	_, err = conn.ExecContext(ctxBG, `COMMIT`)
	return err
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
