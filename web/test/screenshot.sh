#!/usr/bin/env bash
# README screenshots of the mobile web UI: a believable stack on an isolated tmux server, with
# transcripts written the way Claude Code writes them, shot at iPhone size in headless Chrome.
#   bash web/test/screenshot.sh   → docs/mobile.png
export STACKK_TEST_SOCKET=stackk-mobileshot
WEB=$(cd "$(dirname "$0")/.." && pwd)
ROOT=$(cd "$WEB/.." && pwd)
source "$ROOT/cli/testdata/lib.sh"
(cd "$WEB/server" && go build -o "$S/bin/stackk-web" .) || exit 1
PORT=7981
"$S/bin/stackk-web" -addr 127.0.0.1:$PORT -stackk "$S/bin/stackk" > "$S/web.log" 2>&1 &
trap 'kill %1 2>/dev/null; t kill-server' EXIT
U=http://127.0.0.1:$PORT
mkdir -p "$S/tr"

new() { mkdir -p "$S/work/$1"; sk new -stack work -name "$1" "$S/work/$1" >/dev/null; }
hook() { TMUX_PANE="$(paneof "$1")" "$S/bin/stackk" mark claude; }
# say NAME: a transcript on stdin (JSON lines), then the hook that points the task at it
line() { python3 -c 'import json,sys; print(json.dumps({"type":sys.argv[1],"timestamp":"2026-10-06T07:12:00Z","message":{"content":json.loads(sys.argv[2])}}))' "$1" "$2"; }
user() { line user "$(python3 -c 'import json,sys; print(json.dumps(sys.argv[1]))' "$1")"; }
agent() { line assistant "$(python3 -c 'import json,sys; print(json.dumps([{"type":"text","text":sys.argv[1]}]))' "$1")"; }
tool() { line assistant "$(python3 -c 'import json,sys; print(json.dumps([{"type":"tool_use","id":sys.argv[1],"name":sys.argv[2],"input":json.loads(sys.argv[3])}]))' "$1" "$2" "$3")"
         line user "$(python3 -c 'import json,sys; print(json.dumps([{"type":"tool_result","tool_use_id":sys.argv[1],"content":"ok"}]))' "$1")"; }
event() { python3 -c 'import json,sys; d=json.loads(sys.argv[2]); d["hook_event_name"]=sys.argv[1]; d["session_id"]=sys.argv[3]; d["transcript_path"]=sys.argv[4]; print(json.dumps(d))' "$2" "$3" "$1" "$S/tr/$1.jsonl" | hook "$1"; }

for n in auth-refresh-tokens checkout-retry docs-api-v2 search-ranking migrate-postgres-16 flaky-e2e-login; do new $n; done
sleep 4
# the session starts on the transcript below (a task learns its transcript when it starts)
for n in auth-refresh-tokens checkout-retry docs-api-v2; do touch "$S/tr/$n.jsonl"; event $n SessionStart '{"source":"startup"}'; done

{ user "Add refresh token rotation to the auth service. Old tokens must stop working once used."
  tool a1 Read '{"file_path":"auth/session.go"}'; tool a2 Read '{"file_path":"auth/store.go"}'
  tool a3 Edit '{"file_path":"auth/session.go"}'; tool a4 Edit '{"file_path":"auth/store.go"}'
  tool a5 Bash '{"command":"go test ./auth/..."}'
  agent 'Done. Refresh tokens now **rotate on every use**:

- `POST /auth/refresh` returns a new pair and revokes the old refresh token
- reusing a revoked token revokes the **whole family** (stolen token detection)
- `go test ./auth/...` passes, 14 new cases

Want me to add a `DELETE /auth/sessions/:id` endpoint so users can log out other devices?'
} > "$S/tr/auth-refresh-tokens.jsonl"
event auth-refresh-tokens Stop '{"last_assistant_message":"Done. Refresh tokens now rotate on every use."}'

{ user "The 3rd checkout retry never fires. Find out why and fix it."
  tool c1 Grep '{"pattern":"retryCheckout"}'; tool c2 Read '{"file_path":"web/checkout/retry.ts"}'
  agent 'Found it: `backoff()` returns **seconds**, but `setTimeout` gets them as milliseconds, so attempt 3 is scheduled 1000× too early and lands inside the in-flight lock.

I fixed the unit and want to rebuild with a clean cache to check the bundle.'
} > "$S/tr/checkout-retry.jsonl"
event checkout-retry PermissionRequest '{"tool_name":"Bash","tool_input":{"command":"rm -rf node_modules/.cache && pnpm build"},"tool_use_id":"c3"}'

{ user "Write the v2 API migration guide."
  tool d1 Read '{"file_path":"docs/api/v1.md"}'; tool d2 Write '{"file_path":"docs/api/migrating-to-v2.md"}'
  agent 'The guide covers the **3 breaking changes**: pagination cursors, the `errors[]` envelope and the removed `/users/search`.'
} > "$S/tr/docs-api-v2.jsonl"
event docs-api-v2 PreToolUse '{"tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Which language should the code examples use?","header":"Examples","options":[{"label":"curl","description":"works everywhere, no setup"},{"label":"TypeScript SDK","description":"what most of our users call"},{"label":"Both","description":"tabs, curl first"}]}]},"tool_use_id":"d3"}'

t send-keys -t "$(paneof search-ranking)" -l "busy: try BM25 with a recency boost and compare on the eval set"; t send-keys -t "$(paneof search-ranking)" Enter
t send-keys -t "$(paneof migrate-postgres-16)" -l "busy: run the 0042 migration against a prod-sized snapshot"; t send-keys -t "$(paneof migrate-postgres-16)" Enter
sk pause flaky-e2e-login >/dev/null
sleep 3

ids=$(curl -s $U/api/tasks | python3 -c 'import json,sys; ts={t["name"]:t["id"] for t in json.load(sys.stdin)}; print(ts["auth-refresh-tokens"], ts["checkout-retry"], ts["docs-api-v2"])')
python3 - "$S/phones.html" "$U" $ids <<'PY'
import sys
out, u, auth, chk, docs = sys.argv[1:]
phone = lambda path, label: f'<figure><iframe src="{u}/{path}"></iframe><figcaption>{label}</figcaption></figure>'
open(out, "w").write(f"""<!doctype html><meta charset=utf-8><style>
body{{margin:0;background:#5b4a8a;display:flex;gap:40px;padding:48px 56px;font:600 22px -apple-system,system-ui,sans-serif;color:#f2eefc}}
figure{{margin:0;text-align:center}}
iframe{{width:393px;height:852px;border:10px solid #111;border-radius:56px;background:#121212;box-shadow:0 20px 50px rgb(0 0 0/.35)}}
figcaption{{margin-top:20px}}
</style>
{phone("", "your stack")}
{phone("#/t/" + auth, "the conversation")}
{phone("#/t/" + chk, "a permission")}
{phone("#/t/" + docs, "a question")}
""")
PY
"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" --headless=new --disable-gpu --hide-scrollbars \
  --force-device-scale-factor=2 --window-size=1916,1040 --virtual-time-budget=8000 \
  --screenshot="$ROOT/docs/mobile.png" "file://$S/phones.html" 2>/dev/null
echo "docs/mobile.png"
