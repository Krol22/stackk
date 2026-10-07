package ui

import (
	"sort"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/krol22/stackk/cli/internal/config"
	zone "github.com/lrstanley/bubblezone"
)

// Theme is the dashboard's palette. Bg "" keeps the terminal's own background.
type Theme struct {
	Bg, Accent, Muted, Dim, Text, Bright, Purple, Green, Red, Line, Sel, Hover, Button, OnAccent string
}

const defaultTheme = "stackk"

var themes = map[string]Theme{
	"stackk": {"", "#e0b44c", "#8a8a8a", "#6a6a6a", "#d4d4d4", "#f2f2f2", "#a99ae6", "#8fcf7a", "#e27878",
		"#3a3a3a", "#3a321c", "#2a2a2a", "#333333", "#141414"},
	"catppuccin-mocha": {"#1e1e2e", "#f9e2af", "#a6adc8", "#6c7086", "#cdd6f4", "#f5f5ff", "#cba6f7", "#a6e3a1", "#f38ba8",
		"#45475a", "#45403a", "#313244", "#313244", "#1e1e2e"},
	"catppuccin-latte": {"#eff1f5", "#df8e1d", "#6c6f85", "#9ca0b0", "#4c4f69", "#232634", "#8839ef", "#40a02b", "#d20f39",
		"#bcc0cc", "#f3e2c0", "#e6e9ef", "#ccd0da", "#eff1f5"},
	"gruvbox": {"#282828", "#fabd2f", "#a89984", "#7c6f64", "#ebdbb2", "#fbf1c7", "#d3869b", "#b8bb26", "#fb4934",
		"#504945", "#4a3f22", "#3c3836", "#3c3836", "#282828"},
	"tokyo-night": {"#1a1b26", "#e0af68", "#9aa5ce", "#565f89", "#c0caf5", "#e0e6ff", "#bb9af7", "#9ece6a", "#f7768e",
		"#3b4261", "#3a3528", "#24283b", "#292e42", "#1a1b26"},
	"nord": {"#2e3440", "#ebcb8b", "#9aa3b5", "#616e88", "#d8dee9", "#eceff4", "#b48ead", "#a3be8c", "#bf616a",
		"#4c566a", "#4a4a3e", "#3b4252", "#434c5e", "#2e3440"},
	"dracula": {"#282a36", "#f1fa8c", "#a7abbe", "#6272a4", "#f8f8f2", "#ffffff", "#bd93f9", "#50fa7b", "#ff5555",
		"#44475a", "#4a4a36", "#343746", "#44475a", "#282a36"},
	"rose-pine": {"#191724", "#f6c177", "#908caa", "#6e6a86", "#e0def4", "#f4f2ff", "#c4a7e7", "#9ccfd8", "#eb6f92",
		"#403d52", "#3a3030", "#26233a", "#26233a", "#191724"},
	"win95": {"#c0c0c0", "#000080", "#404040", "#707070", "#000000", "#000000", "#800080", "#007000", "#b00000",
		"#808080", "#b4c4e8", "#d8d8d8", "#dcdcdc", "#ffffff"},
	"agent-tree": {"#1a1c23", "#e5a072", "#8b93a6", "#5c6370", "#d7dae0", "#ffffff", "#b4a7f5", "#8fd6a0", "#e88388",
		"#4a5263", "#3a2e26", "#252831", "#252831", "#1a1c23"},
	"neon": {"#0d0221", "#ff2a6d", "#9a8cc2", "#5a4e7c", "#e0d7ff", "#ffffff", "#05d9e8", "#39ff14", "#ff3864",
		"#3d2a6b", "#3a0f35", "#1d0b3a", "#241246", "#0d0221"},
	"charm": {"#1a1823", "#ff5fd2", "#8e8aa8", "#5f5b78", "#dedcf0", "#ffffff", "#7d56f4", "#04b575", "#ff4672",
		"#3c3758", "#3a2440", "#24212f", "#2c2840", "#1a1823"},
	"phosphor": {"#050a05", "#d4ff5c", "#3fcf62", "#1f7a36", "#5cff7a", "#b8ffc4", "#5cffd0", "#5cff7a", "#ff6b5c",
		"#1f5a2c", "#12351b", "#0d200f", "#10301a", "#050a05"},
	"minimal": {"", "#e0b44c", "#7a7a7a", "#555555", "#cfcfcf", "#ffffff", "#a99ae6", "#8fcf7a", "#e27878",
		"#2e2e2e", "#2e2a1e", "#222222", "#262626", "#141414"},
	"solarized-light": {"#fdf6e3", "#b58900", "#657b83", "#93a1a1", "#586e75", "#073642", "#6c71c4", "#859900", "#dc322f",
		"#d3cbb7", "#f1e3b8", "#eee8d5", "#eee8d5", "#fdf6e3"},
}

// Look is how a theme draws, beyond its colors.
type Look struct {
	Frame         string // "" (thin lines), rounded, double, heavy, dashed, none
	Title         string // "" (in the top edge), notch ┤ title ├, pill, center (inside the box)
	SectionColors bool   // frames take their section's color
	Brackets      bool   // [ buttons ]
	Legend        bool   // header: ■ colored squares, an upper-case title and a dotted rule below
	Running       string // RUNNING's color, if not the muted one
}

