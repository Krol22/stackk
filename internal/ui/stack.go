package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"

	"github.com/krol22/stackk/internal/core"
	"github.com/krol22/stackk/internal/sessions"
	"github.com/krol22/stackk/internal/store"
	"github.com/krol22/stackk/internal/tmux"
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
	take          []tmux.Pane // "add windows" list while open
	paused        bool        // paused-sessions modal open
	find          *findState  // "find session" modal
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
	m := &stackModel{app: app, drawer: &core.Drawer{A: app, UI: uiPane, Stack: s.ID}, rec: core.NewReconciler(),
		notes: core.NewNotifier(), stackID: s.ID, stackName: s.Name, ops: make(chan func(), 64)}
	if err := m.drawer.Ensure(); err != nil {
		return err
	}
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
	return func() tea.Msg {
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
		m.menu, m.take, m.paused = nil, nil, false
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

func (m *stackModel) mouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Action == tea.MouseActionMotion {
		m.hover = ""
		for _, t := range m.tasks {
			if in("t:"+t.ID, msg) || in("t2:"+t.ID, msg) {
				m.hover = t.ID
			}
		}
		for _, c := range m.take {
			if in("k:"+c.ID, msg) {
				m.hover = "k:" + c.ID
			}
		}
		if m.find != nil {
			for i := range m.find.hits {
				if in(fmt.Sprintf("fs:%d", i), msg) || in(fmt.Sprintf("fs2:%d", i), msg) {
					m.hover = fmt.Sprintf("fs:%d", i)
				}
			}
		}
		if m.paused {
			for _, t := range append(m.sorted("paused"), m.sorted("hidden")...) {
				if in("pz:"+t.ID, msg) {
					m.hover = "pz:" + t.ID
				}
			}
		}
		if m.menu != nil {
			for _, it := range m.menu.items {
				if in("m:"+it.id, msg) {
					m.hover = "m:" + it.id
				}
			}
		}
		return m, nil
	}
	if msg.Action != tea.MouseActionPress {
		return m, nil
	}
	left, right := msg.Button == tea.MouseButtonLeft, msg.Button == tea.MouseButtonRight
	if !left && !right {
		return m, nil
	}
	if m.blurred {
		// A click on the dashboard while the drawer has focus. Clicking a task or a button acts as
		// usual (opening another task switches the drawer in place); clicking the background closes.
		m.blurred = false
		if m.out != "" {
			modal := func() bool { return m.menu != nil || m.edit != nil || m.form != nil || m.take != nil || m.paused }
			before, out := modal(), m.out
			model, cmd := m.mouse(msg)
			if cmd == nil && modal() == before && m.out == out {
				return model, m.closeDrawer()
			}
			return model, cmd
		}
	}
	if m.menu != nil {
		menu := m.menu
		m.menu = nil
		for _, it := range menu.items {
			if in("m:"+it.id, msg) {
				return m, m.act(menu.task, it.id)
			}
		}
		return m, nil
	}
	if m.edit != nil {
		switch {
		case in("e:ok", msg):
			return m, m.submitEdit()
		case in("e:cancel", msg):
			m.edit = nil
		}
		return m, nil
	}
	if m.form != nil {
		return m, m.formClick(msg)
	}
	if m.take != nil {
		return m, m.takeClick(msg)
	}
	if m.paused {
		return m, m.pausedClick(msg)
	}
	if m.find != nil {
		return m, m.findClick(msg)
	}
	switch {
	case in("b:next", msg):
		return m, m.next()
	case in("b:new", msg):
		m.openForm()
		return m, nil
	case in("b:find", msg):
		return m, m.openFind()
	case in("b:paused", msg):
		m.paused = true
		return m, nil
	case in("b:take", msg):
		if c, err := m.app.Candidates(); err == nil {
			m.take = c
		}
		return m, nil
	case in("b:close", msg):
		return m, m.closeDrawer()
	case in("b:outmenu", msg) && m.out != "":
		if t, ok := m.byID(m.out); ok {
			m.openMenu(t, msg.X, msg.Y)
		}
		return m, nil
	}
	for _, t := range m.tasks {
		if in("x:"+t.ID, msg) {
			m.openMenu(t, msg.X, msg.Y)
			return m, nil
		}
		if in("t:"+t.ID, msg) || in("t2:"+t.ID, msg) {
			if right {
				m.openMenu(t, msg.X, msg.Y)
				return m, nil
			}
			return m, m.primary(t)
		}
	}
	return m, nil
}

// primary is the left-click action: show the task in the drawer, or resume a finished task first.
func (m *stackModel) primary(t store.Task) tea.Cmd {
	if t.RunState == store.RunExited {
		return m.resumeAndOpen(t.ID)
	}
	return m.openPane(t)
}

