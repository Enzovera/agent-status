#!/bin/sh
# agent-status: Claude Code statusLine command (macOS, Linux, Windows with Git Bash).
#
# Claude Code passes it a JSON with the session's context window use and cost
# on every refresh (https://code.claude.com/docs/en/statusline). This script:
#   1. forwards that JSON to the agent-status server in the background
#      (curl, at most 2 s, output discarded; a server that is down changes nothing),
#   2. runs your own statusLine command, if the installer saved one in
#      ~/.claude/agent-status/statusline-next, with the same input and prints
#      its output (and returns its exit code),
#   3. otherwise prints a short line: model · kontekst 82% · $1.23.
#
# settings.json (the installer sets it):
#   "statusLine": {"type": "command",
#     "command": "~/.claude/agent-status/statusline.sh 'http://SERVER:7777/hook/statusline?host=NAME'"}
# The URL may also come from AGENT_STATUS_STATUSLINE_URL.

url=${1:-$AGENT_STATUS_STATUSLINE_URL}
next_file=${AGENT_STATUS_STATUSLINE_NEXT:-$HOME/.claude/agent-status/statusline-next}
input=$(cat)

if [ -n "$url" ] && command -v curl >/dev/null 2>&1; then
  # Background, with every stream detached, so Claude Code never waits for it.
  { printf '%s' "$input" | curl -s -o /dev/null --connect-timeout 1 -m 2 -X POST \
      -H 'Content-Type: application/json' --data-binary @- "$url"; } </dev/null >/dev/null 2>&1 &
fi

next=
if [ -r "$next_file" ]; then next=$(cat "$next_file"); fi
if [ -n "$next" ]; then
  printf '%s' "$input" | sh -c "$next"
  exit $?
fi

printf '%s' "$input" | awk '
{ j = j $0 }
function val(re, s,   v) {
  if (!match(s, re)) return ""
  v = substr(s, RSTART, RLENGTH)
  sub(/^"[^"]*"[ \t]*:[ \t]*"?/, "", v)
  sub(/"$/, "", v)
  return v == "null" ? "" : v
}
END {
  model = val("\"display_name\"[ \t]*:[ \t]*\"[^\"]*\"", j)
  i = index(j, "\"context_window\"")
  ctx = i ? val("\"used_percentage\"[ \t]*:[ \t]*(null|[0-9.eE+-]+)", substr(j, i)) : ""
  cost = val("\"total_cost_usd\"[ \t]*:[ \t]*[0-9.eE+-]+", j)
  out = model
  if (ctx != "") out = out (out != "" ? " · " : "") sprintf("kontekst %d%%", ctx + 0.5)
  if (cost != "") out = out (out != "" ? " · " : "") sprintf("$%.2f", cost)
  print out
}'
exit 0
