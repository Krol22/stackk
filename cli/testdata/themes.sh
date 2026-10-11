#!/usr/bin/env bash
# Color themes: picked in the dashboard or with `stackk theme`, saved in the config, applied live.
CFG=$(mktemp -d)
export STACKK_CONFIG=$CFG/config.json
echo '{"canvas": "kept by other versions", "big": 9007199254740993}' > "$STACKK_CONFIG"
source "$(dirname "$0")/lib.sh"
mkdir -p "$S/work/x"; sk new -stack work -name x "$S/work/x" >/dev/null; sleep 3
style() { t show-options -pqv -t "$(tuipane)" window-style; }
colored() { t capture-pane -e -p -t host | grep -qE "$1"; }

step "the default theme keeps your terminal's background"
check "no pane style" '[ -z "$(style)" ] && colored "38;2;224;1[78][09];76"'

step "◑ theme opens the picker; a click applies a theme"
hclickon "◑ theme"; sleep 1.5
check "the picker lists the themes" 'hv | grep -q "catppuccin-latte" && hv | grep -q "tokyo-night"'
hclickon "catppuccin-latte"; sleep 2
check "light theme: the pane gets its background, the text its colors" '[ "$(style)" = "bg=#eff1f5" ] && colored "38;2;223;142;29"'
check "saved, other settings kept" 'grep -q "\"theme\": \"catppuccin-latte\"" "$STACKK_CONFIG" && grep -q canvas "$STACKK_CONFIG"'
hclickon "done"; sleep 1
check "done closes the picker" '! hv | grep -q "click a theme"'

step "stackk theme lists and sets; the dashboard follows"
check "the current one is marked" 'sk theme | grep -q "^\* catppuccin-latte"'
sk theme dracula >/dev/null; sleep 7
check "dracula within a few seconds" '[ "$(style)" = "bg=#282a36" ] && colored "38;2;241;250;140"'
check "an unknown theme is refused" '! sk theme nope 2>/dev/null'
sk theme stackk >/dev/null; sleep 7
check "back to the default: no pane style, no theme in the config" '[ -z "$(style)" ] && ! grep -q theme "$STACKK_CONFIG"'

step "other keys survive exactly, and a broken file is not overwritten"
check "a big number is kept to the digit" 'grep -q "9007199254740993" "$STACKK_CONFIG"'
cp "$STACKK_CONFIG" "$STACKK_CONFIG.good"; echo "{ broken" > "$STACKK_CONFIG"
check "a broken config is refused, left as it was" '! sk theme nord 2>/dev/null && [ "$(cat "$STACKK_CONFIG")" = "{ broken" ]'
mv "$STACKK_CONFIG.good" "$STACKK_CONFIG"

step "agent-tree: titles in the frame's top edge, dialogs keep their last row"
sk theme agent-tree >/dev/null; sleep 7
hclickon "theme ]"; sleep 1.5
check "the picker shows its done button" 'hv | grep -q "\[ done \]"'
t send-keys -t host Escape; sleep 1

step "a narrow window keeps the buttons"
t resize-window -t scratch:1 -x 120 2>/dev/null; t resize-pane -t "$(tuipane)" -x 120; sleep 2
check "next and find are still there" 'hv | grep -q "next ▶" && hv | grep -q "⌕"'
t resize-window -t scratch:1 -x 180 2>/dev/null; sleep 1

step "quitting puts the pane's own style back"
SH=$(t new-window -d -P -F '#{pane_id}' -t scratch: -n shell "bash --norc")
t set-option -p -t "$SH" window-active-style "bg=#101010"
sk theme dracula >/dev/null
t send-keys -t "$SH" "STACKK_CONFIG=$STACKK_CONFIG $S/bin/stackk work2" Enter; sleep 4
check "dracula paints the dashboard's pane" '[ "$(t show-options -pqv -t "$SH" window-active-style)" = "bg=#282a36" ]'
t send-keys -t "$SH" q; sleep 2
check "after q the shell's own style is back" '[ "$(t show-options -pqv -t "$SH" window-active-style)" = "bg=#101010" ] && [ -z "$(t show-options -pqv -t "$SH" window-style)" ]'

echo
if [ $fails -eq 0 ]; then echo "all passed ($n steps)"; else echo "$fails failed"; fi
t kill-server
exit $fails
