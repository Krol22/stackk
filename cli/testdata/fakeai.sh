#!/usr/bin/env bash
# Stand-in for `claude -p` in tests: reads the ranking prompt on stdin and answers like the model
# would for the demo query, after a short pause so the "asking" state is visible.
prompt=$(cat)
sleep "${FAKEAI_DELAY:-2}"
pick() { printf '%s\n' "$prompt" | grep -nE "^[0-9]+\. .*($1)" | head -1 | sed -E 's/^[0-9]+:([0-9]+)\..*/\1/'; }
ids=()
IFS='|' read -ra pats <<< "${FAKEAI_PICK:-Mondial|tracking}"
for pat in "${pats[@]}"; do n=$(pick "$pat"); [ -n "$n" ] && ids+=("$n"); done
( IFS=,; echo "[${ids[*]}]" )
