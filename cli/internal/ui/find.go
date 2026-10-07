package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/krol22/stackk/cli/internal/sessions"
	"github.com/krol22/stackk/cli/internal/store"
	zone "github.com/lrstanley/bubblezone"
)

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
			switch store.View(t) {
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
