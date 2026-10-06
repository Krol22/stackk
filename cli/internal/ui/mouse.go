package ui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/krol22/stackk/cli/internal/tmux"
)

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
		c, err := m.app.Candidates()
		if err != nil {
			m.setStatus(err.Error(), true)
			return m, nil
		}
		m.take = append([]tmux.Pane{}, c...) // never nil: nil means the modal is closed
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
