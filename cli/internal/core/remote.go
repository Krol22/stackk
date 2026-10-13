package core

import (
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/krol22/stackk/cli/internal/agent"
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
	// One input at a time per task (replies and keys): they must not interleave.
	defer lockTask(id)()
	t, err := a.liveTask(id)
	if err != nil {
		return err
	}
	text = safe.Text(strings.Join(strings.Fields(text), " "))
	// "--" keeps a reply like "-R" from being read as an option; tmux takes an argument ending
	// in ";" as the end of a command, so trailing semicolons go as key codes.
	body := strings.TrimRight(text, ";")
	var cmds [][]string
	if body != "" {
		cmds = append(cmds, []string{"send-keys", "-t", t.TmuxPane, "-l", "--", body})
	}
	for range len(text) - len(body) {
		cmds = append(cmds, []string{"send-keys", "-t", t.TmuxPane, "-H", "3b"})
	}
	if len(cmds) > 0 {
		if err := a.T.Chain(cmds...); err != nil {
			return err
		}
	}
	if enter {
		// Enter on its own, a moment later: arriving with the text, Claude Code takes it for
		// part of a paste (a new line) and the reply sits unsent in its input box.
		time.Sleep(sendSettle)
		_, err = a.T.Run("send-keys", "-t", t.TmuxPane, "Enter")
	}
	return err
}

var sendSettle = 150 * time.Millisecond

// lockTask serializes input to one task across stackk processes.
func lockTask(id string) (unlock func()) {
	dir := filepath.Join(filepath.Dir(store.DefaultPath()), "locks")
	os.MkdirAll(dir, 0o700)
	f, err := os.OpenFile(filepath.Join(dir, id), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return func() {}
	}
	syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
	return func() { f.Close() }
}

// keyNames are the keys Keys accepts besides single letters and digits.
var keyNames = map[string]string{
	"enter": "Enter", "esc": "Escape", "tab": "Tab", "shift-tab": "BTab", "space": "Space", "backspace": "BSpace",
	"up": "Up", "down": "Down", "left": "Left", "right": "Right", "ctrl-c": "C-c", "alt-up": "M-Up", "alt-down": "M-Down",
}

// keyWait in a key sequence pauses before the next keys: for a prompt that opens asynchronously
// (Codex's question behind ⌥↑) and drops keys that arrive with the one that opened it.
const keyWait = "wait"

// KeyNames lists the named keys, for help and errors.
func KeyNames() []string {
	return []string{"enter", "esc", "tab", "shift-tab", "space", "backspace", "up", "down", "left", "right", "ctrl-c", "alt-up", "alt-down", keyWait}
}

// WaitingID names the prompt a task waits on (empty when it doesn't): when it began and what it
// asks. Answers carry it, so one meant for a prompt never lands on the next.
func WaitingID(t store.Task) string {
	if t.Phase != store.PhaseWaiting {
		if q := store.PendingQuestion(t); q != nil { // a question apart from the turn
			return fmt.Sprintf("0-%08x", crc32.ChecksumIEEE(q))
		}
		return ""
	}
	what := t.WaitingDetail
	if what == "" { // a prompt without details (omp, or an older hook): its message
		what = t.LastMsg
	}
	return fmt.Sprintf("%d-%08x", t.PhaseSince, crc32.ChecksumIEEE([]byte(what)))
}

// Keys presses keys in a task's terminal: named ones (enter, esc, up, ctrl-c, …) or a letter or digit.
// With waitingID, only if the agent still waits on that prompt (see WaitingID).
func (a *App) Keys(id string, keys []string, waitingID string) error {
	defer lockTask(id)()
	t, err := a.liveTask(id)
	if err != nil {
		return err
	}
	if waitingID != "" && WaitingID(t) != waitingID {
		return fmt.Errorf("%s no longer asks that: it moved on", t.Name)
	}
	// One send-keys per run of keys between waits.
	runs := [][]string{{"send-keys", "-t", t.TmuxPane}}
	for _, k := range keys {
		cur := &runs[len(runs)-1]
		switch name, ok := keyNames[strings.ToLower(k)]; {
		case strings.EqualFold(k, keyWait):
			runs = append(runs, []string{"send-keys", "-t", t.TmuxPane})
		case ok:
			*cur = append(*cur, name)
		case len(k) == 1 && (k[0] >= 'a' && k[0] <= 'z' || k[0] >= 'A' && k[0] <= 'Z' || k[0] >= '0' && k[0] <= '9'):
			*cur = append(*cur, k)
		default:
			return fmt.Errorf("unknown key %q (a letter, a digit or one of: %s)", k, strings.Join(KeyNames(), ", "))
		}
	}
	for i, run := range runs {
		if i > 0 {
			time.Sleep(300 * time.Millisecond)
			// The prompt may have moved on during the pause: the rest is for that one only.
			cur, err := a.liveTask(id)
			if err != nil || cur.RunID != t.RunID || cur.TmuxPane != t.TmuxPane || (waitingID != "" && WaitingID(cur) != waitingID) {
				return fmt.Errorf("%s no longer asks that: it moved on", t.Name)
			}
		}
		if len(run) == 3 {
			continue
		}
		if _, err = a.T.Run(run...); err != nil {
			return err
		}
	}
	// An agent that reports nothing when its prompt is cancelled is back at its input, waiting for you.
	if ag, err := agent.Get(t.Agent); err == nil && !ag.CancelReported() && waitingID != "" && len(keys) == 1 && strings.EqualFold(keys[0], "esc") {
		return a.SetPhase(id, store.PhaseIdle)
	}
	return nil
}