func (m *stackModel) openPane(t store.Task) tea.Cmd {
	if t.TmuxPane == "" {
		return m.run(func() (string, error) { return "", fmt.Errorf("%s has no terminal yet", t.Name) })
	}
	d, pane := m.drawer, t.TmuxPane
	m.out, m.appliedSeq = t.ID, m.loadSeq+1 // loads already running predate this
	return m.run(func() (string, error) { return "", d.Open(pane, true) })
}

func (m *stackModel) closeDrawer() tea.Cmd {
	d := m.drawer
	m.out, m.appliedSeq = "", m.loadSeq+1
	return m.run(func() (string, error) { return "", d.Close() })
}

func (m *stackModel) resumeAndOpen(id string) tea.Cmd {
	app, d := m.app, m.drawer
	return m.run(func() (string, error) {
		t, err := app.Resume(id)
		if err != nil {
			return "", err
		}
		return "resumed " + t.Name, d.Open(t.TmuxPane, true)
	})
}

func (m *stackModel) next() tea.Cmd {
	for _, t := range m.sorted("stack") {
		if t.ID != m.out && t.TmuxPane != "" {
			return m.openPane(t)
		}
	}
	m.setStatus("nothing is waiting for you", false)
	return nil
}

func (m *stackModel) openMenu(t store.Task, x, y int) {
	var items []menuItem
	add := func(id, label string) { items = append(items, menuItem{id, label}) }
	live := t.RunState != store.RunExited
	switch ViewOf(t) {
	case "stack":
		add("open", "Open")
		add("skip", "Skip (to the end)")
		add("defer", "Schedule")
		if t.Phase == store.PhaseUnknown {
			add("mark_working", "Mark: working")
		}
	case "running":
		add("open", "Open")
		add("defer", "Schedule")
		add("mark_idle", "Mark: waiting for me")
	case "deferred":
		if live {
			add("open", "Open")
		} else {
			add("resume", "Resume")
		}
		add("restore", "Back to stack")
	case "exited":
		add("resume", "Resume")
	}
	add("pause", "Pause")
	add("hide", "Hide (not a task)")
	add("-", "")
	add("rename", "Rename…")
	add("note", "Note…")
	if live && t.Agent != "" && t.Phase != store.PhaseShell {
		add("stop", "Stop")
	}
	if live {
		add("kill", "Close & remove")
	} else {
		add("kill", "Remove")
	}
	w := 26
	h := len(items) + 3
	if x+w > m.width {
		x = max(0, m.width-w)
	}
	if y+h > m.height {
		y = max(0, m.height-h)
	}
	m.menu = &menuState{task: t.ID, x: x, y: y, items: items}
}

func (m *stackModel) act(id, action string) tea.Cmd {
	app := m.app
	t, ok := m.byID(id)
	if !ok {
		return nil
	}
	switch action {
	case "open":
		return m.openPane(t)
	case "resume":
		return m.resumeAndOpen(id)
	case "skip":
		return m.run(func() (string, error) { return t.Name + " moved to the end", app.Skip(id) })
	case "defer":
		var cmds []tea.Cmd
		if m.out == id {
			cmds = append(cmds, m.closeDrawer())
		}
		return tea.Sequence(append(cmds, m.run(func() (string, error) { return "scheduled " + t.Name, app.SetDeferred(id, true) }))...)
	case "pause":
		var cmds []tea.Cmd
		if m.out == id {
			cmds = append(cmds, m.closeDrawer())
		}
		return tea.Sequence(append(cmds, m.run(func() (string, error) { return "paused " + t.Name, app.SetPaused(id, true) }))...)
	case "hide":
		var cmds []tea.Cmd
		if m.out == id {
			cmds = append(cmds, m.closeDrawer())
		}
		return tea.Sequence(append(cmds, m.run(func() (string, error) { return "hid " + t.Name, app.SetHidden(id, true) }))...)
	case "restore":
		return m.run(func() (string, error) { return t.Name + " is back", app.SetDeferred(id, false) })
	case "mark_idle":
		return m.run(func() (string, error) { return "", app.SetPhase(id, store.PhaseIdle) })
	case "mark_working":
		return m.run(func() (string, error) { return "", app.SetPhase(id, store.PhaseWorking) })
	case "stop":
		return m.run(func() (string, error) { return "stopping " + t.Name, app.Stop(id) })
	case "kill":
		var cmds []tea.Cmd
		if m.out == id {
			cmds = append(cmds, m.closeDrawer())
		}
		return tea.Sequence(append(cmds, m.run(func() (string, error) { return "removed " + t.Name, app.Remove(id) }))...)
	case "rename", "note":
		in := textinput.New()
		in.Prompt = ""
		in.CharLimit = 200
		in.Width = 40
		if action == "rename" {
			in.SetValue(t.Name)
		} else {
			in.SetValue(t.Note)
		}
		in.Focus()
		m.edit = &editState{task: id, kind: action, input: in}
	}
	return nil
}

