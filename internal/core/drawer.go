package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/krol22/stackk/internal/store"
	"github.com/krol22/stackk/internal/tmux"
)

// Drawer drives the window the TUI runs in: the dashboard on the left and, on the right, one
// drawer pane holding either a placeholder ("slot") or the agent pulled out of the stack session.
// Swapping (not moving) keeps the agent's home window alive while it is out.
type Drawer struct {
	A     *App
	UI    string // the TUI's own pane id
	Stack string // session id of the stack
	// Canvas, when set, runs under the dashboard. The drawer then closes by dropping its
	// placeholder instead of zooming the dashboard, which would hide the canvas.
	Canvas string
	mu     sync.Mutex
	quit   bool // after Quit nothing may create panes again
	// the canvas last written to the window for other stackk processes
	shared       bool
	sharedCanvas string
}

// Width of the drawer as a share of the window.
var drawerWidth = envStr("STACKK_DRAWER_WIDTH", "62%")

type layout struct {
	ui       tmux.Pane
	side     *tmux.Pane  // drawer pane in the TUI's window: the slot or a pulled-out agent
	slots    []tmux.Pane // this stack's placeholders anywhere on the server
	canvases []tmux.Pane // this stack's canvas panes, the one in the dashboard's window first
	panes    []tmux.Pane
}

func (d *Drawer) read() (layout, error) {
	panes, err := d.A.T.AllPanes()
	if err != nil {
		return layout{}, ErrBackend
	}
	l := layout{panes: panes}
	found := false
	for _, p := range panes {
		if p.ID == d.UI {
			l.ui, found = p, true
		}
	}
	if !found {
		return l, fmt.Errorf("TUI pane %s is gone", d.UI)
	}
	for i, p := range panes {
		if p.Role == "slot" && p.SlotOf == d.Stack {
			l.slots = append(l.slots, p)
		}
		if p.Role == "canvas" && p.SlotOf == d.Stack {
			if p.WindowID == l.ui.WindowID {
				l.canvases = append([]tmux.Pane{p}, l.canvases...)
			} else {
				l.canvases = append(l.canvases, p)
			}
		}
		if p.WindowID == l.ui.WindowID && p.ID != d.UI && l.side == nil && (p.Role == "slot" || p.Task != "") {
			l.side = &panes[i]
		}
	}
	return l, nil
}

// Out returns the task shown in the drawer, or "". It runs every second, so it only looks at
// the dashboard's own window.
func (d *Drawer) Out() string {
	panes, err := d.A.T.WindowPanes(d.UI)
	if err != nil {
		return ""
	}
	for _, p := range panes {
		if p.ID != d.UI && p.Task != "" && p.Role != "slot" {
			return p.Task
		}
	}
	return ""
}

// sidePct is the RUNNING column's share of the window in canvas mode, where the closed drawer
// is that column.
const sidePct = 42

// slotKind is the placeholder this mode wants: with a canvas it shows RUNNING beside the stack.
func (d *Drawer) slotKind() string {
	if d.Canvas != "" {
		return "side"
	}
	return ""
}

func (d *Drawer) slotWidth(kind string) string {
	if kind == "side" {
		return strconv.Itoa(sidePct) + "%"
	}
	return drawerWidth
}

func (d *Drawer) newSlot(kind string) error {
	args := []string{"split-window", "-fhd", "-t", d.UI, "-l", d.slotWidth(kind), "-P", "-F", "#{pane_id}", d.A.Self, "slot"}
	if kind != "" {
		args = append(args, kind, d.Stack) // tags are set after the process starts
	}
	out, err := d.A.T.Run(args...)
	if err != nil {
		return err
	}
	d.A.T.SetPaneOpt(out, "@stackk_slot", d.Stack)
	if kind != "" {
		d.A.T.SetPaneOpt(out, "@stackk_slotkind", kind)
	}
	return d.A.T.SetPaneOpt(out, "@stackk_role", "slot")
}

