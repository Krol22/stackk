package store

import (
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
)

func live(phase string) Task {
	return Task{ID: "t", Agent: "claude", Phase: phase, RunState: RunLive, RunID: "r1"}
}

func TestStopEntersStackAndKeepsQueueSlot(t *testing.T) {
	tk := live(PhaseWorking)
	Reduce(&tk, Event{Kind: "stop", RunID: "r1", Msg: "done"}, 100)
	if !InStack(&tk) || tk.QueuedAt != 100 || tk.LastMsg != "done" {
		t.Fatalf("stop: %+v", tk)
	}
	// A repeated stop must not push the task to the back.
	Reduce(&tk, Event{Kind: "stop", RunID: "r1"}, 200)
	if tk.QueuedAt != 100 {
		t.Fatalf("duplicate stop requeued: %d", tk.QueuedAt)
	}
}

func TestPromptLeavesStack(t *testing.T) {
	tk := live(PhaseIdle)
	tk.QueuedAt = 5
	Reduce(&tk, Event{Kind: "prompt", RunID: "r1", Msg: "go"}, 10)
	if InStack(&tk) || tk.QueuedAt != 0 || tk.Phase != PhaseWorking {
		t.Fatalf("prompt: %+v", tk)
	}
}

func TestStaleRunIgnored(t *testing.T) {
	tk := live(PhaseWorking)
	if Reduce(&tk, Event{Kind: "end", RunID: "old"}, 10) {
		t.Fatal("stale end applied")
	}
	if tk.RunState != RunLive {
		t.Fatal("stale end changed state")
	}
}

func TestWaitResolvedOnlyByMatchingTool(t *testing.T) {
	tk := live(PhaseWorking)
	Reduce(&tk, Event{Kind: "wait", RunID: "r1", ToolUseID: "a", WaitingKind: "permission"}, 10)
	Reduce(&tk, Event{Kind: "tool_done", RunID: "r1", ToolUseID: "b", Tool: "Read x"}, 11)
	if tk.Phase != PhaseWaiting {
		t.Fatal("unrelated tool resolved wait")
	}
	Reduce(&tk, Event{Kind: "tool_done", RunID: "r1", ToolUseID: "a"}, 12)
	if tk.Phase != PhaseWorking {
		t.Fatal("matching tool did not resolve wait")
	}
}

func TestDeferredWorkingRestoresToRunning(t *testing.T) {
	tk := live(PhaseWorking)
	tk.Deferred = true
	Reduce(&tk, Event{Kind: "stop", RunID: "r1"}, 10)
	if InStack(&tk) {
		t.Fatal("deferred task on stack")
	}
	tk.Deferred = false
	Requeue(&tk, false, 20)
	if !InStack(&tk) || tk.QueuedAt != 20 {
		t.Fatalf("restore: %+v", tk)
	}
}

func TestCompactKeepsWorking(t *testing.T) {
	tk := live(PhaseWorking)
	Reduce(&tk, Event{Kind: "start", RunID: "r1", StartSource: "compact"}, 10)
	if tk.Phase != PhaseWorking {
		t.Fatal("compact reset phase")
	}
}

