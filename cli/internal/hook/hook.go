// Package hook turns Claude Code / omp hook payloads into store events.
package hook

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"unicode/utf8"

	agentpkg "github.com/krol22/stackk/cli/internal/agent"
	"github.com/krol22/stackk/cli/internal/safe"
	"github.com/krol22/stackk/cli/internal/store"
)

// Payload is Claude Code's hook input, the shape every agent reports in (omp's extension too).
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
	ag, _ := agentpkg.Get(agent)
	if ag == nil {
		ag = agentpkg.All()[0]
	}
	// An agent may run subagents in its own process and report them like its main session (omp
	// does), with the subagent's transcript: tell them apart by the file, and keep the main ones.
	if p.AgentID == "" && p.TranscriptPath != "" {
		if _, sub := ag.MainSession(p.TranscriptPath); sub {
			p.AgentID = strings.TrimSuffix(filepath.Base(p.TranscriptPath), ".jsonl")
			p.SessionID, p.TranscriptPath = "", ""
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
		if msg == "" {
			msg = ag.LastText(p.TranscriptPath)
		}
		ev.Msg = OneLine(msg)
	case "StopFailure":
		ev.Kind, ev.Msg = "fail", OneLine(p.Error)
	case "PermissionRequest", "PreToolUse":
		// Claude also asks permission for AskUserQuestion: that is the question, not a yes/no.
		if q, _, ok := ag.Question(p.ToolName, p.ToolInput); ok {
			ev.Kind, ev.WaitingKind = "wait", "question"
			ev.Msg = "question: " + question(q)
			ev.Detail = questionDetail(q)
		} else if p.HookEventName == "PermissionRequest" {
			ev.Kind, ev.WaitingKind = "wait", "permission"
			ev.Msg = "permission: " + ToolSummary(p.ToolName, p.ToolInput)
			ev.Detail = permissionDetail(p.ToolName, p.ToolInput)
		} else {
			return ev, p, false
		}
	case "PostToolUse", "PostToolUseFailure":
		if _, async, ok := ag.Question(p.ToolName, p.ToolInput); ok && async {
			return ev, p, false // the question was only posted: the answer comes as a prompt
		}
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

// permissionDetail is what a permission prompt asks, for a client that answers it: the tool and
// its full main argument (the command, the file).
func permissionDetail(name string, input json.RawMessage) string {
	var in map[string]any
	json.Unmarshal(input, &in)
	arg := ""
	for _, k := range []string{"command", "file_path", "path", "url", "pattern", "query", "description"} {
		if v, ok := in[k].(string); ok && v != "" {
			arg = v
			break
		}
	}
	b, _ := json.Marshal(map[string]string{"tool": safe.Text(name), "input": clipRunes(safe.Block(arg), 2000)})
	return string(b)
}

// questionDetail is an AskUserQuestion's questions and their options, in order: option n is
// the key n in the agent's prompt.
func questionDetail(input json.RawMessage) string {
	var in struct {
		Questions []struct {
			Question    string `json:"question"`
			Header      string `json:"header"`
			MultiSelect bool   `json:"multiSelect,omitempty"`
			Options     []struct {
				Label       string `json:"label"`
				Description string `json:"description"`
			} `json:"options"`
		} `json:"questions"`
	}
	json.Unmarshal(input, &in)
	for i := range in.Questions {
		q := &in.Questions[i]
		q.Question, q.Header = clipRunes(safe.Text(q.Question), 500), clipRunes(safe.Text(q.Header), 60)
		for j := range q.Options {
			o := &q.Options[j]
			o.Label, o.Description = clipRunes(safe.Text(o.Label), 120), clipRunes(safe.Text(o.Description), 300)
		}
	}
	b, _ := json.Marshal(in)
	return string(b)
}

func clipRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
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
