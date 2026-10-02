#!/usr/bin/env bash
# Stack lifecycle under realistic conditions: agents started by stackk through fish (with and
# without a first prompt), driven only by their hooks. After every transition we wait longer than
# a reconcile cycle (5 s) so nothing in the background may flip the state.
#   testdata/lifecycle.sh
# Notifications go to a file here, with a short delay, so they can be checked.
export STACKK_IT=${STACKK_IT:-$(mktemp -d)/it}
export STACKK_NOTIFY_AFTER=8 STACKK_NOTIFY_INTERVAL=1 STACKK_NOTIFY_FILE=$STACKK_IT/notify.log
source "$(dirname "$0")/lib.sh"
rm -f "$STACKK_NOTIFY_FILE"

settle() { sleep 6.5; } # > one reconcile cycle
order() { sk ls -json | python3 -c 'import json,sys; ts=[t for t in json.load(sys.stdin) if t["view"]=="stack"]; print(" ".join(t["Name"] for t in sorted(ts, key=lambda t: t["QueuedAt"])))'; }
send() { t send-keys -t "$(paneof "$1")" -l "$2"; t send-keys -t "$(paneof "$1")" Enter; }
# turn_ends TASK MSG: the agent finishes its turn (what Claude's Stop hook reports), process untouched
turn_ends() { printf '{"hook_event_name":"Stop","last_assistant_message":"%s"}' "$2" | TMUX_PANE="$(paneof "$1")" "$S/bin/stackk" mark claude; }

mkdir -p "$S/work"/{l1,l2,l3}
step "sessions started by stackk run as the agent, not as fish"
sk new -stack work -name l1 "$S/work/l1" >/dev/null
sk new -stack work -name l2 "$S/work/l2" "first prompt" >/dev/null
settle
check "no-prompt session: the pane's process is the agent" '[ "$(t display -p -t "$(paneof l1)" "#{pane_current_command}")" = claude ]'
check "prompt session: the pane's process is the agent" '[ "$(t display -p -t "$(paneof l2)" "#{pane_current_command}")" = claude ]'
check "the fake agent runs (not a real claude from PATH)" 't capture-pane -p -t "$(paneof l1)" | grep -q "fake claude"'
check "fresh session waits on the stack" '[ "$(state l1)" = "stack idle live" ]'
check "finished first prompt waits on the stack" '[ "$(state l2)" = "stack idle live" ]'
check "queue order: l1 entered first" '[ "$(order)" = "l1 l2" ]'

step "a first prompt full of shell metacharacters reaches the agent unchanged (fish)"
mkdir -p "$S/work/q1"
TRICKY='it'"'"'s a "test" \ $HOME `date` ; echo pwned'
sk new -stack work -name q1 "$S/work/q1" "$TRICKY" >/dev/null; settle
check "prompt arrived exactly as typed" '[ "$(sk ls -json | python3 -c "import json,sys; print([t[\"LastMsg\"] for t in json.load(sys.stdin) if t[\"Name\"]==\"q1\"][0])")" = "reply: $TRICKY" ]'
sk rm "$(idof q1)" >/dev/null

step "prompt → running, and it stays running across reconcile cycles (and past the quiet threshold)"
send l1 busy; settle
check "working session is in RUNNING" '[ "$(state l1)" = "running working live" ]'
settle
sleep $((STACKK_QUIET_SECS + 2))
check "still RUNNING: an active screen never trips the ⁇ heuristic" '[ "$(state l1)" = "running working live" ]'
check "queue now holds only l2" '[ "$(order)" = "l2" ]'

step "stop → back on the stack, at the end of the queue"
turn_ends l1 "l1 done"; settle
check "finished turn → stack, behind l2" '[ "$(state l1)" = "stack idle live" ] && [ "$(order)" = "l2 l1" ]'
sk new -stack work -name l3 "$S/work/l3" >/dev/null; settle
send l2 "second task"; sleep 3; settle
check "l2 answered and re-queued behind l3" '[ "$(state l2)" = "stack idle live" ] && [ "$(order | awk "{print \$NF}")" = l2 ]'

