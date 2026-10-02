#!/usr/bin/env bash
# Hostile text from an agent must not reach the terminal as control sequences; stackk's state
# must stay private. (The attack in step 1 worked before the fix: it retitled the dashboard.)
source "$(dirname "$0")/lib.sh"
mkdir -p "$S/work/x"; sk new -stack work -name x "$S/work/x" >/dev/null; sleep 3
P=$(paneof x)
TITLE0=$(t display -p -t "$(tuipane)" '#{pane_title}')

step "an agent's reply carries terminal escapes (title, clipboard, colours, bidi)"
printf '{"hook_event_name":"Stop","last_assistant_message":"done \\u001b]2;PWNED-TITLE\\u0007 \\u001b]52;c;ZXZpbA==\\u0007 \\u001b[41mRED\\u001b[0m \\u202Eevil"}' \
  | TMUX_PANE=$P "$S/bin/stackk" mark claude
sleep 3
check "the dashboard's title is untouched" '[ "$(t display -p -t "$(tuipane)" "#{pane_title}")" = "$TITLE0" ]'
check "no control characters were stored" '[ "$(sqlite3 "$STACKK_DB" "select count(*) from tasks where instr(last_msg, char(27)) or instr(last_msg, char(7)) or instr(last_msg, char(8238))")" = 0 ]'
check "the reply is still shown, as plain text" 'hv | grep -q "done ]2;PWNED-TITLE"'

step "a window renamed with escapes"
t rename-window -t "$(sk ls -json | python3 -c 'import json,sys; print([t["HomeWindow"] for t in json.load(sys.stdin) if t["Name"]=="x"][0])')" "$(printf 'x\033]2;PWNED2\007 p')"; sleep 7
check "the task name stays clean" '! sqlite3 "$STACKK_DB" "select name from tasks" | grep -q "$(printf "\033")" && [ "$(t display -p -t "$(tuipane)" "#{pane_title}")" = "$TITLE0" ]'

step "stackk's state is private to you"
check "state directory is 0700" '[ "$(stat -f %Lp "$(dirname "$STACKK_DB")")" = 700 ]'

echo
if [ $fails -eq 0 ]; then echo "all passed ($n steps)"; else echo "$fails failed"; fi
t kill-server
exit $fails
