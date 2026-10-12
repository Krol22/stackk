package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/krol22/stackk/cli/internal/sessions"
)

// claude is Claude Code: hooks in ~/.claude/settings.json, transcripts in ~/.claude/projects.
type claude struct{ base }

// Claude Code sets its process title to its version, e.g. "2.1.286".
func (claude) Detect(command string) bool { return command == "claude" || isVersion(command) }

func isVersion(s string) bool {
	dots := 0
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r == '.':
			dots++
		case r < '0' || r > '9':
			return false
		}
	}
	return dots == 2
}

func claudeDir() string {
	if d := os.Getenv("STACKK_CLAUDE_PROJECTS"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "projects")
}

func (c claude) SessionFiles(cutoff time.Time) []sessions.File {
	return sessions.Glob(c.name, filepath.Join(claudeDir(), "*", "*.jsonl"), cutoff)
}

func (claude) ParseSession(f sessions.File) (sessions.Session, bool) {
	return sessions.Read(f, func(line []byte, s *sessions.Session, prompts *[]string) {
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
			if json.Unmarshal(l.Message.Content, &text) == nil && sessions.UserText(text) {
				*prompts = append(*prompts, sessions.Clip(text, 300))
			}
		case "assistant":
			if t := sessions.BlocksText(l.Message.Content); t != "" {
				s.LastText = sessions.Clip(t, 300)
			}
		}
	})
}

func (claude) Messages(path string, limit int) ([]sessions.Message, error) {
	return sessions.ReadMessages(path, limit, func(line []byte, out []sessions.Message, tools map[string]int) []sessions.Message {
		var l struct {
			Type        string `json:"type"`
			IsSidechain bool   `json:"isSidechain"`
			IsMeta      bool   `json:"isMeta"`
			Timestamp   string `json:"timestamp"`
			Message     struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &l) != nil || l.IsSidechain || l.IsMeta || (l.Type != "user" && l.Type != "assistant") {
			return out
		}
		ts := sessions.Stamp(l.Timestamp)
		var text string
		if json.Unmarshal(l.Message.Content, &text) == nil {
			if l.Type == "user" && sessions.UserText(text) {
				out = append(out, sessions.Message{Role: "user", Text: sessions.Body(text, 4000), Time: ts})
			}
			return out
		}
		var blocks []struct {
			Type      string          `json:"type"`
			Text      string          `json:"text"`
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			Input     json.RawMessage `json:"input"`
			ToolUseID string          `json:"tool_use_id"`
			IsError   bool            `json:"is_error"`
		}
		json.Unmarshal(l.Message.Content, &blocks)
		for _, b := range blocks {
			switch {
			case b.Type == "text" && strings.TrimSpace(b.Text) != "" && (l.Type == "assistant" || sessions.UserText(b.Text)):
				out = append(out, sessions.Message{Role: l.Type, Text: sessions.Body(b.Text, 8000), Time: ts})
			case b.Type == "tool_use":
				tools[b.ID] = len(out)
				out = append(out, sessions.Message{Role: "tool", Tool: sessions.ToolLine(b.Name, b.Input), Time: ts})
			case b.Type == "tool_result":
				if i, ok := tools[b.ToolUseID]; ok {
					out[i].Done, out[i].Error = true, b.IsError
				}
			}
		}
		return out
	})
}

// LastText reads the tail of the transcript for the last assistant text block.
func (claude) LastText(path string) string {
	if path == "" {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	const tail = 256 << 10
	if st, err := f.Stat(); err == nil && st.Size() > tail {
		f.Seek(-tail, io.SeekEnd)
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	last := ""
	for sc.Scan() {
		var line struct {
			Type    string `json:"type"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil || line.Type != "assistant" {
			continue
		}
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(line.Message.Content, &blocks) != nil {
			continue
		}
		for _, b := range blocks {
			if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
				last = b.Text
			}
		}
	}
	return last
}

// AsksTrust: before its first hook, a new session can be stuck on "do you trust this folder?".
func (claude) AsksTrust(screen string) bool {
	return strings.Contains(screen, "trust this folder") || strings.Contains(screen, "one you trust")
}

// Answers: a question's option n is the digit n and the one after the options takes your own
// text; a permission is 1 (yes), 2 (always) or Esc; the trust prompt starts on "No, exit".
func (claude) Answers(n int) Answers {
	a := Answers{
		Steps: []string{"up", "down", "space", "tab", "enter", "esc"},
		Yes:   []string{"1"}, Always: []string{"2"}, No: []string{"esc"}, Cancel: []string{"esc"},
		Trust: []string{"down", "enter"}, Exit: []string{"esc"},
	}
	for i := range n {
		a.Options = append(a.Options, []string{fmt.Sprint(i + 1)})
	}
	if n > 0 {
		a.Own = []string{fmt.Sprint(n + 1)}
	}
	return a
}

// CancelReported: Claude sends no hook when a prompt is cancelled.
func (claude) CancelReported() bool { return false }

// Hook events stackk listens to. PreToolUse only for AskUserQuestion (a question to you).
var claudeEvents = []struct{ event, matcher string }{
	{"SessionStart", ""}, {"UserPromptSubmit", ""}, {"Stop", ""}, {"StopFailure", ""},
	{"PermissionRequest", ""}, {"PreToolUse", "AskUserQuestion"}, {"PostToolUse", ""},
	{"PostToolUseFailure", ""}, {"SessionEnd", ""},
}

func (c claude) Install(home, self string, out func(string)) error {
	return installClaudeHooks(filepath.Join(home, ".claude", "settings.json"), self, out)
}

func installClaudeHooks(path, self string, out func(string)) error {
	settings := map[string]any{}
	mode := os.FileMode(0o600) // a new settings file is private
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm() // keep whatever you chose
	}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &settings); err != nil {
			return fmt.Errorf("%s is not valid JSON: %w", path, err)
		}
	case !os.IsNotExist(err):
		return err
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	cmd := self + " mark claude"
	if strings.ContainsAny(self, " '\"") {
		cmd = "'" + strings.ReplaceAll(self, "'", `'"'"'`) + "' mark claude"
	}
	added := 0
	for _, e := range claudeEvents {
		entries, _ := hooks[e.event].([]any)
		if hasMark(entries, "claude") {
			continue
		}
		entry := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": cmd, "timeout": 3}}}
		if e.matcher != "" {
			entry["matcher"] = e.matcher
		}
		hooks[e.event] = append(entries, entry)
		added++
	}
	if added == 0 {
		out("claude: hooks already in " + path)
		return nil
	}
	settings["hooks"] = hooks
	if raw != nil {
		backup := path + ".bak-stackk-" + time.Now().Format("20060102150405")
		if err := os.WriteFile(backup, raw, 0o600); err != nil {
			return err
		}
		out("claude: backup " + backup)
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomic(path, append(data, '\n'), mode); err != nil {
		return err
	}
	out(fmt.Sprintf("claude: added %d hooks to %s", added, path))
	return nil
}

// hasMark reports an existing stackk hook for agent (any path to the binary) among an event's entries.
func hasMark(entries []any, agent string) bool {
	for _, e := range entries {
		m, _ := e.(map[string]any)
		hs, _ := m["hooks"].([]any)
		for _, h := range hs {
			hm, _ := h.(map[string]any)
			if c, _ := hm["command"].(string); strings.Contains(c, "stackk") && strings.Contains(c, " mark "+agent) {
				return true
			}
		}
	}
	return false
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil { // WriteFile's mode is cut by the umask
		return err
	}
	return os.Rename(tmp, path)
}
