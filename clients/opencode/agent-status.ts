// OpenCode plugin (OpenCode 2.x, @opencode/plugin): forwards session, tool and
// permission events to agent-status.
// Install globally: copy to ~/.config/opencode/plugins/agent-status.ts
// Override the server with AGENT_STATUS_URL and the shown name with AGENT_STATUS_HOST.
import { Plugin } from "@opencode/plugin"
import { hostname } from "node:os"

const SERVER = process.env.AGENT_STATUS_URL ?? "http://SERVER:7777"

// Filtered on the OpenCode side, so streaming deltas never reach the plugin.
const TYPES = [
  "session.created",
  "session.renamed",
  "session.model.selected",
  "session.deleted",
  "session.status",
  "session.idle",
  "session.inbox.enqueued",
  "session.execution.started",
  "session.execution.succeeded",
  "session.execution.failed",
  "session.execution.interrupted",
  "session.tool.input.started",
  "session.tool.called",
  "session.tool.failed",
  "session.text.ended",
  "permission.asked",
  "permission.replied",
  "form.created",
  "form.replied",
  "form.cancelled",
] as const

const MAX_TEXT = 4000
// Tool input keys worth showing; everything else (file contents, patches) stays local.
const TOOL_KEYS = ["description", "filePath", "file_path", "path", "pattern", "url", "command", "query"]

const cut = (s: unknown) => (typeof s === "string" && s.length > MAX_TEXT ? s.slice(0, MAX_TEXT) : s)

export default Plugin.define({
  id: "agent-status",
  async setup(ctx) {
    const host = process.env.AGENT_STATUS_HOST ?? hostname()
    const controller = new AbortController()
    // session.tool.called carries no tool name; remember it from input.started.
    const toolNames = new Map<string, string>()
    // The subagent tool runs each subagent as a child session; only its
    // session.created names the parent, so repeat that on the child's events.
    const parents = new Map<string, string>()

    const send = (type: string, directory: string | undefined, data: any) =>
      // Fire and forget: a dead server must never slow the session down.
      fetch(`${SERVER}/hook/opencode`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ host, directory: directory ?? ctx.location.directory, type, data }),
        signal: AbortSignal.timeout(2000),
      }).catch(() => {})

    void (async () => {
      for await (const event of ctx.event.subscribe({ types: TYPES, signal: controller.signal })) {
        const e = event as any
        // Some OpenCode versions ignore the types filter and deliver every event,
        // streamed reasoning included; never forward those.
        if (!(TYPES as readonly string[]).includes(e.type)) continue
        const data = { ...(e.data ?? {}) }
        const directory = e.location?.directory ?? data.location?.directory
        const sessionID = data.sessionID ?? data.form?.sessionID
        if (e.type === "session.created" && data.parentID) parents.set(data.sessionID, data.parentID)
        else if (sessionID && data.parentID == null && parents.has(sessionID)) data.parentID = parents.get(sessionID)
        if (e.type === "session.deleted") parents.delete(sessionID)

        switch (e.type) {
          case "session.tool.input.started":
            toolNames.set(data.id, data.name)
            continue // the call itself is reported by session.tool.called
          case "session.tool.called": {
            data.name = toolNames.get(data.id) ?? data.name
            toolNames.delete(data.id)
            const input: Record<string, unknown> = {}
            for (const k of TOOL_KEYS) if (data.input?.[k] != null) input[k] = cut(String(data.input[k]))
            data.input = input
            delete data.native
            break
          }
          case "session.tool.failed":
            toolNames.delete(data.id)
            delete data.content
            delete data.metadata
            delete data.native
            break
          case "session.text.ended":
            data.text = cut(data.text)
            delete data.native
            break
          case "session.inbox.enqueued":
            if (data.item?.type !== "user") continue // synthetic, compaction, move
            data.item = { type: "user", payload: { text: cut(data.item.payload?.text) } }
            break
          case "form.created":
            data.form = { id: data.form?.id, sessionID: data.form?.sessionID, title: data.form?.title }
            break
        }
        send(e.type, directory, data)
      }
    })()

    return () => controller.abort()
  },
})