func (m *stackModel) submitEdit() tea.Cmd {
	e := m.edit
	m.edit = nil
	app, val := m.app, e.input.Value()
	if e.kind == "rename" {
		return m.run(func() (string, error) { return "", app.Rename(e.task, val) })
	}
	return m.run(func() (string, error) { return "", app.SetNote(e.task, val) })
}

// --- paused sessions: hidden from every list until brought back ---

func (m *stackModel) pausedClick(msg tea.MouseMsg) tea.Cmd {
	if in("pz:close", msg) {
		m.paused = false
		return nil
	}
	for _, t := range m.sorted("hidden") {
		if in("pz:"+t.ID, msg) {
			m.paused = false
			app, id, name := m.app, t.ID, t.Name
			return m.run(func() (string, error) { return name + " is back", app.SetHidden(id, false) })
		}
	}
	for _, t := range m.sorted("paused") {
		if in("pz:"+t.ID, msg) {
			m.paused = false
			app, id, name := m.app, t.ID, t.Name
			exited := t.RunState == store.RunExited
			unpause := m.run(func() (string, error) { return name + " is back", app.SetPaused(id, false) })
			t.PausedAt = 0
			if exited {
				return tea.Sequence(unpause, m.resumeAndOpen(id))
			}
			return tea.Sequence(unpause, m.openPane(t))
		}
	}
	return nil
}

func (m *stackModel) viewPaused(w int) string {
	lines := []string{"", " " + sGrey.Render("click a session to bring it back:"), ""}
	for _, t := range m.sorted("paused") {
		state := "running"
		if t.RunState == store.RunExited {
			state = "ended · resumes"
		}
		row := " " + sBright.Render(fit(t.Name, 26)) + " " + agentStyle(t.Agent).Render(fit(t.Agent, 7)) +
			sGrey.Render(fit("paused "+ago(t.PausedAt), 13)) + sDim.Render(fit(state, 17)) + sDim.Render(shortPath(t.Cwd))
		st := sText
		if m.hover == "pz:"+t.ID {
			st = st.Background(cHover)
		}
		lines = append(lines, zone.Mark("pz:"+t.ID, st.Render(padRight(row, w-2))))
	}
	if hidden := m.sorted("hidden"); len(hidden) > 0 {
		lines = append(lines, "", " "+sGrey.Render("hidden (not tasks) — click to show again:"), "")
		for _, t := range hidden {
			row := " " + sBright.Render(fit(t.Name, 26)) + " " + agentStyle(t.Agent).Render(fit(agentLabel(t.Agent), 7)) +
				sGrey.Render(fit("hidden "+ago(t.HiddenAt), 13)) + sDim.Render(shortPath(t.Cwd))
			st := sText
			if m.hover == "pz:"+t.ID {
				st = st.Background(cHover)
			}
			lines = append(lines, zone.Mark("pz:"+t.ID, st.Render(padRight(row, w-2))))
		}
	}
	lines = append(lines, "", " "+zone.Mark("pz:close", button("close", true)))
	return box("paused & hidden", sBright, "", lines, w, len(lines)+2)
}

// --- "add windows": move agent windows from other sessions into this stack ---

func (m *stackModel) takeClick(msg tea.MouseMsg) tea.Cmd {
	if in("k:close", msg) {
		m.take = nil
		return nil
	}
	for i, c := range m.take {
		if in("k:"+c.ID, msg) {
			app, stack, win, name := m.app, m.stackName, c.WindowID, c.WindowName
			m.take = append(m.take[:i:i], m.take[i+1:]...)
			return m.run(func() (string, error) { return "added " + name, app.Take(win, stack) })
		}
	}
	return nil
}

func (m *stackModel) viewTake(w int) string {
	lines := []string{"", " " + sGrey.Render("click a window to move it into the stack:"), ""}
	if len(m.take) == 0 {
		lines = append(lines, " "+sDim.Render("no agent windows outside the stack"))
	}
	for _, c := range m.take {
		agent := tmux.AgentOf(c.Command)
		row := " " + sText.Render(fit(c.SessionName+":"+c.WindowIndex, 12)) + sBright.Render(fit(c.WindowName, 24)) + " " +
			agentStyle(agent).Render(fit(agent, 7)) + sDim.Render(shortPath(c.Path))
		st := sText
		if m.hover == "k:"+c.ID {
			st = st.Background(cHover)
		}
		lines = append(lines, zone.Mark("k:"+c.ID, st.Render(padRight(row, w-2))))
	}
	lines = append(lines, "", " "+zone.Mark("k:close", button("done", true)))
	return box("add windows", sBright, "", lines, w, len(lines)+2)
}

