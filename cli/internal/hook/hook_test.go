package hook

import "testing"

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
