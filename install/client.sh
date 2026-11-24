#!/bin/sh
# agent-status client installer for macOS / Linux (Claude Code hooks + OpenCode plugin).
#
#   curl -fsSL https://raw.githubusercontent.com/Enzovera/agent-status/main/install/client.sh | sh -s -- --server 192.168.1.10
#
# Options: --server ADDR (IP, host or host:port), --name NAME (default: hostname),
#          --skip-claude, --skip-opencode, --skip-heartbeat, --skip-skill, --skip-statusline,
#          --uninstall, --ref REF (default: main).
# Re-running replaces the previous agent-status entries. Needs python3 or node to edit JSON.
#
# Skill: with Claude Code, ~/.claude/skills/agent-flow-review/SKILL.md (/agent-flow-review:
# reads the server's /api/report and suggests workflow fixes and permission rules) with the
# server address filled in. --skip-skill leaves it alone (install and uninstall).
#
# Statusline: with Claude Code, settings.json "statusLine" runs ~/.claude/agent-status/
# statusline.sh, which sends context window use and cost to the server and then runs
# your previous statusLine, if any (put back on --uninstall). --skip-statusline leaves it
# alone (install and uninstall).
#
# Heartbeat: the machine shows as online while it POSTs /hook/heartbeat every minute.
# It is per machine, not per agent, so it is installed even with --skip-claude
# --skip-opencode: macOS LaunchAgent com.agent-status.heartbeat; Linux user crontab line
# tagged "# agent-status-heartbeat", or systemd --user agent-status-heartbeat.timer when
# there is no crontab. --skip-heartbeat leaves it alone (install and uninstall).
set -eu

SERVER=""
NAME="$(hostname 2>/dev/null || uname -n)"
NAME="${NAME%%.*}"
SKIP_CLAUDE=0
SKIP_OPENCODE=0
SKIP_HEARTBEAT=0
SKIP_SKILL=0
SKIP_STATUSLINE=0
UNINSTALL=0
REF="main"

while [ $# -gt 0 ]; do
  case "$1" in
    --server) SERVER="$2"; shift 2 ;;
    --name) NAME="$2"; shift 2 ;;
    --skip-claude) SKIP_CLAUDE=1; shift ;;
    --skip-opencode) SKIP_OPENCODE=1; shift ;;
    --skip-heartbeat) SKIP_HEARTBEAT=1; shift ;;
    --skip-skill) SKIP_SKILL=1; shift ;;
    --skip-statusline) SKIP_STATUSLINE=1; shift ;;
    --uninstall) UNINSTALL=1; shift ;;
    --ref) REF="$2"; shift 2 ;;
    *) echo "Unknown option: $1" >&2; exit 2 ;;
  esac
done

REPO="https://raw.githubusercontent.com/Enzovera/agent-status/$REF"

if [ "$UNINSTALL" = 0 ] && [ -z "$SERVER" ]; then
  echo "Give the server address: --server 192.168.1.10 (or host:port)" >&2
  exit 2
fi
if [ -n "$SERVER" ]; then
  case "$SERVER" in http://*|https://*) ;; *) SERVER="http://$SERVER" ;; esac
  SERVER="${SERVER%/}"
  if ! printf '%s' "$SERVER" | grep -Eq '^https?://[^/]+:[0-9]+$'; then SERVER="$SERVER:7777"; fi
fi

if python3 -c "" >/dev/null 2>&1; then RUNNER=python3
elif command -v node >/dev/null 2>&1; then RUNNER=node
else RUNNER=""
fi

# fetch_repo_file REL DEST: a file of this repository, from the checkout next to this
# script (sh install/client.sh) or downloaded from $REPO (curl | sh has no local files).
fetch_repo_file() {
  local_src=""
  case "$0" in
    *client.sh) local_src="$(dirname "$0")/../$1" ;;
  esac
  mkdir -p "$(dirname "$2")"
  if [ -n "$local_src" ] && [ -f "$local_src" ]; then
    cp "$local_src" "$2"
  else
    curl -fsSL "$REPO/$1" -o "$2"
  fi
}