// Ensure tags the TUI and repairs the drawer after crashes or dead agents.
func (d *Drawer) Ensure() error {
	defer d.lock()()
	if d.quit {
		return nil
	}
	t := d.A.T
	l, err := d.read()
	if err != nil {
		return err
	}
	// Tag the dashboard (only what is missing: this runs every few seconds).
	if l.ui.Role != "tui" {
		t.SetPaneOpt(d.UI, "@stackk_role", "tui")
	}
	if !l.ui.UIWindow {
		t.Run("set-option", "-w", "-t", d.UI, "@stackk_ui", "1")
	}
	if l.ui.OfStack != d.Stack {
		t.SetPaneOpt(d.UI, "@stackk_stack", d.Stack)
	}
	// The dashboard's window is not a task's: drop a task name stackk once gave it by mistake.
	if l.ui.SetName != "" {
		t.Run("set-option", "-wu", "-t", d.UI, "@stackk_wname")
		t.Run("rename-window", "-t", d.UI, "dashboard")
	}
	if d.recoverStale(l) {
		if l, err = d.read(); err != nil {
			return err
		}
	}
	for _, c := range l.canvases[min(1, len(l.canvases)):] {
		t.Run("kill-pane", "-t", c.ID)
	}
	if !d.shared || d.sharedCanvas != d.Canvas {
		d.shared, d.sharedCanvas = true, d.Canvas
		if d.Canvas == "" {
			t.Run("set-option", "-wu", "-t", d.UI, "@stackk_canvas")
		} else {
			t.Run("set-option", "-w", "-t", d.UI, "@stackk_canvas", d.Canvas) // for other stackk processes
		}
	}
	kind := d.slotKind()
	if l.side != nil && l.side.Role == "slot" && l.side.SlotKind != kind {
		// The canvas was switched on or off: the placeholder here is the wrong kind.
		t.Run("kill-pane", "-t", l.side.ID)
		if l, err = d.read(); err != nil {
			return err
		}
	}
	if l.side == nil {
		// First start, or the pulled-out agent died: bring a stray slot back or make a new one.
		if len(l.slots) > 0 && l.slots[0].SlotKind == kind {
			if _, err := t.Run("join-pane", "-fhd", "-s", l.slots[0].ID, "-t", d.UI, "-l", d.slotWidth(kind)); err != nil {
				return err
			}
			l.slots = l.slots[1:]
		} else if err := d.newSlot(kind); err != nil {
			return err
		}
		for _, s := range l.slots {
			t.Run("kill-pane", "-t", s.ID)
		}
		return d.zoom(kind == "")
	}
	if kind == "side" && l.ui.Zoomed {
		d.zoom(false)
	}
	if l.side.Role == "slot" {
		for _, s := range l.slots {
			if s.ID != l.side.ID {
				t.Run("kill-pane", "-t", s.ID)
			}
		}
	}
	return nil
}

