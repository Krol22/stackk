package core

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/krol22/stackk/internal/store"
)

// Notifier tells you when sessions came back to the stack from RUNNING and are still waiting
// after a delay, unless you are looking at the dashboard. One notification covers all of them,
// at most once a minute.
type Notifier struct {
	prev     map[string]bool  // task was running in the previous cycle
	pending  map[string]int64 // task → QueuedAt when it came back from running
	lastSent int64
}

var (
	notifyAfter    = int64(envInt("STACKK_NOTIFY_AFTER", 120))
	notifyInterval = int64(envInt("STACKK_NOTIFY_INTERVAL", 60))
)

func NewNotifier() *Notifier {
	return &Notifier{prev: map[string]bool{}, pending: map[string]int64{}}
}

// Check runs once per reconcile cycle. out is the task open in the drawer.
func (n *Notifier) Check(a *App, stack, uiPane, out string) {
	tasks, err := store.Tasks(a.DB, stack)
	if err != nil {
		return
	}
	now := a.Now()
	byID := map[string]store.Task{}
	for _, t := range tasks {
		byID[t.ID] = t
		onStack := store.InStack(&t)
		running := !onStack && t.Phase == store.PhaseWorking && t.RunState == store.RunLive && !t.Deferred && t.PausedAt == 0 && t.HiddenAt == 0
		if n.prev[t.ID] && onStack {
			n.pending[t.ID] = t.QueuedAt
		}
		if !onStack {
			delete(n.pending, t.ID)
		}
		n.prev[t.ID] = running
	}
	var due []string
	for id, queued := range n.pending {
		t, ok := byID[id]
		switch {
		case !ok || !store.InStack(&t) || t.QueuedAt != queued || id == out:
			delete(n.pending, id) // left the stack, re-queued, or you opened it
		case now-queued >= notifyAfter:
			due = append(due, t.Name)
		}
	}
	if len(due) == 0 || now-n.lastSent < notifyInterval {
		return
	}
	for id, queued := range n.pending {
		if now-queued >= notifyAfter {
			delete(n.pending, id)
		}
	}
	if a.watching(uiPane) {
		return // the dashboard is in front of you
	}
	n.lastSent = now
	title := "stackk"
	msg := due[0] + " is waiting for you"
	if len(due) > 1 {
		msg = fmt.Sprintf("%d sessions are waiting: %s", len(due), strings.Join(due, ", "))
	}
	notify(title, msg)
}

// watching reports a focused tmux client showing the dashboard's window.
func (a *App) watching(uiPane string) bool {
	ui, err := a.T.Pane(uiPane)
	if err != nil {
		return false
	}
	rows, _ := a.T.Fields("list-clients", "-F", "#{client_flags}\t#{window_id}")
	for _, r := range rows {
		if len(r) == 2 && strings.Contains(r[0], "focused") && r[1] == ui.WindowID {
			return true
		}
	}
	return false
}

func notify(title, msg string) {
	if f := os.Getenv("STACKK_NOTIFY_FILE"); f != "" { // tests
		if fh, err := os.OpenFile(f, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintf(fh, "%s: %s\n", title, msg)
			fh.Close()
		}
		return
	}
	q := func(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) }
	exec.Command("osascript", "-e", fmt.Sprintf(`display notification "%s" with title "%s" sound name "Glass"`, q(msg), q(title))).Run()
}
