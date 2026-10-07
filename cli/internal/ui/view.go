package ui

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/krol22/stackk/cli/internal/store"
	zone "github.com/lrstanley/bubblezone"
)

// --- view ---

func (m *stackModel) sorted(view string) []store.Task {
	var out []store.Task
	for _, t := range m.tasks {
		v := store.View(t)
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
	if m.themes {
		w := min(m.width-2, 50)
		s = zone.Scan(overlay(s, m.viewThemes(w), max(0, (m.width-w)/2), 2))
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
	if look.Legend {
		item := func(st lipgloss.Style, label string, n int) string {
			return st.Render("■") + " " + sGrey.Render(fmt.Sprintf("%s %d", label, n))
		}
		return item(sYellow, "stack", len(m.sorted("stack"))) + "   " + item(sRun, "running", len(m.sorted("running"))) +
			"   " + item(sPurple, "scheduled", len(m.sorted("deferred")))
	}
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
	name := sText.Render(fit(m.stackName, 32))
	if look.Legend {
		name = sYellow.Bold(true).Render(strings.ToUpper(strings.TrimSpace(fit(m.stackName, 32))))
	}
	title := " " + sBright.Render("STACKK") + sDim.Render(" · ") + name
	buttons := func(compact bool) string {
		pick := func(full, short string) string {
			if compact {
				return short
			}
			return full
		}
		right := ""
		if p, h := len(m.sorted("paused")), len(m.sorted("hidden")); p+h > 0 {
			label := fmt.Sprintf("⏸ paused %d", p)
			if h > 0 {
				label += fmt.Sprintf(" · hidden %d", h)
			}
			right = zone.Mark("b:paused", button(pick(label, fmt.Sprintf("⏸ %d", p+h)), false)) + " "
		}
		return right + zone.Mark("b:theme", button(pick("◑ theme", "◑"), false)) + " " +
			zone.Mark("b:find", button(pick("⌕ find", "⌕"), false)) + " " + zone.Mark("b:take", button(pick("⇲ add windows", "⇲"), false)) + " " +
			zone.Mark("b:new", button(pick("+ new", "+"), false)) + " " + zone.Mark("b:next", button("next ▶", true)) + " "
	}
	// When it doesn't fit, the counts go first (the boxes show them too), then button labels;
	// the buttons themselves always stay.
	for _, try := range []struct {
		left    string
		compact bool
	}{{title + "   " + m.counts(), false}, {title, false}, {title, true}, {"", true}} {
		right := buttons(try.compact)
		if gap := w - lipgloss.Width(try.left) - lipgloss.Width(right); gap >= 1 {
			return try.left + strings.Repeat(" ", gap) + right
		}
	}
	return padRight(buttons(true), w)
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
	right := box("RUNNING", sRun.Bold(true), "do not disturb", runLines, rw, runH) + "\n\n" +
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
	under := ""
	if look.Legend { // a dotted rule under the header
		under = " " + sLine.Render(strings.Repeat("┄", max(0, w-2)))
	}
	parts := []string{m.header(w), under, indent(body)}
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
	if look.Legend {
		sq := func(st lipgloss.Style, n int) string { return st.Render("■") + sGrey.Render(fmt.Sprintf(" %d", n)) }
		lines = append(lines, " "+sq(sYellow, len(m.sorted("stack")))+"  "+sq(sRun, len(m.sorted("running")))+"  "+sq(sPurple, len(m.sorted("deferred"))))
	} else {
		lines = append(lines, " "+sYellow.Render(fmt.Sprintf("● %d", len(m.sorted("stack"))))+"  "+
			sGrey.Render(fmt.Sprintf("◐ %d", len(m.sorted("running"))))+"  "+sPurple.Render(fmt.Sprintf("z %d", len(m.sorted("deferred")))))
	}
	if t, ok := m.byID(m.out); ok {
		lines = append(lines, "", " "+sGrey.Render("open: ")+sBright.Render(fit(t.Name, w-14)),
			" "+zone.Mark("b:close", button("close ▶", false))+" "+zone.Mark("b:outmenu", button("…", false)))
	}
	if n := len(m.sorted("paused")) + len(m.sorted("hidden")); n > 0 {
		lines = append(lines, "", " "+zone.Mark("b:paused", button(fmt.Sprintf("⏸ %d", n), false)))
	}
	lines = append(lines, "", " "+zone.Mark("b:theme", button("◑", false))+" "+zone.Mark("b:find", button("⌕", false))+" "+zone.Mark("b:take", button("⇲", false))+" "+zone.Mark("b:new", button("+", false))+" "+
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
	section("RUNNING", sRun.Bold(true), "running")
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

// lastDirs shows the end of a path ("yuma.ai/yumaa"): where it was is what you recognize.
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
