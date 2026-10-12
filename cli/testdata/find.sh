#!/usr/bin/env bash
# "find session": search past Claude/omp sessions on disk and put one back on the stack.
# Sessions are fake transcripts; the model is testdata/fakeai.sh.
export STACKK_IT=${STACKK_IT:-$(mktemp -d)/it}
export STACKK_CLAUDE_PROJECTS=$STACKK_IT/claude-projects STACKK_OMP_SESSIONS=$STACKK_IT/omp-sessions STACKK_CODEX_SESSIONS=$STACKK_IT/codex-sessions STACKK_INDEX_TMP=1
export STACKK_AI_CMD="$(cd "$(dirname "$0")" && pwd)/fakeai.sh"
[ -n "${STACKK_DEMO:-}" ] && export FAKEAI_DELAY=5   # long enough to see "asking claude…"
source "$(dirname "$0")/lib.sh"
settle() { sleep 6.5; }
ln -sf "$S/bin/claude" "$S/bin/omp"   # the fake agent stands in for omp too

# past sessions, as Claude Code and omp leave them on disk
mk_claude() { # id dir title prompt last age_minutes
  mkdir -p "$S/work/$2" "$STACKK_CLAUDE_PROJECTS/-w-$2"; local f="$STACKK_CLAUDE_PROJECTS/-w-$2/$1.jsonl"
  printf '%s\n' \
    "{\"type\":\"user\",\"sessionId\":\"$1\",\"cwd\":\"$S/work/$2\",\"message\":{\"content\":\"$4\"}}" \
    "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"$5\"}]}}" \
    "{\"type\":\"ai-title\",\"aiTitle\":\"$3\"}" > "$f"
  touch -t "$(date -v-"$6"M +%Y%m%d%H%M.%S)" "$f"
}
mk_omp() { # id dir title prompt age_minutes
  mkdir -p "$S/work/$2" "$STACKK_OMP_SESSIONS/-w-$2"; local f="$STACKK_OMP_SESSIONS/-w-$2/$1.jsonl"
  printf '%s\n' "{\"type\":\"title\",\"title\":\"$3\"}" \
    "{\"type\":\"session\",\"id\":\"$1\",\"cwd\":\"$S/work/$2\"}" \
    "{\"type\":\"message\",\"message\":{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\"$4\"}]}}" > "$f"
  touch -t "$(date -v-"$5"M +%Y%m%d%H%M.%S)" "$f"
}
mk_claude c0ffee01-mondial shop "YUM-4331 Mondial Relay return search fails" "return search on mondial relay fails, start debugging" "Fixed: STAT 24 came from the carrier config" 300
mk_claude c0ffee02-tracking shop "Motoblouz tracking integration issue" "tracking links for motoblouz are broken" "Added tracking_url" 180
mk_claude c0ffee03-snooze panel "Rich panel snooze button" "add a snooze button to the rich panel" "Done, behind a flag" 60
mk_claude c0ffee04-email mail "No email on order confirmation" "customers get no confirmation email" "SMTP creds rotated" 1500
mk_omp 01aa0001-wiki wiki "Wiki migration review" "what did we change in the wiki migration?" 90
mk_omp 01aa0002-tests api "Naprawa licznych błędów w testach" "napraw testy w api" 2000

step "⌕ find opens a search over your past sessions"
hclickon "⌕ find"; sleep 2
check "the modal lists recent sessions" 'hv | grep -q "find session" && hv | grep -q "Rich panel snooze button"'
check "omp sessions are included" 'hv | grep -q "Wiki migration review"'

step "type a keyword: matches show up as you type"
typeslow "mondial"; sleep 1; pause 1
check "only the keyword match is listed" 'hv | grep -A2 "keyword matches" | grep -q "Mondial Relay return search fails" && ! hv | grep -q "Rich panel snooze"'

step "ask in other words → enter asks the AI"
t send-keys -t host C-u
typeslow "zwroty paczek do punktu odbioru się sypią"; sleep 1; pause 1
check "no keyword match for that wording" 'hv | grep -q "0 keyword matches"'
t send-keys -t host Enter; sleep 0.8
check "while it thinks the modal says so" 'hv | grep -q "asking claude"'
sleep "${FAKEAI_DELAY:-2}"; sleep 1
check "AI ranked the Mondial Relay session first" 'hv | grep -A3 "ranked by AI" | tail -n +2 | grep "✨" | head -1 | grep -q "Mondial Relay"'

step "click it → resumed onto the stack and opened"
hclickon "Mondial Relay return search fails"; sleep 3
check "a task resumes that exact conversation" 't capture-pane -p -t "$(paneof yum-4331)" | grep -q "c0ffee01-mondial · resume"'
check "it is open in the drawer" 'drawer | grep -q "^yum-4331"'
settle
check "and sits on the stack" '[ "$(viewof yum-4331)" = stack ]'
hclickon "close ▶"; sleep 1.5

step "finding it again: already on the stack, no duplicate"
hclickon "⌕ find"; sleep 1.5; typeslow "mondial"; sleep 1
check "the result is marked as on the stack" 'hv | grep "Mondial Relay" | grep -q "on the stack"'
hclickon "Mondial Relay return search fails"; sleep 2
check "opens the same task" '[ "$(sk ls | grep -c " yum-4331-")" = 1 ] && drawer | grep -q "^yum-4331"'
hclickon "close ▶"; sleep 1.5

step "removed sessions come back too"
id=$(idof yum-4331); sk rm "$id"; settle
check "removed" '[ -z "$(state yum-4331)" ]'
hclickon "⌕ find"; sleep 1.5; t send-keys -t host -l "mondial"; sleep 1; hclickon "Mondial Relay return search fails"; sleep 3; settle
check "brought back as the same task, resumed" '[ "$(idof yum-4331)" = "$id" ] && [ "$(viewof yum-4331)" = stack ]'
hclickon "close ▶"; sleep 1.5

step "from the CLI: find and attach (omp)"
check "stackk find -json finds the omp session" 'sk find -json wiki | grep -q "01aa0001-wiki"'
sk attach 01aa0001 >/dev/null; sleep 3; settle
check "stackk attach resumes it onto the stack" '[ "$(viewof wiki-migration-review)" = stack ] && t capture-pane -p -t "$(paneof wiki-migration-review)" | grep -q "resume"'

echo
if [ $fails -eq 0 ]; then summary="all passed ($n steps)"; else summary="$fails failed"; fi
echo "$summary"
narrate "#[bg=#8fcf7a,fg=#141414,bold] done: $summary #[default]"
pause 4
t kill-server
exit $fails
