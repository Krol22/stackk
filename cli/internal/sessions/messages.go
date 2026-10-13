package sessions

import (
	"bufio"
	"encoding/json"
	"os"
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

// ReadMessages reads the last limit messages of a transcript with the agent's line reader, which
// appends messages and marks tool calls done when their results come (tools: call id → index).
func ReadMessages(path string, limit int, line func(l []byte, out []Message, tools map[string]int) []Message) ([]Message, error) {
	fh, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil // the agent hasn't written anything yet
	}
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	var out []Message
	tools := map[string]int{}
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		out = line(sc.Bytes(), out, tools)
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, sc.Err()
}

func Stamp(ts string) int64 {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return 0
	}
	return t.Unix()
}

// Body keeps a message readable and bounded: control characters out, at most n runes.
func Body(s string, n int) string {
	s = strings.TrimSpace(safe.Block(s))
	if r := []rune(s); len(r) > n {
		s = string(r[:n-1]) + "…"
	}
	return s
}

func ToolLine(name string, input json.RawMessage) string {
	var in map[string]any
	json.Unmarshal(input, &in)
	for _, k := range []string{"command", "file_path", "path", "pattern", "url", "query", "description", "prompt"} {
		if v, ok := in[k].(string); ok && v != "" {
			return Clip(name+" "+v, 160)
		}
	}
	return name
}