// --- new task form ---

func (m *stackModel) recentDirs() []string {
	seen := map[string]bool{}
	var out []string
	ts := append([]store.Task(nil), m.tasks...)
	sort.Slice(ts, func(i, j int) bool { return ts[i].CreatedAt > ts[j].CreatedAt })
	for _, t := range ts {
		if t.Cwd != "" && !seen[t.Cwd] {
			seen[t.Cwd] = true
			out = append(out, t.Cwd)
		}
		if len(out) == 5 {
			break
		}
	}
	return out
}

func (m *stackModel) openForm() {
	f := &formState{agent: "claude"}
	for i := range f.inputs {
		f.inputs[i] = textinput.New()
		f.inputs[i].Prompt = ""
		f.inputs[i].Width = 50
		f.inputs[i].CharLimit = 500
	}
	dir, _ := os.Getwd()
	if r := m.recentDirs(); len(r) > 0 {
		dir = r[0]
	}
	f.inputs[0].SetValue(dir)
	f.inputs[1].Placeholder = "defaults to the folder name"
	f.inputs[2].Placeholder = "optional first prompt"
	m.form = f
	m.focusForm(0)
}

func (m *stackModel) focusForm(i int) {
	for j := range m.form.inputs {
		m.form.inputs[j].Blur()
	}
	m.form.focus = i
	m.form.inputs[i].Focus()
}

func (m *stackModel) formClick(msg tea.MouseMsg) tea.Cmd {
	f := m.form
	for i, id := range []string{"f:folder", "f:name", "f:prompt"} {
		if in(id, msg) {
			m.focusForm(i)
			return nil
		}
	}
	for i, dir := range m.recentDirs() {
		if in(fmt.Sprintf("f:recent:%d", i), msg) {
			f.inputs[0].SetValue(dir)
			return nil
		}
	}
	switch {
	case in("f:claude", msg):
		f.agent = "claude"
	case in("f:omp", msg):
		f.agent = "omp"
	case in("f:ok", msg):
		return m.submitForm()
	case in("f:cancel", msg):
		m.form = nil
	}
	return nil
}

func (m *stackModel) submitForm() tea.Cmd {
	f := m.form
	folder, name, prompt := f.inputs[0].Value(), f.inputs[1].Value(), f.inputs[2].Value()
	app, d, stack, agent := m.app, m.drawer, m.stackID, f.agent
	m.form = nil
	return m.run(func() (string, error) {
		t, err := app.NewTask(stack, folder, name, agent, prompt)
		if err != nil {
			return "", err
		}
		return "new session " + t.Name, d.Open(t.TmuxPane, true)
	})
}

// --- view ---

func (m *stackModel) sorted(view string) []store.Task {
	var out []store.Task
	for _, t := range m.tasks {
		v := ViewOf(t)
		if v == view || (view == "deferred" && v == "exited") {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		switch view {
		case "stack":
			return out[i].QueuedAt < out[j].QueuedAt
		case "running":
			return out[i].PhaseSince < out[j].PhaseSince
		}
		return out[i].CreatedAt > out[j].CreatedAt
	})
	return out
}

func (m *stackModel) View() string {
	if m.width == 0 {
		return ""
	}
	var base string
	switch {
	case m.width < 100:
		base = m.viewSidebar()
	default:
		base = m.viewFull()
	}
	s := zone.Scan(base)
	if m.menu != nil {
		s = zone.Scan(overlay(s, m.viewMenu(), m.menu.x, m.menu.y))
	}
	if m.form != nil {
		form := m.viewForm()
		fw := lipgloss.Width(strings.SplitN(form, "\n", 2)[0])
		s = zone.Scan(overlay(s, form, max(0, (m.width-fw)/2), max(1, m.height/2-8)))
	}
	if m.find != nil {
		w := min(m.width-4, 120)
		s = zone.Scan(overlay(s, m.viewFind(w), max(0, (m.width-w)/2), 1))
	}
	if m.paused {
		w := min(m.width-2, 90)
		s = zone.Scan(overlay(s, m.viewPaused(w), max(0, (m.width-w)/2), 2))
	}
	if m.take != nil {
		w := min(m.width-2, 90)
		s = zone.Scan(overlay(s, m.viewTake(w), max(0, (m.width-w)/2), 2))
	}
	if m.edit != nil {
		w := 50
		s = zone.Scan(overlay(s, m.viewEdit(w), max(0, (m.width-w)/2), max(0, m.height/2-3)))
	}
	return s
}

func (m *stackModel) counts() string {
	return sYellow.Render(fmt.Sprintf("● stack %d", len(m.sorted("stack")))) + "  " +
		sGrey.Render(fmt.Sprintf("◐ running %d", len(m.sorted("running")))) + "  " +
		sPurple.Render(fmt.Sprintf("z scheduled %d", len(m.sorted("deferred"))))
}

