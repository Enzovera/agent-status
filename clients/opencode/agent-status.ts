// OpenCode plugin: forwards session and permission events to agent-status.
// Install globally: copy to ~/.config/opencode/plugins/agent-status.ts
// Override the server with AGENT_STATUS_URL and the shown name with AGENT_STATUS_HOST.
import { Plugin } from "@opencode/plugin"
import { hostname } from "node:os"

const SERVER = process.env.AGENT_STATUS_URL ?? "http://SERVER:7777"
const FORWARD = /^(session\.(created|updated|status|idle|error|deleted)|permission\.)/

export default Plugin.define({
  id: "agent-status",
  async setup(ctx) {
    const host = process.env.AGENT_STATUS_HOST ?? hostname()
    const directory = ctx.location.directory
    const controller = new AbortController()

    void (async () => {
      for await (const event of ctx.event.subscribe({ signal: controller.signal })) {
        if (!FORWARD.test(event.type)) continue
        // Fire and forget: a dead server must never slow the session down.
        fetch(`${SERVER}/hook/opencode`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ host, directory, type: event.type, properties: (event as any).properties ?? {} }),
          signal: AbortSignal.timeout(2000),
        }).catch(() => {})
      }
    })()

    return () => controller.abort()
  },
})
