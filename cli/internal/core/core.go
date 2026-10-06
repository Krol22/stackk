// Package core implements stackk's operations on top of the store and tmux.
package core

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/krol22/stackk/cli/internal/hook"
	"github.com/krol22/stackk/cli/internal/safe"
	"github.com/krol22/stackk/cli/internal/sessions"
	"github.com/krol22/stackk/cli/internal/store"
	"github.com/krol22/stackk/cli/internal/tmux"
)

type App struct {
	DB   *store.DB
	T    tmux.Client
	Self string // absolute path of the stackk binary
	Now  func() int64
}

func New(db *store.DB, t tmux.Client) *App {
	self, _ := os.Executable()
	if p, err := filepath.EvalSymlinks(self); err == nil {
		self = p
	}
	return &App{DB: db, T: t, Self: self, Now: func() int64 { return time.Now().Unix() }}
}

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func NewID(name string) string {
	s := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(s) > 24 {
		s = s[:24]
	}
	if s == "" {
		s = "task"
	}
	return s + "-" + randHex(2)
}

func NewRunID() string { return "r" + randHex(4) }

// taskName picks a readable name for an adopted pane: the window name unless tmux auto-named it after the process.
func taskName(p tmux.Pane, cwd string) string {
	p.WindowName = safe.Text(p.WindowName)
	if base, _, _ := SplitName(p.WindowName); !autoName(p.WindowName) {
		return base
	}
	if cwd == "" {
		cwd = p.Path
	}
	return filepath.Base(cwd)
}

// Mark handles one hook invocation from inside an agent's pane (already looked up by the caller,
// which also used it to skip panes outside any stack without opening the DB).
func (a *App) Mark(agent string, raw []byte, pane tmux.Pane, envTask, envRun string) error {
	ev, p, ok := hook.Parse(agent, raw)
	if !ok || pane.ID == "" {
		return nil
	}
	paneID := pane.ID
	// A task pulled into the drawer sits outside its stack session; its pane tag still says whose it is.
	if !pane.Stack && pane.Task == "" {
		return nil
	}
	now := a.Now()
	tag := ""
	var tagged store.Task
	err := a.DB.Tx(func(q store.Querier) error {
		taskID := pane.Task
		if taskID == "" {
			taskID = envTask
		}
		var task store.Task
		lookup := store.ErrNotFound
		if taskID != "" {
			task, lookup = store.GetTask(q, taskID)
		}
		if errors.Is(lookup, store.ErrNotFound) {
			task, lookup = store.FindByAgentSession(q, agent, p.SessionID)
		}
		if errors.Is(lookup, store.ErrNotFound) {
			task, lookup = store.FindByPane(q, paneID)
		}
		switch {
		case errors.Is(lookup, store.ErrNotFound) && !pane.Stack:
			return nil
		case errors.Is(lookup, store.ErrNotFound):
			task = store.Task{ID: NewID(taskName(pane, p.Cwd)), Name: taskName(pane, p.Cwd), Cwd: p.Cwd, Agent: agent,
				Stack: pane.SessionID, Phase: store.PhaseUnknown, PhaseSince: now, RunID: NewRunID(),
				RunState: store.RunLive, RunStartedAt: now, TmuxPane: paneID, HomeWindow: pane.WindowID, CreatedAt: now}
			applySuffix(&task, pane.WindowName, now)
			if err := store.InsertTask(q, task); err != nil {
				return err
			}
			store.LogEvent(q, store.EventRow{TS: now, TaskID: task.ID, RunID: task.RunID, Source: "ty", Event: "adopt", Applied: true, Detail: pane.SessionName + ":" + pane.WindowIndex})
			tag = "adopt"
		case lookup != nil:
			return lookup
		}
		runID := pane.Run
		if runID == "" {
			runID = envRun
		}
		if runID == "" && tag == "" {
			// An untagged pane is a new process for this task (e.g. resumed by hand elsewhere).
			task.RunID, task.RunStartedAt, task.RunState = NewRunID(), now, store.RunLive
			tag = "rebind"
		}
		if runID == "" {
			runID = task.RunID
		}
		ev.RunID = runID
		if task.Agent == "" {
			task.Agent = agent // a terminal task whose window just started an agent
		}
		applied := store.Reduce(&task, ev, now)
		if applied {
			if task.RunState != store.RunExited {
				task.TmuxPane = paneID
			}
			if pane.Stack && !pane.UIWindow {
				task.Stack, task.HomeWindow = pane.SessionID, pane.WindowID
			}
			if p.Cwd != "" {
				task.Cwd = p.Cwd
			}
			if err := store.UpdateTask(q, task); err != nil {
				return err
			}
		}
		detail := ev.Msg
		if detail == "" {
			detail = ev.Tool
		}
		if ev.Kind == "tool_done" && applied {
			// Tool calls are frequent; keep them out of the log.
			tagged = task
			return nil
		}
		tagged = task
		return store.LogEvent(q, store.EventRow{TS: now, TaskID: task.ID, RunID: runID, Source: agent, Event: ev.Kind, Applied: applied, Detail: detail})
	})
	if err != nil {
		return err
	}
	if tag != "" {
		a.tagPane(paneID, tagged)
	}
	return nil
}

