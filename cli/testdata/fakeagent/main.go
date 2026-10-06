// fakeagent imitates Claude Code for integration tests: it reads prompts from the terminal
// and fires the same hooks (via `stackk mark claude`) that the real agent would.
//
//	anything   → UserPromptSubmit, PostToolUse, Stop
//	ask        → UserPromptSubmit, PermissionRequest; then "y" → PostToolUse, Stop
//	hang       → UserPromptSubmit, then silence (like an Esc interrupt: no Stop)
//	busy       → UserPromptSubmit, then a spinner that keeps redrawing (long work) until the next line
//	bgagent    → turn ends while a subagent works in the background (Stop with background_tasks),
//	             the subagent keeps using tools; "bgdone" is the final turn once it reports back
//	bgshell    → turn ends with only a background shell running (a dev server)
//	subask     → a subagent asks for permission
//	/exit      → SessionEnd, exit
package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

var sessionID, cwd string

var stopBusy chan struct{}

func hook(ev map[string]any) {
	ev["session_id"] = sessionID
	ev["cwd"] = cwd
	ev["transcript_path"] = transcript()
	b, _ := json.Marshal(ev)
	cmd := exec.Command(os.Getenv("STACKK_BIN"), "mark", "claude")
	cmd.Stdin = strings.NewReader(string(b))
	cmd.Run()
}

func transcript() string { return "/tmp/fake-" + sessionID + ".jsonl" }

// record appends to the transcript the way Claude Code writes it.
func record(kind string, content any) {
	f, err := os.OpenFile(transcript(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(map[string]any{"type": kind, "sessionId": sessionID, "cwd": cwd,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano), "message": map[string]any{"content": content}})
	f.Write(append(b, '\n'))
}

func recordTool(id, name string, input map[string]any, isError bool) {
	record("assistant", []map[string]any{{"type": "tool_use", "id": id, "name": name, "input": input}})
	record("user", []map[string]any{{"type": "tool_result", "tool_use_id": id, "is_error": isError, "content": "ok"}})
}

func main() {
	cwd, _ = os.Getwd()
	args := os.Args[1:]
	source := "startup"
	b := make([]byte, 8)
	rand.Read(b)
	sessionID = hex.EncodeToString(b)
	if len(args) >= 2 && args[0] == "--resume" {
		sessionID, source, args = args[1], "resume", args[2:]
	}
	fmt.Printf("fake claude · %s · %s\n", sessionID, source)
	hook(map[string]any{"hook_event_name": "SessionStart", "source": source})
	if len(args) > 0 {
		turn(strings.Join(args, " "), nil)
	}
	in := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !in.Scan() {
			return
		}
		turn(strings.TrimSpace(in.Text()), in)
	}
}

