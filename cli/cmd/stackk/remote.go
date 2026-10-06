package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/krol22/stackk/cli/internal/core"
	"github.com/krol22/stackk/cli/internal/sessions"
	"github.com/krol22/stackk/cli/internal/store"
)

// taskJSON is the task as `ls -json` and `show -json` print it: a stable contract for scripts
// and the web UI, independent of how tasks are stored.
type taskJSON struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Stack          string          `json:"stack"`
	StackName      string          `json:"stack_name"`
	View           string          `json:"view"`  // stack, running, deferred, exited, paused, hidden
	Phase          string          `json:"phase"` // idle, working, waiting, unknown, shell
	WaitingKind    string          `json:"waiting_kind,omitempty"`
	Waiting        json.RawMessage `json:"waiting,omitempty"` // what it asks: {tool, input} or {questions: [{question, options}]}
	Agent          string          `json:"agent"`             // claude, omp, "" (a shell)
	Cwd            string          `json:"cwd"`
	LastMsg        string          `json:"last_msg"`
	LastTool       string          `json:"last_tool,omitempty"`
	LastToolAt     int64           `json:"last_tool_at,omitempty"`
	Note           string          `json:"note,omitempty"`
	Error          string          `json:"error,omitempty"`
	PhaseSince     int64           `json:"phase_since"`
	QueuedAt       int64           `json:"queued_at"`
	CreatedAt      int64           `json:"created_at"`
	RunState       string          `json:"run_state"` // starting, live, exited
	AgentSessionID string          `json:"agent_session_id,omitempty"`
	Pane           string          `json:"pane,omitempty"`
	Window         string          `json:"window,omitempty"`
}

func toJSON(t store.Task, stackNames map[string]string) taskJSON {
	return taskJSON{ID: t.ID, Name: t.Name, Stack: t.Stack, StackName: stackNames[t.Stack], View: store.View(t),
		Phase: t.Phase, WaitingKind: t.WaitingKind, Agent: t.Agent, Cwd: t.Cwd, LastMsg: t.LastMsg, LastTool: t.LastTool,
		LastToolAt: t.LastToolAt, Note: t.Note, Error: t.Error, PhaseSince: t.PhaseSince, QueuedAt: t.QueuedAt,
		CreatedAt: t.CreatedAt, RunState: t.RunState, AgentSessionID: t.AgentSessionID, Pane: t.TmuxPane, Window: t.HomeWindow,
		Waiting: waitingJSON(t)}
}

func waitingJSON(t store.Task) json.RawMessage {
	if t.Phase != store.PhaseWaiting || !json.Valid([]byte(t.WaitingDetail)) {
		return nil
	}
	return json.RawMessage(t.WaitingDetail)
}

func stackNames(app *core.App) map[string]string {
	names := map[string]string{}
	if ss, err := app.T.Sessions(); err == nil {
		for _, s := range ss {
			names[s.ID] = s.Name
		}
	}
	return names
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// flagsAnywhere parses flags before and after the positional arguments.
func flagsAnywhere(fs *flag.FlagSet, args []string) []string {
	var pos []string
	for {
		fs.Parse(args)
		if args = fs.Args(); len(args) == 0 {
			return pos
		}
		pos, args = append(pos, args[0]), args[1:]
	}
}

// taskArg resolves the task argument (id, id prefix or name) or exits with how to call the command.
func taskArg(app *core.App, args []string, use string) store.Task {
	if len(args) == 0 {
		die(fmt.Errorf("usage: stackk %s", use))
	}
	t, err := app.FindTask(args[0])
	if err != nil {
		die(err)
	}
	return t
}

func showCmd(app *core.App, args []string) error {
	fs := flag.NewFlagSet("show", flag.ExitOnError)
	history := fs.Int("lines", 200, "lines of scrollback above the screen")
	asJSON := fs.Bool("json", false, "JSON output")
	pos := flagsAnywhere(fs, args)
	t := taskArg(app, pos, "show [-lines n] [-json] <task>")
	lines, err := app.Screen(t.ID, *history)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(struct {
			Task  taskJSON `json:"task"`
			Lines []string `json:"lines"`
		}{toJSON(t, stackNames(app)), lines})
	}
	fmt.Println(strings.Join(lines, "\n"))
	return nil
}

