#!/usr/bin/env bash
# Performance budget: 15 sessions (5 working), one minute of a running dashboard, and the hook.
# Fails when stackk does more work than the budget allows.
export STACKK_IT=${STACKK_IT:-$(mktemp -d)/it}
export STACKK_TRACE=$STACKK_IT.trace STACKK_TRACE_ALL=1
source "$(dirname "$0")/lib.sh"
# 15 sessions: ~60 capture-pane (5 working without hooks: the quiet check), ~60 cheap drawer checks
# (one window, every second), ~25 reconcile. Was 264 before the audit.
BUDGET_TMUX_PER_MIN=${BUDGET_TMUX_PER_MIN:-175}
BUDGET_HOOK_MS=${BUDGET_HOOK_MS:-16}

step "15 sessions, 5 of them working"
for i in $(seq 1 15); do mkdir -p "$S/work/p$i"; sk new -stack work -name "p$i" "$S/work/p$i" >/dev/null; done
sleep 5
for i in 1 2 3 4 5; do t send-keys -t "$(paneof p$i)" busy Enter; done
sleep 6

step "one minute of the dashboard running"
: > "$STACKK_TRACE"
UIPID=$(t list-panes -a -F '#{@stackk_role} #{pane_pid}' | awk '$1=="tui"{print $2}')
cpu0=$(ps -o time= -p "$UIPID" | tr -d ' ')
sleep 60
cpu1=$(ps -o time= -p "$UIPID" | tr -d ' ')
calls=$(wc -l < "$STACKK_TRACE" | tr -d ' ')
echo "tmux calls/min: $calls (budget $BUDGET_TMUX_PER_MIN), dashboard cpu: $cpu0 -> $cpu1"
awk '{print $4}' "$STACKK_TRACE" | sort | uniq -c | sort -rn | head -8
check "dashboard stays within the tmux call budget" '[ "$calls" -le "$BUDGET_TMUX_PER_MIN" ]'

step "hook latency (50 PostToolUse events in a stack pane)"
P=$(paneof p6)
ms() { python3 -c 'import sys,time,subprocess; p,n=sys.argv[1],50; t=time.time()
for _ in range(n): subprocess.run([sys.argv[2],"mark","claude"],input=b"{\"hook_event_name\":\"PostToolUse\",\"tool_name\":\"Read\"}",env={**__import__("os").environ,"TMUX_PANE":p})
print(round((time.time()-t)*1000/n,1))' "$1" "$S/bin/stackk"; }
in_stack=$(STACKK_TRACE= ms "$P")
outside=$(STACKK_TRACE= ms "$(t list-panes -t scratch:0 -F '#{pane_id}' | head -1)")
echo "hook: ${in_stack} ms in a stack, ${outside} ms outside (budget ${BUDGET_HOOK_MS} ms)"
check "hook within budget" 'python3 -c "import sys; sys.exit(0 if $in_stack <= $BUDGET_HOOK_MS else 1)"'

echo
if [ $fails -eq 0 ]; then echo "all passed ($n steps)"; else echo "$fails failed"; fi
t kill-server
exit $fails