// tagPane records on the pane which task and run it holds; hooks and reconcile read it back.
func (a *App) tagPane(pane string, t store.Task) {
	a.T.SetPaneOpt(pane, "@stackk_task", t.ID)
	a.T.SetPaneOpt(pane, "@stackk_run", t.RunID)
}

// --- stacks ---

func (a *App) Stacks() ([]tmux.Session, error) {
	all, err := a.T.Sessions()
	if err != nil {
		return nil, err
	}
	var out []tmux.Session
	for _, s := range all {
		if s.Stack {
			out = append(out, s)
		}
	}
	return out, nil
}

func (a *App) FindStack(name string) (tmux.Session, bool) {
	ss, _ := a.Stacks()
	for _, s := range ss {
		if s.Name == name || s.ID == name {
			return s, true
		}
	}
	return tmux.Session{}, false
}

var stackNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

// DefaultStack is the stack `stackk` opens without arguments.
func DefaultStack() string {
	if s := os.Getenv("STACKK_STACK"); s != "" {
		return s
	}
	return "stackk"
}

// EnsureStack returns the stack session, creating it (detached, hidden) on first use.
func (a *App) EnsureStack(name string) (tmux.Session, error) {
	if s, ok := a.FindStack(name); ok {
		return s, nil
	}
	if !stackNameRe.MatchString(name) {
		return tmux.Session{}, fmt.Errorf("stack name: letters, digits, - and _ (max 32)")
	}
	exists := false
	if ss, err := a.T.Sessions(); err == nil {
		for _, s := range ss {
			exists = exists || s.Name == name
		}
	}
	if !exists {
		home, _ := os.UserHomeDir()
		// The session needs one window to live; it only tells you where to look.
		if _, err := a.T.Run("new-session", "-d", "-s", name, "-n", "stack", "-c", home, a.Self, "hold", name); err != nil {
			return tmux.Session{}, err
		}
		a.T.SetPaneOpt(name+":", "@stackk_role", "hold")
	}
	if _, err := a.T.Run("set-option", "-t", name, "@stackk", "1"); err != nil {
		return tmux.Session{}, err
	}
	s, ok := a.FindStack(name)
	if !ok {
		return s, fmt.Errorf("stack %s was not created", name)
	}
	return s, nil
}

// Take moves a window into a stack; reconcile adopts its agent right away.
func (a *App) Take(window, stack string) error {
	s, err := a.EnsureStack(stack)
	if err != nil {
		return err
	}
	if _, err := a.T.Run("move-window", "-s", window, "-t", s.ID+":"); err != nil {
		return err
	}
	return NewReconciler().Run(a)
}

// Candidates lists agent panes outside stacks that could be moved into one.
func (a *App) Candidates() ([]tmux.Pane, error) {
	panes, err := a.T.AllPanes()
	if err != nil {
		return nil, err
	}
	procs := readProcs()
	var out []tmux.Pane
	for _, p := range panes {
		if !p.Stack && !p.Dead && p.Role == "" && procs.agentIn(p) != "" {
			p.Command = procs.agentIn(p) // shown in the "add windows" list
			out = append(out, p)
		}
	}
	return out, nil
}

// --- tasks ---

func agentArgv(agent, prompt, resume string) ([]string, error) {
	bin, err := exec.LookPath(agent)
	if err != nil {
		return nil, fmt.Errorf("%s not found in PATH", agent)
	}
	argv := []string{bin}
	if resume != "" {
		argv = append(argv, "--resume", resume)
	}
	if prompt != "" {
		if strings.HasPrefix(prompt, "-") { // a prompt, never an option of the agent's
			prompt = " " + prompt
		}
		argv = append(argv, prompt)
	}
	return argv, nil
}

// shellArgs runs argv through the user's shell (its config sets PATH and env) with exec, so the
// agent replaces the shell (fish would otherwise stay as the pane's process). The arguments go
// in as environment variables and are expanded as "$STACKK_An": nothing in a prompt is ever
// parsed by fish, bash or zsh, whose quoting rules differ (fish treats \ inside '…').
func shellArgs(argv []string) (env []string, command string) {
	refs := make([]string, len(argv))
	for i, arg := range argv {
		name := fmt.Sprintf("STACKK_A%d", i)
		env = append(env, "-e", name+"="+arg)
		refs[i] = `"$` + name + `"`
	}
	return env, "exec " + strings.Join(refs, " ")
}

