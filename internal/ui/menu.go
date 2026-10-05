package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/krol22/stackk/internal/store"
	zone "github.com/lrstanley/bubblezone"
)

func (m *stackModel) openMenu(t store.Task, x, y int) {
	var items []menuItem
	add := func(id, label string) { items = append(items, menuItem{id, label}) }
	live := t.RunState != store.RunExited
	switch store.View(t) {
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
	var hint []string
	switch m.edit.kind {
	case "note":
		title = "Note"
	case "canvas":
		title = "Canvas"
		hint = []string{"", " " + sGrey.Render("a program to show while the stack is empty, e.g. an ebook reader")}
	}
	lines := append(hint, "", " "+m.edit.input.View(), "",
		" "+zone.Mark("e:cancel", button("cancel", false))+" "+zone.Mark("e:ok", button("save", true)))
	return box(title, sBright, "", lines, w, len(lines)+2)
}
