#!/usr/bin/env bash
# Starts the screenshot scene in the background and attaches to it (used by screenshot.tape).
cd "$(dirname "$0")/.."
bash testdata/screenshot.sh > recordings/screenshot.log 2>&1 &
until tmux -L stackk-shot has-session -t host 2>/dev/null; do sleep 0.2; done
exec env -u TMUX tmux -L stackk-shot attach -t host
