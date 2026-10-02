package core

import (
	"errors"
	"fmt"

	"github.com/krol22/stackk/internal/safe"
	"hash/fnv"
	"os"
	"strconv"

	"github.com/krol22/stackk/internal/store"
	"github.com/krol22/stackk/internal/tmux"
)

var ErrBackend = errors.New("tmux is not responding")

const startDeadline = 30 // s for a spawned agent to show up

// quietAfter is how long a "working" task may show no screen change and no events before it becomes unknown.
var quietAfter = int64(envInt("STACKK_QUIET_SECS", 60))

func envInt(name string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(name)); err == nil && n > 0 {
		return n
	}
	return def
}

type quietState struct {
	hash  uint64
	since int64
}

// Reconciler keeps per-pane screen hashes between runs for the quiet heuristic.
type Reconciler struct {
	quiet map[string]quietState
}

func NewReconciler() *Reconciler { return &Reconciler{quiet: map[string]quietState{}} }

// Run aligns the store with tmux: dead panes, moved windows, new agent windows in stacks, silent "working" tasks.
func (r *Reconciler) Run(a *App) error {
	panes, err := a.T.AllPanes()
	if err != nil {
		return ErrBackend
	}
	now := a.Now()
	// The process table is only needed for agents that never sent a hook and for windows not yet
	// adopted; read it at most once, and only then.
	var procs *procTree
	procsRead := false
	getProcs := func() *procTree {
		if !procsRead {
			procs, procsRead = readProcs(), true
		}
		return procs
	}
	byID := map[string]tmux.Pane{}
	for _, p := range panes {
		byID[p.ID] = p
	}
	tasks, err := store.Tasks(a.DB, "")
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, t := range tasks {
		if t.RunState == store.RunExited {
			continue
		}
		p, ok := byID[t.TmuxPane]
		if t.TmuxPane != "" && ok {
			known[t.TmuxPane] = true
		}
		switch {
		case t.TmuxPane == "":
			if t.RunState == store.RunStarting && now-t.RunStartedAt > startDeadline {
				a.reconcileExit(t.ID, t.RunID, "start failed")
			}
		case !ok || p.Dead || (p.Run != "" && p.Run != t.RunID):
			a.reconcileExit(t.ID, t.RunID, "pane is gone")
		default:
			a.reconcileLive(t, p, now)
			if t.LastEventAt == 0 {
				a.reconcileAgent(t, p, getProcs().agentIn(p), now)
			}
			a.repairPhase(t, now)
			if t.Phase == store.PhaseWorking && t.RunState == store.RunLive {
				r.checkQuiet(a, t, now)
			}
		}
	}
	// Every window in a stack is a task, agent or not: one task per window, on its agent pane if
	// there is one, else on its active pane. Windows holding stackk's own panes are skipped.
	byWindow := map[string][]tmux.Pane{}
	var order []string
	for _, p := range panes {
		if !p.Stack || p.Dead {
			continue
		}
		if _, ok := byWindow[p.WindowID]; !ok {
			order = append(order, p.WindowID)
		}
		byWindow[p.WindowID] = append(byWindow[p.WindowID], p)
	}
	for _, w := range order {
		var pick *tmux.Pane
		skip := false
		for i, p := range byWindow[w] {
			if known[p.ID] || p.Role != "" || p.UIWindow || p.Command == "stackk" {
				skip = true
				break
			}
			if getProcs().agentIn(p) != "" && (pick == nil || getProcs().agentIn(*pick) == "") {
				pick = &byWindow[w][i]
			} else if pick == nil && p.Active {
				pick = &byWindow[w][i]
			}
		}
		if skip {
			continue
		}
		if pick == nil {
			pick = &byWindow[w][0]
		}
		a.adoptPane(*pick, getProcs().agentIn(*pick), now)
	}
	return nil
}

func (a *App) reconcileExit(id, run, why string) {
	now := a.Now()
	a.DB.Tx(func(q store.Querier) error {
		t, err := store.GetTask(q, id)
		if err != nil || t.RunID != run {
			return err
		}
		before := store.InStack(&t)
		t.RunState, t.TmuxPane = store.RunExited, ""
		if t.Phase == store.PhaseShell || t.Phase == store.PhaseWorking || t.Phase == store.PhaseUnknown {
			t.Phase, t.PhaseSince = store.PhaseIdle, now
		}
		if why == "start failed" {
			t.Error = why
		}
		store.Requeue(&t, before, now)
		if err := store.UpdateTask(q, t); err != nil {
			return err
		}
		return store.LogEvent(q, store.EventRow{TS: now, TaskID: id, RunID: run, Source: "reconcile", Event: "end", Applied: true, Detail: why})
	})
}

