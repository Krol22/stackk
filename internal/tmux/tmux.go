// Package tmux is a thin wrapper over the tmux CLI.
package tmux

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	Socket string // -L name; empty = default server (or $TMUX inside a pane)
}

func FromEnv() Client { return Client{Socket: os.Getenv("STACKK_TMUX_SOCKET")} }

func (c Client) args(a []string) []string {
	if c.Socket != "" {
		return append([]string{"-L", c.Socket}, a...)
	}
	return a
}

func (c Client) Run(a ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tmux", c.args(a)...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if f := os.Getenv("STACKK_TRACE"); f != "" && (os.Getenv("STACKK_TRACE_ALL") != "" || (a[0] != "list-panes" && a[0] != "display-message" && a[0] != "list-clients" && a[0] != "capture-pane")) {
		if fh, ferr := os.OpenFile(f, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); ferr == nil {
			fmt.Fprintf(fh, "%s pid=%d tmux %s err=%v\n", time.Now().Format("15:04:05.000"), os.Getpid(), strings.Join(a, " "), err)
			fh.Close()
		}
	}
	if err != nil {
		return "", fmt.Errorf("tmux %s: %v: %s", strings.Join(a, " "), err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimRight(out.String(), "\n"), nil
}

// Chain runs several tmux commands in one invocation, so the client redraws once at the end
// instead of showing every intermediate layout.
func (c Client) Chain(cmds ...[]string) error {
	var args []string
	for i, cmd := range cmds {
		if i > 0 {
			args = append(args, ";")
		}
		args = append(args, cmd...)
	}
	_, err := c.Run(args...)
	return err
}

// Fields runs a -F query and splits each output line on tabs.
func (c Client) Fields(a ...string) ([][]string, error) {
	out, err := c.Run(a...)
	if err != nil {
		return nil, err
	}
	var rows [][]string
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		rows = append(rows, strings.Split(line, "\t"))
	}
	return rows, nil
}

func F(names ...string) string {
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = "#{" + n + "}"
	}
	return strings.Join(parts, "\t")
}

type Pane struct {
	ID, WindowID, SessionID, SessionName, WindowName, Command, Path string
	Dead                                                            bool
	Task, Run, Role                                                 string
	Stack                                                           bool
	WindowIndex                                                     string
	PID                                                             string
	UIWindow, Zoomed                                                bool
	Width, SlotOf                                                   string
	Active                                                          bool
	SetName                                                         string // window name stackk last set (@stackk_wname)
	OfStack                                                         string // the stack a dashboard pane shows (@stackk_stack)
	Height, WinHeight, WinWidth                                     int
	SlotKind                                                        string // "side" for a placeholder that shows RUNNING (@stackk_slotkind)
}

var paneFmt = F("pane_id", "window_id", "session_id", "session_name", "window_name", "pane_current_command",
	"pane_current_path", "pane_dead", "@stackk_task", "@stackk_run", "@stackk_role", "@stackk", "window_index", "pane_pid",
	"@stackk_ui", "window_zoomed_flag", "pane_width", "@stackk_slot", "pane_active", "@stackk_wname", "@stackk_stack",
	"pane_height", "window_height", "window_width", "@stackk_slotkind")

func parsePane(r []string) Pane {
	for len(r) < 25 {
		r = append(r, "")
	}
	return Pane{ID: r[0], WindowID: r[1], SessionID: r[2], SessionName: r[3], WindowName: r[4], Command: r[5],
		Path: r[6], Dead: r[7] == "1", Task: r[8], Run: r[9], Role: r[10], Stack: r[11] == "1", WindowIndex: r[12], PID: r[13],
		UIWindow: r[14] == "1", Zoomed: r[15] == "1", Width: r[16], SlotOf: r[17], Active: r[18] == "1", SetName: r[19], OfStack: r[20],
		Height: atoi(r[21]), WinHeight: atoi(r[22]), WinWidth: atoi(r[23]), SlotKind: r[24]}
}

func (c Client) Pane(target string) (Pane, error) {
	rows, err := c.Fields("display-message", "-p", "-t", target, paneFmt)
	if err != nil {
		return Pane{}, err
	}
	if len(rows) == 0 {
		return Pane{}, fmt.Errorf("no pane %s", target)
	}
	return parsePane(rows[0]), nil
}

// WindowPanes lists the panes of one window (cheaper than AllPanes when that is all you need).
func (c Client) WindowPanes(target string) ([]Pane, error) {
	rows, err := c.Fields("list-panes", "-t", target, "-F", paneFmt)
	if err != nil {
		return nil, err
	}
	out := make([]Pane, 0, len(rows))
	for _, r := range rows {
		out = append(out, parsePane(r))
	}
	return out, nil
}

func (c Client) AllPanes() ([]Pane, error) {
	rows, err := c.Fields("list-panes", "-a", "-F", paneFmt)
	if err != nil {
		return nil, err
	}
	out := make([]Pane, 0, len(rows))
	for _, r := range rows {
		out = append(out, parsePane(r))
	}
	return out, nil
}

type Session struct {
	ID, Name string
	Stack    bool
}

func (c Client) Sessions() ([]Session, error) {
	rows, err := c.Fields("list-sessions", "-F", F("session_id", "session_name", "@stackk"))
	if err != nil {
		return nil, err
	}
	var out []Session
	for _, r := range rows {
		for len(r) < 3 {
			r = append(r, "")
		}
		out = append(out, Session{ID: r[0], Name: r[1], Stack: r[2] == "1"})
	}
	return out, nil
}

func (c Client) SetPaneOpt(pane, name, value string) error {
	_, err := c.Run("set-option", "-p", "-t", pane, name, value)
	return err
}

// AgentOf reports which agent a pane's foreground process is, by its command name.
func AgentOf(command string) string {
	if command == "omp" {
		return "omp"
	}
	if command == "claude" || isVersion(command) {
		return "claude"
	}
	return ""
}

// Claude Code sets its process title to its version, e.g. "2.1.286".
func isVersion(s string) bool {
	dots := 0
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r == '.':
			dots++
		case r < '0' || r > '9':
			return false
		}
	}
	return dots == 2
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
