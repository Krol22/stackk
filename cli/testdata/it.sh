#!/usr/bin/env bash
# End-to-end test on an isolated tmux server (-L stackk-test) with the fake agent.
# A nested tmux client in session "host" plays the user's terminal: clicks and keys go through it,
# so the popup, its buttons and the dashboard are exercised like with a real mouse.
#   testdata/it.sh                 run the checks
#   STACKK_DEMO=1 testdata/it.sh   slower, narrated, waits for a client on session "host" (see demo.sh)
source "$(dirname "$0")/lib.sh"

step "stackk in a normal window — the stack creates itself"
check "UI renders in the current session" 'hv | grep -q STACKK'
check "stack session created automatically" 't has-session -t work && [ "$(t show -v -t work @stackk)" = 1 ]'
check "TUI stays where it was started" '[ "$(t list-panes -a -F "#{@stackk_role} #{session_name}" | grep ^tui)" = "tui scratch" ]'

step "two new sessions: alpha with a prompt, beta without"
sk new -stack work "$S/work/alpha" "write tests" >/dev/null; pause 1
sk new -stack work -name beta "$S/work/beta" >/dev/null; sleep 3
check "stop puts task on stack" '[ "$(state alpha)" = "stack idle live" ]'
check "fresh session without prompt is on stack" '[ "$(state beta)" = "stack idle live" ]'

step "beta asks for a tool permission"
t send-keys -t "$(paneof beta)" ask Enter; sleep 1.5
check "permission request → waiting (?)" '[ "$(state beta)" = "stack waiting live" ]'

step "click alpha → its terminal slides in on the right"
clicktask "alpha"; sleep 2
check "drawer shows alpha, stack stays visible" '[ "$(drawer)" = alpha ] && hv | grep -q " beta "'
check "agent's home window stays in the stack" '[ "$(t list-panes -a -F "#{session_name} #{@stackk_role}" | grep -c "^work slot")" = 1 ]'

step "mouse wheel in the drawer"
for i in 1 2 3; do hclick 150 20 64; done; sleep 0.8
check "wheel scrolls the agent terminal (copy-mode)" '[ "$(t display -p -t "$(paneof alpha)" "#{pane_in_mode}")" = 1 ]'
t send-keys -t "$(paneof alpha)" q; sleep 0.5

step "click beta in the stack → drawer switches"
clicktask "beta"; sleep 2
check "drawer switched to beta" '[ "$(drawer)" = beta ]'
check "alpha went back home" '[ "$(t list-panes -a -F "#{session_name} #{@stackk_task}" | awk "\$2 ~ /^alpha-/ {print \$1}")" = work ]'

step "answer beta in the drawer"
htype y; sleep 1.5
check "approval typed in the drawer resolves the wait" '[ "$(state beta)" = "stack idle live" ]'

step "reach beta's window through tmux → placeholder → Enter"
SLOT=$(t list-panes -a -F '#{pane_id} #{@stackk_role} #{session_name}' | awk '$2=="slot" && $3=="work" {print $1}')
check "placeholder says which task is open in the dashboard" 't capture-pane -p -t "$SLOT" | grep -q "beta is open in the stackk dashboard"'
t send-keys -t "$SLOT" Enter; sleep 2
check "Enter in the placeholder brings beta back to its window" '[ -z "$(drawer)" ] && [ "$(t list-panes -a -F "#{session_name} #{@stackk_task}" | awk "\$2 ~ /^beta-/ {print \$1}")" = work ]'
clicktask "beta"; sleep 2

step "drag the border between the list and the terminal"
UI=$(tuipane); W0=$(t display -p -t "$UI" "#{pane_width}")
BX=$(( W0 + 1 )); BY=20; NX=$(( W0 + 31 ))   # border column (1-based) and where to drop it
t send-keys -t host -l $'\e'"[<0;${BX};${BY}M"
for x in $(seq $(( BX + 5 )) 5 "$NX"); do t send-keys -t host -l $'\e'"[<32;${x};${BY}M"; sleep 0.05; done
t send-keys -t host -l $'\e'"[<0;${NX};${BY}m"; sleep 1
W1=$(t display -p -t "$UI" "#{pane_width}")
check "dragging the border makes the list wider" '[ "$W1" -ge $(( W0 + 25 )) ]'
DW=$(t display -p -t "$(paneof beta)" "#{pane_width}")
clicktask alpha; sleep 2
check "switching tasks keeps the dragged width" '[ "$(drawer)" = alpha ] && [ "$(t display -p -t "$(paneof alpha)" "#{pane_width}")" = "$DW" ]'
hclickon "close ▶"; sleep 1.5; clicktask beta; sleep 2
check "closing and reopening keeps the dragged width" '[ "$(drawer)" = beta ] && [ "$(t display -p -t "$(paneof beta)" "#{pane_width}")" = "$DW" ]'

