package core

import (
	"strings"

	"github.com/krol22/stackk/internal/store"
	"github.com/krol22/stackk/internal/tmux"
)

// Window name suffixes mirror a task's list: "name st" = scheduled, "name p" = paused,
// plain "name" = on the stack or running. Adoption reads them the same way.
const (
	suffixScheduled = " st"
	suffixPaused    = " p"
)

// SplitName strips a status suffix from a window name.
// Repeated suffixes ("x st st", left by older versions) are all stripped; the last one wins.
func SplitName(window string) (base string, scheduled, paused bool) {
	base = window
	first := true
	for {
		switch {
		case strings.HasSuffix(base, suffixScheduled) && len(base) > len(suffixScheduled):
			base = strings.TrimSuffix(base, suffixScheduled)
			if first {
				scheduled = true
			}
		case strings.HasSuffix(base, suffixPaused) && len(base) > len(suffixPaused):
			base = strings.TrimSuffix(base, suffixPaused)
			if first {
				paused = true
			}
		default:
			return base, scheduled, paused
		}
		first = false
	}
}

// WindowName is the tmux window name for a task.
func WindowName(t store.Task) string {
	name, _, _ := SplitName(t.Name) // never stack a second suffix
	switch {
	case t.PausedAt != 0:
		return name + suffixPaused
	case t.Deferred:
		return name + suffixScheduled
	}
	return name
}

// syncWindowName renames the task's home window to match its state.
// It targets the window that really holds the task's pane (the stored home can be stale for a
// moment) and never the dashboard's own window.
func (a *App) syncWindowName(id string) {
	t, err := store.GetTask(a.DB, id)
	if err != nil || t.RunState == store.RunExited {
		return
	}
	window := t.HomeWindow
	if t.TmuxPane != "" {
		if p, err := a.T.Pane(t.TmuxPane); err == nil {
			if p.UIWindow {
				return // pulled into the dashboard: reconcile renames its home once it is back
			}
			window = p.WindowID
		}
	}
	if window == "" {
		return
	}
	if ui, _ := a.T.Run("show-options", "-wqv", "-t", window, "@stackk_ui"); ui == "1" {
		return
	}
	a.setWindowName(window, WindowName(t))
}

// setWindowName renames a window and remembers the name, so a later difference can only mean
// the user renamed it by hand.
func (a *App) setWindowName(window, name string) {
	a.T.Run("rename-window", "-t", window, name)
	a.T.Run("set-option", "-w", "-t", window, "@stackk_wname", name)
}

// autoName reports names tmux gives a window by itself (the running program), not the user.
func autoName(name string) bool {
	base, _, _ := SplitName(name)
	return base == "" || tmux.AgentOf(base) != "" || base == "fish" || base == "bash" || base == "zsh" || base == "sh"
}
