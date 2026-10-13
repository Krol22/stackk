package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStopKinds(t *testing.T) {
	cases := map[string]string{
		`{"hook_event_name":"Stop","background_tasks":[]}`:                                            "stop",
		`{"hook_event_name":"Stop","background_tasks":[{"type":"shell","status":"running"}]}`:         "stop",
		`{"hook_event_name":"Stop","background_tasks":[{"type":"local_agent","status":"running"}]}`:   "stop_bg",
		`{"hook_event_name":"Stop","background_tasks":[{"type":"local_agent","status":"completed"}]}`: "stop",
	}
	for raw, want := range cases {
		ev, _, ok := Parse("claude", []byte(raw))
		if !ok || ev.Kind != want {
			t.Errorf("%s → %q %v, want %q", raw, ev.Kind, ok, want)
		}
	}
}

func TestSubagentEvents(t *testing.T) {
	if ev, _, ok := Parse("claude", []byte(`{"hook_event_name":"PermissionRequest","agent_id":"a1","tool_name":"Bash"}`)); !ok || ev.Kind != "wait" {
		t.Errorf("subagent permission must wait, got %q %v", ev.Kind, ok)
	}
	if ev, _, ok := Parse("claude", []byte(`{"hook_event_name":"PostToolUse","agent_id":"a1","tool_name":"Read"}`)); !ok || ev.Kind != "tool_done" {
		t.Errorf("subagent tool use is activity, got %q %v", ev.Kind, ok)
	}
	if _, _, ok := Parse("claude", []byte(`{"hook_event_name":"Stop","agent_id":"a1"}`)); ok {
		t.Error("a subagent's own Stop must not end the session's turn")
	}
}

func TestOmpSubagentEventsStayOutOfTheSession(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "2026-10-05T12-48-39_01a1.jsonl")
	sub := filepath.Join(strings.TrimSuffix(main, ".jsonl"), "InvestigateReds.jsonl")
	os.MkdirAll(filepath.Dir(sub), 0o755)
	os.WriteFile(main, nil, 0o644)
	os.WriteFile(sub, nil, 0o644)
	payload := func(event string) []byte {
		b, _ := json.Marshal(map[string]any{"hook_event_name": event, "session_id": "sub-id", "transcript_path": sub,
			"tool_name": "bash", "tool_input": map[string]any{"command": "ls"}})
		return b
	}
	if _, _, ok := Parse("omp", payload("Stop")); ok {
		t.Error("a subagent's Stop must not end the session's turn")
	}
	ev, _, ok := Parse("omp", payload("PermissionRequest"))
	if !ok || ev.Kind != "wait" {
		t.Fatalf("a subagent asking for permission still needs you: %+v %v", ev, ok)
	}
	if ev.SessionPath != "" || ev.AgentSessionID != "" {
		t.Errorf("the subagent's session replaced the main one: %+v", ev)
	}
	b, _ := json.Marshal(map[string]any{"hook_event_name": "Stop", "session_id": "main-id", "transcript_path": main})
	if ev, _, ok := Parse("omp", b); !ok || ev.SessionPath != main {
		t.Errorf("the main session's own events are kept: %+v %v", ev, ok)
	}
}

func TestAskUserQuestionPermissionIsTheQuestion(t *testing.T) {
	in := `"tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Which?","options":[{"label":"A"},{"label":"B"}]}]}`
	pre, _, _ := Parse("claude", []byte(`{"hook_event_name":"PreToolUse",`+in+`}`))
	perm, _, ok := Parse("claude", []byte(`{"hook_event_name":"PermissionRequest",`+in+`}`))
	if !ok || perm.WaitingKind != "question" || perm.Detail != pre.Detail || !strings.Contains(perm.Detail, `"label":"B"`) {
		t.Errorf("the permission for AskUserQuestion must keep the question and its options: %+v", perm)
	}
}

func TestCodexAsyncQuestionWaitsForTheAnswer(t *testing.T) {
	in := `"tool_name":"request_user_input_async","tool_input":{"questions":[{"title":"Pick","options":["A","B"]}]}`
	ev, _, ok := Parse("codex", []byte(`{"hook_event_name":"PreToolUse",`+in+`}`))
	if !ok || ev.Kind != "ask" || !strings.Contains(ev.Detail, `"label":"B"`) {
		t.Errorf("Codex's question waits for you apart from the turn, with its options: %+v", ev)
	}
	if _, _, ok := Parse("codex", []byte(`{"hook_event_name":"PostToolUse",`+in+`}`)); ok {
		t.Error("the async question returns at once: that is not the answer")
	}
}
