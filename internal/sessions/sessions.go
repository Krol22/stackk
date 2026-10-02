// Package sessions indexes past Claude Code and omp conversations on disk so they can be found
// and brought back onto a stack.
package sessions

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/krol22/stackk/internal/safe"
)

// Session is what search needs to know about one past conversation.
type Session struct {
	Agent    string    `json:"agent"` // claude | omp
	ID       string    `json:"id"`
	Path     string    `json:"path"` // transcript / session file
	Cwd      string    `json:"cwd"`
	Title    string    `json:"title"`
	Prompts  []string  `json:"prompts"` // first and last user prompts, trimmed
	LastText string    `json:"last_text"`
	Modified time.Time `json:"modified"`
}

func claudeDir() string {
	if d := os.Getenv("STACKK_CLAUDE_PROJECTS"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "projects")
}

func ompDir() string {
	if d := os.Getenv("STACKK_OMP_SESSIONS"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".omp", "agent", "sessions")
}

// File is a session file found on disk, before parsing.
type File struct {
	Agent string
	Path  string
	Mod   time.Time
	Size  int64
}

// Files lists session files of both agents, newest first, modified within maxAge.
func Files(maxAge time.Duration, limit int) []File {
	var out []File
	cutoff := time.Now().Add(-maxAge)
	add := func(agent, pattern string) {
		matches, _ := filepath.Glob(pattern)
		for _, m := range matches {
			st, err := os.Stat(m)
			if err != nil || st.IsDir() || st.ModTime().Before(cutoff) {
				continue
			}
			out = append(out, File{Agent: agent, Path: m, Mod: st.ModTime(), Size: st.Size()})
		}
	}
	add("claude", filepath.Join(claudeDir(), "*", "*.jsonl"))
	add("omp", filepath.Join(ompDir(), "*", "*.jsonl"))
	sort.Slice(out, func(i, j int) bool { return out[i].Mod.After(out[j].Mod) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Parse reads one session file.
func Parse(f File) (Session, bool) {
	fh, err := os.Open(f.Path)
	if err != nil {
		return Session{}, false
	}
	defer fh.Close()
	s := Session{Agent: f.Agent, Path: f.Path, Modified: f.Mod}
	var prompts []string
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		if f.Agent == "claude" {
			parseClaudeLine(sc.Bytes(), &s, &prompts)
		} else {
			parseOmpLine(sc.Bytes(), &s, &prompts)
		}
	}
	if s.ID == "" || scratch(s.Cwd) {
		return s, false
	}
	s.Prompts = firstLast(prompts, 3, 5)
	s.Title = clip(s.Title, 200)
	if s.Title == "" && len(prompts) > 0 {
		s.Title = clip(prompts[0], 80)
	}
	return s, true
}

func parseClaudeLine(line []byte, s *Session, prompts *[]string) {
	var l struct {
		Type        string `json:"type"`
		AITitle     string `json:"aiTitle"`
		CustomTitle string `json:"customTitle"`
		Summary     string `json:"summary"`
		SessionID   string `json:"sessionId"`
		Cwd         string `json:"cwd"`
		IsSidechain bool   `json:"isSidechain"`
		Message     struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &l) != nil || l.IsSidechain {
		return
	}
	switch l.Type {
	case "ai-title":
		if l.AITitle != "" {
			s.Title = l.AITitle
		}
	case "custom-title":
		if l.CustomTitle != "" {
			s.Title = l.CustomTitle
		}
	case "summary":
		if s.Title == "" && l.Summary != "" {
			s.Title = l.Summary
		}
	case "user":
		if s.ID == "" {
			s.ID, s.Cwd = l.SessionID, l.Cwd
		}
		var text string
		if json.Unmarshal(l.Message.Content, &text) == nil && userText(text) {
			*prompts = append(*prompts, clip(text, 300))
		}
	case "assistant":
		if t := blocksText(l.Message.Content); t != "" {
			s.LastText = clip(t, 300)
		}
	}
}

func parseOmpLine(line []byte, s *Session, prompts *[]string) {
	var l struct {
		Type    string `json:"type"`
		ID      string `json:"id"`
		Cwd     string `json:"cwd"`
		Title   string `json:"title"`
		Message struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &l) != nil {
		return
	}
	switch l.Type {
	case "title":
		if l.Title != "" {
			s.Title = l.Title // the file's first line holds the current title
		}
	case "session":
		s.ID, s.Cwd = l.ID, l.Cwd
		if s.Title == "" {
			s.Title = l.Title
		}
	case "message":
		t := blocksText(l.Message.Content)
		switch l.Message.Role {
		case "user":
			if userText(t) {
				*prompts = append(*prompts, clip(t, 300))
			}
		case "assistant":
			if t != "" {
				s.LastText = clip(t, 300)
			}
		}
	}
}

// scratch reports throwaway folders (test runs, temp dirs): not sessions you'd come back to.
func scratch(cwd string) bool {
	if os.Getenv("STACKK_INDEX_TMP") != "" { // tests keep their sessions in temp dirs
		return false
	}
	for _, p := range []string{"/tmp/", "/private/tmp/", "/var/folders/", "/private/var/folders/"} {
		if strings.HasPrefix(cwd+"/", p) {
			return true
		}
	}
	return false
}

// userText filters out tool results, slash-command echoes and injected reminders.
func userText(t string) bool {
	t = strings.TrimSpace(t)
	return t != "" && !strings.HasPrefix(t, "<") && !strings.HasPrefix(t, "[Request interrupted")
}

func blocksText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	json.Unmarshal(raw, &blocks)
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, strings.TrimSpace(b.Text))
		}
	}
	return strings.Join(parts, " ")
}

func firstLast(xs []string, first, last int) []string {
	if len(xs) <= first+last {
		return xs
	}
	return append(append([]string{}, xs[:first]...), xs[len(xs)-last:]...)
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(safe.Text(s)), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