func sendCmd(app *core.App, args []string) error {
	fs := flag.NewFlagSet("send", flag.ExitOnError)
	noEnter := fs.Bool("no-enter", false, "type the text without pressing Enter")
	fs.Parse(args)
	t := taskArg(app, fs.Args(), "send [-no-enter] <task> <text…>   (no text: read it from stdin)")
	text := strings.Join(fs.Args()[1:], " ")
	if len(fs.Args()) == 1 {
		b, err := io.ReadAll(bufio.NewReader(os.Stdin))
		if err != nil {
			return err
		}
		text = string(b)
	}
	return app.Send(t.ID, text, !*noEnter)
}

func transcriptCmd(app *core.App, args []string) error {
	fs := flag.NewFlagSet("transcript", flag.ExitOnError)
	n := fs.Int("n", 50, "the last n messages")
	asJSON := fs.Bool("json", false, "JSON output")
	pos := flagsAnywhere(fs, args)
	t := taskArg(app, pos, "transcript [-n count] [-json] <task>")
	if t.SessionPath == "" {
		return fmt.Errorf("%s has no transcript yet (it hasn't talked to stackk's hooks)", t.Name)
	}
	path := t.SessionPath
	if t.Agent == "omp" { // recorded from a subagent's events by stackk before it told them apart
		path, _ = sessions.OmpMain(path)
	}
	msgs, err := sessions.Messages(t.Agent, path, *n)
	if err != nil {
		return err
	}
	if *asJSON {
		if msgs == nil {
			msgs = []sessions.Message{}
		}
		return printJSON(struct {
			Task     taskJSON           `json:"task"`
			Messages []sessions.Message `json:"messages"`
		}{toJSON(t, stackNames(app)), msgs})
	}
	for _, m := range msgs {
		switch m.Role {
		case "tool":
			mark := "…"
			if m.Done {
				mark = "✓"
			}
			if m.Error {
				mark = "✗"
			}
			fmt.Printf("  » %s %s\n", m.Tool, mark)
		case "user":
			fmt.Printf("\nyou: %s\n", m.Text)
		default:
			fmt.Printf("\n%s: %s\n", t.Agent, m.Text)
		}
	}
	return nil
}

func keyCmd(app *core.App, args []string) error {
	t := taskArg(app, args, "key <task> <key…>   (a letter, a digit or: "+strings.Join(core.KeyNames(), ", ")+")")
	if len(args) < 2 {
		die(fmt.Errorf("usage: stackk key <task> <key…>"))
	}
	return app.Keys(t.ID, args[1:])
}

// maxUpload bounds what a phone may put on this Mac in one file.
const maxUpload = 25 << 20

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// uploadCmd saves stdin as a file for a task to read (a screenshot from the phone, a log) and
// prints its path. Files go to a private directory next to stackk's state, one per task.
func uploadCmd(app *core.App, args []string) error {
	fs := flag.NewFlagSet("upload", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "JSON output")
	pos := flagsAnywhere(fs, args)
	t := taskArg(app, pos, "upload [-json] <task> <file name>   (the content on stdin)")
	if len(pos) < 2 {
		die(fmt.Errorf("usage: stackk upload <task> <file name>"))
	}
	name := strings.Trim(unsafeName.ReplaceAllString(filepath.Base(pos[1]), "_"), "._")
	if name == "" {
		name = "file"
	}
	if len(name) > 80 {
		name = name[len(name)-80:]
	}
	dir := filepath.Join(filepath.Dir(store.DefaultPath()), "uploads", t.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, time.Now().Format("20060102-150405")+"-"+name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(os.Stdin, maxUpload+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > maxUpload {
		err = fmt.Errorf("file too large (over %d MB)", maxUpload>>20)
	}
	if err != nil {
		os.Remove(path)
		return err
	}
	if *asJSON {
		return printJSON(map[string]any{"path": path, "size": n})
	}
	fmt.Println(path)
	return nil
}