step "click the dashboard background"
hclick 10 "$(( $(t display -p -t host "#{pane_height}") - 4 ))"; sleep 1.5
check "focus on the background closes the drawer" '[ -z "$(drawer)" ] && [ "$(t display -p -t "$(tuipane)" "#{window_zoomed_flag}")" = 1 ]'

step "right click alpha → menu → Schedule"
clicktask "alpha" 2; sleep 1.2
hclickon "Schedule"; sleep 1.5
check "schedule from the menu" '[ "$(state alpha)" = "deferred idle live" ]'
check "scheduled window gets the \" st\" suffix" '[ "$(winname alpha)" = "alpha st" ]'

step "rename the window by hand: drop \" st\""
t rename-window -t "$(sk ls -json | python3 -c 'import json,sys; print([t["window"] for t in json.load(sys.stdin) if t["name"]=="alpha"][0])')" alpha; sleep 7
check "removing the suffix brings alpha back to the stack" '[ "$(state alpha)" = "stack idle live" ]'
t rename-window -t "$(sk ls -json | python3 -c 'import json,sys; print([t["window"] for t in json.load(sys.stdin) if t["name"]=="alpha"][0])')" "alpha st"; sleep 7
check "adding \" st\" by hand schedules it again" '[ "$(state alpha)" = "deferred idle live" ]'

step "beta works, then goes silent (like after Esc)"
t send-keys -t "$(paneof beta)" hang Enter; sleep 2
check "prompt → running" '[ "$(state beta)" = "running working live" ]'
narrate "#[bg=#e0b44c,fg=#141414,bold] $n · waiting for silence (${STACKK_QUIET_SECS}s in tests, 60s for real) #[default]"
sleep $((STACKK_QUIET_SECS + 8))
check "silence → unknown on stack (⁇)" '[ "$(state beta)" = "stack unknown live" ]'

step "beta finishes: /exit"
t send-keys -t "$(paneof beta)" /exit Enter; sleep 6
check "exit → exited (○)" '[ "$(state beta)" = "exited idle exited" ]'

step "double click the finished beta → resume"
Y=$(row beta); X=$(( $(hcol "$Y" " beta ") + 2 )); hclick "$X" "$Y"; hclick "$X" "$Y"; sleep 3
check "click resumes exited task once and shows it" '[ "$(sk log "$(idof beta)" | grep -c resume)" = 1 ] && [ "$(state beta)" = "stack idle live" ] && [ "$(drawer)" = beta ]'
check "second resume refused" '! sk resume "$(idof beta)"'

step "kill -9 the TUI with the drawer open"
kill -9 "$(t list-panes -a -F '#{@stackk_role} #{pane_pid}' | awk '$1=="tui"{print $2}')"; sleep 2
t new-window -t scratch: -n ui2 "$S/bin/stackk work"; sleep 3
check "restart recovers: agent back in the stack, one TUI, one slot" '[ "$(t list-panes -a -F "#{session_name} #{@stackk_task}" | awk "\$2 ~ /^beta-/ {print \$1}")" = work ] && [ "$(t list-panes -a -F "#{@stackk_role}" | grep -c tui)" = 1 ] && [ "$(t list-panes -a -F "#{@stackk_role}" | grep -c slot)" = 1 ]'

step "⇲ add windows with nothing to add"
hclickon "add windows"; sleep 1.5
check "the modal opens and says so" 'hv | grep -q "no agent windows outside the stack"'
hclickon "done"; sleep 1

step "agent in another session → ⇲ add windows (modal)"
t new-window -d -t scratch: -n gamma -c "$S/work/gamma" claude; sleep 1.5
hclickon "add windows"; sleep 1.5
hclickon "scratch:" "gamma"; sleep 2
hclickon "done"; sleep 5
check "window added from another session is adopted" '[ "$(state gamma)" = "stack unknown live" ]'