func turn(line string, in *bufio.Scanner) {
	if stopBusy != nil {
		close(stopBusy)
		stopBusy = nil
	}
	switch line {
	case "":
		return
	case "/exit":
		hook(map[string]any{"hook_event_name": "SessionEnd", "reason": "prompt_input_exit"})
		os.Exit(0)
	}
	record("user", line)
	if rest, ok := strings.CutPrefix(line, "busy: "); ok { // long work with a real-looking prompt
		hook(map[string]any{"hook_event_name": "UserPromptSubmit", "prompt": rest})
		line = "busy"
	} else {
		hook(map[string]any{"hook_event_name": "UserPromptSubmit", "prompt": line})
	}
	switch line {
	case "hang":
		fmt.Println("(hung)")
		return
	case "demo": // a believable transcript, for screenshots
		fmt.Print("\x1b[2J\x1b[H\x1b[2m> upgrade postgres to 16 and the pgx driver, keep the migrations green\x1b[0m\n" +
			"\n\x1b[1m⏺\x1b[0m Upgraded the driver to pgx v5 and ran the migration suite.\n" +
			"  \x1b[2m⎿  Bash(go test ./internal/db/...)\x1b[0m\n" +
			"     \x1b[32mok\x1b[0m  internal/db         4.1s\n" +
			"     \x1b[32mok\x1b[0m  internal/db/migrate 11.8s   214 passed\n\n" +
			"\x1b[1m⏺\x1b[0m Postgres 16 is green locally. Two things before merging:\n\n" +
			"  1. The connection pool default dropped from 25 to 4 in pgx v5;\n" +
			"     I set it back explicitly in \x1b[36mconfig/db.go\x1b[0m.\n" +
			"  2. \x1b[36mmigrations/0042_jsonb_index.sql\x1b[0m locks the table for ~40 s on\n" +
			"     prod-sized data. Want me to rewrite it as CREATE INDEX CONCURRENTLY?\n\n")
		hook(map[string]any{"hook_event_name": "Stop", "last_assistant_message": "Postgres 16 is green locally. Rewrite 0042 as CREATE INDEX CONCURRENTLY?"})
		return
	case "bgagent":
		hook(map[string]any{"hook_event_name": "Stop", "last_assistant_message": "launched a subagent",
			"background_tasks": []map[string]any{{"type": "subagent", "status": "running"}}})
		hook(map[string]any{"hook_event_name": "PostToolUse", "agent_id": "sub1", "tool_name": "Bash", "tool_input": map[string]any{"command": "sleep 30"}})
		fmt.Println("subagent running in the background")
		return
	case "bgdone":
		hook(map[string]any{"hook_event_name": "Stop", "last_assistant_message": "subagent finished", "background_tasks": []any{}})
		fmt.Println("subagent finished")
		return
	case "bgshell":
		hook(map[string]any{"hook_event_name": "Stop", "last_assistant_message": "dev server started",
			"background_tasks": []map[string]any{{"type": "shell", "status": "running"}}})
		fmt.Println("dev server in the background")
		return
	case "subask":
		hook(map[string]any{"hook_event_name": "PermissionRequest", "agent_id": "sub1", "tool_name": "Bash", "tool_input": map[string]any{"command": "rm -rf dist"}})
		fmt.Print("subagent: allow Bash rm -rf dist? [y/n] ")
		if in != nil && in.Scan() {
			hook(map[string]any{"hook_event_name": "PostToolUse", "agent_id": "sub1", "tool_name": "Bash", "tool_input": map[string]any{"command": "rm -rf dist"}})
		}
		hook(map[string]any{"hook_event_name": "Stop", "last_assistant_message": "cleaned", "background_tasks": []any{}})
		return
	case "busy":
		stopBusy = make(chan struct{})
		go func(stop chan struct{}) {
			for i := 0; ; i++ {
				select {
				case <-stop:
					fmt.Println()
					return
				case <-time.After(500 * time.Millisecond):
					fmt.Printf("\r%s working %ds ", []string{"✻", "✢", "✳", "✶"}[i%4], i/2)
				}
			}
		}(stopBusy)
		return
	case "question":
		q := map[string]any{"questions": []map[string]any{{"question": "Which database?", "header": "DB",
			"options": []map[string]any{{"label": "Postgres", "description": "the one in prod"}, {"label": "SQLite"}}}}}
		hook(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "AskUserQuestion", "tool_input": q, "tool_use_id": "tu3"})
		fmt.Print("Which database? 1. Postgres 2. SQLite ")
		if in != nil && in.Scan() {
			hook(map[string]any{"hook_event_name": "PostToolUse", "tool_name": "AskUserQuestion", "tool_input": q, "tool_use_id": "tu3"})
			line = "picked " + strings.TrimSpace(in.Text())
		}
	case "ask":
		tool := map[string]any{"command": "rm -rf build"}
		hook(map[string]any{"hook_event_name": "PermissionRequest", "tool_name": "Bash", "tool_input": tool, "tool_use_id": "tu1"})
		fmt.Print("allow Bash rm -rf build? [y/n] ")
		if in != nil && in.Scan() {
			hook(map[string]any{"hook_event_name": "PostToolUse", "tool_name": "Bash", "tool_input": tool, "tool_use_id": "tu1"})
			recordTool("tu1", "Bash", tool, false)
		}
	default:
		for i := 0; i < 3; i++ {
			fmt.Printf("\r%s working %ds", []string{"✻", "✢", "✳"}[i], i)
			time.Sleep(300 * time.Millisecond)
		}
		fmt.Println()
		hook(map[string]any{"hook_event_name": "PostToolUse", "tool_name": "Read", "tool_input": map[string]any{"file_path": cwd + "/main.go"}, "tool_use_id": "tu2"})
		recordTool("tu2", "Read", map[string]any{"file_path": cwd + "/main.go"}, false)
	}
	msg := "reply: " + line
	record("assistant", []map[string]any{{"type": "text", "text": msg + "\ndetails…"}})
	fmt.Println(msg)
	hook(map[string]any{"hook_event_name": "Stop", "last_assistant_message": msg + "\ndetails…"})
}