// lock serializes drawer changes, also across processes: with a canvas the RUNNING column
// (another stackk process) opens tasks too.
func (d *Drawer) lock() (unlock func()) {
	d.mu.Lock()
	f, err := os.OpenFile(filepath.Join(filepath.Dir(store.DefaultPath()), "drawer.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return d.mu.Unlock
	}
	syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
	return func() {
		f.Close() // releases the lock
		d.mu.Unlock()
	}
}

func (d *Drawer) zoom(on bool) error {
	p, err := d.A.T.Pane(d.UI)
	if err != nil {
		return err
	}
	if p.Zoomed != on {
		_, err = d.A.T.Run("resize-pane", "-Z", "-t", d.UI)
	}
	return err
}

// Open pulls the agent pane into the drawer. Switching from another agent or opening from the
// closed state is one chained tmux command: the dashboard never flashes through a full-width
// layout in between. focus moves the keyboard to the agent.
func (d *Drawer) Open(pane string, focus bool) error {
	defer d.lock()()
	l, err := d.read()
	if err != nil {
		return err
	}
	if l.side != nil && l.side.ID == pane {
		if focus {
			d.A.T.Run("select-pane", "-t", pane)
		}
		return d.zoom(false)
	}
	if l.side == nil {
		if err := d.newSlot(d.slotKind()); err != nil {
			return err
		}
		if l, err = d.read(); err != nil {
			return err
		}
	}
	if l.side.Role != "slot" && d.homeSlot(*l.side, l) == "" {
		// The agent that is out has no placeholder at home (its window was closed): close the
		// slow way first, then open normally.
		if err := d.close(l); err != nil {
			return err
		}
		if l, err = d.read(); err != nil {
			return err
		}
	}
	width, _ := d.A.T.Run("show-options", "-wqv", "-t", d.UI, "@stackk_drawer_w")
	if width == "" && d.Canvas != "" { // the placeholder is the narrower RUNNING column
		if pct, err := strconv.Atoi(strings.TrimSuffix(drawerWidth, "%")); err == nil {
			width = strconv.Itoa(l.ui.WinWidth * pct / 100)
		}
	}
	var cmds [][]string
	if l.ui.Zoomed {
		cmds = append(cmds, []string{"resize-pane", "-Z", "-t", d.UI})
	}
	slot := l.side.ID
	if l.side.SlotKind == "side" { // remember the RUNNING column's width for the way back
		cmds = append(cmds, []string{"set-option", "-w", "-t", d.UI, "@stackk_side_w", l.side.Width})
	}
	if l.side.Role != "slot" {
		// Another agent is out: send it home by swapping with the placeholder in its home window,
		// which lands that placeholder in the drawer for the incoming agent.
		home := d.homeSlot(*l.side, l)
		width = l.side.Width
		cmds = append(cmds, []string{"set-option", "-w", "-t", d.UI, "@stackk_drawer_w", width},
			[]string{"swap-pane", "-d", "-s", l.side.ID, "-t", home})
		slot = home
	}
	cmds = append(cmds, []string{"swap-pane", "-d", "-s", pane, "-t", slot})
	if n, err := strconv.Atoi(width); err == nil && n > 20 {
		cmds = append(cmds, []string{"resize-pane", "-t", pane, "-x", width})
	}
	if focus {
		cmds = append(cmds, []string{"select-pane", "-t", pane})
	}
	return d.A.T.Chain(cmds...)
}

// Close returns the pulled-out agent to its home window and gives the TUI the full width.
func (d *Drawer) Close() error {
	defer d.lock()()
	l, err := d.read()
	if err != nil {
		return err
	}
	return d.close(l)
}

func (d *Drawer) close(l layout) error {
	if l.side == nil || l.side.Role == "slot" {
		return d.zoom(d.Canvas == "")
	}
	zoom := []string{"resize-pane", "-Z", "-t", d.UI}
	save := []string{"set-option", "-w", "-t", d.UI, "@stackk_drawer_w", l.side.Width}
	if home := d.homeSlot(*l.side, l); home != "" {
		cmds := [][]string{save, {"swap-pane", "-d", "-s", l.side.ID, "-t", home}}
		if d.Canvas != "" { // the placeholder, now here, is the RUNNING column again, as wide as before
			side, _ := d.A.T.Run("show-options", "-wqv", "-t", d.UI, "@stackk_side_w")
			if n, err := strconv.Atoi(side); err != nil || n < 20 {
				side = strconv.Itoa(l.ui.WinWidth * sidePct / 100)
			}
			cmds = append(cmds, []string{"resize-pane", "-t", home, "-x", side})
		} else if !l.ui.Zoomed {
			cmds = append(cmds, zoom)
		}
		return d.A.T.Chain(cmds...)
	}
	// No placeholder waiting at home (its window was closed): give the agent a new window.
	d.A.T.Run("set-option", "-w", "-t", d.UI, "@stackk_drawer_w", l.side.Width)
	if _, err := d.A.T.Run("break-pane", "-d", "-s", l.side.ID, "-t", d.Stack+":"); err != nil {
		return err
	}
	d.newSlot(d.slotKind())
	return d.zoom(d.Canvas == "")
}

// homeSlot is this stack's placeholder waiting in the agent's home window, if any.
func (d *Drawer) homeSlot(agent tmux.Pane, l layout) string {
	for _, s := range l.slots {
		if s.WindowID != agent.WindowID && s.WindowID != l.ui.WindowID {
			return s.ID
		}
	}
	return ""
}

// sendHome moves an agent pane back into the stack session: swapping with this stack's
// placeholder waiting in its home window, or into a new window when there is none.
func (d *Drawer) sendHome(agent tmux.Pane, l layout) error {
	if home := d.homeSlot(agent, l); home != "" {
		_, err := d.A.T.Run("swap-pane", "-d", "-s", agent.ID, "-t", home)
		return err
	}
	_, err := d.A.T.Run("break-pane", "-d", "-s", agent.ID, "-t", d.Stack+":")
	return err
}

// Quit puts everything back: agent home, placeholder gone, the window as the user left it.
func (d *Drawer) Quit() error {
	d.Close()
	defer d.lock()()
	d.quit = true
	l, err := d.read()
	if err != nil {
		return err
	}
	d.zoom(false)
	for _, s := range append(l.slots, l.canvases...) {
		d.A.T.Run("kill-pane", "-t", s.ID)
	}
	d.A.T.Run("set-option", "-wu", "-t", d.UI, "@stackk_ui")
	return nil
}

// recoverStale handles windows left behind by a crashed TUI of this stack: a pulled-out agent
// goes back to the stack session; the leftover placeholder is cleaned up by Ensure. It reports
// whether it changed anything.
func (d *Drawer) recoverStale(l layout) (changed bool) {
	liveTUI := map[string]bool{}
	for _, p := range l.panes {
		if p.Role == "tui" && !p.Dead {
			liveTUI[p.WindowID] = true
		}
	}
	for _, p := range l.panes {
		if !p.UIWindow || p.WindowID == l.ui.WindowID || liveTUI[p.WindowID] {
			continue
		}
		if p.Task != "" {
			if task, err := store.GetTask(d.A.DB, p.Task); err == nil && task.Stack == d.Stack {
				d.sendHome(p, l)
				if l, err = d.read(); err != nil {
					return true
				}
			}
		}
		d.A.T.Run("set-option", "-wu", "-t", p.WindowID, "@stackk_ui")
		changed = true
	}
	return changed
}

// Recall brings the agent out of the drawer back into the window of the given placeholder pane,
// for when you reach that window through tmux instead of the dashboard.
func (a *App) Recall(slotPane string) error {
	slot, err := a.T.Pane(slotPane)
	if err != nil || slot.Role != "slot" {
		return err
	}
	ui, ok, err := a.dashboard(slot.SlotOf, false)
	if err != nil || !ok {
		return err
	}
	d := a.DrawerOf(ui, ui.OfStack)
	agent := d.Out()
	if err := d.Close(); err != nil {
		return err
	}
	if t, err := store.GetTask(a.DB, agent); err == nil && t.TmuxPane != "" {
		a.T.Run("select-pane", "-t", t.TmuxPane)
	}
	return nil
}

// dashboard finds a live dashboard pane showing the stack; with anyStack, an empty stack
// matches any dashboard.
func (a *App) dashboard(stack string, anyStack bool) (tmux.Pane, bool, error) {
	panes, err := a.T.AllPanes()
	if err != nil {
		return tmux.Pane{}, false, err
	}
	for _, p := range panes {
		if p.Role == "tui" && !p.Dead && (p.OfStack == stack || anyStack && stack == "") {
			return p, true, nil
		}
	}
	return tmux.Pane{}, false, nil
}

// Focus brings the user to the dashboard of the task's stack (or any dashboard) and, given a
// task, opens it in the drawer. Used when a notification is clicked.
func (a *App) Focus(taskID string) error {
	var task store.Task
	stack := ""
	if taskID != "" {
		t, err := store.GetTask(a.DB, taskID)
		if err != nil {
			return err
		}
		task, stack = t, t.Stack
	}
	ui, ok, err := a.dashboard(stack, true)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no stackk dashboard is running")
	}
	// The most recently active client is the one in front of you.
	rows, _ := a.T.Fields("list-clients", "-F", tmux.F("client_name", "client_activity"))
	client, best := "", ""
	for _, r := range rows {
		if len(r) == 2 && r[1] > best {
			client, best = r[0], r[1]
		}
	}
	if client != "" {
		a.T.Run("switch-client", "-c", client, "-t", ui.SessionID)
	}
	a.T.Run("select-window", "-t", ui.WindowID)
	a.T.Run("select-pane", "-t", ui.ID)
	if task.TmuxPane == "" {
		return nil
	}
	d := a.DrawerOf(ui, task.Stack)
	return d.Open(task.TmuxPane, true)
}

// DrawerOf is the drawer of a running dashboard, as that dashboard set it up.
func (a *App) DrawerOf(ui tmux.Pane, stack string) *Drawer {
	canvas, _ := a.T.Run("show-options", "-wqv", "-t", ui.ID, "@stackk_canvas")
	return &Drawer{A: a, UI: ui.ID, Stack: stack, Canvas: canvas}
}
