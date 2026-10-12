#!/usr/bin/env bash
# Builds a realistic stack for README screenshots (see testdata/screenshot.tape). Runs on its own
# tmux server so tests can run meanwhile. Phases are paced so the tape can wait for each screen.
export STACKK_TEST_SOCKET=stackk-shot STACKK_DEMO=1
export STACKK_IT=${STACKK_IT:-$(mktemp -d)/it}
export STACKK_CLAUDE_PROJECTS=$STACKK_IT/claude-projects STACKK_OMP_SESSIONS=$STACKK_IT/omp-sessions STACKK_CODEX_SESSIONS=$STACKK_IT/codex-sessions STACKK_INDEX_TMP=1
export STACKK_AI_CMD="$(cd "$(dirname "$0")" && pwd)/fakeai.sh" FAKEAI_DELAY=1 FAKEAI_PICK="Postgres|pool"
source "$(dirname "$0")/lib.sh"
t set -g status off
say() { printf '{"hook_event_name":"Stop","last_assistant_message":"%s"}' "$2" | TMUX_PANE="$(paneof "$1")" "$S/bin/stackk" mark claude; }
new() { mkdir -p "$S/work/$1"; sk new -stack work -name "$1" "$S/work/$1" >/dev/null; }

for n in auth-refresh-tokens checkout-retry flaky-e2e-login docs-api-v2 search-ranking migrate-postgres-16 rich-panel-snooze billing-webhooks; do new "$n"; done
sleep 4
say auth-refresh-tokens "PR #812 is ready: refresh tokens rotate on use. Merge, or add the revoke endpoint first?"; sleep 1.2
say flaky-e2e-login "Found it: the login test races the cookie banner. Fix pushed to the branch, CI is green."; sleep 1.2
t send-keys -t "$(paneof checkout-retry)" ask Enter; sleep 1.2
say docs-api-v2 "Drafted the v2 migration guide. Want examples in curl or the TypeScript SDK?"; sleep 1.2
t send-keys -t "$(paneof migrate-postgres-16)" -l "busy: run the 0042 migration against a prod-sized snapshot and time it"; t send-keys -t "$(paneof migrate-postgres-16)" Enter
t send-keys -t "$(paneof search-ranking)" -l "busy: try BM25 with a recency boost and compare on the eval set"; t send-keys -t "$(paneof search-ranking)" Enter
sleep 2
sk pause "$(idof billing-webhooks)" >/dev/null
for n in rich-panel-snooze; do clicktask "$n" 2; sleep 1; hclickon "Schedule"; sleep 1; done
sqlite3 "$STACKK_DB" "update tasks set note='after design review' where name='rich-panel-snooze'"
sleep 22   # phase 1: the dashboard (tape: screenshot)

mkdir -p "$STACKK_CLAUDE_PROJECTS"
mk() { mkdir -p "$S/work/$2" "$STACKK_CLAUDE_PROJECTS/-w-$2"; local f="$STACKK_CLAUDE_PROJECTS/-w-$2/$1.jsonl"
  printf '%s\n' "{\"type\":\"user\",\"sessionId\":\"$1\",\"cwd\":\"$S/work/$2\",\"message\":{\"content\":\"$4\"}}" "{\"type\":\"ai-title\",\"aiTitle\":\"$3\"}" > "$f"
  touch -t "$(date -v-"$5"M +%Y%m%d%H%M.%S)" "$f"; }
mk 5e5510a1 api "Connection pool exhaustion under load" "requests hang after a deploy, pool exhausted?" 600
mk 5e5510a2 api "Postgres 16 upgrade: pgx v5 driver" "upgrade postgres to 16 and the pgx driver" 2900
mk 5e5510a3 web "Dark mode for the settings page" "add dark mode to settings" 300
mk 5e5510a4 web "Image upload retries on slow networks" "uploads fail on 3g" 1500
mk 5e5510a5 infra "Terraform state drift in staging" "terraform plan shows drift" 4000
hclickon "⌕ find"; sleep 2; typeslow "the db upgrade we did last week"; sleep 1; t send-keys -t host Enter
sleep 20   # phase 2: find with AI results (tape: screenshot)

hclickon "Postgres 16 upgrade"; sleep 3
t send-keys -t "$(paneof postgres-16-upgrade-pgx)" demo Enter
sleep 25   # phase 3: a session open in the drawer (tape: screenshot)
t kill-server
