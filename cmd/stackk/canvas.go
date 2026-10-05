package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/krol22/stackk/internal/config"
	"github.com/krol22/stackk/internal/tmux"
)

// canvasRun keeps the configured canvas program running in its pane: when it exits, Enter
// starts it again instead of the pane closing and being recreated in a loop.
func canvasRun(command string) {
	// Ctrl-C is for the program. Catching (not ignoring) it keeps the program's own default.
	signal.Notify(make(chan os.Signal, 1), os.Interrupt)
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	var running atomic.Bool
	go settleResizes(&running)
	in := bufio.NewReader(os.Stdin)
	for {
		c := exec.Command(shell, "-c", command)
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
		running.Store(true)
		err := c.Run()
		running.Store(false)
		status := "exited"
		if err != nil {
			status = err.Error()
		}
		fmt.Printf("\n%s: %s\npress Enter to start it again\n", command, status)
		if _, err := in.ReadString('\n'); err != nil {
			return
		}
	}
}

// settleResizes asks the program to redraw (Ctrl-L) once the pane has settled. The canvas is
// resized twice in a row when it comes back (join, then the dashboard's height), and some
// programs (bookokrat) take only the first and keep drawing at that size.
func settleResizes(running *atomic.Bool) {
	winch := make(chan os.Signal, 8)
	signal.Notify(winch, syscall.SIGWINCH)
	pane := os.Getenv("TMUX_PANE")
	timer, again := time.NewTimer(time.Hour), time.NewTimer(time.Hour)
	redraw := func() {
		if running.Load() && pane != "" {
			tmux.FromEnv().Run("send-keys", "-t", pane, "C-l")
		}
	}
	for {
		select {
		case <-winch:
			timer.Reset(300 * time.Millisecond)
			again.Reset(1500 * time.Millisecond) // a redraw asked mid-layout can be dropped
		case <-timer.C:
			redraw()
		case <-again.C:
			redraw()
		}
	}
}

// canvasCmd shows or sets the canvas program; running dashboards pick the change up.
func canvasCmd(args []string) {
	cfg, err := config.Load()
	if err != nil {
		die(err)
	}
	switch {
	case len(args) == 0:
		if cfg.Canvas == "" {
			fmt.Println("no canvas; set one with: stackk canvas <command>")
		} else {
			fmt.Println(cfg.Canvas)
		}
		return
	case len(args) == 1 && args[0] == "off":
		cfg.Canvas = ""
	default:
		cfg.Canvas = strings.Join(args, " ")
	}
	if err := config.Save(cfg); err != nil {
		die(err)
	}
	if cfg.Canvas == "" {
		fmt.Println("canvas off")
	} else {
		fmt.Printf("canvas: %s (the dashboard shows it within a few seconds)\n", cfg.Canvas)
	}
}
