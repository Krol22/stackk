// stackk: reports omp session events to `stackk mark omp` using Claude Code's hook payload shape.
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent"
import { execFileSync } from "node:child_process"

// Written by `stackk setup`, which fills in the path of the stackk binary.
const bin = process.env.STACKK_BIN || "__STACKK_BIN__"

function text(value: unknown): string {
  if (typeof value === "string") return value
  if (Array.isArray(value)) return value.map(text).filter(Boolean).join(" ")
  if (value && typeof value === "object") {
    const v = value as Record<string, unknown>
    if (typeof v.text === "string") return v.text
    return text(v.content ?? (v.message as Record<string, unknown> | undefined)?.content)
  }
  return ""
}

function call<T>(fn: (() => T) | undefined): T | undefined {
  try {
    return fn?.()
  } catch {
    return undefined
  }
}

// Synchronous on purpose: events must reach stackk in the order omp emits them.
function send(hookEventName: string, ctx: any, extra: Record<string, unknown> = {}) {
  if (!process.env.TMUX_PANE) return
  const sm = ctx?.sessionManager
  const payload = {
    hook_event_name: hookEventName,
    session_id: call(() => sm?.getSessionId?.()) ?? "",
    transcript_path: call(() => sm?.getSessionFile?.()) ?? "",
    cwd: ctx?.cwd ?? process.cwd(),
    ...extra,
  }
  try {
    execFileSync(bin, ["mark", "omp"], { input: JSON.stringify(payload), timeout: 2000, stdio: ["pipe", "ignore", "ignore"] })
  } catch {
    // stackk missing or slow: never disturb the omp turn.
  }
}

export default function stackk(omp: ExtensionAPI): void {
  omp.on("session_start", (_e: any, ctx: any) => send("SessionStart", ctx, { source: "startup" }))
  omp.on("session_switch", (_e: any, ctx: any) => send("SessionStart", ctx, { source: "resume" }))
  omp.on("before_agent_start", (e: any, ctx: any) => send("UserPromptSubmit", ctx, { prompt: text(e?.prompt) }))
  omp.on("session_stop", (e: any, ctx: any) => send("Stop", ctx, { last_assistant_message: text(e?.last_assistant_message) }))
  omp.on("tool_approval_requested", (e: any, ctx: any) =>
    send("PermissionRequest", ctx, { tool_name: e?.toolName, tool_input: e?.input ?? e?.args ?? {}, tool_use_id: e?.toolCallId }),
  )
  omp.on("tool_approval_resolved", (e: any, ctx: any) =>
    send("PostToolUse", ctx, { tool_name: e?.toolName, tool_use_id: e?.toolCallId }),
  )
  omp.on("tool_call", (e: any, ctx: any) => {
    if (e?.toolName !== "ask") return
    const question = e?.input?.question ?? e?.input?.questions?.[0]?.question ?? ""
    send("PreToolUse", ctx, { tool_name: "AskUserQuestion", tool_input: { questions: [{ question }] }, tool_use_id: e?.toolCallId })
  })
  omp.on("tool_result", (e: any, ctx: any) =>
    send("PostToolUse", ctx, { tool_name: e?.toolName, tool_input: e?.input ?? {}, tool_use_id: e?.toolCallId }),
  )
  omp.on("session_shutdown", (_e: any, ctx: any) => send("SessionEnd", ctx))
}
