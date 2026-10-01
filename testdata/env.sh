# Isolated test environment: own tmux server, own DB, fake agent first in PATH.
S=${STACKK_IT:-/private/tmp/claude-501/-Users-krol22-Work-stackk/6fc26507-8f9b-4d62-9fa9-7574a19b68a1/scratchpad/it}
export STACKK_DB=$S/test.db STACKK_TMUX_SOCKET=stackk-test STACKK_BIN=$S/bin/stackk STACKK_QUIET_SECS=${STACKK_QUIET_SECS:-8}
export PATH=$S/bin:$PATH
unset TMUX TMUX_PANE
t() { tmux -L stackk-test "$@"; }
sk() { $S/bin/stackk "$@"; }
# click X Y [button]  — SGR mouse press+release into the stackk TUI pane (1-based coords)
click() { local p; p=$(t list-panes -a -F '#{pane_id} #{@stackk_role}' | awk '$2=="tui"{print $1; exit}'); local b=${3:-0};
  t send-keys -t "$p" -l $'\e'"[<${b};${1};${2}M"; t send-keys -t "$p" -l $'\e'"[<${b};${1};${2}m"; }
ui() { t capture-pane -p -t "$(t list-panes -a -F '#{pane_id} #{@stackk_role}' | awk '$2=="tui"{print $1; exit}')"; }
layout() { t list-panes -a -F '#{session_name}:#{window_index} #{window_name} #{pane_id} role=#{@stackk_role} task=#{@stackk_task} ui=#{@stackk_ui} zoom=#{window_zoomed_flag} w=#{pane_width} cmd=#{pane_current_command}'; }