func TestDBRoundTripAndConcurrentWrites(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	tk := Task{ID: "a", Name: "a", Agent: "claude", Stack: "$1", Phase: PhaseIdle, RunState: RunLive, CreatedAt: 1}
	if err := InsertTask(db, tk); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := db.Tx(func(q Querier) error {
				return LogEvent(q, EventRow{TaskID: "a", Source: "claude", Event: "stop", Applied: true})
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	ev, err := Events(db, "$1", "", 100)
	if err != nil || len(ev) != 20 {
		t.Fatalf("events: %d %v", len(ev), err)
	}
	// Two tasks cannot hold the same conversation: moving it clears the old owner.
	b := Task{ID: "b", Name: "b", Agent: "claude", Stack: "$1", Phase: PhaseIdle, RunState: RunLive, CreatedAt: 2}
	InsertTask(db, b)
	tk.AgentSessionID = "s1"
	if err := UpdateTask(db, tk); err != nil {
		t.Fatal(err)
	}
	b.AgentSessionID = "s1"
	if err := UpdateTask(db, b); err != nil {
		t.Fatal(err)
	}
	got, _ := GetTask(db, "a")
	if got.AgentSessionID != "" {
		t.Fatal("conversation not moved")
	}
}

func TestStopWithBackgroundSubagentsKeepsWorking(t *testing.T) {
	tk := live(PhaseWorking)
	Reduce(&tk, Event{Kind: "stop_bg", RunID: "r1", Msg: "launched"}, 10)
	if InStack(&tk) || tk.Phase != PhaseWorking {
		t.Fatalf("stop_bg: %+v", tk)
	}
	Reduce(&tk, Event{Kind: "stop", RunID: "r1", Msg: "all done"}, 20)
	if !InStack(&tk) {
		t.Fatal("final stop did not reach the stack")
	}
}

func TestMigratesAnOldDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	// A database from before paused_at / hidden_at and user_version.
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE tasks (id TEXT PRIMARY KEY, name TEXT NOT NULL, cwd TEXT NOT NULL DEFAULT '', agent TEXT NOT NULL,
  agent_session_id TEXT, session_path TEXT NOT NULL DEFAULT '', stack TEXT NOT NULL, deferred INTEGER NOT NULL DEFAULT 0,
  phase TEXT NOT NULL, waiting_kind TEXT NOT NULL DEFAULT '', waiting_tool_use_id TEXT NOT NULL DEFAULT '', phase_since INTEGER NOT NULL,
  queued_at INTEGER NOT NULL DEFAULT 0, last_msg TEXT NOT NULL DEFAULT '', last_tool TEXT NOT NULL DEFAULT '', last_tool_at INTEGER NOT NULL DEFAULT 0,
  last_event_at INTEGER NOT NULL DEFAULT 0, run_id TEXT NOT NULL DEFAULT '', run_state TEXT NOT NULL, run_started_at INTEGER NOT NULL DEFAULT 0,
  tmux_pane TEXT NOT NULL DEFAULT '', home_window TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '', note TEXT NOT NULL DEFAULT '',
  archived_at INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	raw.Exec(`INSERT INTO tasks (id, name, agent, stack, phase, phase_since, run_state, created_at) VALUES ('old','old','claude','$1','idle',1,'live',1)`)
	raw.Close()
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := GetTask(db, "old")
	if err != nil || got.PausedAt != 0 || got.HiddenAt != 0 {
		t.Fatalf("old row after migration: %+v %v", got, err)
	}
	db.Close()
	if _, err := Open(path); err != nil { // second open: already current
		t.Fatal(err)
	}
}

func TestTxRollsBackOnPanic(t *testing.T) {
	db, _ := Open(filepath.Join(t.TempDir(), "s.db"))
	err := db.Tx(func(q Querier) error {
		InsertTask(q, Task{ID: "x", Name: "x", Agent: "claude", Stack: "$1", Phase: PhaseIdle, RunState: RunLive, CreatedAt: 1})
		panic("boom")
	})
	if err == nil {
		t.Fatal("panic not reported")
	}
	if _, err := GetTask(db, "x"); err != ErrNotFound {
		t.Fatal("insert not rolled back")
	}
	if err := db.Tx(func(q Querier) error { return nil }); err != nil {
		t.Fatalf("next transaction blocked: %v", err)
	}
}

func TestPendingQuestionsOutliveTheTurn(t *testing.T) {
	tk := live(PhaseWorking)
	Reduce(&tk, Event{Kind: "ask", RunID: "r1", ToolUseID: "c1", Msg: "question: fruit?",
		Detail: `{"questions":[{"question":"fruit?"},{"question":"color?"}]}`}, 10)
	Reduce(&tk, Event{Kind: "stop", RunID: "r1"}, 11)
	if !InStack(&tk) || string(PendingQuestion(tk)) != `{"questions":[{"question":"fruit?"}]}` {
		t.Fatalf("the questions wait after the turn ends, first one first: %+v", tk)
	}
	Reduce(&tk, Event{Kind: "prompt", RunID: "r1", Answered: []string{AskKey("c1", 0)}}, 12)
	if string(PendingQuestion(tk)) != `{"questions":[{"question":"color?"}]}` || !InStack(&tk) {
		t.Fatalf("answering the first leaves the second, still on the stack: %+v", tk)
	}
	Reduce(&tk, Event{Kind: "prompt", RunID: "r1", Answered: []string{AskKey("c1", 1)}}, 13)
	if PendingQuestion(tk) != nil || InStack(&tk) {
		t.Fatalf("all answered: back to work, off the stack: %+v", tk)
	}
}

func TestStackLessPutsPriorityFirst(t *testing.T) {
	a := Task{ID: "a", QueuedAt: 1}
	b := Task{ID: "b", QueuedAt: 2, Priority: true}
	c := Task{ID: "c", QueuedAt: 3, Priority: true}
	if !StackLess(b, a) || StackLess(a, b) {
		t.Fatal("a priority task goes before one that came back earlier")
	}
	if !StackLess(b, c) || StackLess(c, b) {
		t.Fatal("priority tasks keep the order they came back in")
	}
}