install_claude() {
  settings="$HOME/.claude/settings.json"
  if [ ! -f "$settings" ]; then
    [ "$UNINSTALL" = 1 ] && return 0
    mkdir -p "$HOME/.claude"
    echo '{}' > "$settings"
  else
    cp "$settings" "$settings.bak"
  fi
  if [ -z "$RUNNER" ]; then
    echo "Claude Code: no python3 or node - add clients/claude/hooks.json to $settings by hand" >&2
    return 0
  fi
  # Statusline: the script goes in first; the JSON step below points statusLine at it,
  # keeping the user's own statusLine as the one it runs next.
  sl_dir="$HOME/.claude/agent-status"
  AS_STATUSLINE=0
  AS_SL_CMD=""
  if [ "$SKIP_STATUSLINE" = 0 ]; then
    AS_STATUSLINE=1
    if [ "$UNINSTALL" = 0 ]; then
      if fetch_repo_file clients/claude/statusline/statusline.sh "$sl_dir/statusline.sh"; then
        AS_SL_CMD="sh ~/.claude/agent-status/statusline.sh '$SERVER/hook/statusline?host=$(urlencode "$NAME")'"
      else
        AS_STATUSLINE=0
        echo "WARNING: statusline: could not download clients/claude/statusline/statusline.sh" >&2
      fi
    fi
  fi
  export AS_SETTINGS="$settings" AS_SERVER="$SERVER" AS_NAME="$NAME" AS_UNINSTALL="$UNINSTALL"
  export AS_STATUSLINE AS_SL_CMD AS_SL_DIR="$sl_dir"
  if [ "$RUNNER" = python3 ]; then
    python3 - <<'PY'
import json, os, urllib.parse
path = os.environ["AS_SETTINGS"]
with open(path, encoding="utf-8") as f:
    text = f.read().strip()
s = json.loads(text) if text else {}
hooks = s.setdefault("hooks", {})
url = "%s/hook/claude?host=%s" % (os.environ["AS_SERVER"], urllib.parse.quote(os.environ["AS_NAME"], safe=""))
ours = lambda g: any("/hook/claude" in str(h.get("url", "")) for h in g.get("hooks", []))
events = ["UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure",
          "PermissionRequest", "PermissionDenied", "Notification", "SubagentStart",
          "SubagentStop", "TaskCreated", "TaskCompleted", "PreCompact", "PostCompact",
          "PostModelSwitch", "Stop", "StopFailure", "SessionEnd"]
# Registered by older versions, now only cleaned up (no http hooks on SessionStart).
retired = ["SessionStart"]
for ev in events + retired:
    kept = [g for g in hooks.get(ev, []) if not ours(g)]
    if os.environ["AS_UNINSTALL"] != "1" and ev in events:
        kept.append({"hooks": [{"type": "http", "url": url, "timeout": 2}]})
    if kept: hooks[ev] = kept
    else: hooks.pop(ev, None)
if not hooks: s.pop("hooks")
if os.environ.get("AS_STATUSLINE") == "1":
    d = os.environ["AS_SL_DIR"]
    nxt, org = os.path.join(d, "statusline-next"), os.path.join(d, "statusline-orig.json")
    cur = s.get("statusLine")
    ours_sl = isinstance(cur, dict) and "agent-status/statusline" in str(cur.get("command", ""))
    if os.environ["AS_UNINSTALL"] == "1":
        if ours_sl:
            if os.path.exists(org):
                with open(org, encoding="utf-8") as f:
                    s["statusLine"] = json.load(f)
                print("Statusline: your previous statusLine restored")
            else:
                s.pop("statusLine")
                print("Statusline: removed")
    else:
        cmd = os.environ["AS_SL_CMD"]
        chained = False
        if ours_sl:
            cur["command"] = cmd
            chained = os.path.exists(nxt)
        elif isinstance(cur, dict):
            with open(nxt, "w", encoding="utf-8") as f:
                f.write(str(cur.get("command", "")))
            with open(org, "w", encoding="utf-8") as f:
                json.dump(cur, f, indent=2, ensure_ascii=False)
            new = dict(cur)
            new["type"], new["command"] = "command", cmd
            s["statusLine"] = new
            chained = True
        else:
            for p in (nxt, org):
                if os.path.exists(p): os.remove(p)
            s["statusLine"] = {"type": "command", "command": cmd}
        print("Statusline: session context use and cost sent to the server" + (" (your previous statusline keeps running)" if chained else ""))
with open(path, "w", encoding="utf-8") as f:
    json.dump(s, f, indent=2, ensure_ascii=False)
    f.write("\n")
PY
  else
    node - <<'JS'
const fs = require("fs")
const path = process.env.AS_SETTINGS
const text = fs.readFileSync(path, "utf8").trim()
const s = text ? JSON.parse(text) : {}
const hooks = (s.hooks ??= {})
const url = `${process.env.AS_SERVER}/hook/claude?host=${encodeURIComponent(process.env.AS_NAME)}`
const ours = (g) => (g.hooks ?? []).some((h) => String(h.url ?? "").includes("/hook/claude"))
const events = ["UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure",
                "PermissionRequest", "PermissionDenied", "Notification", "SubagentStart",
                "SubagentStop", "TaskCreated", "TaskCompleted", "PreCompact", "PostCompact",
                "PostModelSwitch", "Stop", "StopFailure", "SessionEnd"]
// Registered by older versions, now only cleaned up (no http hooks on SessionStart).
const retired = ["SessionStart"]
for (const ev of [...events, ...retired]) {
  const kept = (hooks[ev] ?? []).filter((g) => !ours(g))
  if (process.env.AS_UNINSTALL !== "1" && events.includes(ev)) kept.push({ hooks: [{ type: "http", url, timeout: 2 }] })
  if (kept.length) hooks[ev] = kept
  else delete hooks[ev]
}
if (!Object.keys(hooks).length) delete s.hooks
if (process.env.AS_STATUSLINE === "1") {
  const d = process.env.AS_SL_DIR
  const nxt = `${d}/statusline-next`, org = `${d}/statusline-orig.json`
  const cur = s.statusLine
  const oursSl = cur && typeof cur === "object" && String(cur.command ?? "").includes("agent-status/statusline")
  if (process.env.AS_UNINSTALL === "1") {
    if (oursSl) {
      if (fs.existsSync(org)) {
        s.statusLine = JSON.parse(fs.readFileSync(org, "utf8"))
        console.log("Statusline: your previous statusLine restored")
      } else {
        delete s.statusLine
        console.log("Statusline: removed")
      }
    }
  } else {
    const cmd = process.env.AS_SL_CMD
    let chained = false
    if (oursSl) {
      cur.command = cmd
      chained = fs.existsSync(nxt)
    } else if (cur && typeof cur === "object") {
      fs.writeFileSync(nxt, String(cur.command ?? ""))
      fs.writeFileSync(org, JSON.stringify(cur, null, 2) + "\n")
      s.statusLine = { ...cur, type: "command", command: cmd }
      chained = true
    } else {
      for (const p of [nxt, org]) if (fs.existsSync(p)) fs.rmSync(p)
      s.statusLine = { type: "command", command: cmd }
    }
    console.log("Statusline: session context use and cost sent to the server" + (chained ? " (your previous statusline keeps running)" : ""))
  }
}
fs.writeFileSync(path, JSON.stringify(s, null, 2) + "\n")
JS
  fi
  if [ "$UNINSTALL" = 1 ] && [ "$AS_STATUSLINE" = 1 ]; then
    rm -f "$sl_dir/statusline.sh" "$sl_dir/statusline.ps1" "$sl_dir/statusline-next" "$sl_dir/statusline-orig.json"
    rmdir "$sl_dir" 2>/dev/null || true
  fi
  if [ "$UNINSTALL" = 1 ]; then echo "Claude Code: hooks removed from $settings"
  else echo "Claude Code: hooks added to $settings (backup: settings.json.bak)"; fi
}

