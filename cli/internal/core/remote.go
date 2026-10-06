package core

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/krol22/stackk/cli/internal/safe"
	"github.com/krol22/stackk/cli/internal/store"
)

// FindTask resolves what you typed to a task: its id, a unique id prefix or its name.
func (a *App) FindTask(ref string) (store.Task, error) {
	if t, err := store.GetTask(a.DB, ref); err == nil {
		return t, nil
	}
	all, err := store.Tasks(a.DB, "")
	if err != nil {
		return store.Task{}, err
	}
	var hits []store.Task
	for _, t := range all {
		if t.Name == ref || strings.HasPrefix(t.ID, ref) {
			hits = append(hits, t)
		}
	}
	switch len(hits) {
	case 0:
		return store.Task{}, fmt.Errorf("no task %q", ref)
	case 1:
		return hits[0], nil
	}
	ids := make([]string, len(hits))
	for i, t := range hits {
		ids[i] = t.ID
	}
	return store.Task{}, fmt.Errorf("%q matches %d tasks: %s", ref, len(hits), strings.Join(ids, ", "))
}

func (a *App) liveTask(id string) (store.Task, error) {
	t, err := store.GetTask(a.DB, id)
	if err != nil {
		return t, err
	}
	if t.TmuxPane == "" || t.RunState == store.RunExited { // starting is fine: it may be asking already
		return t, fmt.Errorf("%s is not running", t.Name)
	}
	return t, nil
}

// Screen is the text on a task's terminal: the visible screen with up to history lines above it.
func (a *App) Screen(id string, history int) ([]string, error) {
	t, err := a.liveTask(id)
	if err != nil {
		return nil, err
	}
	out, err := a.T.Run("capture-pane", "-p", "-J", "-t", t.TmuxPane, "-S", "-"+strconv.Itoa(max(0, history)))
	if err != nil {
		return nil, err
	}
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(safe.Text(l), " ")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines, nil
}

// Send types text into a task's terminal, then Enter unless told not to. It is typed as text
// (control characters dropped), so it can't smuggle in keys or escape sequences.
func (a *App) Send(id, text string, enter bool) error {
	t, err := a.liveTask(id)
	if err != nil {
		return err
	}
	text = safe.Text(strings.Join(strings.Fields(text), " "))
	if text != "" {
		if _, err := a.T.Run("send-keys", "-t", t.TmuxPane, "-l", text); err != nil {
			return err
		}
	}
	if enter {
		_, err = a.T.Run("send-keys", "-t", t.TmuxPane, "Enter")
	}
	return err
}

// keyNames are the keys Keys accepts besides single letters and digits.
var keyNames = map[string]string{
	"enter": "Enter", "esc": "Escape", "tab": "Tab", "shift-tab": "BTab", "space": "Space", "backspace": "BSpace",
	"up": "Up", "down": "Down", "left": "Left", "right": "Right", "ctrl-c": "C-c",
}

// KeyNames lists the named keys, for help and errors.
func KeyNames() []string {
	return []string{"enter", "esc", "tab", "shift-tab", "space", "backspace", "up", "down", "left", "right", "ctrl-c"}
}

// Keys presses keys in a task's terminal: named ones (enter, esc, up, ctrl-c, …) or a letter or digit.
func (a *App) Keys(id string, keys []string) error {
	t, err := a.liveTask(id)
	if err != nil {
		return err
	}
	args := []string{"send-keys", "-t", t.TmuxPane}
	for _, k := range keys {
		switch name, ok := keyNames[strings.ToLower(k)]; {
		case ok:
			args = append(args, name)
		case len(k) == 1 && (k[0] >= 'a' && k[0] <= 'z' || k[0] >= 'A' && k[0] <= 'Z' || k[0] >= '0' && k[0] <= '9'):
			args = append(args, k)
		default:
			return fmt.Errorf("unknown key %q (a letter, a digit or one of: %s)", k, strings.Join(KeyNames(), ", "))
		}
	}
	_, err = a.T.Run(args...)
	return err
}