func (a *App) spawn(t store.Task, argv []string) (pane, window string, err error) {
	args := []string{"new-window", "-d", "-t", t.Stack + ":", "-n", WindowName(t), "-c", t.Cwd,
		"-e", "STACKK_TASK_ID=" + t.ID, "-e", "STACKK_RUN_ID=" + t.RunID, "-P", "-F", "#{pane_id}\t#{window_id}"}
	env, command := shellArgs(argv)
	args = append(args, env...)
	out, err := a.T.Run(append(args, command)...)
	if err != nil {
		return "", "", err
	}
	parts := strings.SplitN(out, "\t", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("new-window: %q", out)
	}
	a.tagPane(parts[0], t)
	a.setWindowName(parts[1], WindowName(t))
	return parts[0], parts[1], nil
}

func (a *App) NewTask(stack, cwd, name, agent, prompt string) (store.Task, error) {
	cwd = expandHome(cwd)
	if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		return store.Task{}, fmt.Errorf("no such folder %s", cwd)
	}
	if name == "" {
		name = filepath.Base(cwd)
	}
	argv, err := agentArgv(agent, prompt, "")
	if err != nil {
		return store.Task{}, err
	}
	now := a.Now()
	phase := store.PhaseIdle
	msg := ""
	if prompt != "" {
		phase, msg = store.PhaseWorking, "› "+hook.OneLine(prompt)
	}
	t := store.Task{ID: NewID(name), Name: name, Cwd: cwd, Agent: agent, Stack: stack, Phase: phase, PhaseSince: now,
		LastMsg: msg, RunID: NewRunID(), RunState: store.RunStarting, RunStartedAt: now, CreatedAt: now}
	if err := a.DB.Tx(func(q store.Querier) error {
		if err := store.InsertTask(q, t); err != nil {
			return err
		}
		return store.LogEvent(q, store.EventRow{TS: now, TaskID: t.ID, RunID: t.RunID, Source: "ty", Event: "new", Applied: true, Detail: cwd})
	}); err != nil {
		return t, err
	}
	pane, window, err := a.spawn(t, argv)
	return a.afterSpawn(t.ID, t.RunID, pane, window, err)
}

func (a *App) afterSpawn(id, run, pane, window string, spawnErr error) (store.Task, error) {
	var t store.Task
	err := a.DB.Tx(func(q store.Querier) error {
		var err error
		if t, err = store.GetTask(q, id); err != nil {
			return err
		}
		if t.RunID != run {
			return nil
		}
		if spawnErr != nil {
			t.RunState, t.Error = store.RunExited, spawnErr.Error()
		} else {
			t.TmuxPane, t.HomeWindow = pane, window
		}
		return store.UpdateTask(q, t)
	})
	if spawnErr != nil {
		return t, spawnErr
	}
	return t, err
}

func (a *App) Resume(id string) (store.Task, error) {
	now := a.Now()
	var t store.Task
	var argv []string
	err := a.DB.Tx(func(q store.Querier) error {
		var err error
		if t, err = store.GetTask(q, id); err != nil {
			return err
		}
		if t.RunState != store.RunExited {
			return fmt.Errorf("%s is already running", t.Name)
		}
		ref := t.AgentSessionID
		if t.Agent == "omp" && t.SessionPath != "" {
			ref, _ = sessions.OmpMain(t.SessionPath) // never a subagent's file
		}
		if ref == "" {
			return fmt.Errorf("%s: no conversation id, nothing to resume", t.Name)
		}
		if argv, err = agentArgv(t.Agent, "", ref); err != nil {
			return err
		}
		t.RunID, t.RunState, t.RunStartedAt, t.Error = NewRunID(), store.RunStarting, now, ""
		t.Phase, t.PhaseSince = store.PhaseIdle, now
		if err := store.UpdateTask(q, t); err != nil {
			return err
		}
		return store.LogEvent(q, store.EventRow{TS: now, TaskID: t.ID, RunID: t.RunID, Source: "ty", Event: "resume", Applied: true})
	})
	if err != nil {
		return t, err
	}
	pane, window, err := a.spawn(t, argv)
	return a.afterSpawn(t.ID, t.RunID, pane, window, err)
}

