# agent-status

A view of Claude Code and OpenCode session status from many machines on the local network. Read-only: agents send events, and the server shows a page with the state of each session (working / waiting for you / error / done). It does not take over or block any session.

Two parts:

- **server** — one program on a single always-on machine in the LAN; it collects events and serves the page,
- **clients** — every machine that runs Claude Code or OpenCode; you install nothing beyond a config entry (HTTP hooks in Claude Code, a small plugin in OpenCode).

## 1. Server

Download the file for your system from the [latest release](https://github.com/Enzovera/agent-status/releases/latest) (`agent-status-windows-amd64.exe`, `agent-status-linux-amd64`, `agent-status-darwin-arm64` etc.; `arm64` for ARM processors, including Macs with Apple Silicon) and run:

```
agent-status-windows-amd64.exe -addr :7777 -state agent-status.json
```

On Linux / macOS run `chmod +x agent-status-*` first. The `-log file.log` option writes the log to a file instead of the screen. Session state is saved in the `-state` file, so it survives a server restart. Next to it the server keeps:

- `archive/events-YYYY-MM.jsonl` — an archive of all events (one per line, no time limit) that the statistics are computed from. Another directory: `-archive DIR`, to turn it off: `-archive ""`. Prompts in it are cut to 160 characters; agent replies are not stored there.
- `machines.json` — the last heartbeat of each machine.

Page: `http://<server IP>:7777/`, statistics: `http://<server IP>:7777/stats`. The server should have a fixed IP address in the LAN (a DHCP reservation in the router).

API:

| | |
|---|---|
| `GET /api/sessions` | session and machine state (what the page shows) |
| `GET /api/stats` | statistics as JSON |
| `GET /api/report` | the same statistics as a Markdown report written for an AI agent that is meant to help improve how you work with agents: metric definitions, numbers, facts to analyse (e.g. the most frequent permission requests as allowlist candidates) |
| `GET /api/events` | raw events from the archive (JSON lines); filters `host=`, `project=`, `key=` |
| `POST /hook/statusline?host=` | data from the Claude Code statusline (context window use, cost, lines changed); sent by the script installed as the statusline |

An identical event from the same machine that arrives again within 2 s is dropped (OpenCode can load the plugin several times and send each event more than once).

Period for the last three: `?days=7` (default) or `?from=2026-11-01&to=2026-11-15` (a date or RFC3339). To have an agent analyse your work, give it e.g. `http://<server IP>:7777/api/report?days=14`.

A machine can be renamed on the page ("rename" in the machine header or in the session details). The change only affects what the page shows, applies to everyone viewing it and is saved in the `-state` file; an empty name restores the one the agent sends.

### The page on a tablet or second screen

The header is a single bar of icons (a description on hover; on a wide screen the layout icons also have words): list layout (machine / project / queue), timeline range, sound, statistics and the **⋯** menu with less used toggles and the colour legend. On a phone the timeline range is also in the ⋯ menu, so the bar fits on one line.

- **sound** (speaker) — a short beep when a session starts waiting for you, hits an error, loses signal or finishes. It also plays when the page is in the foreground (notifications are not shown then). After a page reload the browser holds back sound until the first touch — the icon is orange then.
- **⋯ → keep screen on** — the screen does not turn off while the page is visible.
- **⋯ → full screen** — without the browser bars.
- **⋯ → show finished / remove finished from the list**, **notifications**.

When a session has been waiting for more than 5 minutes, its border starts pulsing, and with sound on the beep repeats every 5 minutes while it waits. Sessions done or finished more than an hour ago collapse at the bottom of their section into a single "older: N" line that can be expanded.

When something is waiting for you, the whole screen gets a red border, and a large bar above the list shows the number of sessions and how long the longest one has been waiting (orange when a session has an error or lost signal). Tapping the bar switches to the "queue" view, where waiting sessions are ordered from the longest waiting. A session row shows since when it has been waiting, in error or done ("7 min ago"); for a working one — when it was last heard from. On a touch screen the buttons are bigger.

Above the list there is also a machine bar: green dot — the machine is sending a heartbeat, orange circle — "offline since …" (no heartbeat for 3 min). The same is shown in the machine header.

The browser only allows notifications and "keep screen on" on `localhost` or over HTTPS. On a device that opens the page by its LAN address (`http://192.168.1.10:7777`), open `edge://flags/#unsafely-treat-insecure-origin-as-secure` (in Chrome `chrome://flags/...`), enter the server address, set *Enabled* and restart the browser. Sound, the border and full screen work without this.

Port 7777 has to be allowed through the firewall, preferably only from the private network. On Windows (PowerShell as administrator):

```powershell
New-NetFirewallRule -DisplayName "agent-status" -Direction Inbound -Protocol TCP -LocalPort 7777 -Profile Private -Action Allow
```

The server has no login and no encryption — do not expose it to the internet.

## 2. Clients (devices that report)

On every machine with Claude Code or OpenCode, run the installer with the server address. The installer detects what is installed, adds hooks to `~/.claude/settings.json` (keeping your other settings and hooks, and making a `settings.json.bak` copy) and/or writes the plugin to `~/.config/opencode/plugins/`. At the end it checks that the server responds.

**Windows** (PowerShell, no administrator rights):

```powershell
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/Enzovera/agent-status/main/install/client.ps1))) -Server 192.168.1.10
```

**macOS / Linux** (needs `python3` or `node`):

```sh
curl -fsSL https://raw.githubusercontent.com/Enzovera/agent-status/main/install/client.sh | sh -s -- --server 192.168.1.10
```

Instead of `192.168.1.10` enter the server IP (or `host:port` if the server does not run on 7777). After installing, restart open Claude Code / OpenCode sessions.

Options:

| Windows | macOS / Linux | Effect |
|---|---|---|
| `-Server ADDR` | `--server ADDR` | server address (required) |
| `-Name NAME` | `--name NAME` | machine name on the page (default: computer name) |
| `-SkipClaude` | `--skip-claude` | leave Claude Code alone |
| `-SkipOpenCode` | `--skip-opencode` | leave OpenCode alone |
| `-SkipHeartbeat` | `--skip-heartbeat` | leave the heartbeat alone (the machine will not be shown as online/offline) |
| `-SkipSkill` | `--skip-skill` | do not install / remove the `/agent-flow-review` skill |
| `-SkipStatusline` | `--skip-statusline` | leave the Claude Code statusline alone (no context use or cost on the page) |
| `-Uninstall` | `--uninstall` | remove agent-status entries (hooks, plugin, heartbeat) |

Running the installer again replaces the previous entries (e.g. after changing the server address or when new hooks were added). The installer only touches hooks pointing at `/hook/claude`, the `agent-status.ts` file, the heartbeat task, the `~/.claude/skills/agent-flow-review` directory, the `~/.claude/agent-status` directory and `statusLine` in `settings.json` (only when it is the agent-status statusline).

**Statusline.** Hooks do not report context window use or cost, but the Claude Code statusline gets them on every refresh. The installer sets `statusLine` in `settings.json` to the script `~/.claude/agent-status/statusline.sh` (macOS, Linux, Windows with Git Bash) or `statusline.ps1` (Windows without Git Bash). The script sends this data to the server in the background (`/hook/statusline`) and then runs your existing statusline with the same data. If there was none, it prints a short line like `Opus · kontekst 64% · $0.87` ("kontekst" = context). Your previous `statusLine` is saved in `~/.claude/agent-status/statusline-orig.json` and is restored on `-Uninstall` / `--uninstall`. On the page the session gets a context NN% badge (orange from 70%, red from 85%), and the details show cost and lines changed. Statistics sum cost per project, machine, model and day. A statusline set in a project (`.claude/settings.json`) takes precedence, so in such a project no data is sent.

**Flow analysis skill.** When Claude Code is present, the installer also writes the skill `~/.claude/skills/agent-flow-review/SKILL.md` with the server address filled in. In Claude Code type `/agent-flow-review` (optionally a number of days or a date range, e.g. `/agent-flow-review 30` or `/agent-flow-review 2026-11-01 2026-11-15`) or ask it to analyse your flow. The agent fetches `/api/report` with `curl` and gives recommendations in Polish based on the numbers: where agents wait for you, how many sessions at once make sense, where errors and compactions happen, and ready-made `permissions.allow` rules for recurring permission requests (it does not suggest risky commands). It does not change settings without your consent; before a change it makes a `settings.json.bak-agent-flow` copy. The skill is pre-approved only for `curl` to `/api/…` on your server.

**Heartbeat.** The installer also sets up a light task that every minute (and right after logon / boot) sends `POST /hook/heartbeat?host=NAME` — so the page shows whether the machine is on (offline after 3 minutes of silence). The heartbeat is about the machine, not the agent, so it is installed even with `-SkipClaude -SkipOpenCode`. On Windows it is the `agent-status-heartbeat` task in the current user's Task Scheduler (hidden `curl.exe`, no administrator rights); on macOS the LaunchAgent `~/Library/LaunchAgents/com.agent-status.heartbeat.plist`; on Linux a line in the user's `crontab` tagged `# agent-status-heartbeat`, and when there is no `crontab` — the `systemctl --user` timer `agent-status-heartbeat.timer` (without `loginctl enable-linger` it only runs while you are logged in).

### Manual installation

**Claude Code:** add the contents of [`clients/claude/hooks.json`](clients/claude/hooks.json) to `~/.claude/settings.json` (the `hooks` key; if it already exists, merge the lists). Replace `SERVER` with the server IP, and `HOST` with the machine name you want to see on the page (without `?host=` the server shows the IP).

The hooks are of type `http` with a 2 s timeout; when the server is down, Claude Code shows a non-blocking "hook error" and carries on. When the server machine is off (not just the server), every tool call can wait up to 2 s on `PreToolUse` and as long again on `PostToolUse`. `SessionStart` is not used: Claude Code does not accept `http` hooks for it, so a session appears on the page with its first prompt.

**Skill:** copy [`clients/claude/skills/agent-flow-review/SKILL.md`](clients/claude/skills/agent-flow-review/SKILL.md) to `~/.claude/skills/agent-flow-review/` and replace `http://SERVER:7777` with the server address.

**OpenCode:** copy [`clients/opencode/agent-status.ts`](clients/opencode/agent-status.ts) to `~/.config/opencode/plugins/` and replace `SERVER` (or set the `AGENT_STATUS_URL` variable). The machine name is `hostname()`, overridden by `AGENT_STATUS_HOST`.

## What is sent

Claude Code sends the full hook data (including prompts and tool arguments) — the server keeps only what the page shows: directory, prompt, last reply, model, tool name and a short description. The data goes only to your server in the LAN.

The OpenCode plugin is for OpenCode 2.x (`@opencode/plugin`, the `Plugin.define` / `ctx.event.subscribe` API; event names and fields checked against the types from version 2.3.0). It subscribes only to the types it needs, so streamed reply fragments do not leave OpenCode. It sends:

- session title, model and directory,
- prompt text (`session.inbox.enqueued`) and the last reply text (`session.text.ended`), both cut to 4000 characters,
- tool calls with name and a short description: from the arguments only `description`, path, `pattern`, `url`, `command`, `query` are sent, never file contents or patches,
- permission requests, agent questions (`form.*`), errors and interruptions,
- for subagent sessions (the `subagent` tool creates a child session) — the parent session id and the agent name; the page shows them as subagents of the parent session and draws their rows under it.

OpenCode 1.x has a different plugin format and this plugin will not work there.

## States

| State | Claude Code | OpenCode |
|---|---|---|
| working | `UserPromptSubmit`, `PreToolUse` | `session.execution.started`, `session.tool.called`, `session.status` busy/retry, `permission.replied`, `form.replied`/`cancelled` |
| waiting for you | `PermissionRequest`, `Notification` (permission_prompt, agent_needs_input, elicitation) | `permission.asked`, `form.created` |
| done | `Stop`, `Notification` idle_prompt | `session.created`, `session.execution.succeeded`/`interrupted`, `session.idle`, `session.status` idle |
| error | `StopFailure` | `session.execution.failed` |
| finished | `SessionEnd` | `session.deleted` |

Claude Code does not report the approval itself or the answer to a question, so a session waiting for approval or an answer goes back to "working" when the same tool finishes (`PostToolUse`/`PostToolUseFailure`) — or earlier, with the next tool. Tools of subagents running in the background do not do this.

Claude Code subagents (the `Agent` tool) get their own rows under the main session, just like OpenCode subagents: everything a hook reports with an `agent_id` goes to the subagent's session (`agent_id` as the id, the main session as the parent). The row title is the task it was started with. The main session shows only its own main thread: a subagent's tools, permission requests and end of work do not change its state or description. A subagent waiting for permission lands in "Waiting for you" in the queue. Finished subagents collapse after a minute into a "✓ N subagents finished" line. In the statistics, subagent time and waits are counted, but subagents are not counted as separate sessions.

No state change in Claude Code, only description and counters: `PostToolUse` (tool duration, for statistics), `PostToolUseFailure` (tool error), `PermissionDenied` (denied in auto mode), `SubagentStart`/`SubagentStop` (running subagents), `TaskCreated`/`TaskCompleted` (task list progress, e.g. "tasks 3/5"), `PreCompact`/`PostCompact` (context compaction), `PostModelSwitch` (model). They do not overwrite the description of a session waiting for you.

No state change in OpenCode: `session.renamed` (title), `session.model.selected` (model), `session.inbox.enqueued` (prompt), `session.text.ended` (reply), `session.tool.failed` (description). When a session goes to "done" without a description of its own, the row shows the start of the last reply.

"No signal": the session is "working", but no event has arrived for 10 min. Finished sessions disappear after 2 h, silent ones after 24 h.

## Building from source

Requires Go 1.23+:

```
go build -o bin/agent-status.exe ./cmd/agent-status
```

New release: `git tag v0.2.0 && git push origin v0.2.0` — the `.github/workflows/release.yml` workflow builds the server for all platforms and publishes a release with the files.

## License

MIT — see [LICENSE](LICENSE).