install_opencode() {
  dir="$HOME/.config/opencode/plugins"
  path="$dir/agent-status.ts"
  if [ "$UNINSTALL" = 1 ]; then
    [ -f "$path" ] && rm "$path" && echo "OpenCode: removed $path"
    return 0
  fi
  mkdir -p "$dir"
  tmp="$(mktemp)"
  curl -fsSL "$REPO/clients/opencode/agent-status.ts" -o "$tmp"
  esc_server=$(printf '%s' "$SERVER" | sed 's/[\/&|]/\\&/g')
  # JS string literal first, then escaped for the sed replacement.
  esc_name=$(printf '%s' "$NAME" | sed -e 's/[\\"]/\\&/g' | sed -e 's/[\\&|]/\\&/g')
  sed -e "s|\"http://SERVER:7777\"|\"$esc_server\"|" \
      -e "s|process.env.AGENT_STATUS_HOST ?? hostname()|process.env.AGENT_STATUS_HOST ?? \"$esc_name\"|" \
      "$tmp" > "$path"
  rm -f "$tmp"
  echo "OpenCode: plugin written to $path"
}

SKILL_NAME="agent-flow-review"

# From a local checkout (sh install/client.sh) the template next to the script is used;
# piped from curl ($0 is the shell) it is downloaded from $REPO.
install_skill() {
  dir="$HOME/.claude/skills/$SKILL_NAME"
  path="$dir/SKILL.md"
  if [ "$UNINSTALL" = 1 ]; then
    if [ -f "$path" ]; then rm -f "$path" && echo "Skill: removed $path"; fi
    rmdir "$dir" 2>/dev/null || true
    return 0
  fi
  local_src=""
  case "$0" in
    *client.sh) local_src="$(dirname "$0")/../clients/claude/skills/$SKILL_NAME/SKILL.md" ;;
  esac
  tmp="$(mktemp)"
  if [ -n "$local_src" ] && [ -f "$local_src" ]; then
    cp "$local_src" "$tmp"
  elif ! curl -fsSL "$REPO/clients/claude/skills/$SKILL_NAME/SKILL.md" -o "$tmp"; then
    rm -f "$tmp"
    echo "WARNING: skill: could not download $REPO/clients/claude/skills/$SKILL_NAME/SKILL.md" >&2
    return 0
  fi
  mkdir -p "$dir"
  esc_server=$(printf '%s' "$SERVER" | sed 's/[\/&|]/\\&/g')
  sed -e "s|http://SERVER:7777|$esc_server|g" "$tmp" > "$path"
  rm -f "$tmp"
  echo "Skill: written to $path - in Claude Code: /$SKILL_NAME (stats analysis and recommendations)"
}

