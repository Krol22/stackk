package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/krol22/stackk/cli/internal/agent"
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
	WaitingID      string          `json:"waiting_id,omitempty"` // pass to key -if-waiting
	Waiting        json.RawMessage `json:"waiting,omitempty"`    // what it asks: {tool, input} or {questions: [{question, options}]}
	Agent          string          `json:"agent"`                // its name, "" for a shell
	Answers        *agent.Answers  `json:"answers,omitempty"`    // keys that answer its prompts, the current question's options included
	Cwd            string          `json:"cwd"`
	LastMsg        string          `json:"last_msg"`
	LastTool       string          `json:"last_tool,omitempty"`
	LastToolAt     int64           `json:"last_tool_at,omitempty"`
	Note           string          `json:"note,omitempty"`
	Error          string          `json:"error,omitempty"`
	PhaseSince     int64           `json:"phase_since"`
	QueuedAt       int64           `json:"queued_at"`
	Priority       bool            `json:"priority,omitempty"`
	CreatedAt      int64           `json:"created_at"`
	RunState       string          `json:"run_state"` // starting, live, exited
	AgentSessionID string          `json:"agent_session_id,omitempty"`
	Pane           string          `json:"pane,omitempty"`
	Window         string          `json:"window,omitempty"`
}

func toJSON(t store.Task, stackNames map[string]string) taskJSON {
	return taskJSON{ID: t.ID, Name: t.Name, Stack: t.Stack, StackName: stackNames[t.Stack], View: store.View(t),
		Phase: t.Phase, WaitingKind: waitingKind(t), Agent: t.Agent, Cwd: t.Cwd, LastMsg: t.LastMsg, LastTool: t.LastTool,
		LastToolAt: t.LastToolAt, Note: t.Note, Error: t.Error, PhaseSince: t.PhaseSince, QueuedAt: t.QueuedAt, Priority: t.Priority,
		CreatedAt: t.CreatedAt, RunState: t.RunState, AgentSessionID: t.AgentSessionID, Pane: t.TmuxPane, Window: t.HomeWindow,
		Waiting: waitingJSON(t), WaitingID: core.WaitingID(t), Answers: answers(t)}
}

func waitingKind(t store.Task) string {
	if t.Phase != store.PhaseWaiting && store.PendingQuestion(t) != nil {
		return "question"
	}
	return t.WaitingKind
}

func answers(t store.Task) *agent.Answers {
	ag, err := agent.Get(t.Agent)
	if err != nil {
		return nil
	}
	var w struct {
		Questions []struct {
			Options []json.RawMessage `json:"options"`
		} `json:"questions"`
	}
	n := 0
	if json.Unmarshal(waitingJSON(t), &w) == nil && len(w.Questions) > 0 {
		n = len(w.Questions[0].Options)
	}
	a := ag.Answers(n)
	return &a
}

