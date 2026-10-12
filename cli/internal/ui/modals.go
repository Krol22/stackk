package ui

import (
	"fmt"
	"github.com/krol22/stackk/cli/internal/agent"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/krol22/stackk/cli/internal/store"
	zone "github.com/lrstanley/bubblezone"
)

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
		agent := agent.Of(c.Command)
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
	f := &formState{agent: agent.Names()[0]}
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
	for _, name := range agent.Names() {
		if in("f:agent:"+name, msg) {
			f.agent = name
			return nil
		}
	}
	switch {
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
	var radios []string
	for _, name := range agent.Names() {
		radios = append(radios, radio(name, "f:agent:"+name))
	}
	lines := []string{"",
		field("folder", "f:folder", 0),
		"          " + strings.Join(chips, " "),
		field("name", "f:name", 1),
		" " + sGrey.Render(fit("agent", 8)) + strings.Join(radios, "  "),
		field("prompt", "f:prompt", 2),
		"",
		" " + sRed.Render(f.err),
		" " + strings.Repeat(" ", max(0, w-30)) + zone.Mark("f:cancel", button("cancel", false)) + "  " + zone.Mark("f:ok", button("create ▶", true)),
	}
	return box("new session", sBright, "", lines, w, len(lines)+2)
}
