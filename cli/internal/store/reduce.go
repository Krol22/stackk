package store

import (
	"encoding/json"
	"slices"
	"strconv"
)

const (
	PhaseWorking = "working"
	PhaseIdle    = "idle"
	PhaseWaiting = "waiting"
	PhaseUnknown = "unknown"
	PhaseShell   = "shell" // window alive, no agent running in it (exited to the shell, or never had one)

	RunStarting = "starting"
	RunLive     = "live"
	RunExited   = "exited"
)

// Event is a normalized agent or user event.
type Event struct {
	Kind           string // start, prompt, stop, fail, wait, ask, tool_done, end, unknown, mark_idle, mark_working
	Source         string // claude, omp, ty, reconcile
	RunID          string
	StartSource    string // SessionStart source: startup, resume, clear, compact
	AgentSessionID string
	SessionPath    string
	Msg            string
	Tool           string
	ToolUseID      string
	WaitingKind    string
	Detail         string   // wait, ask: what is asked, as JSON
	Answered       []string // prompt: the pending questions it answers (AskKey)
}

// The list a task shows up in. Hidden and paused are out of every list (the ⏸ modal),
// deferred is SCHEDULED, exited sits with SCHEDULED as finished.
const (
	ViewStack    = "stack"
	ViewRunning  = "running"
	ViewDeferred = "deferred"
	ViewExited   = "exited"
	ViewPaused   = "paused"
	ViewHidden   = "hidden"
)

// View names the list a task shows up in.
func View(t Task) string {
	switch {
	case t.HiddenAt != 0:
		return ViewHidden
	case t.PausedAt != 0:
		return ViewPaused
	case t.Deferred:
		return ViewDeferred
	case t.RunState == RunExited:
		return ViewExited
	case InStack(&t):
		return ViewStack
	default:
		return ViewRunning
	}
}

func InStack(t *Task) bool {
	if t.Deferred || t.ArchivedAt != 0 || t.PausedAt != 0 || t.HiddenAt != 0 || t.RunState != RunLive {
		return false
	}
	// Anything we cannot classify as running stays on the stack, shells included.
	return t.Phase == PhaseIdle || t.Phase == PhaseWaiting || t.Phase == PhaseUnknown || t.Phase == PhaseShell || t.PendingAsk != ""
}

// askItem is one pending question: which call posted it, and the question with its options.
type askItem struct {
	Key      string          `json:"key"`
	Question json.RawMessage `json:"question"`
}

// AskKey names question i of the tool call that posted it, as an agent reports its answer.
func AskKey(toolUseID string, i int) string { return toolUseID + "#" + strconv.Itoa(i) }

func pending(t *Task) []askItem {
	var items []askItem
	json.Unmarshal([]byte(t.PendingAsk), &items)
	return items
}

func setPending(t *Task, items []askItem) {
	t.PendingAsk = ""
	if len(items) > 0 {
		b, _ := json.Marshal(items)
		t.PendingAsk = string(b)
	}
}

// PendingQuestion is the first question still waiting for an answer, as a waiting detail
// ({questions: [q]}), or nil.
func PendingQuestion(t Task) json.RawMessage {
	items := pending(&t)
	if len(items) == 0 {
		return nil
	}
	b, _ := json.Marshal(map[string][]json.RawMessage{"questions": {items[0].Question}})
	return b
}

// StackLess orders the stack: priority tasks first, each group in the order it came back.
func StackLess(a, b Task) bool {
	if a.Priority != b.Priority {
		return a.Priority
	}
	return a.QueuedAt < b.QueuedAt
}

func setPhase(t *Task, phase string, now int64) {
	if t.Phase != phase {
		t.Phase = phase
		t.PhaseSince = now
	}
	if phase != PhaseWaiting {
		t.WaitingKind = ""
		t.WaitingToolUseID = ""
		t.WaitingDetail = ""
	}
}

