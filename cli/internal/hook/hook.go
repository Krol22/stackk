// Package hook turns Claude Code / omp hook payloads into store events.
package hook

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/krol22/stackk/cli/internal/safe"
	"github.com/krol22/stackk/cli/internal/store"
)

// Payload is Claude Code's hook input; the omp extension sends the same shape.
type Payload struct {
	HookEventName        string          `json:"hook_event_name"`
	SessionID            string          `json:"session_id"`
	TranscriptPath       string          `json:"transcript_path"`
	Cwd                  string          `json:"cwd"`
	Source               string          `json:"source"`
	Prompt               string          `json:"prompt"`
	LastAssistantMessage string          `json:"last_assistant_message"`
	ToolName             string          `json:"tool_name"`
	ToolInput            json.RawMessage `json:"tool_input"`
	ToolUseID            string          `json:"tool_use_id"`
	AgentID              string          `json:"agent_id"`
	Error                string          `json:"error"`
	BackgroundTasks      []struct {
		Type   string `json:"type"`
		Status string `json:"status"`
	} `json:"background_tasks"`
}

// subagentsRunning reports background work that keeps the session busy after the main turn ends.
// Background shells (dev servers, watchers) do not count: they can run forever.
func (p Payload) subagentsRunning() bool {
	for _, t := range p.BackgroundTasks {
		if t.Type != "shell" && (t.Status == "" || t.Status == "running" || t.Status == "pending") {
			return true
		}
	}
	return false
}

// Parse maps a payload to an event. ok=false means the event is irrelevant (e.g. from a subagent).
func Parse(agent string, raw []byte) (store.Event, Payload, bool) {
	var p Payload
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return store.Event{}, p, false
		}
	}
	// Inside a subagent only what needs the user (permission, question) or shows activity counts;
	// its own lifecycle events are not the session's.
	if p.AgentID != "" {
		switch p.HookEventName {
		case "PermissionRequest", "PreToolUse", "PostToolUse", "PostToolUseFailure":
		default:
			return store.Event{}, p, false
		}
	}
	ev := store.Event{Source: agent, AgentSessionID: p.SessionID, SessionPath: p.TranscriptPath, ToolUseID: p.ToolUseID}
	switch p.HookEventName {
	case "SessionStart":
		ev.Kind, ev.StartSource = "start", p.Source
	case "UserPromptSubmit":
		ev.Kind, ev.Msg = "prompt", OneLine(p.Prompt)
	case "Stop":
		ev.Kind = "stop"
		if p.subagentsRunning() {
			ev.Kind = "stop_bg" // the turn ended but subagents work on in the background
		}
		msg := p.LastAssistantMessage
		if msg == "" && agent == "claude" {
			msg = lastAssistantText(p.TranscriptPath)
		}
		ev.Msg = OneLine(msg)
	case "StopFailure":
		ev.Kind, ev.Msg = "fail", OneLine(p.Error)
	case "PermissionRequest":
		ev.Kind, ev.WaitingKind = "wait", "permission"
		ev.Msg = "permission: " + ToolSummary(p.ToolName, p.ToolInput)
	case "PreToolUse":
		if p.ToolName != "AskUserQuestion" {
			return ev, p, false
		}
		ev.Kind, ev.WaitingKind = "wait", "question"
		ev.Msg = "question: " + question(p.ToolInput)
	case "PostToolUse", "PostToolUseFailure":
		ev.Kind, ev.Tool = "tool_done", ToolSummary(p.ToolName, p.ToolInput)
	case "SessionEnd":
		ev.Kind = "end"
	default:
		return ev, p, false
	}
	return ev, p, true
}

func ToolSummary(name string, input json.RawMessage) string {
	var in map[string]any
	json.Unmarshal(input, &in)
	arg := ""
	for _, k := range []string{"command", "file_path", "path", "pattern", "url", "query", "description"} {
		if v, ok := in[k].(string); ok && v != "" {
			arg = v
			if k == "file_path" || k == "path" {
				arg = filepath.Base(v)
			}
			break
		}
	}
	return OneLine(strings.TrimSpace(name + " " + arg))
}

func question(input json.RawMessage) string {
	var in struct {
		Questions []struct {
			Question string `json:"question"`
		} `json:"questions"`
	}
	json.Unmarshal(input, &in)
	if len(in.Questions) > 0 {
		return OneLine(in.Questions[0].Question)
	}
	return ""
}

// OneLine returns the first non-empty line, whitespace-collapsed, at most 200 runes.
func OneLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = safe.Text(line)
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			continue
		}
		if utf8.RuneCountInString(line) > 200 {
			r := []rune(line)
			line = string(r[:199]) + "…"
		}
		return line
	}
	return ""
}

// lastAssistantText reads the tail of a Claude transcript and returns the last assistant text block.
func lastAssistantText(path string) string {
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
