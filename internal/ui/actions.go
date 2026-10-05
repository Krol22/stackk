package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/krol22/stackk/internal/config"
	"github.com/krol22/stackk/internal/store"
)

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

func (m *stackModel) act(id, action string) tea.Cmd {
	app := m.app
	t, ok := m.byID(id)
	if !ok {
		return nil
	}
	do := func(info string, fn func() error) func() tea.Cmd {
		return func() tea.Cmd { return m.run(func() (string, error) { return info, fn() }) }
	}
	switch action {
	case "open":
		return m.openPane(t)
	case "resume":
		return m.resumeAndOpen(id)
	case "skip":
		return do(t.Name+" moved to the end", func() error { return app.Skip(id) })()
	case "defer":
		return m.offDrawer(id, do("scheduled "+t.Name, func() error { return app.SetDeferred(id, true) }))
	case "pause":
		return m.offDrawer(id, do("paused "+t.Name, func() error { return app.SetPaused(id, true) }))
	case "hide":
		return m.offDrawer(id, do("hid "+t.Name, func() error { return app.SetHidden(id, true) }))
	case "kill":
		return m.offDrawer(id, do("removed "+t.Name, func() error { return app.Remove(id) }))
	case "restore":
		return do(t.Name+" is back", func() error { return app.SetDeferred(id, false) })()
	case "mark_idle":
		return do("", func() error { return app.SetPhase(id, store.PhaseIdle) })()
	case "mark_working":
		return do("", func() error { return app.SetPhase(id, store.PhaseWorking) })()
	case "stop":
		return do("stopping "+t.Name, func() error { return app.Stop(id) })()
	case "rename", "note":
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = 200
		ti.Width = 40
		if action == "rename" {
			ti.SetValue(t.Name)
		} else {
			ti.SetValue(t.Note)
		}
		ti.Focus()
		m.edit = &editState{task: id, kind: action, input: ti}
	}
	return nil
}

// offDrawer closes the drawer first when it shows the task, for actions that take the task off
// the stack. m.run queues work when it is called, so the close is created before the action;
// once both are queued, the order the cmds themselves run in no longer matters.
func (m *stackModel) offDrawer(id string, action func() tea.Cmd) tea.Cmd {
	if m.out != id {
		return action()
	}
	closing := m.closeDrawer()
	return tea.Batch(closing, action())
}

func (m *stackModel) submitEdit() tea.Cmd {
	e := m.edit
	m.edit = nil
	app, val := m.app, e.input.Value()
	if e.kind == "canvas" {
		val = strings.TrimSpace(val)
		if val == "" {
			return nil
		}
		m.cfgCanvas, m.canvasForce = val, true
		return tea.Batch(m.run(func() (string, error) {
			cfg, err := config.Load()
			if err == nil {
				cfg.Canvas = val
				err = config.Save(cfg)
			}
			return "", err
		}), m.setCanvas(val))
	}
	if e.kind == "rename" {
		return m.run(func() (string, error) { return "", app.Rename(e.task, val) })
	}
	return m.run(func() (string, error) { return "", app.SetNote(e.task, val) })
}

// toggleCanvas shows or hides the canvas for this run; with none configured it asks for one.
func (m *stackModel) toggleCanvas() tea.Cmd {
	switch {
	case m.canvasShown():
		m.canvasForce = false
		return m.setCanvas("")
	case m.canvas != "": // on, but aside for the stack: show it anyway
		m.canvasForce, m.canvasWant = true, 0
		return nil
	case m.cfgCanvas != "":
		m.canvasForce = true
		return m.setCanvas(m.cfgCanvas)
	}
	ti := textinput.New()
	ti.Prompt = ""
	ti.CharLimit = 500
	ti.Width = 60
	ti.Placeholder = "bookokrat ~/books/current.epub"
	ti.Focus()
	m.edit = &editState{kind: "canvas", input: ti}
	return nil
}

func (m *stackModel) setCanvas(command string) tea.Cmd {
	m.canvas = command
	want := 0
	if command != "" && m.width > 0 {
		want = m.dashLines()
	}
	m.canvasWant = want
	d := m.drawer
	return m.run(func() (string, error) { return "", d.SetCanvas(command, want) })
}