var looks = map[string]Look{
	"agent-tree":       {Frame: "dashed", Title: "center", SectionColors: true, Brackets: true, Legend: true, Running: "#8fa8d6"},
	"neon":             {Frame: "double", Title: "notch", SectionColors: true, Legend: true},
	"charm":            {Frame: "rounded", Title: "pill", Legend: true},
	"phosphor":         {Frame: "heavy", Brackets: true, Legend: true},
	"minimal":          {Frame: "none", Title: "notch"},
	"tokyo-night":      {Frame: "rounded"},
	"catppuccin-mocha": {Frame: "rounded", Title: "pill"},
}

// look is the current theme's Look; onPill the text color on a pill title.
var (
	look   Look
	onPill lipgloss.Color
	sRun   lipgloss.Style // RUNNING's title
)

// blockFrame draws boxes the old Windows way: a filled title bar and a bevelled frame of half
// blocks, light on the left, shadow on the right and bottom.
type blockFrame struct{ TitleBg, TitleFg, NoteFg, Light, Shadow string }

var blockFrames = map[string]blockFrame{
	"win95": {"#000080", "#ffffff", "#c0c0ff", "#ffffff", "#404040"},
}

// frame is the current theme's block frame, nil for thin lines.
var frame *blockFrame

// ThemeNames lists the built-in themes, the default first.
func ThemeNames() []string {
	names := []string{defaultTheme}
	for n := range themes {
		if n != defaultTheme {
			names = append(names, n)
		}
	}
	sort.Strings(names[1:])
	return names
}

// ThemeExists reports a built-in theme of that name.
func ThemeExists(name string) bool { _, ok := themes[name]; return ok }

var currentTheme = ""

// applyTheme sets the palette every style is drawn with; an unknown name means the default.
func applyTheme(name string) Theme {
	th, ok := themes[name]
	if !ok {
		name, th = defaultTheme, themes[defaultTheme]
	}
	currentTheme = name
	frame = nil
	if f, ok := blockFrames[name]; ok {
		frame = &f
	}
	look = looks[name]
	onPill = lipgloss.Color(th.OnAccent)
	c := func(hex string) lipgloss.Color { return lipgloss.Color(hex) }
	cYellow, cGrey, cDim, cText, cBright = c(th.Accent), c(th.Muted), c(th.Dim), c(th.Text), c(th.Bright)
	cPurple, cGreen, cRed, cLine, cSel, cHover = c(th.Purple), c(th.Green), c(th.Red), c(th.Line), c(th.Sel), c(th.Hover)
	sYellow = lipgloss.NewStyle().Foreground(cYellow)
	sGrey = lipgloss.NewStyle().Foreground(cGrey)
	sDim = lipgloss.NewStyle().Foreground(cDim)
	sText = lipgloss.NewStyle().Foreground(cText)
	sBright = lipgloss.NewStyle().Foreground(cBright).Bold(true)
	sPurple = lipgloss.NewStyle().Foreground(cPurple)
	sGreen = lipgloss.NewStyle().Foreground(cGreen)
	sRed = lipgloss.NewStyle().Foreground(cRed)
	sLine = lipgloss.NewStyle().Foreground(cLine)
	sButton = lipgloss.NewStyle().Foreground(cBright).Background(c(th.Button))
	sHotBtn = lipgloss.NewStyle().Foreground(c(th.OnAccent)).Background(cYellow).Bold(true)
	sRun = sGrey
	if look.Running != "" {
		sRun = lipgloss.NewStyle().Foreground(c(look.Running))
	}
	return th
}

// paneStyle is the tmux style giving a pane the theme's background ("" for the terminal's own).
func (th Theme) paneStyle() string {
	if th.Bg == "" {
		return ""
	}
	return "bg=" + th.Bg
}

// setTheme switches the dashboard's colors, and its pane's background through tmux.
func (m *stackModel) setTheme(name string) tea.Cmd {
	m.themeName = name
	style := applyTheme(name).paneStyle()
	t, pane := m.app.T, m.drawer.UI
	return m.run(func() (string, error) {
		for _, opt := range []string{"window-style", "window-active-style"} {
			if style == "" {
				t.Run("set-option", "-pu", "-t", pane, opt)
			} else if _, err := t.Run("set-option", "-p", "-t", pane, opt, style); err != nil {
				return "", err
			}
		}
		return "", nil
	})
}

// pickTheme applies a theme and saves it as yours.
func (m *stackModel) pickTheme(name string) tea.Cmd {
	if name == defaultTheme {
		name = ""
	}
	return tea.Batch(m.setTheme(name), m.run(func() (string, error) {
		cfg, err := config.Load()
		if err != nil {
			return "", err
		}
		cfg.Theme = name
		return "", config.Save(cfg)
	}))
}

func (m *stackModel) viewThemes(w int) string {
	lines := []string{"", " " + sGrey.Render("click a theme to use it:"), ""}
	for _, name := range ThemeNames() {
		th := themes[name]
		swatch := ""
		for _, c := range []string{th.Accent, th.Text, th.Purple, th.Green, th.Red} {
			swatch += lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Render("██")
		}
		bg := th.Bg
		if bg == "" {
			bg = "#141414"
		}
		swatch = lipgloss.NewStyle().Background(lipgloss.Color(bg)).Render(" " + swatch + " ")
		mark := "  "
		if name == currentTheme {
			mark = sYellow.Render("● ")
		}
		st := sText
		if m.hover == "th:"+name {
			st = st.Background(cHover)
		}
		row := " " + mark + swatch + "  " + st.Render(fit(name, w-22))
		lines = append(lines, zone.Mark("th:"+name, padRight(row, w-2)))
	}
	lines = append(lines, "", " "+zone.Mark("th:close", button("done", true)))
	return box("theme", sBright, "", lines, w, len(lines)+2)
}
