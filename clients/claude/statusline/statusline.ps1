# agent-status: Claude Code statusLine command for Windows without Git Bash
# (Claude Code then runs statusLine commands through PowerShell; with Git Bash
# use statusline.sh, it starts faster).
#
# Claude Code passes it a JSON with the session's context window use and cost
# on every refresh (https://code.claude.com/docs/en/statusline). This script:
#   1. forwards that JSON to the agent-status server in the background
#      (curl.exe, detached, at most 2 s; a server that is down changes nothing),
#   2. runs your own statusLine command, if the installer saved one in
#      ~/.claude/agent-status/statusline-next, with the same input (through
#      PowerShell, as Claude Code would) and prints its output and exit code,
#   3. otherwise prints a short line: model, context use and cost
#      ("Opus - kontekst 82% - $1.23", with a middle dot).
# Keep this file ASCII: Windows PowerShell reads a script without a BOM as ANSI.
#
# settings.json (the installer sets it):
#   "statusLine": {"type": "command", "command":
#     "powershell -NoProfile -NonInteractive -ExecutionPolicy Bypass -File C:/Users/me/.claude/agent-status/statusline.ps1 \"http://SERVER:7777/hook/statusline?host=NAME\""}
param([string]$Url = $env:AGENT_STATUS_STATUSLINE_URL)

$ErrorActionPreference = 'SilentlyContinue'
$ms = New-Object IO.MemoryStream
[Console]::OpenStandardInput().CopyTo($ms)
$bytes = $ms.ToArray()
$text = [Text.Encoding]::UTF8.GetString($bytes)
$stdout = [Console]::OpenStandardOutput()

# 1. Forward. Start-Process (ShellExecute) inherits none of our handles, so
# Claude Code never waits for curl; the body goes through a file per session,
# overwritten on each refresh.
if ($Url) {
    try {
        $curl = Join-Path $env:WINDIR 'System32\curl.exe'
        $id = if ($text -match '"session_id"\s*:\s*"([A-Za-z0-9_-]{1,100})"') { $Matches[1] } else { 'x' }
        if (Test-Path $curl) {
            $tmp = Join-Path ([IO.Path]::GetTempPath()) "agent-status-statusline-$id.json"
            [IO.File]::WriteAllBytes($tmp, $bytes)
            Start-Process -FilePath $curl -WindowStyle Hidden -ArgumentList @(
                '-s', '-o', 'NUL', '--connect-timeout', '1', '-m', '2', '-X', 'POST',
                '-H', '"Content-Type: application/json"', '--data-binary', "`"@$tmp`"", "`"$Url`"")
        } else {
            # No curl.exe (before Windows 10 1803): a short synchronous request.
            $req = [Net.WebRequest]::Create($Url)
            $req.Method = 'POST'; $req.ContentType = 'application/json'; $req.Timeout = 1000
            $s = $req.GetRequestStream(); $s.Write($bytes, 0, $bytes.Length); $s.Close()
            $req.GetResponse().Close()
        }
    } catch {}
}

# 2. The user's own statusLine command.
$nextFile = Join-Path $HOME '.claude\agent-status\statusline-next'
if ($env:AGENT_STATUS_STATUSLINE_NEXT) { $nextFile = $env:AGENT_STATUS_STATUSLINE_NEXT }
$next = if (Test-Path $nextFile) { [IO.File]::ReadAllText($nextFile).Trim() } else { '' }
if ($next) {
    # The command gets the JSON on its stdin, as from Claude Code: $input inside
    # it, or a program it starts (powershell -File x.ps1, node x.js) reads it.
    # -EncodedCommand keeps its quotes intact.
    $cmd = "`$ProgressPreference = 'SilentlyContinue'`n" + $next
    $psi = New-Object Diagnostics.ProcessStartInfo 'powershell.exe'
    $psi.Arguments = '-NoProfile -NonInteractive -EncodedCommand ' + [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($cmd))
    $psi.UseShellExecute = $false
    $psi.CreateNoWindow = $true
    $psi.RedirectStandardInput = $true
    $psi.RedirectStandardOutput = $true
    $p = [Diagnostics.Process]::Start($psi)
    $p.StandardInput.BaseStream.Write($bytes, 0, $bytes.Length)
    $p.StandardInput.Close()
    $p.StandardOutput.BaseStream.CopyTo($stdout)
    $p.WaitForExit()
    exit $p.ExitCode
}

# 3. Default line.
$line = ''
try {
    $j = $text | ConvertFrom-Json
    $inv = [Globalization.CultureInfo]::InvariantCulture
    $parts = @()
    if ($j.model.display_name) { $parts += $j.model.display_name }
    if ($null -ne $j.context_window.used_percentage) { $parts += 'kontekst {0}%' -f [math]::Round([double]$j.context_window.used_percentage) }
    if ($null -ne $j.cost.total_cost_usd) { $parts += '$' + ([double]$j.cost.total_cost_usd).ToString('0.00', $inv) }
    $line = $parts -join (' ' + [char]0x00B7 + ' ')
} catch {}
$b = [Text.Encoding]::UTF8.GetBytes($line + "`n")
$stdout.Write($b, 0, $b.Length)
exit 0