func (m *stackModel) statusLine(w int) string {
	switch {
	case m.backendDown:
		return sRed.Render(fit(" tmux is not responding — state may be stale", w))
	case m.statusErr:
		return sRed.Render(fit(" "+m.status, w))
	case m.status != "":
		return sGrey.Render(fit(" "+m.status, w))
	}
	return ""
}

// rowStyles returns styles carrying the row background (selected / hovered).
func (m *stackModel) rowBg(t store.Task) func(lipgloss.Style) lipgloss.Style {
	var bg lipgloss.Color
	switch {
	case t.ID == m.out:
		bg = cSel
	case t.ID == m.hover:
		bg = cHover
	}
	return func(s lipgloss.Style) lipgloss.Style {
		if bg != "" {
			return s.Background(bg)
		}
		return s
	}
}

var spinner = []string{"◐", "◓", "◑", "◒"}

func (m *stackModel) mark(t store.Task) (string, lipgloss.Style) {
	switch {
	case t.RunState == store.RunExited:
		return "○", sDim
	case t.Phase == store.PhaseShell:
		return "$", sGrey
	case t.RunState == store.RunStarting:
		return "…", sGrey
	case t.Phase == store.PhaseWaiting:
		return "?", sYellow
	case t.Phase == store.PhaseUnknown:
		return "⁇", sYellow
	case t.Phase == store.PhaseIdle:
		return "✓", sGrey
	}
	return spinner[int(time.Now().Unix())%4], sGrey
}

func (m *stackModel) detail(t store.Task, view string) (string, lipgloss.Style) {
	switch {
	case t.Error != "":
		return t.Error, sRed
	case view == "running" && t.LastTool != "":
		return "last: " + t.LastTool + " · " + ago(t.LastToolAt), sDim
	case view == "deferred" && t.Note != "":
		return t.Note, sGrey
	case t.Phase == store.PhaseUnknown && t.LastMsg == "":
		return "no signal from the agent — take a look", sDim
	}
	return t.LastMsg, sDim
}

// taskRows renders one task as two lines, full width w.
func (m *stackModel) taskRows(t store.Task, view string, pos int, w int) []string {
	st := m.rowBg(t)
	lead := "  "
	if pos > 0 {
		lead = fmt.Sprintf("%2d", pos)
	}
	if t.ID == m.out {
		lead = "▶" + strings.TrimSpace(lead)
		lead = fit(lead, 2)
	}
	markTxt, markSt := m.mark(t)
	age := ago(t.PhaseSince)
	nameW := max(8, w-4-9-3-6-4)
	leadSt := st(sGrey)
	if view == "stack" {
		leadSt = st(sYellow.Bold(true))
	} else if view != "running" {
		leadSt = st(sPurple)
		lead = " z"
		if t.RunState == store.RunExited {
			lead = " ○"
			leadSt = st(sDim)
		}
	}
	line1 := leadSt.Render(" "+lead+" ") +
		st(sBright).Render(fit(t.Name, nameW)) + st(sText).Render(" ") +
		st(agentStyle(t.Agent)).Render(fit(agentLabel(t.Agent), 8)) + st(sText).Render(" ") +
		st(markSt).Render(fit(markTxt, 2)) + st(sText).Render(" ") +
		st(sGrey).Render(fit(fmt.Sprintf("%5s", age), 6)) +
		zone.Mark("x:"+t.ID, st(sGrey).Render(" … "))
	line1 = padRight(line1, w)
	text, ds := m.detail(t, view)
	line2 := st(ds).Render(fit("     "+text, w))
	return []string{zone.Mark("t:"+t.ID, line1), zone.Mark("t2:"+t.ID, line2)}
}