HB_LABEL="com.agent-status.heartbeat"
HB_PLIST="$HOME/Library/LaunchAgents/$HB_LABEL.plist"
HB_MARK="# agent-status-heartbeat"
HB_UNIT="agent-status-heartbeat"
HB_UNIT_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"

# Percent-encodes everything except RFC 3986 unreserved characters (byte-wise, so UTF-8 works).
urlencode() {
  printf '%s' "$1" | od -An -tx1 -v | awk '
    BEGIN { for (i = 0; i < 256; i++) hex[sprintf("%02x", i)] = i }
    { for (i = 1; i <= NF; i++) {
        c = hex[$i]
        if ((c >= 48 && c <= 57) || (c >= 65 && c <= 90) || (c >= 97 && c <= 122) || c == 45 || c == 46 || c == 95 || c == 126)
          printf "%c", c
        else
          printf "%%%s", toupper($i)
    } }'
}

HB_URL=""
[ -n "$SERVER" ] && HB_URL="$SERVER/hook/heartbeat?host=$(urlencode "$NAME")"

if command -v curl >/dev/null 2>&1; then HB_CURL="$(command -v curl)"; else HB_CURL=""; fi
if command -v wget >/dev/null 2>&1; then HB_WGET="$(command -v wget)"; else HB_WGET=""; fi

# Prints the heartbeat command as words: one per line, URL last.
hb_argv() {
  if [ -n "$HB_CURL" ]; then printf '%s\n' "$HB_CURL" -s -m 5 -o /dev/null -X POST "$HB_URL"
  else printf '%s\n' "$HB_WGET" -q -O /dev/null -T 5 --post-data= "$HB_URL"
  fi
}

