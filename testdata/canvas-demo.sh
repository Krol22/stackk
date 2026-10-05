#!/usr/bin/env bash
# Demo of the canvas with a real ebook reader (bookokrat), for recording:
#   testdata/demo.sh testdata/canvas-demo.sh   (vhs testdata/canvas.tape)
BOOK=${STACKK_DEMO_BOOK:-$HOME/Downloads/alice-rackham.epub}
CFG=$(mktemp -d)
printf '{"canvas": "bookokrat --test-mode --zen-mode %s"}' "$BOOK" > "$CFG/config.json"
export STACKK_CONFIG=$CFG/config.json
source "$(dirname "$0")/lib.sh"
canvas() { t list-panes -a -F '#{pane_id} #{@stackk_role}' | awk '$2=="canvas"{print $1}'; }
read_on() { local i; for i in 1 2 3; do t send-keys -t "$(canvas)" PageDown; pause 1.2; done; }

step "nothing waits for you: the book takes the STACK box's place"
sk new -stack work -name delta "$S/work/delta" "busy" >/dev/null
pause 3; read_on

step "a session comes back: the book steps aside, still open"
sk new -stack work -name alpha "$S/work/alpha" >/dev/null
pause 4

step "▤ canvas: read on anyway, the header says what waits"
hclickon "▤ canvas"; pause 3; read_on

step "next ▶ opens the waiting session"
hclickon "next ▶"; pause 3
hclickon "close ▶"; pause 2

step "pause it: the stack is empty, the book is back on the same page"
sk pause "$(idof alpha)" >/dev/null; pause 4

step "click a running session: it opens in the drawer"
clicktask delta; pause 3
hclickon "close ▶"; pause 3

echo "all passed ($n steps)"
t kill-server
