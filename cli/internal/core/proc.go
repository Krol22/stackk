package core

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/krol22/stackk/cli/internal/tmux"
)

// procTree is a snapshot of the process table, to see what runs under a pane: an agent started
// through `fish -c` sits below the shell, so pane_current_command alone says "fish".
type procTree struct {
	children map[int][]int
	comm     map[int]string
}

func readProcs() *procTree {
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=,comm=").Output()
	if err != nil {
		return nil
	}
	pt := &procTree{children: map[int][]int{}, comm: map[int]string{}}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		if err1 != nil || err2 != nil {
			continue
		}
		pt.children[ppid] = append(pt.children[ppid], pid)
		pt.comm[pid] = filepath.Base(strings.Join(f[2:], " "))
	}
	return pt
}

// agentIn returns the agent running in the pane: its foreground command, or the closest agent
// process among the pane process's descendants (two levels: shell → agent).
func (pt *procTree) agentIn(p tmux.Pane) string {
	if a := tmux.AgentOf(p.Command); a != "" {
		return a
	}
	if pt == nil {
		return ""
	}
	root, err := strconv.Atoi(p.PID)
	if err != nil {
		return ""
	}
	level := []int{root}
	for depth := 0; depth < 3 && len(level) > 0; depth++ {
		var next []int
		for _, pid := range level {
			if a := tmux.AgentOf(pt.comm[pid]); a != "" {
				return a
			}
			next = append(next, pt.children[pid]...)
		}
		level = next
	}
	return ""
}
