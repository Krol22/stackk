#!/usr/bin/env bash
# The CLI the web UI is built on: see a task's terminal, reply, press keys, by id or by name.
source "$(dirname "$0")/lib.sh"
for x in alpha alpine; do mkdir -p "$S/work/$x"; sk new -stack work -name $x "$S/work/$x" >/dev/null; done; sleep 4
field() { sk ls -json | python3 -c 'import json,sys; print([t[sys.argv[2]] for t in json.load(sys.stdin) if t["name"]==sys.argv[1]][0])' "$1" "$2"; }

step "ls -json is a stable, documented shape"
check "snake_case fields with the view" 'sk ls -json | python3 -c "import json,sys; t=json.load(sys.stdin)[0]; assert {\"id\",\"name\",\"view\",\"phase\",\"agent\",\"last_msg\",\"run_state\"} <= set(t)"'

step "show: what is on the task's terminal"
check "plain text, by name" 'sk show alpha | grep -q "fake claude"'
check "JSON: the task and its lines" 'sk show -json alpha | python3 -c "import json,sys; d=json.load(sys.stdin); assert d[\"task\"][\"name\"]==\"alpha\" and any(\"fake claude\" in l for l in d[\"lines\"])"'
check "flags after the task work too" 'sk show alpha -lines 5 -json | grep -q "\"lines\""'

step "send: a reply arrives exactly as typed"
TRICKY='it'"'"'s a "test" \ $HOME `date` ; echo pwned'
sk send alpha "$TRICKY"; sleep 3
check "the agent got it" '[ "$(field alpha last_msg)" = "reply: $TRICKY" ]'
printf 'from stdin' | sk send alpha; sleep 3
check "text from stdin" '[ "$(field alpha last_msg)" = "reply: from stdin" ]'
sk send alpha "$(printf 'esc \033]2;PWNED\007 bell')"; sleep 3
check "control characters are dropped, not typed" '[ "$(field alpha last_msg)" = "reply: esc ]2;PWNED bell" ]'

step "key: answer a permission prompt"
sk send alpha ask; sleep 2
check "the agent waits for permission" '[ "$(field alpha view) $(field alpha phase)" = "stack waiting" ] && sk show alpha | grep -q "allow Bash"'
sk key alpha y enter; sleep 3
check "y + enter answered it" '[ "$(field alpha phase)" = idle ]'
check "an unknown key is refused" '! sk key alpha F13 2>/dev/null && sk key alpha F13 2>&1 | grep -q "unknown key"'

step "naming a task"
check "an ambiguous name prefix is refused, listing the matches" 'sk show alp 2>&1 | grep -q "matches 2 tasks"'
check "an unknown task is refused" 'sk show nope 2>&1 | grep -q "no task"'
check "an id prefix works" 'sk show "$(idof alpine | cut -c1-8)" | grep -q "fake claude"'
sk send alpine /exit; sleep 3
check "a finished task has no screen" 'sk show alpine 2>&1 | grep -q "not running"'

echo
if [ $fails -eq 0 ]; then echo "all passed ($n steps)"; else echo "$fails failed"; fi
t kill-server
exit $fails
