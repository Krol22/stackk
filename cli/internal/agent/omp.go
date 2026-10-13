package agent

import (
	_ "embed"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/krol22/stackk/cli/internal/sessions"
)

//go:embed omp_extension.ts
var ompExtension string

// omp is oh-my-pi: an extension in ~/.omp/agent/extensions reports in the shape of Claude's hooks,
// sessions live in ~/.omp/agent/sessions.
type omp struct{ base }

func ompDir() string {
	if d := os.Getenv("STACKK_OMP_SESSIONS"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".omp", "agent", "sessions")
}

func (o omp) SessionFiles(cutoff time.Time) []sessions.File {
	return sessions.Glob(o.name, filepath.Join(ompDir(), "*", "*.jsonl"), cutoff)
}

// ResumeRef: omp resumes from the session file, never a subagent's.
func (o omp) ResumeRef(sessionID, sessionPath string) string {
	if sessionPath != "" {
		main, _ := o.MainSession(sessionPath)
		return main
	}
	return sessionID
}

// MainSession: omp keeps a subagent's transcript in a directory named after the parent's file,
// <session>/<Agent>.jsonl next to <session>.jsonl.
func (omp) MainSession(path string) (string, bool) {
	dir := filepath.Dir(path)
	if strings.HasSuffix(path, ".jsonl") && dir != "." {
		if st, err := os.Stat(dir + ".jsonl"); err == nil && !st.IsDir() {
			return dir + ".jsonl", true
		}
	}
	return path, false
}

func (omp) ParseSession(f sessions.File) (sessions.Session, bool) {
	return sessions.Read(f, func(line []byte, s *sessions.Session, prompts *[]string) {
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
			t := sessions.BlocksText(l.Message.Content)
			switch l.Message.Role {
			case "user":
				if sessions.UserText(t) {
					*prompts = append(*prompts, sessions.Clip(t, 300))
				}
			case "assistant":
				if t != "" {
					s.LastText = sessions.Clip(t, 300)
				}
			}
		}
	})
}

func (omp) Messages(path string, limit int) ([]sessions.Message, error) {
	return sessions.ReadMessages(path, limit, func(line []byte, out []sessions.Message, tools map[string]int) []sessions.Message {
		var l struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			Message   struct {
				Role       string          `json:"role"`
				Content    json.RawMessage `json:"content"`
				ToolCallID string          `json:"toolCallId"`
				IsError    bool            `json:"isError"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &l) != nil || l.Type != "message" {
			return out
		}
		ts := sessions.Stamp(l.Timestamp)
		if l.Message.Role == "toolResult" {
			if i, ok := tools[l.Message.ToolCallID]; ok {
				out[i].Done, out[i].Error = true, l.Message.IsError
			}
			return out
		}
		var blocks []struct {
			Type      string          `json:"type"`
			Text      string          `json:"text"`
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		json.Unmarshal(l.Message.Content, &blocks)
		for _, b := range blocks {
			switch {
			case b.Type == "text" && l.Message.Role == "user" && sessions.UserText(b.Text):
				out = append(out, sessions.Message{Role: "user", Text: sessions.Body(b.Text, 4000), Time: ts})
			case b.Type == "text" && l.Message.Role == "assistant" && strings.TrimSpace(b.Text) != "":
				out = append(out, sessions.Message{Role: "assistant", Text: sessions.Body(b.Text, 8000), Time: ts})
			case b.Type == "toolCall":
				tools[b.ID] = len(out)
				out = append(out, sessions.Message{Role: "tool", Tool: sessions.ToolLine(b.Name, b.Arguments), Time: ts})
			}
		}
		return out
	})
}

// Answers: omp's ask takes no digits and starts on the recommended option, so go to the top (the
// list doesn't wrap) and down to the option; "Other (type your own)" follows the options.
func (omp) Answers(n int) Answers {
	pick := func(i int) []string {
		keys := make([]string, 0, n+1+i+1)
		for range n + 1 {
			keys = append(keys, "up")
		}
		for range i {
			keys = append(keys, "down")
		}
		return append(keys, "enter")
	}
	a := Answers{
		Steps: []string{"up", "down", "space", "tab", "enter", "esc"},
		Yes:   []string{"1"}, Always: []string{"2"}, No: []string{"esc"}, Cancel: []string{"esc"},
	}
	for i := range n {
		a.Options = append(a.Options, pick(i))
	}
	if n > 0 {
		a.Own = pick(n)
	}
	return a
}

func (omp) Install(home, self string, out func(string)) error {
	return installOmpExtension(filepath.Join(home, ".omp", "agent", "extensions", "stackk.ts"), self, out)
}

func installOmpExtension(path, self string, out func(string)) error {
	if _, err := os.Stat(filepath.Dir(filepath.Dir(path))); os.IsNotExist(err) {
		out("omp: not installed, skipped")
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	want := strings.ReplaceAll(ompExtension, "__STACKK_BIN__", self)
	if cur, err := os.ReadFile(path); err == nil && string(cur) == want {
		out("omp: extension up to date at " + path)
		return nil
	}
	os.Remove(path) // may be a symlink from a manual install
	if err := writeAtomic(path, []byte(want), 0o644); err != nil {
		return err
	}
	out("omp: extension written to " + path)
	return nil
}
