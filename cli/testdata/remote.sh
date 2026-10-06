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

sk send alpha "-R"; sleep 3
check "a reply that looks like an option is typed, not run" '[ "$(field alpha last_msg)" = "reply: -R" ]'
sk send alpha "done;;"; sleep 3
check "trailing semicolons arrive" '[ "$(field alpha last_msg)" = "reply: done;;" ]'

step "key: answer a permission prompt"
sk send alpha ask; sleep 2
check "the agent waits for permission" '[ "$(field alpha view) $(field alpha phase)" = "stack waiting" ] && sk show alpha | grep -q "allow Bash"'
check "an answer for a prompt that moved on is refused" 'sk key -if-waiting 12345 alpha y 2>&1 | grep -q "moved on"'
sk key alpha y enter; sleep 3
check "y + enter answered it" '[ "$(field alpha phase)" = idle ]'
check "an unknown key is refused" '! sk key alpha F13 2>/dev/null && sk key alpha F13 2>&1 | grep -q "unknown key"'

step "transcript: the conversation, not the screen"
sk send alpha "hello transcript"; sleep 3
check "your prompt, the reply and the tool it ran" 'sk transcript alpha | grep -q "you: hello transcript" && sk transcript alpha | grep -q "claude: reply: hello transcript" && sk transcript alpha | grep -q "» Read .*main.go ✓"'
check "JSON, the last n" 'sk transcript -json -n 2 alpha | python3 -c "import json,sys; d=json.load(sys.stdin); assert len(d[\"messages\"])==2 and d[\"messages\"][-1][\"role\"]==\"assistant\""'

step "what a waiting agent asks, for a client to answer"
sk send alpha ask; sleep 2
check "a permission: the tool and the full command" 'sk ls -json | python3 -c "import json,sys; w=[t for t in json.load(sys.stdin) if t[\"name\"]==\"alpha\"][0][\"waiting\"]; assert w==dict(tool=\"Bash\", input=\"rm -rf build\"), w"'
sk key alpha y enter; sleep 3
check "answered: nothing is asked any more" 'sk ls -json | python3 -c "import json,sys; assert \"waiting\" not in [t for t in json.load(sys.stdin) if t[\"name\"]==\"alpha\"][0]"'
sk send alpha question; sleep 2
check "a question: its options, in order" 'sk ls -json | python3 -c "import json,sys; q=[t for t in json.load(sys.stdin) if t[\"name\"]==\"alpha\"][0][\"waiting\"][\"questions\"][0]; assert q[\"question\"]==\"Which database?\" and [o[\"label\"] for o in q[\"options\"]]==[\"Postgres\",\"SQLite\"], q"'
sk key alpha 1 enter; sleep 3
check "option 1 picked with key 1" '[ "$(field alpha last_msg)" = "reply: picked 1" ]'

step "upload: a file from the phone, for the agent to read"
UP=$(printf 'PNGDATA' | sk upload alpha '../../a b?.png')
check "saved under stackk's state, name made safe, private" '[ "$(cat "$UP")" = PNGDATA ] && case "$UP" in "$(dirname "$STACKK_DB")"/uploads/*-a_b_.png) true;; *) false;; esac && [ "$(stat -f %Lp "$UP")" = 600 ]'
check "a file over the limit is refused, nothing left behind" '! head -c 26214401 /dev/zero | sk upload alpha big.bin 2>/dev/null && ! ls "$(dirname "$UP")" | grep -q big.bin'

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