// reconcileLive aligns a live task with its window. It works on the task as stored right now
// (inside the transaction), never on the snapshot taken at the start of the cycle, so a pause or
// schedule made meanwhile is not overwritten.
func (a *App) reconcileLive(snap store.Task, p tmux.Pane, now int64) {
	rename, accepted := "", ""
	a.DB.Tx(func(q store.Querier) error {
		t, err := store.GetTask(q, snap.ID)
		if err != nil || t.RunID != snap.RunID {
			return err
		}
		changed := false
		if p.Stack && !p.UIWindow {
			if clean, _, _ := SplitName(t.Name); clean != t.Name {
				t.Name, changed = clean, true // stored with its suffix by an older version
			}
			// The window name is part of the UI: a rename by hand ("x st", "x p", "x") moves the
			// task. It counts only in a window whose name stackk set before and that has changed
			// since; a window stackk never named (new, or from an older version) is just renamed.
			if !autoName(p.WindowName) && p.SetName != "" && p.WindowName != p.SetName && p.WindowName != WindowName(t) {
				base, scheduled, paused := SplitName(safe.Text(p.WindowName))
				before := store.InStack(&t)
				t.Name, t.Deferred = base, scheduled
				switch {
				case paused && t.PausedAt == 0:
					t.PausedAt = now
				case !paused:
					t.PausedAt = 0
				}
				store.Requeue(&t, before, now)
				changed, accepted = true, p.WindowName
				store.LogEvent(q, store.EventRow{TS: now, TaskID: t.ID, RunID: t.RunID, Source: "reconcile", Event: "window-name",
					Applied: true, Detail: fmt.Sprintf("%q renamed by hand (was %q)", p.WindowName, p.SetName)})
			}
			if want := WindowName(t); p.WindowName != want {
				rename = want // e.g. "x st st" → "x st", or a state changed since the last rename
			}
			// Only panes inside a stack session move tasks (a window moved out keeps its last stack).
			if p.SessionID != t.Stack {
				t.Stack, changed = p.SessionID, true
			}
			if p.WindowID != t.HomeWindow {
				t.HomeWindow, changed = p.WindowID, true
			}
		}
		// Without hooks an agent never reports SessionStart; a live agent process is enough.
		if t.RunState == store.RunStarting && now-t.RunStartedAt > 10 && tmux.AgentOf(p.Command) != "" {
			before := store.InStack(&t)
			t.RunState, changed = store.RunLive, true
			store.Requeue(&t, before, now)
		}
		if !changed {
			return nil
		}
		return store.UpdateTask(q, t)
	})
	switch {
	case rename != "":
		a.setWindowName(p.WindowID, rename)
	case accepted != "":
		a.T.Run("set-option", "-w", "-t", p.WindowID, "@stackk_wname", accepted) // the user's name is now the known one
	}
}

// checkQuiet flags a "working" session whose screen has not changed and that sent no event for
// quietAfter. A session that reported recently cannot qualify, so its screen is not captured at
// all until half the threshold has passed without events (detection lands at about 1.5×).
func (r *Reconciler) checkQuiet(a *App, t store.Task, now int64) {
	if now-t.LastEventAt < quietAfter/2 {
		delete(r.quiet, t.TmuxPane)
		return
	}
	screen, err := a.T.Run("capture-pane", "-p", "-t", t.TmuxPane)
	if err != nil {
		return
	}
	h := fnv.New64a()
	h.Write([]byte(screen))
	sum := h.Sum64()
	q, ok := r.quiet[t.TmuxPane]
	if !ok || q.hash != sum {
		r.quiet[t.TmuxPane] = quietState{hash: sum, since: now}
		return
	}
	if now-q.since < quietAfter || now-t.LastEventAt < quietAfter {
		return
	}
	// The agent said its subagents work on in the background: a still screen is expected then.
	if last, _ := store.LastHookEvent(a.DB, t.ID); last == "stop_bg" {
		return
	}
	a.DB.Tx(func(qq store.Querier) error {
		cur, err := store.GetTask(qq, t.ID)
		if err != nil || cur.Phase != store.PhaseWorking || now-cur.LastEventAt < quietAfter {
			return err // a hook arrived meanwhile
		}
		store.Reduce(&cur, store.Event{Kind: "unknown", Source: "reconcile"}, now)
		if err := store.UpdateTask(qq, cur); err != nil {
			return err
		}
		return store.LogEvent(qq, store.EventRow{TS: now, TaskID: cur.ID, RunID: cur.RunID, Source: "reconcile", Event: "unknown", Applied: true, Detail: "silent for " + strconv.FormatInt(quietAfter, 10) + "s"})
	})
}

