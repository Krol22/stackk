package core

import (
	"strconv"

	"github.com/krol22/stackk/internal/tmux"
)

// canvasMin is the smallest canvas worth showing; below it the canvas waits, still running (a
// book stays on its page), in a session of its own: out of your window list.
const canvasMin = 6

const parkSession = "_stackk"

// FitCanvas gives the dashboard dashH lines and the canvas, below it, the rest of the window.
func (d *Drawer) FitCanvas(dashH int) error {
	defer d.lock()()
	if d.Canvas == "" || d.quit {
		return nil
	}
	l, err := d.read()
	if err != nil {
		return err
	}
	t := d.A.T
	h := l.ui.WinHeight - dashH - 1 // one line of pane border
	var c *tmux.Pane
	if len(l.canvases) > 0 {
		c = &l.canvases[0]
	}
	here := c != nil && c.WindowID == l.ui.WindowID
	switch {
	case h < canvasMin:
		if here {
			err = d.park(*c)
		}
	case c == nil:
		var id string
		if id, err = t.Run("split-window", "-vd", "-t", d.UI, "-l", strconv.Itoa(h), "-P", "-F", "#{pane_id}",
			d.A.Self, "canvas-run", d.Canvas); err == nil {
			t.SetPaneOpt(id, "@stackk_role", "canvas")
			t.SetPaneOpt(id, "@stackk_slot", d.Stack)
			t.SetPaneOpt(id, "allow-passthrough", "on") // images (kitty, iTerm2, sixel) through tmux
		}
	case !here:
		_, err = t.Run("join-pane", "-vd", "-s", c.ID, "-t", d.UI, "-l", strconv.Itoa(h))
	case l.ui.Height != dashH:
		_, err = t.Run("resize-pane", "-t", d.UI, "-y", strconv.Itoa(dashH))
	}
	return err
}

// park moves the canvas out of sight into a window of the same size, so the program isn't
// resized (and a book not reflowed) on the way out and back.
func (d *Drawer) park(c tmux.Pane) error {
	t := d.A.T
	if _, err := t.Run("has-session", "-t", "="+parkSession); err != nil {
		if _, err := t.Run("new-session", "-d", "-s", parkSession, "-n", "parked", d.A.Self, "hold", parkSession); err != nil {
			return err
		}
		t.SetPaneOpt(parkSession+":", "@stackk_role", "hold")
	}
	win, err := t.Run("break-pane", "-d", "-P", "-F", "#{window_id}", "-s", c.ID, "-t", parkSession+":", "-n", "canvas")
	if err != nil {
		return err
	}
	return t.Chain([]string{"set-option", "-w", "-t", win, "window-size", "manual"},
		[]string{"resize-window", "-t", win, "-x", c.Width, "-y", strconv.Itoa(c.Height)})
}

// SetCanvas switches the canvas program while the dashboard runs; "" removes the canvas and
// brings back the zoomed dashboard. dashH is the dashboard's height in the new mode, so the
// canvas appears in the same step as the columns.
func (d *Drawer) SetCanvas(command string, dashH int) error {
	unlock := d.lock()
	if d.Canvas == command || d.quit {
		unlock()
		return nil
	}
	d.Canvas = command
	l, err := d.read()
	if err == nil {
		for _, c := range l.canvases { // a new command starts fresh
			d.A.T.Run("kill-pane", "-t", c.ID)
		}
	}
	unlock()
	if err != nil {
		return err
	}
	if err := d.Ensure(); err != nil {
		return err
	}
	return d.FitCanvas(dashH)
}
