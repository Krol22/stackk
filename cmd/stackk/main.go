package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/krol22/stackk/internal/core"
	"github.com/krol22/stackk/internal/sessions"
	"github.com/krol22/stackk/internal/setup"
	"github.com/krol22/stackk/internal/store"
	"github.com/krol22/stackk/internal/tmux"
	"github.com/krol22/stackk/internal/ui"
)

const usage = `stackk — a round-robin stack of AI sessions in tmux

  stackk setup                        hook stackk into Claude Code and omp (run once after installing)
  stackk [stack]                      dashboard in the current pane (default stack "stackk", created on first use)
  stackk take [stack]                 move the current window into the stack
  stackk focus [task]                 bring the dashboard to the front (and open a task in the drawer)
  stackk find [-ai] [-json] <query>   search past Claude/omp sessions (title, prompts, folder)
  stackk attach [-stack s] <session>  resume a past session (id or id prefix) onto the stack
  stackk new [-agent claude|omp] [-name n] [-stack s] <folder> [prompt]
  stackk ls [-json]                   tasks
  stackk log [task]                   events
  stackk paused                       paused sessions (hidden from the dashboard)
  stackk resume <task>                bring back a paused session / restart a finished one
  stackk pause <task>                 hide a session (it keeps running)
  stackk hide|unhide <task>           take a window that is not a task out of the lists
  stackk rm <task>                    close the task's window and remove it for good
  stackk mark <claude|omp>            from agent hooks (payload on stdin)
`

func main() {
	args := os.Args[1:]
	cmd := ""
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	if cmd == "mark" {
		mark(args)
		return
	}
	switch cmd {
	case "slot":
		slot(os.Getenv("TMUX_PANE"))
		return
	case "hold":
		name := core.DefaultStack()
		if len(args) > 0 {
			name = args[0]
		}
		ui.Slot(func() string { return "stack " + name + "\nyour sessions live here; open the dashboard with: stackk" }, nil)
		return
	}
	app, err := open()
	if err != nil {
		die(err)
	}
	switch cmd {
	case "", "ui":
		err = ui.RunStack(app, core.DefaultStack(), os.Getenv("TMUX_PANE"))
	case "setup":
		err = setup.Run(app.Self, func(s string) { fmt.Println(s) })
	case "focus":
		task := ""
		if len(args) > 0 {
			task = args[0]
		}
		err = app.Focus(task)
	case "test-notify":
		app.TestNotify()
		fmt.Println("sent: click the notification; it should bring your terminal and the stackk dashboard to the front")
	case "find":
		err = findCmd(app, args)
	case "attach":
		err = attachCmd(app, args)
	case "take":
		err = take(app, args)
	case "new":
		err = newTask(app, args)
	case "ls":
		err = ls(app, args)
	case "log":
		err = logCmd(app, args)
	case "resume":
		if len(args) != 1 {
			die(fmt.Errorf("stackk resume <task>"))
		}
		err = resume(app, args[0])
	case "paused":
		err = pausedCmd(app)
	case "pause":
		if len(args) != 1 {
			die(fmt.Errorf("stackk pause <task>"))
		}
		err = app.SetPaused(args[0], true)
	case "hide", "unhide":
		if len(args) != 1 {
			die(fmt.Errorf("stackk %s <task>", cmd))
		}
		err = app.SetHidden(args[0], cmd == "hide")
	case "rm":
		if len(args) != 1 {
			die(fmt.Errorf("stackk rm <task>"))
		}
		err = app.Remove(args[0])
	case "reconcile":
		err = core.NewReconciler().Run(app)
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		if strings.HasPrefix(cmd, "-") {
			fmt.Print(usage)
			os.Exit(2)
		}
		err = ui.RunStack(app, cmd, os.Getenv("TMUX_PANE"))
	}
	if err != nil {
		die(err)
	}
}

func open() (*core.App, error) {
	db, err := store.Open(store.DefaultPath())
	if err != nil {
		return nil, err
	}
	return core.New(db, tmux.FromEnv()), nil
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "stackk:", err)
	os.Exit(1)
}

// mark never fails the agent: errors go to a log file and the exit code is always 0.
func mark(args []string) {
	go func() {
		time.Sleep(1500 * time.Millisecond)
		logErr(fmt.Errorf("mark: timeout"))
		os.Exit(0)
	}()
	agent := "claude"
	if len(args) > 0 {
		agent = args[0]
	}
	raw, _ := io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
	pane := os.Getenv("TMUX_PANE")
	if pane == "" {
		os.Exit(0)
	}
	// One tmux query decides and feeds Mark. Most hook calls come from panes outside any stack
	// (not in a stack session, no task tag): those exit here without touching the DB.
	p, err := tmux.FromEnv().Pane(pane)
	if err != nil || (!p.Stack && p.Task == "") {
		os.Exit(0)
	}
	app, err := open()
	if err == nil {
		err = app.Mark(agent, raw, p, os.Getenv("STACKK_TASK_ID"), os.Getenv("STACKK_RUN_ID"))
	}
	if err != nil {
		logErr(err)
	}
	os.Exit(0)
}

func logErr(err error) {
	path := filepath.Join(filepath.Dir(store.DefaultPath()), "errors.log")
	f, ferr := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if ferr != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %v\n", time.Now().Format(time.RFC3339), err)
}

// currentStack resolves -stack, else the default stack (created on demand).
func currentStack(app *core.App, name string) (tmux.Session, error) {
	if name == "" {
		name = core.DefaultStack()
	}
	return app.EnsureStack(name)
}

