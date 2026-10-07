package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/krol22/stackk/cli/internal/config"
	"github.com/krol22/stackk/cli/internal/core"
	"github.com/krol22/stackk/cli/internal/store"
	"github.com/krol22/stackk/cli/internal/tmux"
	zone "github.com/lrstanley/bubblezone"
)

type menuItem struct{ id, label string }

type menuState struct {
	task  string
	x, y  int
	items []menuItem
}

type editState struct {
	task, kind string
	input      textinput.Model
}

type formState struct {
	inputs [3]textinput.Model // folder, name, prompt
	agent  string
	focus  int
	err    string
}

type stackModel struct {
	app    *core.App
	drawer *core.Drawer
	ops    chan func() // drawer and task actions run one at a time, in click order
	rec    *core.Reconciler
	notes  *core.Notifier

	stackID, stackName string
	tasks              []store.Task
	events             []store.EventRow
	out                string

	width, height int
	menu          *menuState
	edit          *editState
	form          *formState
	take          []tmux.Pane       // "add windows" list while open
	themes        bool              // theme picker open
	themeName     string            // as configured ("" for the default)
	paneStyles    map[string]string // the pane's own styles before a theme, put back on quit
	paused        bool              // paused-sessions modal open
	find          *findState        // "find session" modal
	hover         string
	blurred       bool // keyboard focus is in the drawer (tmux focus events)
	status        string
	statusErr     bool
	backendDown   bool
	loading       bool
	fullRunning   bool // a reconcile cycle is in flight
	loadSeq       int  // last load started
	appliedSeq    int  // newest load (or user action) the view reflects
	lastFull      time.Time
}

type dataMsg struct {
	seq         int
	tasks       []store.Task
	events      []store.EventRow
	out, name   string
	backendDown bool
	full        bool
	err         error
	theme       *string // the config's theme, read on full loads
}

type doneMsg struct {
	err  error
	info string
	quit bool
}

type tickMsg struct{}

func tick() tea.Cmd { return tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{} }) }

// RunStack runs the dashboard of the named stack in the given pane, wherever that pane is;
// the stack session (created on first use) only stores the task windows.
func RunStack(app *core.App, stack, uiPane string) error {
	if uiPane == "" {
		return fmt.Errorf("stackk runs inside tmux: start it in any pane")
	}
	s, err := app.EnsureStack(stack)
	if err != nil {
		return err
	}
	zone.NewGlobal()
	cfg, _ := config.Load() // a broken config file must not keep the dashboard from starting
	m := &stackModel{app: app, drawer: &core.Drawer{A: app, UI: uiPane, Stack: s.ID}, rec: core.NewReconciler(),
		notes: core.NewNotifier(), stackID: s.ID, stackName: s.Name, ops: make(chan func(), 64)}
	if err := m.drawer.Ensure(); err != nil {
		return err
	}
	m.ownPaneStyles()
	m.setTheme(cfg.Theme)
	go func() {
		for op := range m.ops {
			op()
		}
	}()
	_, err = tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseAllMotion(), tea.WithReportFocus()).Run()
	return err
}

func (m *stackModel) Init() tea.Cmd { return tea.Batch(m.load(true), tick()) }

// load reads the state in the background. Loads are numbered so a slow one can never overwrite
// newer data, and full ones (reconcile, notifications) never overlap: the tick only starts one
// when the previous has come back.
func (m *stackModel) load(full bool) tea.Cmd {
	app, drawer, rec, notes, stack := m.app, m.drawer, m.rec, m.notes, m.stackID
	m.loadSeq++
	seq := m.loadSeq
	return func() tea.Msg {
		d := dataMsg{seq: seq, full: full}
		if full {
			if err := rec.Run(app); err == core.ErrBackend {
				d.backendDown = true
			}
			drawer.Ensure()
		}
		d.out = drawer.Out()
		if full {
			notes.Check(app, stack, drawer.UI, d.out)
		}
		var err error
		if d.tasks, err = store.Tasks(app.DB, stack); err != nil {
			d.err = err
			return d
		}
		if d.events, err = store.Events(app.DB, stack, "", 30); err != nil {
			d.err = err
			return d
		}
		if full {
			if cfg, err := config.Load(); err == nil {
				d.theme = &cfg.Theme
			}
		}
		if full { // the stack can be renamed; no need to ask every second
			if ss, err := app.T.Sessions(); err == nil {
				for _, s := range ss {
					if s.ID == stack {
						d.name = s.Name
					}
				}
			}
		}
		return d
	}
}

