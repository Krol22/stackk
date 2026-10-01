#!/usr/bin/env bash
# Runs the e2e suite in demo mode and attaches to the test tmux server so it can be watched/recorded.
cd "$(dirname "$0")/.."
export STACKK_DEMO=1 STACKK_QUIET_SECS=${STACKK_QUIET_SECS:-6}
export STACKK_IT=${STACKK_IT:-$(mktemp -d)/it}
bash testdata/it.sh > "$STACKK_IT.log" 2>&1 &
source testdata/env.sh
until t has-session -t host 2>/dev/null; do sleep 0.2; done
t attach -t host
wait
clear
grep -E '^(ok|FAIL|all passed|[0-9]+ failed)' "$STACKK_IT.log"
echo DEMO-KONIEC