func (a *App) adoptPane(p tmux.Pane, agent string, now int64) {
	phase := store.PhaseUnknown
	if agent == "" {
		phase = store.PhaseShell
	}
	t := store.Task{ID: NewID(taskName(p, p.Path)), Name: taskName(p, p.Path), Cwd: p.Path, Agent: agent, Stack: p.SessionID,
		Phase: phase, PhaseSince: now, RunID: NewRunID(), RunState: store.RunLive, RunStartedAt: now,
		TmuxPane: p.ID, HomeWindow: p.WindowID, CreatedAt: now}
	if p.Task != "" {
		// A tagged pane whose task was archived or lost: keep its id stable.
		if old, err := store.GetTask(a.DB, p.Task); err == nil {
			t.ID, t.Name, t.AgentSessionID, t.SessionPath, t.Note, t.CreatedAt = old.ID, old.Name, old.AgentSessionID, old.SessionPath, old.Note, old.CreatedAt
		}
	}
	applySuffix(&t, p.WindowName, now)
	adopted := false
	err := a.DB.Tx(func(q store.Querier) error {
		if held, err := store.FindByPane(q, p.ID); err == nil && held.RunState != store.RunExited {
			return nil // a hook adopted this pane since the snapshot
		}
		adopted = true
		if _, err := store.GetTask(q, t.ID); err == nil {
			if err := store.UpdateTask(q, t); err != nil {
				return err
			}
		} else if err := store.InsertTask(q, t); err != nil {
			return err
		}
		return store.LogEvent(q, store.EventRow{TS: now, TaskID: t.ID, RunID: t.RunID, Source: "reconcile", Event: "adopt", Applied: true, Detail: p.SessionName + ":" + p.WindowIndex})
	})
	if err == nil && adopted {
		a.tagPane(p.ID, t)
		a.setWindowName(p.WindowID, WindowName(t))
	}
}

// reconcileAgent follows the foreground process: an agent quitting to its shell (without hooks
// telling us) makes the task a terminal, and an agent started in a terminal brings it back.
func (a *App) reconcileAgent(t store.Task, p tmux.Pane, agent string, now int64) {
	// Hooks are the truth once a task has sent any: the process name is not (an agent started
	// through the user's shell shows up as "fish"). Quitting to the shell is reported by SessionEnd.
	if t.RunState != store.RunLive || t.LastEventAt != 0 {
		return
	}
	kind := ""
	switch {
	case agent == "" && t.Phase != store.PhaseShell:
		kind = "shell"
	case agent != "" && t.Phase == store.PhaseShell:
		kind = "agent"
	default:
		return
	}
	a.DB.Tx(func(q store.Querier) error {
		cur, err := store.GetTask(q, t.ID)
		if err != nil || cur.RunID != t.RunID || cur.LastEventAt != 0 {
			return err // hooks started reporting meanwhile: they are the truth now
		}
		if agent != "" {
			cur.Agent = agent
		}
		store.Reduce(&cur, store.Event{Kind: kind, Source: "reconcile"}, now)
		if err := store.UpdateTask(q, cur); err != nil {
			return err
		}
		return store.LogEvent(q, store.EventRow{TS: now, TaskID: cur.ID, RunID: cur.RunID, Source: "reconcile", Event: kind, Applied: true, Detail: p.Command})
	})
}

// repairPhase undoes "shell" set by older versions from the process name while hooks said
// otherwise: the agent's own last event decides.
func (a *App) repairPhase(t store.Task, now int64) {
	if t.Phase != store.PhaseShell || t.LastEventAt == 0 || t.RunState != store.RunLive {
		return
	}
	last, err := store.LastHookEvent(a.DB, t.ID)
	if err != nil {
		return
	}
	phase := map[string]string{"start": store.PhaseIdle, "prompt": store.PhaseWorking, "stop": store.PhaseIdle, "stop_bg": store.PhaseWorking,
		"fail": store.PhaseIdle, "wait": store.PhaseWaiting}[last]
	if phase == "" {
		return // "end": the agent really quit to the shell
	}
	a.DB.Tx(func(q store.Querier) error {
		cur, err := store.GetTask(q, t.ID)
		if err != nil || cur.Phase != store.PhaseShell {
			return err
		}
		before := store.InStack(&cur)
		cur.Phase, cur.PhaseSince = phase, now
		store.Requeue(&cur, before, now)
		if err := store.UpdateTask(q, cur); err != nil {
			return err
		}
		return store.LogEvent(q, store.EventRow{TS: now, TaskID: cur.ID, RunID: cur.RunID, Source: "reconcile", Event: "repair", Applied: true, Detail: "phase from last hook: " + last})
	})
}