// waitingJSON is what the task waits on you for: its prompt, else a question it posted apart
// from the turn (one at a time, in order).
func waitingJSON(t store.Task) json.RawMessage {
	if t.Phase != store.PhaseWaiting {
		return store.PendingQuestion(t)
	}
	if !json.Valid([]byte(t.WaitingDetail)) {
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
	var msgs []sessions.Message
	ag, _ := agent.Get(t.Agent)           // nil for a shell
	if t.SessionPath != "" && ag != nil { // empty until the agent's first hook
		// The main transcript even if stackk recorded a subagent's before it told them apart.
		path, _ := ag.MainSession(t.SessionPath)
		var err error
		if msgs, err = ag.Messages(path, *n); err != nil && !errors.Is(err, agent.ErrNotImplemented) {
			return err
		}
	}
	// A new session can be stuck before its first hook, on Claude's "do you trust this folder?":
	// nothing in the transcript says so, the screen does (and only until that first hook).
	trust := ag != nil && t.LastEventAt == 0 && t.RunState != store.RunExited && asksTrust(app, ag, t.ID)
	if *asJSON {
		if msgs == nil {
			msgs = []sessions.Message{}
		}
		return printJSON(struct {
			Task      taskJSON           `json:"task"`
			Messages  []sessions.Message `json:"messages"`
			AsksTrust bool               `json:"asks_trust,omitempty"` // answer with key down, enter (yes) or esc (no)
		}{toJSON(t, stackNames(app)), msgs, trust})
	}
	if trust {
		fmt.Println("asks: do you trust this folder? (stackk key <task> down enter: yes)")
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

func asksTrust(app *core.App, ag agent.Agent, id string) bool {
	lines, err := app.Screen(id, 0)
	return err == nil && ag.AsksTrust(strings.Join(lines, "\n"))
}

func keyCmd(app *core.App, args []string) error {
	fs := flag.NewFlagSet("key", flag.ExitOnError)
	waiting := fs.String("if-waiting", "", "only if the agent still waits on that prompt (waiting_id in ls -json)")
	trust := fs.Bool("if-trust", false, "only if it still asks whether to trust its folder")
	fs.Parse(args)
	args = fs.Args()
	t := taskArg(app, args, "key [-if-waiting id] [-if-trust] <task> <key…>   (a letter, a digit or: "+strings.Join(core.KeyNames(), ", ")+")")
	if len(args) < 2 {
		die(fmt.Errorf("usage: stackk key <task> <key…>"))
	}
	if ag, err := agent.Get(t.Agent); *trust && (err != nil || t.LastEventAt != 0 || !asksTrust(app, ag, t.ID)) {
		return fmt.Errorf("%s no longer asks to trust its folder", t.Name)
	}
	return app.Keys(t.ID, args[1:], *waiting)
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

// macFolders are the home folders macOS makes, where no code lives.
var macFolders = map[string]bool{"Library": true, "Applications": true, "Applications (Parallels)": true,
	"Movies": true, "Music": true, "Pictures": true, "Public": true}

type dirEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Git  bool   `json:"git,omitempty"` // a repository: likely where a session should start
}

// dirsCmd lists the folders in one folder, for picking where a session starts without typing a
// path. It never leaves your home folder: that is what a phone may browse.
func dirsCmd(app *core.App, args []string) error {
	fs := flag.NewFlagSet("dirs", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "JSON output")
	pos := flagsAnywhere(fs, args)
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := home
	if len(pos) > 0 && pos[0] != "" {
		if dir, err = filepath.Abs(pos[0]); err != nil {
			return err
		}
	}
	if real, err := filepath.EvalSymlinks(home); err == nil {
		home = real
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	if rel, err := filepath.Rel(home, dir); err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return fmt.Errorf("%s is outside your home folder", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	out := struct {
		Path   string     `json:"path"`
		Parent string     `json:"parent,omitempty"`
		Dirs   []dirEntry `json:"dirs"`
		Recent []dirEntry `json:"recent,omitempty"`
	}{Path: dir, Dirs: []dirEntry{}}
	if dir != home {
		out.Parent = filepath.Dir(dir)
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || name == "node_modules" || dir == home && macFolders[name] {
			continue
		}
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err != nil || !st.IsDir() {
			continue
		}
		_, gerr := os.Stat(filepath.Join(p, ".git"))
		out.Dirs = append(out.Dirs, dirEntry{Name: name, Path: p, Git: gerr == nil})
	}
	if dir == home { // where your sessions ran lately, newest first
		tasks, _ := store.Tasks(app.DB, "")
		for _, d := range core.RecentDirs(tasks, 8) {
			out.Recent = append(out.Recent, dirEntry{Name: filepath.Base(d), Path: d})
		}
	}
	if *asJSON {
		return printJSON(out)
	}
	for _, d := range out.Dirs {
		fmt.Println(d.Path)
	}
	return nil
}

// renameCmd renames a task (and its window); the name comes after the task, or on stdin.
func renameCmd(app *core.App, args []string) error {
	t := taskArg(app, args, "rename <task> <new name>   (no name: read it from stdin)")
	name := strings.Join(args[1:], " ")
	if len(args) == 1 {
		b, err := io.ReadAll(io.LimitReader(os.Stdin, 4096))
		if err != nil {
			return err
		}
		name = string(b)
	}
	if name = strings.TrimSpace(name); name == "" {
		return fmt.Errorf("a name can't be empty")
	}
	return app.Rename(t.ID, name)
}

// reconcileCmd brings the stored state in line with tmux once, or every interval with -every.
// It is the dashboard's job while one runs; -every does it without one (stackk-web), keeping
// what it saw between rounds so it can tell an agent that went quiet.
func reconcileCmd(app *core.App, args []string) error {
	fs := flag.NewFlagSet("reconcile", flag.ExitOnError)
	every := fs.Duration("every", 0, "repeat at this interval (e.g. 10s) until stopped")
	fs.Parse(args)
	r := core.NewReconciler()
	if *every <= 0 {
		return r.Run(app)
	}
	// Run as long as whoever started it: an orphan would go on acting on tmux after its
	// owner (stackk-web) is gone.
	parent := os.Getppid()
	for os.Getppid() == parent {
		if err := r.Run(app); err != nil && err != core.ErrBackend {
			fmt.Fprintln(os.Stderr, "stackk reconcile:", err)
		}
		time.Sleep(*every)
	}
	return nil
}
