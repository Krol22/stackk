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
	ev["transcript_path"] = "/tmp/fake-" + sessionID + ".jsonl"
	b, _ := json.Marshal(ev)
	cmd := exec.Command(os.Getenv("STACKK_BIN"), "mark", "claude")
	cmd.Stdin = strings.NewReader(string(b))
	cmd.Run()
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
	hook(map[string]any{"hook_event_name": "UserPromptSubmit", "prompt": line})
	switch line {
	case "hang":
		fmt.Println("(hung)")
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
	case "ask":
		tool := map[string]any{"command": "rm -rf build"}
		hook(map[string]any{"hook_event_name": "PermissionRequest", "tool_name": "Bash", "tool_input": tool, "tool_use_id": "tu1"})
		fmt.Print("allow Bash rm -rf build? [y/n] ")
		if in != nil && in.Scan() {
			hook(map[string]any{"hook_event_name": "PostToolUse", "tool_name": "Bash", "tool_input": tool, "tool_use_id": "tu1"})
		}
	default:
		for i := 0; i < 3; i++ {
			fmt.Printf("\r%s working %ds", []string{"✻", "✢", "✳"}[i], i)
			time.Sleep(300 * time.Millisecond)
		}
		fmt.Println()
		hook(map[string]any{"hook_event_name": "PostToolUse", "tool_name": "Read", "tool_input": map[string]any{"file_path": cwd + "/main.go"}, "tool_use_id": "tu2"})
	}
	msg := "reply: " + line
	fmt.Println(msg)
	hook(map[string]any{"hook_event_name": "Stop", "last_assistant_message": msg + "\ndetails…"})
}
