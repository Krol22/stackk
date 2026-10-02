package core

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/krol22/stackk/internal/safe"
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
	var due []store.Task
	for id, queued := range n.pending {
		t, ok := byID[id]
		switch {
		case !ok || !store.InStack(&t) || t.QueuedAt != queued || id == out:
			delete(n.pending, id) // left the stack, re-queued, or you opened it
		case now-queued >= notifyAfter:
			due = append(due, t)
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
	// Names are quoted: a message can never start with "-" and be read as an option.
	msg := "“" + safe.Text(due[0].Name) + "” is waiting for you"
	click := []string{a.Self, "focus"} // click: back to the dashboard…
	if len(due) == 1 {
		click = append(click, due[0].ID) // …with that session open
	} else {
		names := make([]string, len(due))
		for i, t := range due {
			names[i] = "“" + safe.Text(t.Name) + "”"
		}
		msg = fmt.Sprintf("%d sessions are waiting: %s", len(due), strings.Join(names, ", "))
	}
	a.notify("stackk", msg, click)
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

// notify shows a macOS notification. With terminal-notifier, clicking it brings the terminal
// app to the front and runs click (stackk focus …); osascript notifications can only open
// Script Editor, so they are the fallback.
func (a *App) notify(title, msg string, click []string) {
	if f := os.Getenv("STACKK_NOTIFY_FILE"); f != "" { // tests
		if fh, err := os.OpenFile(f, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintf(fh, "%s: %s | click: %s\n", title, msg, shellJoin(a.tmuxEnv(click)))
			fh.Close()
		}
		return
	}
	if tn, err := exec.LookPath("terminal-notifier"); err == nil {
		args := []string{"-title", title, "-message", msg, "-group", "stackk", "-sound", "Glass"}
		if bundle := a.terminalBundle(); bundle != "" {
			args = append(args, "-activate", bundle)
		}
		if len(click) > 0 {
			args = append(args, "-execute", shellJoin(a.tmuxEnv(click)))
		}
		cmd := exec.Command(tn, args...)
		if cmd.Start() == nil {
			go cmd.Wait() // reap it: the dashboard runs for days
		}
		return
	}
	q := func(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) }
	exec.Command("osascript", "-e", fmt.Sprintf(`display notification "%s" with title "%s" sound name "Glass"`, q(msg), q(title))).Run()
}

// terminalBundle is the macOS app the tmux client runs in (e.g. com.github.wez.wezterm).
func (a *App) terminalBundle() string {
	if b := os.Getenv("STACKK_TERMINAL_BUNDLE"); b != "" {
		return b
	}
	out, _ := a.T.Run("show-environment", "-g", "__CFBundleIdentifier")
	if v, ok := strings.CutPrefix(out, "__CFBundleIdentifier="); ok {
		return v
	}
	return ""
}

// tmuxEnv makes a click command reach this tmux server: terminal-notifier runs it outside tmux.
// It starts from launchd's bare environment, so PATH (for tmux), the server and the DB go along.
func (a *App) tmuxEnv(cmd []string) []string {
	env := []string{"env", "PATH=" + os.Getenv("PATH")}
	for _, k := range []string{"TMUX", "STACKK_TMUX_SOCKET", "STACKK_DB", "HOME"} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	return append(env, cmd...)
}

func shellJoin(args []string) string {
	q := make([]string, len(args))
	for i, s := range args {
		q[i] = "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
	}
	return strings.Join(q, " ")
}

// TestNotify sends a notification exactly like a real one, for checking the click from your desk.
func (a *App) TestNotify() {
	a.notify("stackk", "test: click me to go back to the dashboard", []string{a.Self, "focus"})
}
