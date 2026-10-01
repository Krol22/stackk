# Shared setup for the e2e scripts: isolated tmux server (-L stackk-test), fake agent, helpers,
# and a nested tmux client in session "host" playing the user's terminal. Source it from a test.
set -u
cd "$(dirname "${BASH_SOURCE[0]}")/.."
export STACKK_IT=${STACKK_IT:-$(mktemp -d)/it}
mkdir -p "$STACKK_IT/bin" "$STACKK_IT/work"/{alpha,beta,gamma,delta}
go build -o "$STACKK_IT/bin/stackk" ./cmd/stackk && go build -o "$STACKK_IT/bin/claude" ./testdata/fakeagent || exit 1
source testdata/env.sh
DEMO=${STACKK_DEMO:-}

fails=0 n=0
narrate() { [ -n "$DEMO" ] && t set -t scratch status-left "$1" 2>/dev/null; return 0; }
pause() { [ -n "$DEMO" ] && sleep "${1:-2}"; return 0; }
step() { n=$((n + 1)); narrate "#[bg=#e0b44c,fg=#141414,bold] $n · $1 #[default]"; pause 1.5; }
check() { # check "name" <command that must succeed>
  local ok=1 i
  for i in 1 2 3 4 5 6 7 8 9 10; do eval "$2" >/dev/null 2>&1 && { ok=0; break; }; sleep 0.3; done # UI updates are async
  if [ $ok = 0 ]; then
    echo "ok   $1"; narrate "#[bg=#8fcf7a,fg=#141414,bold] ✓ $1 #[default]"
  else
    echo "FAIL $1"; fails=$((fails + 1)); narrate "#[bg=#e27878,fg=#141414,bold] ✗ $1 #[default]"
    [ -n "${DEBUG:-}" ] && { sk ls; sk log | tail -6; hv | head -24; echo "--- status:"; hv | tail -3; layout | grep -v "^host"; }
  fi
  pause 2
}
state() { sk ls | awk -v n="$1" '$2 ~ "^"n"-" {print $4, $5, $6}'; }
paneof() { t list-panes -a -F '#{pane_id} #{@stackk_task}' | awk -v n="$1" '$2 ~ "^"n"-" {print $1}'; }
idof() { sk ls | awk -v n="$1" '$2 ~ "^"n"-" {print $2}'; }
tuipane() { t list-panes -a -F '#{pane_id} #{@stackk_role}' | awk '$2=="tui"{print $1}'; }
drawer() { t list-panes -a -F '#{@stackk_ui} #{@stackk_task}' | awk '$1=="1" && $2!="" {sub(/-[0-9a-f]+$/, "", $2); print $2}'; } # task in the drawer
hv() { t capture-pane -p -t host; }                                     # what the user sees
hline() { hv | grep -n "$1" | head -1 | cut -d: -f1; }
hcol() { hv | sed -n "${1}p" | python3 -c 'import sys; print(sys.stdin.read().find(sys.argv[1]) + 1)' "$2"; }
hclick() { local b=${3:-0}; t send-keys -t host -l $'\e'"[<${b};${1};${2}M"; t send-keys -t host -l $'\e'"[<${b};${1};${2}m"; }
hclickon() { local y; y=$(hline "$1"); hclick "$(( $(hcol "$y" "${2:-$1}") + 1 ))" "$y" "${3:-0}"; } # click TEXT (on the line containing $1)
row() { hv | grep -n " $1 " | head -1 | cut -d: -f1; }
# clicktask NAME [button]: click on the task's name, wherever its row is (left or right column)
clicktask() { local y; y=$(row "$1"); hclick "$(( $(hcol "$y" " $1 ") + 2 ))" "$y" "${2:-0}"; }
winname() { t display -p -t "$(sk ls -json | python3 -c 'import json,sys; print([t["HomeWindow"] for t in json.load(sys.stdin) if t["Name"]==sys.argv[1]][0])' "$1")" '#{window_name}'; }
htype() { # type like a human into whatever has focus (the popup when it is open)
  if [ -n "$DEMO" ]; then for ((i = 0; i < ${#1}; i++)); do t send-keys -t host -l "${1:i:1}"; sleep 0.06; done
  else t send-keys -t host -l "$1"; fi
  t send-keys -t host Enter
}

t kill-server 2>/dev/null; rm -f "$STACKK_DB"*
# Like the user's tmux: fish runs every pane (default-shell), but with an empty fish config so the
# fake agent stays first in PATH.
mkdir -p "$S/xdg"
XDG_CONFIG_HOME="$S/xdg" t -f /dev/null new-session -d -s scratch -x 180 -y 48
t set -g default-shell /opt/homebrew/bin/fish
t set -g mouse on; t set -g base-index 1; t set -g escape-time 0; t set -g focus-events on
t set -t scratch status-left-length 170; t set -t scratch status-right ""; t set -t scratch status-style "bg=#222222,fg=#8a8a8a"
t set -t scratch window-status-format ""; t set -t scratch window-status-current-format ""
t new-window -t scratch: -n ui "sleep 0.5; exec $S/bin/stackk work"
t new-session -d -s host -x 180 -y 50 "env -u TMUX -u TMUX_PANE tmux -L stackk-test attach -t scratch"
t set -t host status off
if [ -n "$DEMO" ]; then
  while [ "$(t list-clients -F '#{session_name}' | grep -c host)" = 0 ]; do sleep 0.2; done
  sleep 1
fi
sleep 2