hb_send() {
  if [ -n "$HB_CURL" ]; then "$HB_CURL" -fsS -m 3 -o /dev/null -X POST "$HB_URL" 2>/dev/null
  else "$HB_WGET" -q -O /dev/null -T 3 --post-data= "$HB_URL"
  fi
}

has_systemd_user() {
  command -v systemctl >/dev/null 2>&1 && systemctl --user show-environment >/dev/null 2>&1
}

# Replaces our crontab line with $1 (empty: just removes it). Returns 1 if nothing changed.
hb_cron_set() {
  current="$(crontab -l 2>/dev/null)" || current=""
  rest="$(printf '%s\n' "$current" | grep -vF "$HB_MARK" || true)"
  [ -z "$1" ] && [ "$rest" = "$current" ] && return 1
  new="$rest"
  if [ -n "$1" ]; then
    if [ -n "$new" ]; then new="$new
$1"; else new="$1"; fi
  fi
  if [ -n "$new" ]; then printf '%s\n' "$new" | crontab -
  else crontab -r 2>/dev/null || printf '' | crontab -
  fi
}

hb_remove_launchd() {
  launchctl bootout "gui/$(id -u)/$HB_LABEL" >/dev/null 2>&1 || launchctl unload "$HB_PLIST" >/dev/null 2>&1 || true
  [ -f "$HB_PLIST" ] || return 1
  rm -f "$HB_PLIST"
}

hb_remove_systemd() {
  [ -f "$HB_UNIT_DIR/$HB_UNIT.timer" ] || [ -f "$HB_UNIT_DIR/$HB_UNIT.service" ] || return 1
  systemctl --user disable --now "$HB_UNIT.timer" >/dev/null 2>&1 || true
  rm -f "$HB_UNIT_DIR/$HB_UNIT.timer" "$HB_UNIT_DIR/$HB_UNIT.service"
  systemctl --user daemon-reload >/dev/null 2>&1 || true
}

install_heartbeat_launchd() {
  hb_remove_launchd || true
  mkdir -p "$(dirname "$HB_PLIST")"
  {
    printf '%s\n' '<?xml version="1.0" encoding="UTF-8"?>' \
      '<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">' \
      '<plist version="1.0">' '<dict>' \
      "  <key>Label</key><string>$HB_LABEL</string>" \
      '  <key>ProgramArguments</key>' '  <array>'
    hb_argv | sed -e 's/&/\&amp;/g' -e 's/</\&lt;/g' -e 's/>/\&gt;/g' -e 's|.*|    <string>&</string>|'
    printf '%s\n' '  </array>' \
      '  <key>StartInterval</key><integer>60</integer>' \
      '  <key>RunAtLoad</key><true/>' '</dict>' '</plist>'
  } > "$HB_PLIST"
  # bootout finishes asynchronously; bootstrap can fail for a moment right after it.
  for try in 1 2 3; do
    launchctl bootstrap "gui/$(id -u)" "$HB_PLIST" >/dev/null 2>&1 && break
    launchctl load -w "$HB_PLIST" >/dev/null 2>&1 && break
    sleep 1
  done
  if launchctl list "$HB_LABEL" >/dev/null 2>&1; then
    echo "Heartbeat: LaunchAgent $HB_PLIST (every minute)"
  else
    echo "WARNING: heartbeat: wrote $HB_PLIST, but launchctl did not load it (it will work after logging in again)." >&2
  fi
}

install_heartbeat_cron() {
  # cron turns an unescaped % into a newline.
  cmd="$(hb_argv | sed -e "s/'/'\\\\''/g" -e "s/.*/'&'/" -e 's/%/\\%/g' | tr '\n' ' ')"
  hb_cron_set "* * * * * $cmd>/dev/null 2>&1 $HB_MARK" || return 1
  echo "Heartbeat: line in the user crontab (every minute, tagged '$HB_MARK')"
  if command -v pgrep >/dev/null 2>&1 && ! pgrep -x cron >/dev/null 2>&1 && ! pgrep -x crond >/dev/null 2>&1; then
    echo "WARNING: no running cron daemon (cron/crond) found - start it, otherwise the heartbeat will not be sent." >&2
  fi
}