step "a window with just a shell is a task too (\$ on the stack)"
mkdir -p "$S/work/omega"
t new-window -d -t work: -n omega -c "$S/work/omega" "bash --norc"; sleep 7
check "shell window adopted onto the stack" '[ "$(state omega)" = "stack shell live" ]'
t send-keys -t "$(paneof omega)" claude Enter; sleep 2
check "starting claude in it puts it on the stack" '[ "$(state omega)" = "stack idle live" ]'
t send-keys -t "$(paneof omega)" hang Enter; sleep 7
check "a hooked agent under a shell stays running (process name ignored)" '[ "$(state omega)" = "running working live" ]'
t send-keys -t "$(paneof omega)" C-c; sleep 0.5; t send-keys -t "$(paneof omega)" claude Enter; sleep 2
t send-keys -t "$(paneof omega)" /exit Enter; sleep 3
check "quitting to the shell keeps the task on the stack" '[ "$(state omega)" = "stack shell live" ] && [ -n "$(paneof omega)" ]'
Y=$(row omega); hclick "$(( $(hcol "$Y" " omega ") + 2 ))" "$Y"; sleep 2
check "clicking a terminal opens the same window (no resume)" '[ "$(drawer)" = omega ] && ! sk log "$(idof omega)" | grep -q resume'
hclickon "close ▶"; sleep 1.5

step "windows named \"… st\" / \"… p\" keep their meaning when adopted"
mkdir -p "$S/work/sigma" "$S/work/pi"
t new-window -d -t work: -n "sigma st" -c "$S/work/sigma" claude
t new-window -d -t work: -n "pi p" -c "$S/work/pi" claude; sleep 7
check "\"sigma st\" adopted as scheduled" '[ "$(state sigma)" = "deferred idle live" ]'
t new-window -d -t work: -n "kappa st st" -c "$S/work/pi" "bash --norc"; sleep 7
check "a doubled suffix is repaired, not doubled again" '[ "$(winname kappa)" = "kappa st" ] && [ "$(state kappa)" = "deferred shell live" ]'
check "\"pi p\" adopted as paused and hidden" '[ "$(state pi)" = "paused idle live" ] && hv | grep -q "paused 1"'

step "+ new session (modal)"
hclickon "+ new"; sleep 1
t send-keys -t host C-u; t send-keys -t host -l "$S/work/delta"; pause 1
hclickon " name " "["; t send-keys -t host -l "delta"; pause 1
hclickon "prompt " "["; t send-keys -t host -l "review the code"; pause 1.5
hclickon "create"; sleep 3
check "form creates a session and shows it" '[ "$(state delta)" = "stack idle live" ] && [ "$(drawer)" = delta ]'

step "right click delta → Pause (hide)"
hclickon "close ▶"; sleep 1.5
clicktask "delta" 2; sleep 1.2
hclickon "Pause"; sleep 2
check "paused: hidden, agent still running, window \"delta p\"" '[ "$(state delta)" = "paused idle live" ] && [ -n "$(paneof delta)" ] && [ "$(winname delta)" = "delta p" ]'

step "⏸ paused → bring delta back"
hclickon "paused 2"; sleep 1.2
hclickon "click a session" ; hclickon "delta "; sleep 2.5
check "delta is back on the stack and open" '[ "$(state delta)" = "stack idle live" ] && [ "$(drawer)" = delta ] && [ "$(winname delta)" = delta ]'

step "close ▶"
hclickon "close ▶"; sleep 1.5
check "close button closes the drawer" '[ -z "$(drawer)" ]'

step "next ▶"
hclickon "next ▶"; sleep 2
check "next opens the first task on the stack" '[ -n "$(drawer)" ]'

step "click background, then q"
hclick 10 "$(( $(t display -p -t host "#{pane_height}") - 4 ))"; sleep 1.5
t send-keys -t host q; sleep 2
check "quit closes the TUI and leaves agents in the stack" '[ -z "$(tuipane)" ] && ! t list-panes -a -F "#{@stackk_role}" | grep -q slot && [ "$(t list-panes -a -F "#{session_name} #{@stackk_task}" | awk "\$2 != \"\" && \$1 != \"work\"" | wc -l | tr -d " ")" = 0 ]'

echo
if [ $fails -eq 0 ]; then summary="all passed ($n steps)"; else summary="$fails failed"; fi
echo "$summary"
narrate "#[bg=#8fcf7a,fg=#141414,bold] done: $summary #[default]"
pause 4
t kill-server
exit $fails
