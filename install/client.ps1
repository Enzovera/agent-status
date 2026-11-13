# agent-status client installer for Windows (Claude Code hooks + OpenCode plugin).
#
#   & ([scriptblock]::Create((irm https://raw.githubusercontent.com/Enzovera/agent-status/main/install/client.ps1))) -Server 192.168.1.10
#
# Re-running replaces the previous agent-status entries (e.g. to change the server).
# -Uninstall removes them. Only entries pointing at /hook/claude are touched.
param(
    [string]$Server,
    [string]$Name = $env:COMPUTERNAME,
    [switch]$SkipClaude,
    [switch]$SkipOpenCode,
    [switch]$Uninstall,
    [string]$Ref = "main"
)

$ErrorActionPreference = "Stop"
$Repo = "https://raw.githubusercontent.com/Enzovera/agent-status/$Ref"
$Events = "SessionStart", "UserPromptSubmit", "PreToolUse", "PermissionRequest",
          "Notification", "Stop", "StopFailure", "SessionEnd"

if (-not $Uninstall -and -not $Server) { throw "Podaj adres serwera: -Server 192.168.1.10 (albo host:port)" }
if ($Server) {
    if ($Server -notmatch '^https?://') { $Server = "http://$Server" }
    $Server = $Server.TrimEnd('/')
    if ($Server -notmatch '^https?://[^/]+:\d+$') { $Server = "${Server}:7777" }
}

function Test-Ours($group) {
    foreach ($h in @($group.hooks)) { if ("$($h.url)" -like "*/hook/claude*") { return $true } }
    return $false
}

function Install-Claude {
    $dir = Join-Path $HOME ".claude"
    $path = Join-Path $dir "settings.json"
    if (Test-Path $path) {
        $raw = [IO.File]::ReadAllText($path)
        $settings = if ($raw.Trim()) { $raw | ConvertFrom-Json } else { [pscustomobject]@{} }
        Copy-Item $path "$path.bak" -Force
    } else {
        if ($Uninstall) { return }
        New-Item -ItemType Directory -Force $dir | Out-Null
        $settings = [pscustomobject]@{}
    }
    if (-not $settings.PSObject.Properties["hooks"]) {
        $settings | Add-Member -NotePropertyName hooks -NotePropertyValue ([pscustomobject]@{})
    }
    $url = "$Server/hook/claude?host=$([uri]::EscapeDataString($Name))"
    foreach ($ev in $Events) {
        $kept = @()
        if ($settings.hooks.PSObject.Properties[$ev]) {
            $kept = @($settings.hooks.$ev | Where-Object { -not (Test-Ours $_) })
        }
        if (-not $Uninstall) {
            $kept += [pscustomobject]@{ hooks = @([pscustomobject]@{ type = "http"; url = $url; timeout = 2 }) }
        }
        $settings.hooks.PSObject.Properties.Remove($ev)
        if ($kept.Count -gt 0) {
            $settings.hooks | Add-Member -NotePropertyName $ev -NotePropertyValue ([object[]]$kept)
        }
    }
    if (@($settings.hooks.PSObject.Properties).Count -eq 0) { $settings.PSObject.Properties.Remove("hooks") }
    $json = ConvertTo-Json -InputObject $settings -Depth 50
    [IO.File]::WriteAllText($path, $json, (New-Object Text.UTF8Encoding $false))
    if ($Uninstall) { Write-Host "Claude Code: usunieto hooki z $path" }
    else { Write-Host "Claude Code: hooki dopisane do $path (kopia: settings.json.bak)" }
}

function Install-OpenCode {
    $dir = Join-Path $HOME ".config\opencode\plugins"
    $path = Join-Path $dir "agent-status.ts"
    if ($Uninstall) {
        if (Test-Path $path) { Remove-Item $path; Write-Host "OpenCode: usunieto $path" }
        return
    }
    New-Item -ItemType Directory -Force $dir | Out-Null
    $src = (Invoke-WebRequest -UseBasicParsing "$Repo/clients/opencode/agent-status.ts").Content
    if ($src -is [byte[]]) { $src = [Text.Encoding]::UTF8.GetString($src) }
    $src = $src.Replace('"http://SERVER:7777"', "`"$Server`"")
    $src = $src.Replace('process.env.AGENT_STATUS_HOST ?? hostname()', "process.env.AGENT_STATUS_HOST ?? $(ConvertTo-Json $Name)")
    [IO.File]::WriteAllText($path, $src, (New-Object Text.UTF8Encoding $false))
    Write-Host "OpenCode: plugin zapisany w $path"
}

$claude = -not $SkipClaude -and ((Test-Path (Join-Path $HOME ".claude")) -or (Get-Command claude -ErrorAction SilentlyContinue))
$opencode = -not $SkipOpenCode -and ((Test-Path (Join-Path $HOME ".config\opencode")) -or (Get-Command opencode -ErrorAction SilentlyContinue))

if ($claude) { Install-Claude } else { Write-Host "Claude Code: pominieto (nie znaleziono)" }
if ($opencode) { Install-OpenCode } else { Write-Host "OpenCode: pominieto (nie znaleziono)" }

if (-not $Uninstall) {
    try {
        Invoke-WebRequest -UseBasicParsing -TimeoutSec 3 "$Server/api/sessions" | Out-Null
        Write-Host "Serwer $Server odpowiada."
    } catch {
        Write-Warning "Serwer $Server nie odpowiada. Sprawdz adres i zapore na serwerze (port TCP)."
    }
    Write-Host "Gotowe. Nowe sesje beda widoczne jako '$Name'. Uruchom ponownie otwarte sesje Claude Code / OpenCode."
}
