package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/krol22/stackk/internal/store"
)

var (
	cYellow = lipgloss.Color("#e0b44c")
	cGrey   = lipgloss.Color("#8a8a8a")
	cDim    = lipgloss.Color("#6a6a6a")
	cText   = lipgloss.Color("#d4d4d4")
	cBright = lipgloss.Color("#f2f2f2")
	cPurple = lipgloss.Color("#a99ae6")
	cGreen  = lipgloss.Color("#8fcf7a")
	cRed    = lipgloss.Color("#e27878")
	cLine   = lipgloss.Color("#3a3a3a")
	cSel    = lipgloss.Color("#3a321c")
	cHover  = lipgloss.Color("#2a2a2a")

	sYellow = lipgloss.NewStyle().Foreground(cYellow)
	sGrey   = lipgloss.NewStyle().Foreground(cGrey)
	sDim    = lipgloss.NewStyle().Foreground(cDim)
	sText   = lipgloss.NewStyle().Foreground(cText)
	sBright = lipgloss.NewStyle().Foreground(cBright).Bold(true)
	sPurple = lipgloss.NewStyle().Foreground(cPurple)
	sGreen  = lipgloss.NewStyle().Foreground(cGreen)
	sRed    = lipgloss.NewStyle().Foreground(cRed)
	sLine   = lipgloss.NewStyle().Foreground(cLine)
	sButton = lipgloss.NewStyle().Foreground(cBright).Background(lipgloss.Color("#333333"))
	sHotBtn = lipgloss.NewStyle().Foreground(lipgloss.Color("#141414")).Background(cYellow).Bold(true)
)

// ViewOf names the list a task shows up in.
func ViewOf(t store.Task) string {
	switch {
	case t.HiddenAt != 0:
		return "hidden"
	case t.PausedAt != 0:
		return "paused"
	case t.Deferred:
		return "deferred"
	case t.RunState == store.RunExited:
		return "exited"
	case store.InStack(&t):
		return "stack"
	default:
		return "running"
	}
}

func agentStyle(agent string) lipgloss.Style {
	switch agent {
	case "omp":
		return sPurple
	case "":
		return sGrey
	}
	return sGreen
}

func agentLabel(agent string) string {
	if agent == "" {
		return "shell"
	}
	return agent
}

func ago(ts int64) string {
	if ts == 0 {
		return ""
	}
	d := time.Since(time.Unix(ts, 0))
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// fit truncates or pads plain text to exactly w cells.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) > w {
		return ansi.Truncate(s, w, "…")
	}
	return s + strings.Repeat(" ", w-lipgloss.Width(s))
}

// padRight pads a styled line to w cells (or truncates it).
func padRight(s string, w int) string {
	lw := lipgloss.Width(s)
	if lw > w {
		return ansi.Truncate(s, w, "")
	}
	return s + strings.Repeat(" ", w-lw)
}

// box draws a titled frame of exactly w×h cells around lines.
func box(title string, titleStyle lipgloss.Style, note string, lines []string, w, h int) string {
	if w < 8 || h < 3 {
		return ""
	}
	inner := w - 2
	top := sLine.Render("┌─ ") + titleStyle.Render(title) + " "
	if note != "" {
		top += sLine.Render(strings.Repeat("─", max(1, inner-lipgloss.Width(title)-lipgloss.Width(note)-6))) + " " + sDim.Render(note) + " " + sLine.Render("─┐")
	}
	top = padLine(top, w, "─┐")
	out := []string{top}
	for i := 0; i < h-2; i++ {
		l := ""
		if i < len(lines) {
			l = lines[i]
		}
		out = append(out, sLine.Render("│")+padRight(l, inner)+sLine.Render("│"))
	}
	out = append(out, sLine.Render("└"+strings.Repeat("─", inner)+"┘"))
	return strings.Join(out, "\n")
}

func padLine(top string, w int, end string) string {
	lw := lipgloss.Width(top)
	if lw >= w {
		return ansi.Truncate(top, w, "")
	}
	if strings.HasSuffix(top, sLine.Render(end)) {
		return top
	}
	return top + sLine.Render(strings.Repeat("─", max(0, w-lw-len([]rune(end))))+end)
}

func rule(title string, style lipgloss.Style, w int) string {
	s := sLine.Render("── ") + style.Render(title) + " "
	return s + sLine.Render(strings.Repeat("─", max(0, w-lipgloss.Width(s))))
}

func button(label string, hot bool) string {
	if hot {
		return sHotBtn.Render(" " + label + " ")
	}
	return sButton.Render(" " + label + " ")
}

// overlay draws box over base with its top-left corner at (x, y).
func overlay(base, top string, x, y int) string {
	bl := strings.Split(base, "\n")
	tl := strings.Split(top, "\n")
	for i, l := range tl {
		row := y + i
		if row < 0 || row >= len(bl) {
			continue
		}
		line := bl[row]
		if lipgloss.Width(line) < x {
			line += strings.Repeat(" ", x-lipgloss.Width(line))
		}
		left := ansi.Truncate(line, x, "")
		right := ansi.TruncateLeft(line, x+lipgloss.Width(l), "")
		bl[row] = left + l + right
	}
	return strings.Join(bl, "\n")
}

func eventIcon(ev string) string {
	switch ev {
	case "stop":
		return sYellow.Render("↩")
	case "wait":
		return sYellow.Render("?")
	case "prompt":
		return sGrey.Render("→")
	case "unknown":
		return sYellow.Render("⁇")
	case "defer":
		return sPurple.Render("z")
	case "pause", "hide":
		return sDim.Render("⏸")
	case "adopt", "new":
		return sGreen.Render("+")
	case "resume":
		return sGreen.Render("↻")
	case "end", "kill", "remove":
		return sDim.Render("○")
	case "fail":
		return sRed.Render("!")
	}
	return sDim.Render("·")
}
