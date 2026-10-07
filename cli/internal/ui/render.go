package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/krol22/stackk/cli/internal/safe"
)

// The palette and styles; applyTheme sets them.
var (
	cYellow, cGrey, cDim, cText, cBright, cPurple, cGreen, cRed, cLine, cSel, cHover lipgloss.Color

	sYellow, sGrey, sDim, sText, sBright, sPurple, sGreen, sRed, sLine, sButton, sHotBtn lipgloss.Style
)

func init() { applyTheme(defaultTheme) }

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
	s = safe.Text(s)
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

// box draws a titled frame of exactly w×h cells around lines, in the theme's look. titleStyle
// carries the section's color, which some looks give the whole frame.
func box(title string, titleStyle lipgloss.Style, note string, lines []string, w, h int) string {
	if w < 8 || h < 3 {
		return ""
	}
	if frame != nil {
		return blockBox(title, note, lines, w, h)
	}
	ch := frameChars[look.Frame]
	border := sLine
	if look.SectionColors {
		border = lipgloss.NewStyle().Foreground(titleStyle.GetForeground())
	}
	inner := w - 2
	label := titleStyle.Render(title)
	if look.Title == "center" {
		head := strings.Repeat(" ", max(0, (inner-lipgloss.Width(label))/2)) + label
		if note != "" {
			head = padRight(head, inner-lipgloss.Width(note)-1) + sDim.Render(note) + " "
		}
		lines = append([]string{head}, lines...)
		label, note = "", ""
	}
	top := edge(ch.tl, ch.tr, ch.h, label, note, w, border, titleStyle)
	out := []string{top}
	for i := 0; i < h-2; i++ {
		l := ""
		if i < len(lines) {
			l = lines[i]
		}
		out = append(out, border.Render(ch.v)+padRight(l, inner)+border.Render(ch.v))
	}
	out = append(out, border.Render(ch.bl+strings.Repeat(ch.h, inner)+ch.br))
	return strings.Join(out, "\n")
}

type frameSet struct{ tl, tr, bl, br, h, v string }

var frameChars = map[string]frameSet{
	"":        {"┌", "┐", "└", "┘", "─", "│"},
	"rounded": {"╭", "╮", "╰", "╯", "─", "│"},
	"double":  {"╔", "╗", "╚", "╝", "═", "║"},
	"heavy":   {"┏", "┓", "┗", "┛", "━", "┃"},
	"dashed":  {"+", "+", "+", "+", "╌", "╎"},
	"none":    {" ", " ", " ", " ", " ", " "},
}

// edge is a frame's top line: the label (already styled) placed the look's way, the note on the right.
func edge(left, right, h, label, note string, w int, border, section lipgloss.Style) string {
	var head string
	switch {
	case label == "":
	case look.Title == "notch":
		head = border.Render(h+h+"┤ ") + label + border.Render(" ├")
	case look.Title == "pill":
		head = border.Render(h+" ") + lipgloss.NewStyle().Background(section.GetForeground()).Foreground(onPill).Bold(true).
			Render(" "+ansi.Strip(label)+" ") + " "
	default:
		head = border.Render(h+" ") + label + " "
	}
	tail := ""
	if note != "" {
		tail = " " + sDim.Render(note) + " " + border.Render(h)
	}
	fill := w - 2 - lipgloss.Width(head) - lipgloss.Width(tail)
	if fill < 0 {
		return ansi.Truncate(border.Render(left)+head+tail, w-1, "") + border.Render(right)
	}
	return border.Render(left) + head + border.Render(strings.Repeat(h, fill)) + tail + border.Render(right)
}

// blockBox is box in a block frame: the title bar is the top edge.
func blockBox(title, note string, lines []string, w, h int) string {
	fg := func(c string) lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color(c)) }
	light, shadow := fg(frame.Light), fg(frame.Shadow)
	out := []string{titleBar(title, note, w)}
	inner := w - 2
	for i := 0; i < h-2; i++ {
		l := ""
		if i < len(lines) {
			l = lines[i]
		}
		out = append(out, light.Render("▌")+padRight(l, inner)+shadow.Render("▐"))
	}
	out = append(out, shadow.Render(strings.Repeat("▀", w)))
	return strings.Join(out, "\n")
}

func titleBar(title, note string, w int) string {
	bar := lipgloss.NewStyle().Background(lipgloss.Color(frame.TitleBg))
	left := bar.Foreground(lipgloss.Color(frame.TitleFg)).Bold(true).Render(" " + title + " ")
	right := ""
	if note != "" {
		right = bar.Foreground(lipgloss.Color(frame.NoteFg)).Render(" " + note + " ")
	}
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 0 {
		return ansi.Truncate(left+right, w, "")
	}
	return left + bar.Render(strings.Repeat(" ", gap)) + right
}

func rule(title string, style lipgloss.Style, w int) string {
	if frame != nil {
		return titleBar(title, "", w)
	}
	h := frameChars[look.Frame].h
	if look.Frame == "none" || look.Frame == "dashed" {
		h = "┄"
	}
	border := sLine
	if look.SectionColors {
		border = lipgloss.NewStyle().Foreground(style.GetForeground())
	}
	label := style.Render(title)
	if look.Title == "center" {
		side := max(0, w-lipgloss.Width(label)-2)
		return border.Render(strings.Repeat(h, side/2)) + " " + label + " " + border.Render(strings.Repeat(h, side-side/2))
	}
	return edge(h, h, h, label, "", w, border, style)
}

func button(label string, hot bool) string {
	if look.Brackets {
		if hot {
			return sYellow.Bold(true).Render("[ " + label + " ]")
		}
		return sGrey.Render("[ ") + sText.Render(label) + sGrey.Render(" ]")
	}
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