step "permission request → waiting on the stack; approve → running → stop → stack"
send l3 ask; settle
check "waiting for permission is on the stack (?)" '[ "$(state l3)" = "stack waiting live" ]'
t send-keys -t "$(paneof l3)" y Enter; sleep 2; settle
check "approved and finished → idle on the stack" '[ "$(state l3)" = "stack idle live" ]'

step "repeated events don't reorder the queue"
before=$(order)
echo '{"hook_event_name":"Stop","last_assistant_message":"dup"}' | TMUX_PANE="$(paneof l2)" "$S/bin/stackk" mark claude
settle
check "a duplicate Stop keeps the queue as it was" '[ "$(order)" = "$before" ]'

step "skip → end of the queue; next opens the head"
first=$(order | awk "{print \$1}")
clicktask "$first" 2; sleep 1.2; hclickon "Skip"; sleep 1.5
check "skipped task moved to the end" '[ "$(order | awk "{print \$NF}")" = "$first" ]'
head=$(order | awk "{print \$1}")
hclickon "next ▶"; sleep 2
check "next opens the head of the queue" '[ "$(drawer)" = "$head" ]'
hclick 10 "$(( $(t display -p -t host "#{pane_height}") - 4 ))"; sleep 1.5

step "scheduled while working: stop does not pull it back"
send l3 hang; sleep 2
check "l3 is running" '[ "$(state l3)" = "running working live" ]'
clicktask "l3" 2; sleep 1.2; hclickon "Schedule"; sleep 1.5
turn_ends l3 "done while scheduled"; settle
check "stays scheduled after its turn ends" '[ "$(state l3)" = "deferred idle live" ] && [ "$(winname l3)" = "l3 st" ]'
clicktask "l3" 2; sleep 1.2; hclickon "Back to stack"; sleep 1.5
check "back to stack → at the end of the queue" '[ "$(state l3)" = "stack idle live" ] && [ "$(order | awk "{print \$NF}")" = l3 ] && [ "$(winname l3)" = l3 ]'

step "paused while working: stays hidden through its turn"
send l2 hang; sleep 2
check "l2 is running" '[ "$(state l2)" = "running working live" ]'
clicktask "l2" 2; sleep 1.2; hclickon "Pause"; sleep 1.5
turn_ends l2 "done while paused"; settle
check "stays paused and hidden" '[ "$(state l2)" = "paused idle live" ] && [ "$(winname l2)" = "l2 p" ]'
"$S/bin/stackk" resume "$(idof l2)"; settle
check "resume from the CLI → back on the stack" '[ "$(state l2)" = "stack idle live" ] && [ "$(winname l2)" = l2 ]'

step "old wrong \"shell\" states are repaired from the last hook"
sqlite3 "$STACKK_DB" "UPDATE tasks SET phase='shell' WHERE name='l2'" 2>/dev/null || python3 -c "import sqlite3,os; c=sqlite3.connect(os.environ['STACKK_DB']); c.execute(\"UPDATE tasks SET phase='shell' WHERE name='l2'\"); c.commit()"
settle
check "phase restored from the last Stop" '[ "$(state l2)" = "stack idle live" ] && sk log "$(idof l2)" | grep -q repair'

step "agent quits: pane gone → finished (○); click resumes the same conversation"
sid=$(sk ls -json | python3 -c 'import json,sys; print([t["AgentSessionID"] for t in json.load(sys.stdin) if t["Name"]=="l1"][0])')
send l1 /exit; settle
check "exited agent is finished (its window closed)" '[ "$(state l1)" = "exited idle exited" ]'
Y=$(row l1); hclick "$(( $(hcol "$Y" " l1 ") + 2 ))" "$Y"; sleep 3; settle
check "resume continues the same conversation id" '[ "$(state l1)" = "stack idle live" ] && t capture-pane -p -t "$(paneof l1)" | grep -q "$sid · resume"'

step "quiet heuristic only for silent working sessions"
send l3 hang; sleep $((STACKK_QUIET_SECS + 8))
check "silent working session → ⁇ on the stack" '[ "$(state l3)" = "stack unknown live" ]'
check "idle sessions are left alone" '[ "$(state l2)" = "stack idle live" ]'

