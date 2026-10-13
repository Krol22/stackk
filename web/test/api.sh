#!/usr/bin/env bash
# stackk-web end to end: the real server, on the isolated test tmux server with the fake agent,
# driven over HTTP like the phone does.
WEB=$(cd "$(dirname "$0")/.." && pwd)
source "$WEB/../cli/testdata/lib.sh"
(cd "$WEB/server" && go build -o "$S/bin/stackk-web" .) || exit 1
PORT=${STACKK_WEB_PORT:-7979}
"$S/bin/stackk-web" -addr 127.0.0.1:$PORT -stackk "$S/bin/stackk" > "$S/web.log" 2>&1 &
WEBPID=$!
trap 'kill $WEBPID 2>/dev/null' EXIT
U=http://127.0.0.1:$PORT
get() { curl -s "$U$1"; }
post() { curl -s -X POST -H 'Content-Type: application/json' "$U$1" -d "$2"; }
code() { curl -s -o /dev/null -w '%{http_code}' "$@"; }
mkdir -p "$S/work/alpha"; sk new -stack work -name alpha "$S/work/alpha" >/dev/null; sleep 4
ID=$(idof alpha)
field() { get /api/tasks | python3 -c 'import json,sys; print([t.get(sys.argv[2]) for t in json.load(sys.stdin) if t["name"]==sys.argv[1]][0])' "$1" "$2"; }

step "the app and the task list"
check "the app is served" 'get / | grep -q "<div id=\"app\">"'
check "a client-side route gets the app too" 'get /t/whatever | grep -q "<div id=\"app\">"'
check "tasks come from stackk ls -json" '[ "$(field alpha view)" = stack ]'

step "a session: its screen, a reply, a key"
check "the screen" 'get "/api/tasks/$ID/screen?lines=50" | grep -q "fake claude"'
post "/api/tasks/$ID/send" '{"text":"hello from the phone"}' >/dev/null; sleep 3
check "the reply reached the agent" '[ "$(field alpha last_msg)" = "reply: hello from the phone" ]'
post "/api/tasks/$ID/send" '{"text":"ask"}' >/dev/null; sleep 2
check "it asks for permission" '[ "$(field alpha phase)" = waiting ]'
post "/api/tasks/$ID/key" '{"keys":["y","enter"]}' >/dev/null; sleep 3
check "y + enter from the key row answered it" '[ "$(field alpha phase)" = idle ]'
check "a CLI error comes back as JSON" 'post "/api/tasks/$ID/key" "{\"keys\":[\"F13\"]}" | grep -q "unknown key"'

step "the conversation"
check "the transcript as JSON messages" 'get "/api/tasks/$ID/transcript?n=10" | python3 -c "import json,sys; m=json.load(sys.stdin)[\"messages\"]; assert any(x[\"role\"]==\"user\" and x[\"text\"]==\"hello from the phone\" for x in m)"'

step "attachments"
UP=$(curl -s -X POST -H "Content-Type: application/octet-stream" --data-binary "SCREENSHOT" "$U/api/tasks/$ID/upload?name=shot.png" | python3 -c "import json,sys; print(json.load(sys.stdin)[\"path\"])")
check "an upload lands on the Mac and comes back as a path" '[ "$(cat "$UP")" = SCREENSHOT ]'
post "/api/tasks/$ID/send" "{\"text\":\"look (attached: $UP)\"}" >/dev/null; sleep 3
check "the reply carries the path to the agent" '[ "$(field alpha last_msg)" = "reply: look (attached: $UP)" ]'
check "a cross-site upload is refused" '[ "$(code -X POST -H "Content-Type: application/octet-stream" -H "Origin: https://evil.example" --data-binary x "$U/api/tasks/$ID/upload?name=x")" = 403 ]'

step "pause and resume"
post "/api/tasks/$ID/pause" '{}' >/dev/null; sleep 1
check "paused" '[ "$(field alpha view)" = paused ]'
post "/api/tasks/$ID/resume" '{}' >/dev/null; sleep 1
check "back" '[ "$(field alpha view)" != paused ]'

