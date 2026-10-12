package core

import (
	"encoding/json"
	"fmt"
	"github.com/krol22/stackk/cli/internal/agent"
	"os"
	"strings"
	"time"

	"github.com/krol22/stackk/cli/internal/hook"
	"github.com/krol22/stackk/cli/internal/sessions"
	"github.com/krol22/stackk/cli/internal/store"
)

// How far back and how many session files search looks at.
const (
	indexAge   = 60 * 24 * time.Hour
	indexLimit = 400
)

// SessionIndex returns past Claude/omp sessions, parsing only files that changed since last time.
func (a *App) SessionIndex() []sessions.Session {
	var out []sessions.Session
	for _, f := range agent.Files(indexAge, indexLimit) {
		mod, size := f.Mod.UnixNano(), f.Size
		if data, ok := store.CachedSession(a.DB, f.Path, mod, size); ok {
			var s sessions.Session
			if json.Unmarshal([]byte(data), &s) == nil && s.ID != "" {
				out = append(out, s)
				continue
			}
		}
		s, ok := agent.Parse(f)
		if !ok {
			continue
		}
		if data, err := json.Marshal(s); err == nil {
			store.CacheSession(a.DB, f.Path, mod, size, string(data))
		}
		out = append(out, s)
	}
	return out
}

// TaskForSession is the stackk task that already holds this conversation, if any.
func (a *App) TaskForSession(s sessions.Session) (store.Task, bool) {
	t, err := store.FindByAgentSession(a.DB, s.Agent, s.ID)
	return t, err == nil
}

// Attach puts a past session on the stack: an existing task (even removed or paused) is brought
// back and resumed if its process is gone; otherwise a new task resumes the conversation.
func (a *App) Attach(stack string, s sessions.Session) (store.Task, error) {
	if st, err := os.Stat(s.Cwd); err != nil || !st.IsDir() {
		return store.Task{}, fmt.Errorf("the session's folder is gone: %s", s.Cwd)
	}
	now := a.Now()
	t, found := a.TaskForSession(s)
	if found {
		err := a.update(t.ID, "attach", s.Title, func(t *store.Task, now int64) error {
			before := store.InStack(t)
			t.ArchivedAt, t.PausedAt, t.HiddenAt, t.Deferred = 0, 0, 0, false
			if t.SessionPath == "" {
				t.SessionPath = s.Path
			}
			if t.RunState == store.RunExited {
				t.Stack = stack
			}
			store.Requeue(t, before, now)
			return nil
		})
		if err != nil {
			return t, err
		}
		a.syncWindowName(t.ID)
		if t.RunState != store.RunExited {
			cur, err := store.GetTask(a.DB, t.ID)
			return cur, err
		}
		return a.Resume(t.ID)
	}
	name := hook.OneLine(s.Title)
	if r := []rune(name); len(r) > 40 {
		name = strings.TrimSpace(string(r[:40]))
	}
	if name == "" {
		name = s.ID
	}
	t = store.Task{ID: NewID(name), Name: name, Cwd: s.Cwd, Agent: s.Agent, AgentSessionID: s.ID, SessionPath: s.Path,
		Stack: stack, Phase: store.PhaseIdle, PhaseSince: now, LastMsg: s.LastText, RunState: store.RunExited, CreatedAt: now}
	if err := a.DB.Tx(func(q store.Querier) error {
		if err := store.InsertTask(q, t); err != nil {
			return err
		}
		return store.LogEvent(q, store.EventRow{TS: now, TaskID: t.ID, Source: "ty", Event: "attach", Applied: true, Detail: s.Title})
	}); err != nil {
		return t, err
	}
	return a.Resume(t.ID)
}
