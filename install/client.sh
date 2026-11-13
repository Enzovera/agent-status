#!/bin/sh
# agent-status client installer for macOS / Linux (Claude Code hooks + OpenCode plugin).
#
#   curl -fsSL https://raw.githubusercontent.com/Enzovera/agent-status/main/install/client.sh | sh -s -- --server 192.168.1.10
#
# Options: --server ADDR (IP, host or host:port), --name NAME (default: hostname),
#          --skip-claude, --skip-opencode, --uninstall, --ref REF (default: main).
# Re-running replaces the previous agent-status entries. Needs python3 or node to edit JSON.
set -eu

SERVER=""
NAME="$(hostname 2>/dev/null || uname -n)"
NAME="${NAME%%.*}"
SKIP_CLAUDE=0
SKIP_OPENCODE=0
UNINSTALL=0
REF="main"

while [ $# -gt 0 ]; do
  case "$1" in
    --server) SERVER="$2"; shift 2 ;;
    --name) NAME="$2"; shift 2 ;;
    --skip-claude) SKIP_CLAUDE=1; shift ;;
    --skip-opencode) SKIP_OPENCODE=1; shift ;;
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
for ev in ["SessionStart", "UserPromptSubmit", "PreToolUse", "PermissionRequest",
           "Notification", "Stop", "StopFailure", "SessionEnd"]:
    kept = [g for g in hooks.get(ev, []) if not ours(g)]
    if os.environ["AS_UNINSTALL"] != "1":
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
for (const ev of ["SessionStart", "UserPromptSubmit", "PreToolUse", "PermissionRequest",
                  "Notification", "Stop", "StopFailure", "SessionEnd"]) {
  const kept = (hooks[ev] ?? []).filter((g) => !ours(g))
  if (process.env.AS_UNINSTALL !== "1") kept.push({ hooks: [{ type: "http", url, timeout: 2 }] })
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

if [ "$UNINSTALL" = 0 ]; then
  if curl -fsS -m 3 "$SERVER/api/sessions" >/dev/null 2>&1; then
    echo "Serwer $SERVER odpowiada."
  else
    echo "UWAGA: serwer $SERVER nie odpowiada. Sprawdź adres i zaporę na serwerze (port TCP)." >&2
  fi
  echo "Gotowe. Nowe sesje będą widoczne jako '$NAME'. Uruchom ponownie otwarte sesje Claude Code / OpenCode."
fi