step "answers go to the prompt on screen"
post "/api/tasks/$ID/send" '{"text":"ask"}' >/dev/null; sleep 2
check "a stale answer is refused" 'post "/api/tasks/$ID/key" "{\"keys\":[\"y\",\"enter\"],\"if_waiting\":\"1-00000000\"}" | grep -q "moved on"'
WID=$(field alpha waiting_id)
post "/api/tasks/$ID/key" "{\"keys\":[\"y\",\"enter\"],\"if_waiting\":\"$WID\"}" >/dev/null; sleep 3
check "the current one goes through" '[ "$(field alpha phase)" = idle ]'

step "rename"
post "/api/tasks/$ID/rename" '{"name":"--alpha two"}' >/dev/null; sleep 2
check "renamed, window too, a name that looks like a flag included" 'sk ls -json | grep -q "\"name\": \"--alpha two\"" && [ "$(winname "--alpha two")" = "--alpha two" ]'
post "/api/tasks/$ID/rename" '{"name":"alpha"}' >/dev/null; sleep 2
check "and back" '[ "$(field alpha view)" != "" ]'
check "an empty name is refused" '[ "$(code -X POST -H "Content-Type: application/json" "$U/api/tasks/$ID/rename" -d "{\"name\":\" \"}")" = 400 ]'

step "priority"
post "/api/tasks/$ID/priority" '{}' >/dev/null; sleep 1
check "on" '[ "$(field alpha priority)" = True ]'
post "/api/tasks/$ID/unpriority" '{}' >/dev/null; sleep 1
check "off" '[ "$(field alpha priority)" != True ]'

step "hide, unhide, close & remove"
post "/api/tasks/$ID/hide" '{}' >/dev/null; sleep 1
check "hidden" '[ "$(field alpha view)" = hidden ]'
post "/api/tasks/$ID/unhide" '{}' >/dev/null; sleep 1
check "back" '[ "$(field alpha view)" != hidden ]'
mkdir -p "$S/work/gone"; sk new -stack work -name gone "$S/work/gone" >/dev/null; sleep 3
GONE=$(idof gone); GW=$(sk ls -json | python3 -c 'import json,sys; print([t["window"] for t in json.load(sys.stdin) if t["name"]=="gone"][0])')
post "/api/tasks/$GONE/rm" '{}' >/dev/null; sleep 2
check "removed: off every list, its window closed" '! sk ls -json | grep -q "\"name\": \"gone\"" && ! t list-windows -a -F "#{window_id}" | grep -qx "$GW"'
check "an unknown action is refused" '[ "$(code -X POST -H "Content-Type: application/json" "$U/api/tasks/$ID/explode" -d "{}")" = 404 ]'

step "only you, only from the app"
check "a POST from another site is refused" '[ "$(code -X POST -H "Content-Type: application/json" -H "Origin: https://evil.example" "$U/api/tasks/$ID/send" -d "{\"text\":\"x\"}")" = 403 ]'
check "a form POST (no JSON) is refused" '[ "$(code -X POST "$U/api/tasks/$ID/send" -d "text=x")" = 415 ]'
check "a request addressed to another host is refused (DNS rebinding)" '[ "$(code -H "Host: evil.example:7979" "$U/api/tasks")" = 403 ]'
check "a task id that looks like a flag is refused" '[ "$(code "$U/api/tasks/-json/screen")" = 400 ]'
check "a key that looks like a flag is refused" '[ "$(code -X POST -H "Content-Type: application/json" "$U/api/tasks/$ID/key" -d "{\"keys\":[\"-x\"]}")" = 400 ]'
kill $WEBPID; sleep 0.5
"$S/bin/stackk-web" -addr 127.0.0.1:$PORT -stackk "$S/bin/stackk" -allow you@github > "$S/web.log" 2>&1 &
WEBPID=$!; sleep 1
check "with -allow, a stranger is refused" '[ "$(code -H "X-Forwarded-For: 100.64.0.9" -H "Tailscale-User-Login: stranger@github" "$U/api/tasks")" = 403 ]'
check "…and you are let in" '[ "$(code -H "X-Forwarded-For: 100.64.0.9" -H "Tailscale-User-Login: you@github" "$U/api/tasks")" = 200 ]'

