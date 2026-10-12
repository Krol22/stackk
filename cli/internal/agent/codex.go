package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/krol22/stackk/cli/internal/sessions"
)

// codex is OpenAI's Codex CLI: hooks in ~/.codex/hooks.json report in the shape of Claude's,
// sessions are rollouts in ~/.codex/sessions/<y>/<m>/<d>.
type codex struct{ base }

func codexHome() string {
	if d := os.Getenv("CODEX_HOME"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex")
}

func codexDir() string {
	if d := os.Getenv("STACKK_CODEX_SESSIONS"); d != "" {
		return d
	}
	return filepath.Join(codexHome(), "sessions")
}

// Argv: `codex [prompt]`, or `codex resume <id> [prompt]`.
func (c codex) Argv(prompt, ref string) ([]string, error) {
	bin, err := exec.LookPath(c.name)
	if err != nil {
		return nil, fmt.Errorf("%s not found in PATH", c.name)
	}
	argv := []string{bin}
	if ref != "" {
		argv = append(argv, "resume", ref)
	}
	if prompt != "" {
		if strings.HasPrefix(prompt, "-") { // a prompt, never an option
			prompt = " " + prompt
		}
		argv = append(argv, prompt)
	}
	return argv, nil
}

func (c codex) SessionFiles(cutoff time.Time) []sessions.File {
	return sessions.Glob(c.name, filepath.Join(codexDir(), "*", "*", "*", "*.jsonl"), cutoff)
}

// line is one rollout record: what stackk reads of it.
type codexLine struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		Type   string          `json:"type"`
		ID     string          `json:"id"`
		Cwd    string          `json:"cwd"`
		Source json.RawMessage `json:"source"`
		Item   codexItem       `json:"item"`
	} `json:"payload"`
}

type codexItem struct {
	Type     string          `json:"type"` // UserMessage, AgentMessage, CommandExecution, Extension, …
	Content  json.RawMessage `json:"content"`
	Command  []string        `json:"command"`
	ExitCode *int            `json:"exit_code"`
	Kind     string          `json:"kind"`
	Query    string          `json:"query"`
}