step "pause holds: rapid pause/unpause racing reconcile, then 3 full cycles"
id=$(idof l3)
for i in $(seq 1 12); do
  sk pause "$id"; sleep "0.$(( RANDOM % 9 + 1 ))"
  sk resume "$id"; sleep "0.$(( RANDOM % 9 + 1 ))"
done
sk pause "$id"
for c in 1 2 3; do settle; check "still paused after reconcile cycle $c" '[ "$(viewof l3)" = paused ] && [ "$(winname l3)" = "l3 p" ]'; done
sk resume "$id"
for c in 1 2; do settle; check "unpaused stays on the stack (cycle $c)" '[ "$(viewof l3)" = stack ] && [ "$(winname l3)" = l3 ]'; done

step "pause holds in a window stackk never named (new window / older version)"
W3=$(sk ls -json | python3 -c 'import json,sys; print([t["HomeWindow"] for t in json.load(sys.stdin) if t["Name"]=="l3"][0])')
t set-option -wu -t "$W3" @stackk_wname
sk pause "$id"
for c in 1 2; do settle; check "paused with no remembered window name (cycle $c)" '[ "$(viewof l3)" = paused ] && [ "$(winname l3)" = "l3 p" ]'; done
sk resume "$id"; settle

step "pause with a stale home window never renames the dashboard"
UIW=$(t display -p -t "$(tuipane)" "#{window_id}"); UIN=$(t display -p -t "$(tuipane)" "#{window_name}")
sqlite3 "$STACKK_DB" "UPDATE tasks SET home_window='$UIW' WHERE name='l3'"
sk pause "$id"
for c in 1 2; do settle; check "stays paused, dashboard window keeps its name (cycle $c)" '[ "$(viewof l3)" = paused ] && [ "$(t display -p -t "$(tuipane)" "#{window_name}")" = "$UIN" ] && [ "$(winname l3)" = "l3 p" ]'; done
sk resume "$id"; settle

step "pause from the dashboard menu holds too"
clicktask "l3" 2; sleep 1.2; hclickon "Pause"; sleep 0.3
for c in 1 2 3; do settle; check "menu pause still holds (cycle $c)" '[ "$(viewof l3)" = paused ]'; done
sk resume "$id"; settle

step "a rename by hand still works: add \" p\", then remove it"
t rename-window -t "$(sk ls -json | python3 -c 'import json,sys; print([t["HomeWindow"] for t in json.load(sys.stdin) if t["Name"]=="l3"][0])')" "l3 p"; settle
check "typing \" p\" pauses" '[ "$(viewof l3)" = paused ]'
t rename-window -t "$(sk ls -json | python3 -c 'import json,sys; print([t["HomeWindow"] for t in json.load(sys.stdin) if t["Name"]=="l3"][0])')" "l3"; settle
check "removing it unpauses" '[ "$(viewof l3)" = stack ]'

step "subagents: the session stays RUNNING while a background subagent works"
send l3 bgagent; settle
check "turn ended with a subagent in the background → still RUNNING" '[ "$(state l3)" = "running working live" ]'
settle
check "subagent tool use keeps it RUNNING across cycles" '[ "$(state l3)" = "running working live" ]'
send l3 bgdone; settle
check "final turn after the subagent reports back → stack" '[ "$(viewof l3)" = stack ]'
send l3 bgshell; settle
check "only a background shell (dev server) running → stack" '[ "$(viewof l3)" = stack ]'
send l3 subask; settle
check "a subagent asking for permission → stack, waiting (?)" '[ "$(state l3)" = "stack waiting live" ]'
t send-keys -t "$(paneof l3)" y Enter; settle
check "after the approval the turn ends → idle on the stack" '[ "$(state l3)" = "stack idle live" ]'

