package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/krol22/stackk/cli/internal/core"
	"github.com/krol22/stackk/cli/internal/store"
)

// taskJSON is the task as `ls -json` and `show -json` print it: a stable contract for scripts
// and the web UI, independent of how tasks are stored.
type taskJSON struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Stack          string `json:"stack"`
	StackName      string `json:"stack_name"`
	View           string `json:"view"`  // stack, running, deferred, exited, paused, hidden
	Phase          string `json:"phase"` // idle, working, waiting, unknown, shell
	WaitingKind    string `json:"waiting_kind,omitempty"`
	Agent          string `json:"agent"` // claude, omp, "" (a shell)
	Cwd            string `json:"cwd"`
	LastMsg        string `json:"last_msg"`
	LastTool       string `json:"last_tool,omitempty"`
	LastToolAt     int64  `json:"last_tool_at,omitempty"`
	Note           string `json:"note,omitempty"`
	Error          string `json:"error,omitempty"`
	PhaseSince     int64  `json:"phase_since"`
	QueuedAt       int64  `json:"queued_at"`
	CreatedAt      int64  `json:"created_at"`
	RunState       string `json:"run_state"` // starting, live, exited
	AgentSessionID string `json:"agent_session_id,omitempty"`
	Pane           string `json:"pane,omitempty"`
	Window         string `json:"window,omitempty"`
}

func toJSON(t store.Task, stackNames map[string]string) taskJSON {
	return taskJSON{ID: t.ID, Name: t.Name, Stack: t.Stack, StackName: stackNames[t.Stack], View: store.View(t),
		Phase: t.Phase, WaitingKind: t.WaitingKind, Agent: t.Agent, Cwd: t.Cwd, LastMsg: t.LastMsg, LastTool: t.LastTool,
		LastToolAt: t.LastToolAt, Note: t.Note, Error: t.Error, PhaseSince: t.PhaseSince, QueuedAt: t.QueuedAt,
		CreatedAt: t.CreatedAt, RunState: t.RunState, AgentSessionID: t.AgentSessionID, Pane: t.TmuxPane, Window: t.HomeWindow}
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

func keyCmd(app *core.App, args []string) error {
	t := taskArg(app, args, "key <task> <key…>   (a letter, a digit or: "+strings.Join(core.KeyNames(), ", ")+")")
	if len(args) < 2 {
		die(fmt.Errorf("usage: stackk key <task> <key…>"))
	}
	return app.Keys(t.ID, args[1:])
}