// Requeue keeps queue order consistent: entering the stack appends, leaving clears the slot.
func Requeue(t *Task, wasInStack bool, now int64) {
	in := InStack(t)
	switch {
	case in && !wasInStack:
		t.QueuedAt = now
	case !in:
		t.QueuedAt = 0
	}
}

// Reduce applies ev to t. It returns false when the event belongs to another run and must only be logged.
func Reduce(t *Task, ev Event, now int64) bool {
	if ev.RunID != "" && t.RunID != "" && ev.RunID != t.RunID {
		return false
	}
	before := InStack(t)
	if ev.Source != "ty" && ev.Source != "reconcile" {
		t.LastEventAt = now
	}
	switch ev.Kind {
	case "start":
		if ev.StartSource != "compact" {
			setPending(t, nil) // questions belong to the conversation that asked them
		}
		t.RunState = RunLive
		t.Error = ""
		if ev.AgentSessionID != "" {
			t.AgentSessionID = ev.AgentSessionID
		}
		if ev.SessionPath != "" {
			t.SessionPath = ev.SessionPath
		}
		// Auto-compaction restarts the session mid-turn; the agent keeps working.
		if ev.StartSource != "compact" {
			setPhase(t, PhaseIdle, now)
		}
	case "prompt":
		if len(ev.Answered) > 0 {
			var left []askItem
			for _, it := range pending(t) {
				if !slices.Contains(ev.Answered, it.Key) {
					left = append(left, it)
				}
			}
			setPending(t, left)
		}
		t.RunState = RunLive
		setPhase(t, PhaseWorking, now)
		if ev.Msg != "" {
			t.LastMsg = "› " + ev.Msg
		}
		t.Error = ""
	case "stop":
		setPhase(t, PhaseIdle, now)
		if ev.Msg != "" {
			t.LastMsg = ev.Msg
		}
	case "stop_bg":
		setPhase(t, PhaseWorking, now)
		if ev.Msg != "" {
			t.LastMsg = ev.Msg
		}
	case "fail":
		setPhase(t, PhaseIdle, now)
		t.Error = ev.Msg
		if ev.Msg != "" {
			t.LastMsg = "error: " + ev.Msg
		}
	case "wait":
		setPhase(t, PhaseWaiting, now)
		t.WaitingKind = ev.WaitingKind
		t.WaitingToolUseID = ev.ToolUseID
		t.WaitingDetail = ev.Detail
		if ev.Msg != "" {
			t.LastMsg = ev.Msg
		}
	case "ask":
		var in struct {
			Questions []json.RawMessage `json:"questions"`
		}
		json.Unmarshal([]byte(ev.Detail), &in)
		items := pending(t)
		for i, q := range in.Questions {
			items = append(items, askItem{AskKey(ev.ToolUseID, i), q})
		}
		setPending(t, items)
		if ev.Msg != "" {
			t.LastMsg = ev.Msg
		}
	case "tool_done":
		if ev.Tool != "" {
			t.LastTool = ev.Tool
			t.LastToolAt = now
		}
		switch t.Phase {
		case PhaseWaiting:
			if t.WaitingToolUseID == "" || ev.ToolUseID == "" || ev.ToolUseID == t.WaitingToolUseID {
				setPhase(t, PhaseWorking, now)
			}
		case PhaseIdle, PhaseUnknown:
			// Tool activity after Stop means a hook kept the agent going.
			setPhase(t, PhaseWorking, now)
		}
	case "end":
		setPending(t, nil)
		// The agent quit; its window may live on as a shell. Reconcile marks the run exited once the pane is gone.
		setPhase(t, PhaseShell, now)
	case "unknown":
		if t.Phase == PhaseWorking {
			setPhase(t, PhaseUnknown, now)
		}
	case "mark_idle":
		setPhase(t, PhaseIdle, now)
	case "mark_working":
		setPhase(t, PhaseWorking, now)
	case "shell":
		setPhase(t, PhaseShell, now)
	case "agent":
		if t.Phase == PhaseShell {
			setPhase(t, PhaseUnknown, now)
		}
	}
	Requeue(t, before, now)
	return true
}
