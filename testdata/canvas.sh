#!/usr/bin/env bash
# The canvas: a program of your choice (an ebook reader) in the STACK box's place while nothing
# waits for you. Tasks coming back push it aside, still running; RUNNING stays beside it.
CFG=$(mktemp -d)
cat > "$CFG/canvas.sh" <<'SH'
#!/bin/sh
while :; do printf '\033[H\033[2JCANVAS-READY %s\n' "$(tput lines)"; sleep 0.5; done
SH
chmod +x "$CFG/canvas.sh"
printf '{"canvas": "%s"}' "$CFG/canvas.sh" > "$CFG/config.json"
export STACKK_CONFIG=$CFG/config.json
source "$(dirname "$0")/lib.sh"
canvas() { t list-panes -a -F '#{pane_id} #{@stackk_role}' | awk '$2=="canvas"{print $1}'; }
slotp() { t list-panes -a -F '#{pane_id} #{@stackk_role}' | awk '$2=="slot"{print $1}'; }
npanes() { t list-panes -t "$(tuipane)" | wc -l | tr -d " "; }
cheight() { t display -p -t "$(canvas)" '#{pane_height}'; }
inui() { [ "$(t display -p -t "$(canvas)" '#{window_id}')" = "$(t display -p -t "$(tuipane)" '#{window_id}')" ]; }

step "an empty stack: the canvas takes the STACK box's place"
check "the canvas runs in the stack column" 'inui && hv | grep -q CANVAS-READY && ! hv | grep -q "─ STACK"'
check "it fills the column under the header" '[ "$(cheight)" -ge 44 ]'
check "RUNNING and SCHEDULED stay beside it, full height" '[ "$(npanes)" = 3 ] && [ "$(t display -p -t "$(slotp)" "#{pane_height}")" -ge 45 ] && hv | grep -q RUNNING && hv | grep -q SCHEDULED'
check "the canvas is as wide as the stack column, nothing zoomed" '[ "$(t display -p -t "$(canvas)" "#{pane_width}")" = "$(t display -p -t "$(tuipane)" "#{pane_width}")" ] && [ "$(t display -p -t "$(tuipane)" "#{window_zoomed_flag}")" = 0 ]'
C0=$(canvas)

step "a running task does not disturb it"
sk new -stack work -name delta "$S/work/delta" "busy" >/dev/null
check "delta shows in the RUNNING column" '[ "$(viewof delta)" = running ] && t capture-pane -p -t "$(slotp)" | grep -q " delta "'
check "the canvas stays" 'inui && [ "$(canvas)" = "$C0" ]'

step "a task comes back to the stack: the canvas steps aside"
sk new -stack work -name alpha "$S/work/alpha" >/dev/null
check "alpha is on the stack, in the STACK box" '[ "$(viewof alpha)" = stack ] && hv | grep -q "─ STACK" && hv | grep -q " alpha "'
check "the canvas waits outside, still running" '! inui && [ "$(canvas)" = "$C0" ] && t capture-pane -p -t "$(canvas)" | grep -q CANVAS-READY'
check "out of your window list, at the same size" '[ "$(t display -p -t "$(canvas)" "#{session_name}")" = _stackk ] && [ "$(cheight)" -ge 44 ] && ! t list-windows -t work -F "#{window_name}" | grep -q canvas'

step "▤ canvas shows it anyway, until another task comes back"
hclickon "▤ canvas"; sleep 2
check "the canvas is back in the STACK box's place" 'inui && [ "$(canvas)" = "$C0" ] && ! hv | grep -q "─ STACK"'
check "the header says what waits" 'hv | head -1 | grep -q "● 1 waiting"'
sk new -stack work -name beta "$S/work/beta" >/dev/null
check "beta comes back: the canvas steps aside for the list" '[ "$(viewof beta)" = stack ] && ! inui && hv | grep -q " beta "'

step "open it and close it"
clicktask alpha; sleep 2
check "alpha is in the drawer, full height" '[ "$(drawer)" = alpha ] && [ "$(t display -p -t "$(paneof alpha)" "#{pane_height}")" -ge 45 ]'
check "the RUNNING column holds alpha's window meanwhile" 't capture-pane -p -t "$(slotp)" | grep -q "is open in the stackk dashboard"'
hclickon "close ▶"; sleep 1.5
check "closed: RUNNING is back beside the stack" '[ -z "$(drawer)" ] && [ "$(npanes)" = 2 ] && hv | grep -q RUNNING'

step "the stack empties again: the canvas is back, same program"
sk pause "$(idof alpha)" >/dev/null; sk pause "$(idof beta)" >/dev/null; sleep 2
check "back in the STACK box's place" 'inui && [ "$(canvas)" = "$C0" ] && [ "$(cheight)" -ge 44 ] && hv | grep -q CANVAS-READY'

step "click a task in the RUNNING column"
SW=$(t display -p -t "$(slotp)" "#{pane_width}")
clicktask delta; sleep 2
check "delta opens in the drawer, the canvas stays under the list" '[ "$(drawer)" = delta ] && inui'
hclickon "close ▶"; sleep 1.5
check "closed again, the RUNNING column as wide as before" '[ -z "$(drawer)" ] && [ "$(npanes)" = 3 ] && [ "$(t display -p -t "$(slotp)" "#{pane_width}")" = "$SW" ]'

step "a modal needs the whole column: the canvas steps aside and comes back"
hclickon "⌕ find"; sleep 2
check "out of the way while find is open" '! inui && [ "$(canvas)" = "$C0" ] && hv | grep -q "find session"'
t send-keys -t host Escape; sleep 2
check "back after the modal" 'inui && [ "$(canvas)" = "$C0" ] && hv | grep -q CANVAS-READY'

step "▤ canvas hides it and brings it back"
hclickon "▤ canvas"; sleep 2
check "hidden: the dashboard has the window to itself again" '[ -z "$(canvas)" ] && [ "$(t display -p -t "$(tuipane)" "#{window_zoomed_flag}")" = 1 ] && hv | grep -q "─ STACK"'
check "with a plain placeholder" '[ -z "$(t display -p -t "$(slotp)" "#{@stackk_slotkind}")" ]'
hclickon "▤ canvas"; sleep 2
check "shown again" 'inui && hv | grep -q CANVAS-READY'

step "stackk canvas off / stackk canvas <command> change it while the dashboard runs"
sk canvas off >/dev/null; sleep 7
check "off" '[ -z "$(canvas)" ] && [ "$(sk canvas)" = "no canvas; set one with: stackk canvas <command>" ]'
sk canvas "$CFG/canvas.sh" >/dev/null; sleep 7
check "on" 'inui && hv | grep -q CANVAS-READY'

step "with no canvas set, the button asks for a command"
sk canvas off >/dev/null; sleep 7
hclickon "▤ canvas"; sleep 1.5
check "the Canvas modal opens" 'hv | grep -q "a program to show while the stack is empty"'
htype "$CFG/canvas.sh"; sleep 3
check "the canvas runs and is saved to the config" 'inui && hv | grep -q CANVAS-READY && [ "$(sk canvas)" = "$CFG/canvas.sh" ]'

step "quit"
hclick 10 1; sleep 1; t send-keys -t host q; sleep 2
check "quitting removes the canvas" '[ -z "$(tuipane)" ] && [ -z "$(canvas)" ]'

echo
if [ $fails -eq 0 ]; then echo "all passed ($n steps)"; else echo "$fails failed"; fi
t kill-server
exit $fails