func take(app *core.App, args []string) error {
	pane := os.Getenv("TMUX_PANE")
	if pane == "" {
		return fmt.Errorf("take only works inside tmux")
	}
	p, err := app.T.Pane(pane)
	if err != nil {
		return err
	}
	name := ""
	if len(args) > 0 {
		name = args[0]
	}
	s, err := currentStack(app, name)
	if err != nil {
		return err
	}
	return app.Take(p.WindowID, s.Name)
}

func newTask(app *core.App, args []string) error {
	fs := flag.NewFlagSet("new", flag.ExitOnError)
	agent := fs.String("agent", "claude", "claude | omp")
	name := fs.String("name", "", "task name")
	stack := fs.String("stack", "", "stack")
	fs.Parse(args)
	if fs.NArg() < 1 {
		return fmt.Errorf("stackk new <folder> [prompt]")
	}
	s, err := currentStack(app, *stack)
	if err != nil {
		return err
	}
	t, err := app.NewTask(s.ID, fs.Arg(0), *name, *agent, strings.Join(fs.Args()[1:], " "))
	if err == nil {
		fmt.Println(t.ID)
	}
	return err
}

func ls(app *core.App, args []string) error {
	fs := flag.NewFlagSet("ls", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "JSON")
	fs.Parse(args)
	tasks, err := store.Tasks(app.DB, "")
	if err != nil {
		return err
	}
	names := map[string]string{}
	if ss, err := app.T.Sessions(); err == nil {
		for _, s := range ss {
			names[s.ID] = s.Name
		}
	}
	if *asJSON {
		type row struct {
			store.Task
			StackName string `json:"stack_name"`
			View      string `json:"view"`
		}
		out := []row{}
		for _, t := range tasks {
			out = append(out, row{t, names[t.Stack], store.View(t)})
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	for _, t := range tasks {
		agent := t.Agent
		if agent == "" {
			agent = "shell"
		}
		fmt.Printf("%-10s %-24s %-7s %-8s %-8s %-8s %s\n", names[t.Stack], t.ID, agent, store.View(t), t.Phase, t.RunState, t.LastMsg)
	}
	return nil
}

func logCmd(app *core.App, args []string) error {
	task := ""
	if len(args) > 0 {
		task = args[0]
	}
	evs, err := store.Events(app.DB, "", task, 200)
	if err != nil {
		return err
	}
	for i := len(evs) - 1; i >= 0; i-- {
		e := evs[i]
		mark := ""
		if !e.Applied {
			mark = " (ignored)"
		}
		fmt.Printf("%s  %-24s %-9s %-10s %s%s\n", time.Unix(e.TS, 0).Format("01-02 15:04:05"), e.TaskName, e.Source, e.Event, e.Detail, mark)
	}
	return nil
}

func resume(app *core.App, id string) error {
	t, err := store.GetTask(app.DB, id)
	if err != nil {
		return err
	}
	if t.PausedAt != 0 {
		if err := app.SetPaused(id, false); err != nil {
			return err
		}
	}
	switch {
	case t.RunState == store.RunExited:
		_, err = app.Resume(id)
	case t.PausedAt == 0:
		err = fmt.Errorf("%s is already running", t.Name)
	}
	return err
}

func pausedCmd(app *core.App) error {
	tasks, err := store.Tasks(app.DB, "")
	if err != nil {
		return err
	}
	for _, t := range tasks {
		if t.PausedAt != 0 {
			fmt.Printf("%-24s %-7s %-8s paused %s  %s\n", t.ID, t.Agent, t.RunState, time.Unix(t.PausedAt, 0).Format("01-02 15:04"), t.Cwd)
		}
	}
	return nil
}

// slot is the placeholder shown in a task's window while the task is open in the dashboard.
func slot(pane string) {
	t := tmux.FromEnv()
	name := func() string {
		w, _ := t.Run("display-message", "-p", "-t", pane, "#{window_name}")
		if w == "" || w == "stackk" {
			return "stackk"
		}
		return w + " is open in the stackk dashboard\npress Enter or click to bring it back here"
	}
	ui.Slot(name, func() {
		app, err := open()
		if err != nil {
			return
		}
		if err := app.Recall(pane); err != nil {
			logErr(err)
		}
	})
}

func findCmd(app *core.App, args []string) error {
	fs := flag.NewFlagSet("find", flag.ExitOnError)
	ai := fs.Bool("ai", false, "rank with a small model (claude -p, haiku)")
	asJSON := fs.Bool("json", false, "JSON output")
	fs.Parse(args)
	query := strings.Join(fs.Args(), " ")
	all := app.SessionIndex()
	var hits []sessions.Hit
	var err error
	if *ai {
		hits, err = sessions.AI(all, query, 12)
	} else {
		hits = sessions.Local(all, query, 12)
	}
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(hits)
	}
	for _, h := range hits {
		fmt.Printf("%-6s %-38s %-12s %s  %s\n", h.Agent, h.ID, h.Modified.Format("01-02 15:04"), h.Title, h.Cwd)
	}
	return nil
}

func attachCmd(app *core.App, args []string) error {
	fs := flag.NewFlagSet("attach", flag.ExitOnError)
	stack := fs.String("stack", "", "stack")
	fs.Parse(args)
	if fs.NArg() != 1 {
		return fmt.Errorf("stackk attach <session id or prefix>")
	}
	var match []sessions.Session
	for _, s := range app.SessionIndex() {
		if strings.HasPrefix(s.ID, fs.Arg(0)) {
			match = append(match, s)
		}
	}
	if len(match) != 1 {
		return fmt.Errorf("%d sessions match %q", len(match), fs.Arg(0))
	}
	st, err := currentStack(app, *stack)
	if err != nil {
		return err
	}
	t, err := app.Attach(st.ID, match[0])
	if err == nil {
		fmt.Println(t.ID)
	}
	return err
}
