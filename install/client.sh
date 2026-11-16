#!/bin/sh
# agent-status client installer for macOS / Linux (Claude Code hooks + OpenCode plugin).
#
#   curl -fsSL https://raw.githubusercontent.com/Enzovera/agent-status/main/install/client.sh | sh -s -- --server 192.168.1.10
#
# Options: --server ADDR (IP, host or host:port), --name NAME (default: hostname),
#          --skip-claude, --skip-opencode, --skip-heartbeat, --uninstall,
#          --ref REF (default: main).
# Re-running replaces the previous agent-status entries. Needs python3 or node to edit JSON.
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
UNINSTALL=0
REF="main"

while [ $# -gt 0 ]; do
  case "$1" in
    --server) SERVER="$2"; shift 2 ;;
    --name) NAME="$2"; shift 2 ;;
    --skip-claude) SKIP_CLAUDE=1; shift ;;
    --skip-opencode) SKIP_OPENCODE=1; shift ;;
    --skip-heartbeat) SKIP_HEARTBEAT=1; shift ;;
    --uninstall) UNINSTALL=1; shift ;;
    --ref) REF="$2"; shift 2 ;;
    *) echo "Nieznana opcja: $1" >&2; exit 2 ;;
  esac
done

REPO="https://raw.githubusercontent.com/Enzovera/agent-status/$REF"

if [ "$UNINSTALL" = 0 ] && [ -z "$SERVER" ]; then
  echo "Podaj adres serwera: --server 192.168.1.10 (albo host:port)" >&2
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
    echo "Claude Code: brak python3 i node - dopisz ręcznie clients/claude/hooks.json do $settings" >&2
    return 0
  fi
  export AS_SETTINGS="$settings" AS_SERVER="$SERVER" AS_NAME="$NAME" AS_UNINSTALL="$UNINSTALL"
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
fs.writeFileSync(path, JSON.stringify(s, null, 2) + "\n")
JS
  fi
  if [ "$UNINSTALL" = 1 ]; then echo "Claude Code: usunięto hooki z $settings"
  else echo "Claude Code: hooki dopisane do $settings (kopia: settings.json.bak)"; fi
}

install_opencode() {
  dir="$HOME/.config/opencode/plugins"
  path="$dir/agent-status.ts"
  if [ "$UNINSTALL" = 1 ]; then
    [ -f "$path" ] && rm "$path" && echo "OpenCode: usunięto $path"
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
  echo "OpenCode: plugin zapisany w $path"
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
    echo "Heartbeat: LaunchAgent $HB_PLIST (co minutę)"
  else
    echo "UWAGA: heartbeat: zapisano $HB_PLIST, ale launchctl go nie wczytał (zadziała po ponownym zalogowaniu)." >&2
  fi
}

install_heartbeat_cron() {
  # cron turns an unescaped % into a newline.
  cmd="$(hb_argv | sed -e "s/'/'\\\\''/g" -e "s/.*/'&'/" -e 's/%/\\%/g' | tr '\n' ' ')"
  hb_cron_set "* * * * * $cmd>/dev/null 2>&1 $HB_MARK" || return 1
  echo "Heartbeat: wpis w crontab użytkownika (co minutę, znacznik '$HB_MARK')"
  if command -v pgrep >/dev/null 2>&1 && ! pgrep -x cron >/dev/null 2>&1 && ! pgrep -x crond >/dev/null 2>&1; then
    echo "UWAGA: nie widać działającego demona cron (cron/crond) - włącz go, inaczej heartbeat nie będzie wysyłany." >&2
  fi
}

install_heartbeat_systemd() {
  mkdir -p "$HB_UNIT_DIR"
  # systemd: % and $ are specifiers / variables, words in double quotes.
  exec_start="$(hb_argv | sed -e 's/[\\"]/\\&/g' -e 's/%/%%/g' -e 's/\$/$$/g' -e 's/.*/"&"/' | tr '\n' ' ')"
  printf '%s\n' '[Unit]' 'Description=agent-status heartbeat' '' \
    '[Service]' 'Type=oneshot' "ExecStart=$exec_start" > "$HB_UNIT_DIR/$HB_UNIT.service"
  printf '%s\n' '[Unit]' 'Description=agent-status heartbeat co minutę' '' \
    '[Timer]' 'OnBootSec=30' 'OnUnitActiveSec=60' 'AccuracySec=5s' '' \
    '[Install]' 'WantedBy=timers.target' > "$HB_UNIT_DIR/$HB_UNIT.timer"
  systemctl --user daemon-reload &&
    systemctl --user enable "$HB_UNIT.timer" >/dev/null 2>&1 &&
    systemctl --user restart "$HB_UNIT.timer" || return 1
  echo "Heartbeat: timer systemd --user $HB_UNIT.timer (co minutę)"
  if [ "$(loginctl show-user "$(id -un)" -p Linger --value 2>/dev/null)" != yes ]; then
    echo "UWAGA: bez 'loginctl enable-linger $(id -un)' heartbeat działa tylko, gdy jesteś zalogowany." >&2
  fi
}

install_heartbeat() {
  if [ "$UNINSTALL" = 1 ]; then
    if [ "$(uname -s)" = Darwin ]; then
      hb_remove_launchd && echo "Heartbeat: usunięto $HB_PLIST"
    else
      command -v crontab >/dev/null 2>&1 && hb_cron_set "" && echo "Heartbeat: usunięto wpis z crontab"
      hb_remove_systemd && echo "Heartbeat: usunięto timer systemd --user $HB_UNIT.timer"
    fi
    return 0
  fi
  if [ -z "$HB_CURL" ] && [ -z "$HB_WGET" ]; then
    echo "Heartbeat: pominięto (brak curl i wget)" >&2
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
    echo "Heartbeat: pominięto (nie da się użyć crontab ani systemd --user) - maszyna nie będzie widoczna jako online." >&2
  fi
}

if [ "$SKIP_CLAUDE" = 0 ] && { [ -d "$HOME/.claude" ] || command -v claude >/dev/null 2>&1; }; then
  install_claude
else
  echo "Claude Code: pominięto (nie znaleziono)"
fi
if [ "$SKIP_OPENCODE" = 0 ] && { [ -d "$HOME/.config/opencode" ] || command -v opencode >/dev/null 2>&1; }; then
  install_opencode
else
  echo "OpenCode: pominięto (nie znaleziono)"
fi
if [ "$SKIP_HEARTBEAT" = 0 ]; then
  install_heartbeat || echo "UWAGA: nie udało się ustawić heartbeatu." >&2
else
  echo "Heartbeat: pominięto (--skip-heartbeat)"
fi

if [ "$UNINSTALL" = 0 ]; then
  if curl -fsS -m 3 "$SERVER/api/sessions" >/dev/null 2>&1; then
    echo "Serwer $SERVER odpowiada."
    # First heartbeat now, so the machine shows as online without waiting a minute.
    if [ "$SKIP_HEARTBEAT" = 0 ] && { [ -n "$HB_CURL" ] || [ -n "$HB_WGET" ]; }; then
      hb_send || echo "UWAGA: serwer nie przyjął heartbeatu. Zaktualizuj serwer." >&2
    fi
  else
    echo "UWAGA: serwer $SERVER nie odpowiada. Sprawdź adres i zaporę na serwerze (port TCP)." >&2
  fi
  echo "Gotowe. Nowe sesje będą widoczne jako '$NAME'. Uruchom ponownie otwarte sesje Claude Code / OpenCode."
fi
