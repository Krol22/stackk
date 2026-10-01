# stackk

A round-robin stack for the AI coding sessions you juggle in tmux.

You run several Claude Code / [omp](https://github.com/can1357/oh-my-pi) sessions at once and hop between them. stackk keeps them in one dashboard, ordered by who has been waiting for you the longest:

- **STACK**: sessions waiting for you (finished a turn, need a permission or an answer), oldest first
- **RUNNING**: agents at work, so you don't have to look
- **SCHEDULED**: work you parked on purpose

Click a session and its real terminal slides in on the right. Answer it, then click the next one. When the agent finishes, the session goes back on the stack by itself, and you get a macOS notification if it's still waiting a couple of minutes later.

## Requirements

- macOS (notifications use `osascript`; the rest works anywhere tmux does)
- tmux 3.3+ with `set -g mouse on` (stackk is driven with the mouse)
- Go 1.24+ to build (newer toolchains download themselves via `GOTOOLCHAIN`)
- Claude Code and/or omp

## Install

```sh
git clone git@github.com:Krol22/stackk.git && cd stackk
make setup   # builds into ~/.local/bin/stackk and runs `stackk setup`
```

`stackk setup` adds stackk's hooks to `~/.claude/settings.json` (it keeps a backup and leaves your other hooks alone) and writes the omp extension to `~/.omp/agent/extensions/stackk.ts`. Sessions that were already running before this don't report anything until you restart them: `/exit`, then `claude -c` / `omp -c`.

## Use

```sh
stackk          # dashboard in the current pane; tasks live in a hidden tmux session "stackk"
stackk main     # or: turn an existing session (all its windows) into the stack
```

- **Left click** a session to open it in the drawer. Clicking the dashboard closes it, and clicking another session switches to it. Drag the border to resize.
- **Right click** (or `…`) for Schedule, Pause, Hide, Skip, Rename, Note, Stop, Close & remove.
- **next ▶** opens the session at the head of the stack.
- **+ new** starts a session in a folder, optionally with a first prompt.
- **⇲ add windows** moves agent windows from your other sessions into the stack.

Window names carry the state, both ways: stackk appends ` st` (scheduled) and ` p` (paused), and renaming a window yourself does the same.

| state | where | meaning |
|---|---|---|
| on the stack | STACK | waiting for you, or nothing stackk can tell (`$` shell, `⁇` silent) |
| running | RUNNING | the agent is working, including subagents in the background |
| scheduled | SCHEDULED | parked by you, visible |
| paused | `⏸` modal | hidden until you bring it back; the agent keeps running |
| hidden | `⏸` modal | not a task at all (notes, an editor) |

CLI: `stackk ls [-json]`, `stackk log [task]`, `stackk new <folder> [prompt]`, `stackk take`, `stackk pause|resume|hide|unhide|rm <task>`.

## How it works

- **Agent hooks report state:** `stackk mark claude|omp` runs on Claude's hook events and from the omp extension. Hooks are synchronous and take about 10 ms; outside a stack they exit after one tmux query.
- **tmux holds the processes:** a stack is a tmux session marked `@stackk`, and each window is a task.
- **SQLite holds the state and log:** `~/.local/state/stackk/stackk.db`.
- **The drawer swaps panes:** it `swap-pane`s the agent's real pane next to the dashboard, so scrolling, selection and paste are plain tmux.
- **Reconcile runs every few seconds.** It picks up moved and new windows, notices dead panes, and flags a silent "working" session as `⁇`.

`DESIGN.md` has the full design, in Polish.

## Tests

```sh
make test   # unit tests
make e2e    # end-to-end on an isolated tmux server (-L stackk-test) with a fake agent
make demo   # record the e2e run with vhs
```