step "notifications: back on the stack from RUNNING and still waiting after the delay"
away() { t select-window -t scratch:0; }      # you are working in another window
back() { t select-window -t scratch:ui; }     # looking at the dashboard again
: > "$STACKK_NOTIFY_FILE"
send l3 busy; settle
turn_ends l3 "seen"; sleep $((STACKK_NOTIFY_AFTER + 1)); settle
check "no notification while you look at the dashboard" '[ ! -s "$STACKK_NOTIFY_FILE" ]'
send l3 busy; settle
away; turn_ends l3 "l3 finished"; sleep $((STACKK_NOTIFY_AFTER + 1)); settle; back
check "a notification names the session" 'grep -q "“l3” is waiting for you" "$STACKK_NOTIFY_FILE"'
check "exactly one notification for it" '[ "$(grep -c "l3" "$STACKK_NOTIFY_FILE")" = 1 ]'
CLICK=$(sed -n 's/.*| click: //p' "$STACKK_NOTIFY_FILE" | head -1)
check "clicking it runs stackk focus for that session" 'echo "$CLICK" | grep -q "focus'"'"' '"'"'$(idof l3)"'
t select-window -t scratch:0; sleep 0.5
env -i HOME="$HOME" sh -c "$CLICK" 2>/dev/null; sleep 2   # what macOS runs on click
check "the click works from launchd's bare env: dashboard in front, session in the drawer" '[ "$(t display -p -t "$(tuipane)" "#{window_active}")" = 1 ] && [ "$(drawer)" = l3 ]'
hclick 10 "$(( $(t display -p -t host "#{pane_height}") - 4 ))"; sleep 1.5
: > "$STACKK_NOTIFY_FILE"
send l3 busy; settle
turn_ends l3 "back again"; clicktask l3; sleep $((STACKK_NOTIFY_AFTER + 1)); settle
check "no notification when you opened it yourself" '[ ! -s "$STACKK_NOTIFY_FILE" ]'
hclick 10 "$(( $(t display -p -t host "#{pane_height}") - 4 ))"; sleep 1.5
: > "$STACKK_NOTIFY_FILE"
send l3 busy; settle
away; turn_ends l3 "quick"; sleep 1; send l3 busy; sleep $((STACKK_NOTIFY_AFTER + 1)); settle; back
check "no notification when it went back to work within the delay" '[ ! -s "$STACKK_NOTIFY_FILE" ]'
turn_ends l3 "done"; settle

step "hide a window that is not a task (like TODO)"
[ -n "$(drawer)" ] && { hclickon "close ▶"; sleep 1.5; }
mkdir -p "$S/work/notes"; t new-window -d -t work: -n notes -c "$S/work/notes" "bash --norc"; settle
check "the notes window is adopted onto the stack" '[ "$(viewof notes)" = stack ]'
clicktask "notes" 2; sleep 1.2; hclickon "Hide (not a task)"; sleep 1.5
check "hidden: out of every list, window name unchanged" '[ "$(viewof notes)" = hidden ] && [ "$(winname notes)" = notes ] && hv | grep -qE "hidden 1|⏸ 1"'
settle; settle
check "stays hidden and is not adopted again" '[ "$(viewof notes)" = hidden ] && [ "$(sk ls | grep -c " notes-")" = 1 ]'
hclickon "⏸"; sleep 1.2; hclickon "hidden (not tasks)" ; hclickon "notes "; sleep 2
check "shown again from the modal" '[ "$(viewof notes)" = stack ]'

step "Close & remove ends a session for good"
t split-window -d -t "$(paneof l2)" "bash --norc"; sleep 1   # a split in its window must go too
win=$(sk ls -json | python3 -c 'import json,sys; print([t["HomeWindow"] for t in json.load(sys.stdin) if t["Name"]=="l2"][0])')
clicktask "l2" 2; sleep 1.2; hclickon "Close & remove"; sleep 2; settle
check "its window is closed" '! t list-windows -a -F "#{window_id}" | grep -qx "$win"'
check "the task is gone from every list" '[ -z "$(state l2)" ] && ! sk ls -json | grep -q "\"Name\": \"l2\""'
settle
check "nothing gets adopted back" '[ -z "$(state l2)" ] && ! sk ls | grep -q " l2-"'
"$S/bin/stackk" rm "$(idof l1)"; settle
check "stackk rm removes a task from the CLI" '[ -z "$(state l1)" ]'

echo
if [ $fails -eq 0 ]; then echo "all passed ($n steps)"; else echo "$fails failed"; fi
t kill-server
exit $fails