func (m *stackModel) header(w int) string {
	left := " " + sBright.Render("STACKK") + sDim.Render(" · ") + sText.Render(fit(m.stackName, 32)) + "   " + m.counts()
	right := ""
	if p, h := len(m.sorted("paused")), len(m.sorted("hidden")); p+h > 0 {
		label := fmt.Sprintf("⏸ paused %d", p)
		if h > 0 {
			label += fmt.Sprintf(" · hidden %d", h)
		}
		right = zone.Mark("b:paused", button(label, false)) + " "
	}
	right += zone.Mark("b:find", button("⌕ find", false)) + " " + zone.Mark("b:take", button("⇲ add windows", false)) + " " + zone.Mark("b:new", button("+ new", false)) + " " +
		zone.Mark("b:next", button("next ▶", true)) + " "
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return padRight(left, w)
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m *stackModel) viewFull() string {
	w, h := m.width, m.height
	const pad, gap = 1, 2 // outer margin and space between columns
	inner := w - 2*pad
	logH := 0
	if h >= 28 {
		logH = 7
	}
	// header, blank, body, [blank, log], status
	bodyH := h - 3
	if logH > 0 {
		bodyH -= logH + 1
	}
	lw := inner * 58 / 100
	rw := inner - lw - gap

	var stackLines []string
	stack := m.sorted("stack")
	for i, t := range stack {
		stackLines = append(stackLines, "")
		stackLines = append(stackLines, m.taskRows(t, "stack", i+1, lw-2)...)
	}
	if len(stack) == 0 {
		stackLines = m.emptyStack(lw - 2)
	}
	left := box("STACK", sYellow.Bold(true), "oldest first", stackLines, lw, bodyH)

	rows := func(view string) []string {
		var out []string
		for _, t := range m.sorted(view) {
			out = append(out, "")
			out = append(out, m.taskRows(t, view, 0, rw-2)...)
		}
		return out
	}
	runLines, defLines := rows("running"), rows("deferred")
	// RUNNING takes what SCHEDULED leaves, one blank line between boxes.
	defH := 4
	if len(defLines) > 0 {
		defH = max(4, min(len(defLines)+3, bodyH/2))
	}
	runH := bodyH - defH - 1
	right := box("RUNNING", sGrey.Bold(true), "do not disturb", runLines, rw, runH) + "\n\n" +
		box("SCHEDULED", sPurple.Bold(true), "", defLines, rw, defH)

	margin := strings.Repeat(" ", pad)
	indent := func(block string) string {
		lines := strings.Split(block, "\n")
		for i := range lines {
			lines[i] = margin + lines[i]
		}
		return strings.Join(lines, "\n")
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, strings.Repeat(" ", gap), right)
	parts := []string{m.header(w), "", indent(body)}
	if logH > 0 {
		parts = append(parts, "", indent(box("LOG", sGrey.Bold(true), "", m.logLines(inner-2, logH-2), inner, logH)))
	}
	parts = append(parts, m.statusLine(w))
	return strings.Join(parts, "\n")
}

func (m *stackModel) emptyStack(w int) []string {
	center := func(s string, st lipgloss.Style) string {
		pad := max(0, (w-lipgloss.Width(s))/2)
		return strings.Repeat(" ", pad) + st.Render(s)
	}
	lines := []string{"", "", center("nothing is waiting for you", sGrey)}
	if r := m.sorted("running"); len(r) > 0 {
		lines = append(lines, center(fmt.Sprintf("working longest: %s (%s)", r[0].Name, ago(r[0].PhaseSince)), sDim))
	}
	return lines
}

func (m *stackModel) logLines(w, n int) []string {
	var out []string
	for i, e := range m.events {
		if i == n {
			break
		}
		detail := e.Detail
		if !e.Applied {
			detail += " (old run, ignored)"
		}
		source := e.Source
		if source == "ty" { // stored as "ty" (you) since the first version
			source = "you"
		}
		line := " " + sDim.Render(time.Unix(e.TS, 0).Format("15:04:05")) + "  " + eventIcon(e.Event) + "  " +
			sText.Render(fit(e.TaskName, 24)) + " " + sGrey.Render(fit(source, 10)) + " " +
			sGrey.Render(fit(e.Event, 11)) + " " + sDim.Render(detail)
		out = append(out, padRight(line, w))
	}
	return out
}

func (m *stackModel) viewSidebar() string {
	w, h := m.width, m.height
	var lines []string
	lines = append(lines, " "+sBright.Render("STACKK")+sDim.Render(" · ")+sText.Render(m.stackName))
	lines = append(lines, " "+sYellow.Render(fmt.Sprintf("● %d", len(m.sorted("stack"))))+"  "+
		sGrey.Render(fmt.Sprintf("◐ %d", len(m.sorted("running"))))+"  "+sPurple.Render(fmt.Sprintf("z %d", len(m.sorted("deferred")))))
	if t, ok := m.byID(m.out); ok {
		lines = append(lines, "", " "+sGrey.Render("open: ")+sBright.Render(fit(t.Name, w-14)),
			" "+zone.Mark("b:close", button("close ▶", false))+" "+zone.Mark("b:outmenu", button("…", false)))
	}
	if n := len(m.sorted("paused")) + len(m.sorted("hidden")); n > 0 {
		lines = append(lines, "", " "+zone.Mark("b:paused", button(fmt.Sprintf("⏸ %d", n), false)))
	}
	lines = append(lines, "", " "+zone.Mark("b:find", button("⌕", false))+" "+zone.Mark("b:take", button("⇲", false))+" "+zone.Mark("b:new", button("+", false))+" "+
		zone.Mark("b:next", button("next ▶", true)), "")
	section := func(title string, st lipgloss.Style, view string) {
		if view != "stack" {
			lines = append(lines, "")
		}
		lines = append(lines, rule(title, st, w))
		for i, t := range m.sorted(view) {
			pos := 0
			if view == "stack" {
				pos = i + 1
			}
			rows := m.taskRows(t, view, pos, w)
			if view == "running" {
				rows = rows[:1]
			}
			lines = append(lines, rows...)
		}
	}
	section("STACK", sYellow.Bold(true), "stack")
	section("RUNNING", sGrey.Bold(true), "running")
	section("SCHEDULED", sPurple.Bold(true), "deferred")

	if len(lines) > h-1 {
		lines = lines[:h-1]
	}
	for len(lines) < h-1 {
		lines = append(lines, "")
	}
	for i := range lines {
		lines[i] = padRight(lines[i], w)
	}
	return strings.Join(append(lines, m.statusLine(w)), "\n")
}

