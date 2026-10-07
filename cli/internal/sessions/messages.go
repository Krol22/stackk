package sessions

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/krol22/stackk/cli/internal/safe"
)

// Message is one entry of a conversation, as a reader wants it: what you said, what the agent
// answered, and the tools it ran (one line each). Thinking and bookkeeping are left out.
type Message struct {
	Role  string `json:"role"` // user, assistant, tool
	Text  string `json:"text,omitempty"`
	Tool  string `json:"tool,omitempty"`  // role tool: name and main argument
	Error bool   `json:"error,omitempty"` // role tool: it failed
	Done  bool   `json:"done,omitempty"`  // role tool: its result came back
	Time  int64  `json:"time,omitempty"`
}

// Messages reads the last limit messages of an agent's transcript.
func Messages(agent, path string, limit int) ([]Message, error) {
	fh, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil // the agent hasn't written anything yet
	}
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	var out []Message
	tools := map[string]int{} // tool call id → index in out, for its result
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		if agent == "omp" {
			out = ompMessage(sc.Bytes(), out, tools)
		} else {
			out = claudeMessage(sc.Bytes(), out, tools)
		}
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, sc.Err()
}

func stamp(ts string) int64 {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return 0
	}
	return t.Unix()
}

// body keeps a message readable and bounded: control characters out, at most n runes.
func body(s string, n int) string {
	s = strings.TrimSpace(safe.Block(s))
	if r := []rune(s); len(r) > n {
		s = string(r[:n-1]) + "…"
	}
	return s
}

func toolLine(name string, input json.RawMessage) string {
	var in map[string]any
	json.Unmarshal(input, &in)
	for _, k := range []string{"command", "file_path", "path", "pattern", "url", "query", "description", "prompt"} {
		if v, ok := in[k].(string); ok && v != "" {
			return clip(name+" "+v, 160)
		}
	}
	return name
}

func claudeMessage(line []byte, out []Message, tools map[string]int) []Message {
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
	ts := stamp(l.Timestamp)
	var text string
	if json.Unmarshal(l.Message.Content, &text) == nil {
		if l.Type == "user" && userText(text) {
			out = append(out, Message{Role: "user", Text: body(text, 4000), Time: ts})
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
		case b.Type == "text" && strings.TrimSpace(b.Text) != "" && (l.Type == "assistant" || userText(b.Text)):
			out = append(out, Message{Role: l.Type, Text: body(b.Text, 8000), Time: ts})
		case b.Type == "tool_use":
			tools[b.ID] = len(out)
			out = append(out, Message{Role: "tool", Tool: toolLine(b.Name, b.Input), Time: ts})
		case b.Type == "tool_result":
			if i, ok := tools[b.ToolUseID]; ok {
				out[i].Done, out[i].Error = true, b.IsError
			}
		}
	}
	return out
}

func ompMessage(line []byte, out []Message, tools map[string]int) []Message {
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
	ts := stamp(l.Timestamp)
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
		case b.Type == "text" && l.Message.Role == "user" && userText(b.Text):
			out = append(out, Message{Role: "user", Text: body(b.Text, 4000), Time: ts})
		case b.Type == "text" && l.Message.Role == "assistant" && strings.TrimSpace(b.Text) != "":
			out = append(out, Message{Role: "assistant", Text: body(b.Text, 8000), Time: ts})
		case b.Type == "toolCall":
			tools[b.ID] = len(out)
			out = append(out, Message{Role: "tool", Tool: toolLine(b.Name, b.Arguments), Time: ts})
		}
	}
	return out
}

// OmpMain maps an omp subagent's session file to its parent's. omp keeps a subagent's
// transcript in a directory named after the parent's file: <session>/<Agent>.jsonl next to
// <session>.jsonl. Any other path comes back as is.
func OmpMain(path string) (main string, subagent bool) {
	dir := filepath.Dir(path)
	if strings.HasSuffix(path, ".jsonl") && dir != "." {
		if st, err := os.Stat(dir + ".jsonl"); err == nil && !st.IsDir() {
			return dir + ".jsonl", true
		}
	}
	return path, false
}