// text joins an item's text blocks ({"type": "text"|"Text", "text": …}).
func (it codexItem) text() string {
	var blocks []struct {
		Text string `json:"text"`
	}
	json.Unmarshal(it.Content, &blocks)
	var parts []string
	for _, b := range blocks {
		if t := strings.TrimSpace(b.Text); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n\n")
}

// ParseSession keeps interactive sessions only: `codex exec` runs and subagents have another source.
func (codex) ParseSession(f sessions.File) (sessions.Session, bool) {
	return sessions.Read(f, func(raw []byte, s *sessions.Session, prompts *[]string) {
		var l codexLine
		if json.Unmarshal(raw, &l) != nil {
			return
		}
		switch {
		case l.Type == "session_meta":
			if string(l.Payload.Source) == `"cli"` {
				s.ID, s.Cwd = l.Payload.ID, l.Payload.Cwd
			}
		case l.Type == "event_msg" && l.Payload.Type == "item_completed":
			switch t := l.Payload.Item.text(); l.Payload.Item.Type {
			case "UserMessage":
				if sessions.UserText(t) {
					*prompts = append(*prompts, sessions.Clip(t, 300))
				}
			case "AgentMessage":
				if t != "" {
					s.LastText = sessions.Clip(t, 300)
				}
			}
		}
	})
}

func (codex) Messages(path string, limit int) ([]sessions.Message, error) {
	return sessions.ReadMessages(path, limit, func(raw []byte, out []sessions.Message, _ map[string]int) []sessions.Message {
		var l codexLine
		if json.Unmarshal(raw, &l) != nil || l.Type != "event_msg" || l.Payload.Type != "item_completed" {
			return out
		}
		ts, it := sessions.Stamp(l.Timestamp), l.Payload.Item
		switch it.Type {
		case "UserMessage":
			if t := it.text(); sessions.UserText(t) {
				out = append(out, sessions.Message{Role: "user", Text: sessions.Body(t, 4000), Time: ts})
			}
		case "AgentMessage":
			if t := it.text(); t != "" {
				out = append(out, sessions.Message{Role: "assistant", Text: sessions.Body(t, 8000), Time: ts})
			}
		case "CommandExecution": // completed: its result is in
			cmd := ""
			if len(it.Command) > 0 {
				cmd = it.Command[len(it.Command)-1] // ["/bin/zsh", "-lc", "<the command>"]
			}
			failed := it.ExitCode != nil && *it.ExitCode != 0
			out = append(out, sessions.Message{Role: "tool", Tool: sessions.Clip("Bash "+cmd, 160), Done: true, Error: failed, Time: ts})
		case "Extension":
			out = append(out, sessions.Message{Role: "tool", Tool: sessions.Clip(it.Kind+" "+it.Query, 160), Done: true, Time: ts})
		}
		return out
	})
}

// Question: Codex asks with request_user_input, options as plain strings. The async one returns
// at once and Codex waits; your answer comes back as a prompt.
func (b codex) Question(tool string, input json.RawMessage) (json.RawMessage, bool, bool) {
	if tool != "request_user_input" && tool != "request_user_input_async" {
		return b.base.Question(tool, input)
	}
	var in struct {
		Questions []struct {
			Title   string   `json:"title"`
			Options []string `json:"options"`
		} `json:"questions"`
	}
	json.Unmarshal(input, &in)
	type option struct {
		Label string `json:"label"`
	}
	type question struct {
		Question string   `json:"question"`
		Options  []option `json:"options"`
	}
	var out struct {
		Questions []question `json:"questions"`
	}
	for _, q := range in.Questions {
		qq := question{Question: q.Title}
		for _, o := range q.Options {
			qq.Options = append(qq.Options, option{o})
		}
		out.Questions = append(out.Questions, qq)
	}
	raw, _ := json.Marshal(out)
	return raw, tool == "request_user_input_async", true
}

func (codex) AsksTrust(screen string) bool { return strings.Contains(screen, "Trust this folder?") }

// Answers: the question waits behind ⌥↑ while the main prompt keeps the keyboard; once it opens
// the digit n picks option n, and the one after the options ("Other") takes your text. A
// permission is y, p (don't ask again) or Esc; the trust prompt starts on "Trust and continue".
func (codex) Answers(n int) Answers {
	a := Answers{
		Yes: []string{"y"}, Always: []string{"p"}, No: []string{"esc"},
		Trust: []string{"enter"}, Exit: []string{"esc"},
	}
	for i := range n {
		a.Options = append(a.Options, []string{"alt-up", "wait", fmt.Sprint(i + 1)})
	}
	if n > 0 {
		a.Own = []string{"alt-up", "wait", fmt.Sprint(n + 1)}
	}
	return a
}

// CancelReported: Esc on a prompt interrupts the turn without a hook.
func (codex) CancelReported() bool { return false }

var codexEvents = []string{"SessionStart", "UserPromptSubmit", "Stop", "PermissionRequest", "PreToolUse", "PostToolUse"}

func (codex) Install(_, self string, out func(string)) error {
	home := codexHome()
	if _, err := os.Stat(home); os.IsNotExist(err) {
		out("codex: not installed, skipped")
		return nil
	}
	if err := installCodexHooks(filepath.Join(home, "hooks.json"), self, out); err != nil {
		return err
	}
	if cfg, _ := os.ReadFile(filepath.Join(home, "config.toml")); !strings.Contains(string(cfg), "hooks = true") {
		out("codex: turn hooks on in " + filepath.Join(home, "config.toml") + ": [features] hooks = true")
	}
	out("codex: approve the new hooks once in Codex (Hooks need review → Trust all)")
	return nil
}

func installCodexHooks(path, self string, out func(string)) error {
	file := map[string]any{}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &file); err != nil {
			return fmt.Errorf("%s is not valid JSON: %w", path, err)
		}
	case !os.IsNotExist(err):
		return err
	}
	hooks, _ := file["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	cmd := self + " mark codex"
	if strings.ContainsAny(self, " '\"") {
		cmd = "'" + strings.ReplaceAll(self, "'", `'"'"'`) + "' mark codex"
	}
	added := 0
	for _, event := range codexEvents {
		entries, _ := hooks[event].([]any)
		if hasMark(entries, "codex") {
			continue
		}
		hooks[event] = append(entries, map[string]any{"hooks": []any{map[string]any{"type": "command", "command": cmd}}})
		added++
	}
	if added == 0 {
		out("codex: hooks already in " + path)
		return nil
	}
	file["hooks"] = hooks
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomic(path, append(data, '\n'), mode); err != nil {
		return err
	}
	out(fmt.Sprintf("codex: added %d hooks to %s", added, path))
	return nil
}
