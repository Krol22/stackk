---
name: stackk
description: Start new Claude Code, omp or Codex sessions on the user's stackk stack (tmux), and check on them. Use when the user asks to spawn, start, kick off or hand off work to another session or agent, run something in parallel in another repo or folder, or asks what another stackk session is doing.
---

# stackk: start and check sessions

The user runs many agent sessions with **stackk**: each is a tmux window on a stack, and a dashboard (and a phone app) shows which ones wait for them. You can start new ones and look at them through the `stackk` CLI.

## Start a session

```bash
stackk new -name <short-kebab-name> [-agent claude|omp|codex] <absolute folder> "<first prompt>"
```

- It prints the new task's id. The session lands on the user's stack (the one you run in), and the user answers its permissions and questions there; you don't drive it.
- **The prompt must stand on its own.** The new session has none of your context: say what to do, where (files, branch, ticket, PR), what done looks like, and what to report back. Pass decisions the user already made.
- **The folder** is the repo root it should work in. If unsure where something lives, browse with `stackk dirs [-json] [folder]` (only the user's home), or ask.
- **The name** is what the user will see in the list: short, kebab-case, about the job (`fix-checkout-retry`, not `session-2`).
- Default agent is `claude`. Use `omp` or `codex` only if the user asks for it.
- Start only the sessions the user asked for. Several independent jobs, several sessions; one job, one session.
- A folder Claude has never opened asks "do you trust this folder?" before it starts: tell the user the session is waiting on that.

## Check on sessions

```bash
stackk ls -json                       # all tasks: id, name, view (stack/running/…), phase, last_msg
stackk transcript -n 20 <task>        # what a session said and did (prompts, replies, tools)
```

A task is its id, a unique id prefix or its name.

## Rules

- Never type into or press keys in a session you didn't start for the user (`stackk send`, `stackk key`): those are the user's conversations. For one you started, only when the user asks.
- Don't pause, hide or remove sessions unless the user asks.
- Tell the user what you started: name, folder, and one line on what it will do.