// run queues fn behind earlier actions (so "close" then "open other" never interleave) and
// reports its result.
func (m *stackModel) run(fn func() (string, error)) tea.Cmd {
	done := make(chan doneMsg, 1)
	m.ops <- func() {
		info, err := fn()
		done <- doneMsg{err: err, info: info}
	}
	return func() tea.Msg { return <-done }
}

func (m *stackModel) setStatus(s string, isErr bool) { m.status, m.statusErr = s, isErr }

func (m *stackModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tickMsg:
		cmds := []tea.Cmd{tick()}
		if !m.loading {
			m.loading = true
			full := !m.fullRunning && time.Since(m.lastFull) >= 5*time.Second
			if full {
				m.lastFull, m.fullRunning = time.Now(), true
			}
			cmds = append(cmds, m.load(full))
		}
		return m, tea.Batch(cmds...)
	case dataMsg:
		m.loading = false
		if msg.full {
			m.fullRunning = false
			m.backendDown = msg.backendDown
		}
		if msg.err != nil {
			m.setStatus("can't read state: "+msg.err.Error(), true) // keep showing the last good data
			return m, nil
		}
		if msg.seq < m.appliedSeq {
			return m, nil // started before newer data or a user action: stale
		}
		m.appliedSeq = msg.seq
		m.tasks, m.events, m.out = msg.tasks, msg.events, msg.out
		if msg.name != "" {
			m.stackName = msg.name
		}
		if msg.theme != nil && *msg.theme != m.themeName { // `stackk theme` changed the config
			return m, m.setTheme(*msg.theme)
		}
	case doneMsg:
		if msg.quit {
			return m, tea.Quit
		}
		if msg.err != nil {
			m.setStatus(msg.err.Error(), true)
		} else if msg.info != "" {
			m.setStatus(msg.info, false)
		}
		return m, m.load(false)
	case tea.BlurMsg:
		m.blurred = true
	case tea.FocusMsg:
		// Not a close signal: tmux also sends focus-in when the drawer opens (unzoom). Closing on
		// "click on the dashboard" is handled by the first click after a blur.
		m.blurred = false
	case findIndexMsg:
		if m.find != nil {
			m.find.all, m.find.loading = msg.all, false
			m.find.local()
		}
	case findAIMsg:
		if m.find != nil && msg.query == strings.TrimSpace(m.find.input.Value()) {
			m.find.asking = false
			if msg.err != nil {
				m.find.err = msg.err.Error()
			} else {
				m.find.hits, m.find.aiQuery = msg.hits, msg.query
			}
		}
	case tea.KeyMsg:
		return m.key(msg)
	case tea.MouseMsg:
		return m.mouse(msg)
	}
	return m, nil
}

func (m *stackModel) quit() tea.Cmd {
	d := m.drawer
	restored := make(chan struct{})
	m.ops <- func() { // after any theme change still queued
		m.restorePaneStyles()
		close(restored)
	}
	return func() tea.Msg {
		<-restored
		d.Quit()
		return doneMsg{quit: true}
	}
}

func (m *stackModel) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case m.find != nil:
		switch k.String() {
		case "esc":
			m.find = nil
		case "enter":
			return m, m.findAI()
		default:
			var cmd tea.Cmd
			m.find.input, cmd = m.find.input.Update(k)
			m.find.err, m.find.aiQuery = "", ""
			m.find.local()
			return m, cmd
		}
		return m, nil
	case m.edit != nil:
		switch k.String() {
		case "esc":
			m.edit = nil
		case "enter":
			return m, m.submitEdit()
		default:
			var cmd tea.Cmd
			m.edit.input, cmd = m.edit.input.Update(k)
			return m, cmd
		}
		return m, nil
	case m.form != nil:
		switch k.String() {
		case "esc":
			m.form = nil
		case "tab", "down":
			m.focusForm((m.form.focus + 1) % 3)
		case "shift+tab", "up":
			m.focusForm((m.form.focus + 2) % 3)
		case "enter":
			return m, m.submitForm()
		default:
			var cmd tea.Cmd
			m.form.inputs[m.form.focus], cmd = m.form.inputs[m.form.focus].Update(k)
			return m, cmd
		}
		return m, nil
	}
	switch k.String() {
	case "esc":
		m.menu, m.take, m.paused, m.themes = nil, nil, false, false
	case "q", "ctrl+c":
		return m, m.quit()
	}
	return m, nil
}

func in(id string, msg tea.MouseMsg) bool { return zone.Get(id).InBounds(msg) }

func (m *stackModel) byID(id string) (store.Task, bool) {
	for _, t := range m.tasks {
		if t.ID == id {
			return t, true
		}
	}
	return store.Task{}, false
}