kill $WEBPID; sleep 0.5
"$S/bin/stackk-web" -addr 127.0.0.1:$PORT -stackk "$S/bin/stackk" -devices 100.64.0.10 > "$S/web.log" 2>&1 &
WEBPID=$!; sleep 1
check "with -devices, another tailnet device is refused" '[ "$(code -H "X-Forwarded-For: 100.64.0.11" "$U/api/tasks")" = 403 ]'
check "…the listed one is let in" '[ "$(code -H "X-Forwarded-For: 100.64.0.10" "$U/api/tasks")" = 200 ]'
check "…and so is the Mac itself, on localhost" '[ "$(code "$U/api/tasks")" = 200 ]'

step "a new session, from a folder picked on the phone"
kill $WEBPID; sleep 0.5
mkdir -p "$S/home/code/newone/.git"
HOME="$S/home" "$S/bin/stackk-web" -addr 127.0.0.1:$PORT -stackk "$S/bin/stackk" > "$S/web.log" 2>&1 &
WEBPID=$!; sleep 1
check "the home folder lists its folders, repos marked" 'get "/api/dirs" | python3 -c "import json,sys; d=json.load(sys.stdin); assert [x[\"name\"] for x in d[\"dirs\"]]==[\"code\"], d"'
check "a repo inside it" 'get "/api/dirs?path=$S/home/code" | python3 -c "import json,sys; d=json.load(sys.stdin); assert d[\"dirs\"][0][\"git\"] and d[\"parent\"], d"'
check "nothing outside the home folder" 'get "/api/dirs?path=/etc" | grep -q "outside your home folder"'
NEW=$(post /api/new "{\"cwd\":\"$S/home/code/newone\",\"prompt\":\"hello from the phone\",\"stack\":\"work\"}" | python3 -c "import json,sys; print(json.load(sys.stdin)[\"id\"])"); sleep 4
check "it starts in the stack with its first prompt" '[ "$(sk ls -json | python3 -c "import json,sys; print([t[\"last_msg\"] for t in json.load(sys.stdin) if t[\"id\"]==sys.argv[1]][0])" "$NEW")" = "reply: hello from the phone" ]'
mkdir -p "$S/home/code/fresh"; touch "$S/home/code/fresh/.untrusted"
FRESH=$(post /api/new "{\"cwd\":\"$S/home/code/fresh\",\"stack\":\"work\"}" | python3 -c "import json,sys; print(json.load(sys.stdin)[\"id\"])"); sleep 3
check "a new folder: the app sees Claude asking to trust it" 'get "/api/tasks/$FRESH/transcript" | python3 -c "import json,sys; assert json.load(sys.stdin).get(\"asks_trust\")"'
post "/api/tasks/$FRESH/key" '{"keys":["down","enter"],"if_trust":true}' >/dev/null; sleep 3
check "trust (down, enter) lets the session start" '! get "/api/tasks/$FRESH/transcript" | grep -q asks_trust && [ "$(sk ls -json | python3 -c "import json,sys; print([t[\"run_state\"] for t in json.load(sys.stdin) if t[\"id\"]==sys.argv[1]][0])" "$FRESH")" = live ]'
check "a trust answer once it started is refused" 'post "/api/tasks/$FRESH/key" "{\"keys\":[\"esc\"],\"if_trust\":true}" | grep -q "no longer asks"'
mkdir -p "$S/home/code/nope"; touch "$S/home/code/nope/.untrusted"
NOPE=$(post /api/new "{\"cwd\":\"$S/home/code/nope\",\"stack\":\"work\"}" | python3 -c "import json,sys; print(json.load(sys.stdin)[\"id\"])"); sleep 3
post "/api/tasks/$NOPE/key" '{"keys":["esc"],"if_trust":true}' >/dev/null; sleep 3
check "no, exit (esc) answers the trust prompt" '[ "$(sk ls -json | python3 -c "import json,sys; print([t[\"run_state\"] for t in json.load(sys.stdin) if t[\"id\"]==sys.argv[1]][0])" "$NOPE")" = exited ]'
check "and never outside the home folder" 'post /api/new "{\"cwd\":\"/tmp\",\"stack\":\"work\"}" | grep -q "outside your home folder"'

echo
if [ $fails -eq 0 ]; then echo "all passed ($n steps)"; else echo "$fails failed"; fi
t kill-server
exit $fails