install_heartbeat_systemd() {
  mkdir -p "$HB_UNIT_DIR"
  # systemd: % and $ are specifiers / variables, words in double quotes.
  exec_start="$(hb_argv | sed -e 's/[\\"]/\\&/g' -e 's/%/%%/g' -e 's/\$/$$/g' -e 's/.*/"&"/' | tr '\n' ' ')"
  printf '%s\n' '[Unit]' 'Description=agent-status heartbeat' '' \
    '[Service]' 'Type=oneshot' "ExecStart=$exec_start" > "$HB_UNIT_DIR/$HB_UNIT.service"
  printf '%s\n' '[Unit]' 'Description=agent-status heartbeat every minute' '' \
    '[Timer]' 'OnBootSec=30' 'OnUnitActiveSec=60' 'AccuracySec=5s' '' \
    '[Install]' 'WantedBy=timers.target' > "$HB_UNIT_DIR/$HB_UNIT.timer"
  systemctl --user daemon-reload &&
    systemctl --user enable "$HB_UNIT.timer" >/dev/null 2>&1 &&
    systemctl --user restart "$HB_UNIT.timer" || return 1
  echo "Heartbeat: systemd --user timer $HB_UNIT.timer (every minute)"
  if [ "$(loginctl show-user "$(id -un)" -p Linger --value 2>/dev/null)" != yes ]; then
    echo "WARNING: without 'loginctl enable-linger $(id -un)' the heartbeat only runs while you are logged in." >&2
  fi
}

install_heartbeat() {
  if [ "$UNINSTALL" = 1 ]; then
    if [ "$(uname -s)" = Darwin ]; then
      hb_remove_launchd && echo "Heartbeat: removed $HB_PLIST"
    else
      command -v crontab >/dev/null 2>&1 && hb_cron_set "" && echo "Heartbeat: removed the crontab line"
      hb_remove_systemd && echo "Heartbeat: removed systemd --user timer $HB_UNIT.timer"
    fi
    return 0
  fi
  if [ -z "$HB_CURL" ] && [ -z "$HB_WGET" ]; then
    echo "Heartbeat: skipped (no curl or wget)" >&2
    return 0
  fi
  if [ "$(uname -s)" = Darwin ]; then
    install_heartbeat_launchd
    return
  fi
  hb_remove_systemd || true
  if command -v crontab >/dev/null 2>&1 && install_heartbeat_cron; then
    :
  elif has_systemd_user; then
    install_heartbeat_systemd
  else
    echo "Heartbeat: skipped (neither crontab nor systemd --user is usable) - the machine will not show as online." >&2
  fi
}

if [ "$SKIP_CLAUDE" = 0 ] && { [ -d "$HOME/.claude" ] || command -v claude >/dev/null 2>&1; }; then
  install_claude
  if [ "$SKIP_SKILL" = 0 ]; then install_skill; else echo "Skill: skipped (--skip-skill)"; fi
else
  echo "Claude Code: skipped (not found)"
fi
if [ "$SKIP_OPENCODE" = 0 ] && { [ -d "$HOME/.config/opencode" ] || command -v opencode >/dev/null 2>&1; }; then
  install_opencode
else
  echo "OpenCode: skipped (not found)"
fi
if [ "$SKIP_HEARTBEAT" = 0 ]; then
  install_heartbeat || echo "WARNING: could not set up the heartbeat." >&2
else
  echo "Heartbeat: skipped (--skip-heartbeat)"
fi

if [ "$UNINSTALL" = 0 ]; then
  if curl -fsS -m 3 "$SERVER/api/sessions" >/dev/null 2>&1; then
    echo "Server $SERVER responds."
    # First heartbeat now, so the machine shows as online without waiting a minute.
    if [ "$SKIP_HEARTBEAT" = 0 ] && { [ -n "$HB_CURL" ] || [ -n "$HB_WGET" ]; }; then
      hb_send || echo "WARNING: the server did not accept the heartbeat. Update the server." >&2
    fi
  else
    echo "WARNING: server $SERVER does not respond. Check the address and the firewall on the server (TCP port)." >&2
  fi
  echo "Done. New sessions will show up as '$NAME'. Restart open Claude Code / OpenCode sessions."
fi