// lastDirs shows the end of a path ("acme/api"): where it was is what you recognize.
func lastDirs(p string) string {
	parts := strings.Split(strings.TrimRight(p, "/"), "/")
	if len(parts) <= 2 {
		return shortPath(p)
	}
	return "…/" + strings.Join(parts[len(parts)-2:], "/")
}

func shortPath(p string) string {
	home, _ := os.UserHomeDir()
	if home != "" && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

func (m *stackModel) viewMenu() string {
	w := 26
	var lines []string
	for _, it := range m.menu.items {
		if it.id == "-" {
			lines = append(lines, sLine.Render(strings.Repeat("─", w-2)))
			continue
		}
		st := sText
		if it.id == "kill" {
			st = sRed
		}
		if m.hover == "m:"+it.id {
			st = st.Background(cHover)
		}
		lines = append(lines, zone.Mark("m:"+it.id, st.Render(fit(" "+it.label, w-2))))
	}
	name := ""
	if t, ok := m.byID(m.menu.task); ok {
		name = t.Name
	}
	if lipgloss.Width(name) > w-8 {
		name = fit(name, w-8)
	}
	return box(name, sBright, "", lines, w, len(lines)+2)
}

func (m *stackModel) viewEdit(w int) string {
	title := "Rename"
	if m.edit.kind == "note" {
		title = "Note"
	}
	lines := []string{"", " " + m.edit.input.View(), "",
		" " + zone.Mark("e:cancel", button("cancel", false)) + " " + zone.Mark("e:ok", button("save", true))}
	return box(title, sBright, "", lines, w, len(lines)+2)
}

func (m *stackModel) viewForm() string {
	w := min(m.width, 90)
	f := m.form
	field := func(label, id string, i int) string {
		return " " + sGrey.Render(fit(label, 8)) + zone.Mark(id, sLine.Render("[ ")+padRight(f.inputs[i].View(), 52)+sLine.Render(" ]"))
	}
	var chips []string
	for i, d := range m.recentDirs() {
		chips = append(chips, zone.Mark(fmt.Sprintf("f:recent:%d", i), button(filepath.Base(d), false)))
	}
	radio := func(label, id string) string {
		mark := "○"
		st := sGrey
		if f.agent == label {
			mark, st = "●", agentStyle(label)
		}
		return zone.Mark(id, st.Render("( "+mark+" "+label+" )"))
	}
	lines := []string{"",
		field("folder", "f:folder", 0),
		"          " + strings.Join(chips, " "),
		field("name", "f:name", 1),
		" " + sGrey.Render(fit("agent", 8)) + radio("claude", "f:claude") + "  " + radio("omp", "f:omp"),
		field("prompt", "f:prompt", 2),
		"",
		" " + sRed.Render(f.err),
		" " + strings.Repeat(" ", max(0, w-30)) + zone.Mark("f:cancel", button("cancel", false)) + "  " + zone.Mark("f:ok", button("create ▶", true)),
	}
	return box("new session", sBright, "", lines, w, len(lines)+2)
}

// --- "find session": search past Claude/omp sessions and put one back on the stack ---

type findState struct {
	input   textinput.Model
	all     []sessions.Session
	hits    []sessions.Hit
	loading bool // reading the session index
	asking  bool // the model is ranking
	askedAt time.Time
	aiQuery string
	err     string
}

type findIndexMsg struct{ all []sessions.Session }

type findAIMsg struct {
	query string
	hits  []sessions.Hit
	err   error
}

func (f *findState) local() {
	if f.asking {
		return
	}
	f.hits = sessions.Local(f.all, f.input.Value(), 12)
}

func (m *stackModel) openFind() tea.Cmd {
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = "what was it about? (title, words from your prompts, folder)"
	in.CharLimit = 200
	in.Width = 70
	in.Focus()
	m.find = &findState{input: in, loading: true}
	app := m.app
	return func() tea.Msg { return findIndexMsg{all: app.SessionIndex()} }
}

func (m *stackModel) findAI() tea.Cmd {
	f := m.find
	q := strings.TrimSpace(f.input.Value())
	if q == "" || f.loading || f.asking {
		return nil
	}
	f.asking, f.err, f.askedAt = true, "", time.Now()
	all := f.all
	return func() tea.Msg {
		hits, err := sessions.AI(all, q, 12)
		return findAIMsg{query: q, hits: hits, err: err}
	}
}

func (m *stackModel) findClick(msg tea.MouseMsg) tea.Cmd {
	f := m.find
	switch {
	case in("fs:close", msg):
		m.find = nil
		return nil
	case in("fs:ai", msg):
		return m.findAI()
	}
	for i, h := range f.hits {
		if !in(fmt.Sprintf("fs:%d", i), msg) && !in(fmt.Sprintf("fs2:%d", i), msg) {
			continue
		}
		m.find = nil
		app, d, stack, s := m.app, m.drawer, m.stackID, h.Session
		return m.run(func() (string, error) {
			t, err := app.Attach(stack, s)
			if err != nil {
				return "", err
			}
			if t.TmuxPane == "" {
				return "attached " + t.Name, nil
			}
			return "attached " + t.Name, d.Open(t.TmuxPane, true)
		})
	}
	return nil
}

// sessionState tells whether a found session is already one of the stack's tasks.
func (m *stackModel) sessionState(s sessions.Session) string {
	for _, t := range m.tasks {
		if t.Agent == s.Agent && t.AgentSessionID == s.ID {
			switch ViewOf(t) {
			case "stack":
				return "on the stack"
			case "running":
				return "running"
			case "deferred":
				return "scheduled"
			case "paused":
				return "paused"
			case "hidden":
				return "hidden"
			}
			return "finished"
		}
	}
	return ""
}

func (m *stackModel) viewFind(w int) string {
	f := m.find
	inner := w - 2
	status := ""
	switch {
	case f.loading:
		status = "reading your sessions…"
	case f.asking:
		status = fmt.Sprintf("✨ asking claude to pick the best matches… %ds (keyword matches below meanwhile)", int(time.Since(f.askedAt).Seconds()))
	case f.err != "":
		status = f.err
	case f.aiQuery != "":
		status = fmt.Sprintf("✨ ranked by AI for %q · %d sessions searched", f.aiQuery, len(f.all))
	case !sessions.HasTerms(f.input.Value()):
		status = fmt.Sprintf("%d recent sessions · type to filter, enter asks AI", len(f.all))
	default:
		status = fmt.Sprintf("%d keyword matches in %d sessions · enter asks AI", len(f.hits), len(f.all))
	}
	statusSt := sGrey
	if f.err != "" {
		statusSt = sRed
	}
	lines := []string{"",
		" " + sYellow.Render("⌕ ") + sLine.Render("[ ") + padRight(f.input.View(), inner-26) + sLine.Render(" ]") + " " + zone.Mark("fs:ai", button("✨ ask AI", true)),
		" " + statusSt.Render(fit(status, inner-2)), ""}
	if !f.loading && len(f.hits) == 0 {
		lines = append(lines, " "+sDim.Render("no matching sessions — try other words, or enter to ask AI"))
	}
	maxRows := max(1, (m.height-12)/2)
	for i, h := range f.hits {
		if i == maxRows {
			break
		}
		bg := func(s lipgloss.Style) lipgloss.Style { return s }
		if m.hover == fmt.Sprintf("fs:%d", i) {
			bg = func(s lipgloss.Style) lipgloss.Style { return s.Background(cHover) }
		}
		mark := "  "
		if h.AI {
			mark = "✨"
		}
		state := m.sessionState(h.Session)
		right := fit(h.Agent, 7) + fit(ago(h.Modified.Unix()), 5)
		stateW := 14
		titleW := max(10, inner-4-lipgloss.Width(right)-stateW-2)
		line1 := bg(sYellow).Render(" "+mark+" ") + bg(sBright).Render(fit(h.Title, titleW)) + bg(sText).Render(" ") +
			bg(sPurple).Render(fit(state, stateW)) + bg(agentStyle(h.Agent)).Render(right)
		snippet := ""
		if len(h.Prompts) > 0 {
			snippet = " · › " + h.Prompts[len(h.Prompts)-1]
		}
		line2 := bg(sDim).Render(fit("     "+lastDirs(h.Cwd)+snippet, inner))
		lines = append(lines, zone.Mark(fmt.Sprintf("fs:%d", i), padRight(line1, inner)), zone.Mark(fmt.Sprintf("fs2:%d", i), line2))
	}
	lines = append(lines, "", " "+sDim.Render("click a session to put it on the stack and open it")+"   "+zone.Mark("fs:close", button("close", false)))
	return box("find session", sBright, "", lines, w, len(lines)+2)
}