// update loads a task, applies fn and logs the user action in one transaction.
func (a *App) update(id, event, detail string, fn func(t *store.Task, now int64) error) error {
	now := a.Now()
	return a.DB.Tx(func(q store.Querier) error {
		t, err := store.GetTask(q, id)
		if err != nil {
			return err
		}
		if err := fn(&t, now); err != nil {
			return err
		}
		if err := store.UpdateTask(q, t); err != nil {
			return err
		}
		return store.LogEvent(q, store.EventRow{TS: now, TaskID: t.ID, RunID: t.RunID, Source: "ty", Event: event, Applied: true, Detail: detail})
	})
}

func (a *App) SetDeferred(id string, deferred bool) error {
	ev := "restore"
	if deferred {
		ev = "defer"
	}
	err := a.update(id, ev, "", func(t *store.Task, now int64) error {
		before := store.InStack(t)
		t.Deferred = deferred
		store.Requeue(t, before, now)
		return nil
	})
	a.syncWindowName(id)
	return err
}

// SetPaused hides a task from every list (the agent keeps running) or brings it back.
func (a *App) SetPaused(id string, paused bool) error {
	ev := "unpause"
	if paused {
		ev = "pause"
	}
	err := a.update(id, ev, "", func(t *store.Task, now int64) error {
		before := store.InStack(t)
		t.PausedAt = 0
		if paused {
			t.PausedAt = now
		}
		store.Requeue(t, before, now)
		return nil
	})
	a.syncWindowName(id)
	return err
}

// SetHidden takes a window that is not a task (notes, an editor) out of every list, or back.
func (a *App) SetHidden(id string, hidden bool) error {
	ev := "unhide"
	if hidden {
		ev = "hide"
	}
	return a.update(id, ev, "", func(t *store.Task, now int64) error {
		before := store.InStack(t)
		t.HiddenAt = 0
		if hidden {
			t.HiddenAt = now
		}
		store.Requeue(t, before, now)
		return nil
	})
}

// applySuffix reads the user's "st" / "p" window naming when a window is adopted.
func applySuffix(t *store.Task, window string, now int64) {
	_, scheduled, paused := SplitName(window)
	t.Deferred = scheduled
	if paused {
		t.PausedAt = now
	}
	store.Requeue(t, false, now)
}

func (a *App) Skip(id string) error {
	return a.update(id, "skip", "", func(t *store.Task, now int64) error {
		if store.InStack(t) {
			t.QueuedAt = now
		}
		return nil
	})
}

func (a *App) SetPhase(id, phase string) error {
	kind := map[string]string{store.PhaseIdle: "mark_idle", store.PhaseWorking: "mark_working"}[phase]
	return a.update(id, kind, "", func(t *store.Task, now int64) error {
		store.Reduce(t, store.Event{Kind: kind, Source: "ty"}, now)
		return nil
	})
}

func (a *App) Rename(id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	name = safe.Text(name)
	err := a.update(id, "rename", name, func(t *store.Task, now int64) error {
		t.Name, _, _ = SplitName(name)
		return nil
	})
	a.syncWindowName(id)
	return err
}

func (a *App) SetNote(id, note string) error {
	note = safe.Text(note)
	return a.update(id, "note", note, func(t *store.Task, now int64) error {
		t.Note = strings.TrimSpace(note)
		return nil
	})
}

// Stop asks the agent to exit; Kill is the forced fallback.
func (a *App) Stop(id string) error {
	t, err := store.GetTask(a.DB, id)
	if err != nil {
		return err
	}
	if t.TmuxPane == "" {
		return fmt.Errorf("%s is not running", t.Name)
	}
	a.T.Run("send-keys", "-t", t.TmuxPane, "Escape")
	time.Sleep(200 * time.Millisecond)
	a.T.Run("send-keys", "-t", t.TmuxPane, "-l", "/exit")
	_, err = a.T.Run("send-keys", "-t", t.TmuxPane, "Enter")
	a.update(id, "stop", "", func(*store.Task, int64) error { return nil })
	return err
}

// Remove ends a task for good: its whole window is closed (agent, shell and any splits, so nothing
// is left to be adopted again) and the task leaves stackk. The event log keeps its history.
func (a *App) Remove(id string) error {
	t, err := store.GetTask(a.DB, id)
	if err != nil {
		return err
	}
	if t.TmuxPane != "" {
		if p, err := a.T.Pane(t.TmuxPane); err == nil {
			if p.UIWindow {
				a.T.Run("kill-pane", "-t", p.ID) // pulled into the dashboard: never close the dashboard's window
			} else {
				a.T.Run("kill-window", "-t", p.WindowID)
			}
		}
	} else if t.HomeWindow != "" && t.RunState != store.RunExited {
		a.T.Run("kill-window", "-t", t.HomeWindow)
	}
	return a.update(id, "remove", "", func(t *store.Task, now int64) error {
		t.RunState, t.TmuxPane, t.ArchivedAt = store.RunExited, "", now
		return nil
	})
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return p
}
