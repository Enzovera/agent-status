# agent-status client installer for Windows (Claude Code hooks + OpenCode plugin).
#
#   & ([scriptblock]::Create((irm https://raw.githubusercontent.com/Enzovera/agent-status/main/install/client.ps1))) -Server 192.168.1.10
#
# Options: -Server ADDR (IP, host or host:port), -Name NAME (default: computer name),
#          -SkipClaude, -SkipOpenCode, -SkipHeartbeat, -SkipSkill, -SkipStatusline, -Uninstall,
#          -Ref REF (default: main).
#
# Statusline: with Claude Code, settings.json "statusLine" runs ~/.claude/agent-status/
# statusline.sh (Git Bash) or statusline.ps1, which sends context window use and cost to
# the server and then runs your previous statusLine, if any (put back on -Uninstall).
# -SkipStatusline leaves it alone (install and uninstall).
#
# Skill: with Claude Code, ~/.claude/skills/agent-flow-review/SKILL.md (/agent-flow-review:
# reads the server's /api/report and suggests workflow fixes and permission rules) with the
# server address filled in. -SkipSkill leaves it alone (install and uninstall).
#
# Heartbeat: the machine shows as online while it POSTs /hook/heartbeat every minute.
# It is per machine, not per agent, so it is installed even with -SkipClaude
# -SkipOpenCode: a per-user scheduled task "agent-status-heartbeat" (at logon + every
# minute, hidden, no admin). -SkipHeartbeat leaves that task alone (install and uninstall).
#
# Re-running replaces the previous agent-status entries (e.g. to change the server).
# -Uninstall removes them. Only entries pointing at /hook/claude are touched.
param(
    [string]$Server,
    [string]$Name = $env:COMPUTERNAME,
    [switch]$SkipClaude,
    [switch]$SkipOpenCode,
    [switch]$SkipHeartbeat,
    [switch]$SkipSkill,
    [switch]$SkipStatusline,
    [switch]$Uninstall,
    [string]$Ref = "main"
)

$ErrorActionPreference = "Stop"
$Repo = "https://raw.githubusercontent.com/Enzovera/agent-status/$Ref"
$Events = "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure",
          "PermissionRequest", "PermissionDenied", "Notification", "SubagentStart",
          "SubagentStop", "TaskCreated", "TaskCompleted", "PreCompact", "PostCompact",
          "PostModelSwitch", "Stop", "StopFailure", "SessionEnd"
# Events older versions registered: only cleaned up. Claude Code runs no http
# hooks on SessionStart.
$Retired = @("SessionStart")

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
    foreach ($ev in @($Events) + $Retired) {
        $kept = @()
        if ($settings.hooks.PSObject.Properties[$ev]) {
            $kept = @($settings.hooks.$ev | Where-Object { -not (Test-Ours $_) })
        }
        if (-not $Uninstall -and $Events -contains $ev) {
            $kept += [pscustomobject]@{ hooks = @([pscustomobject]@{ type = "http"; url = $url; timeout = 2 }) }
        }
        $settings.hooks.PSObject.Properties.Remove($ev)
        if ($kept.Count -gt 0) {
            $settings.hooks | Add-Member -NotePropertyName $ev -NotePropertyValue ([object[]]$kept)
        }
    }
    if (@($settings.hooks.PSObject.Properties).Count -eq 0) { $settings.PSObject.Properties.Remove("hooks") }
    if (-not $SkipStatusline) { Update-Statusline $settings }
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

$SkillName = "agent-flow-review"

# A file of this repository as bytes: from the checkout next to this script when there
# is one, else downloaded from $Repo (irm | scriptblock runs have no local files).
function Get-RepoBytes($rel) {
    $local = if ($PSScriptRoot) { Join-Path $PSScriptRoot ("..\" + $rel.Replace('/', '\')) } else { "" }
    if ($local -and (Test-Path $local)) { return [IO.File]::ReadAllBytes((Resolve-Path $local)) }
    return (Invoke-WebRequest -UseBasicParsing "$Repo/$rel").RawContentStream.ToArray()
}

# Statusline: Claude Code hands its statusLine command the session's context window use
# and cost; ours forwards that to the server, then runs the user's own statusLine (saved
# in statusline-next) or prints a short line. The original statusLine object is kept in
# statusline-orig.json and put back on -Uninstall.
$SlDir = Join-Path $HOME ".claude\agent-status"

function Test-OurStatusline($sl) { return [bool]($sl -and ("$($sl.command)" -like "*agent-status/statusline*")) }

# Claude Code runs statusLine commands through Git Bash when it is installed, else PowerShell.
function Test-GitBash {
    if ($env:CLAUDE_CODE_GIT_BASH_PATH -and (Test-Path $env:CLAUDE_CODE_GIT_BASH_PATH)) { return $true }
    $git = Get-Command git.exe -ErrorAction SilentlyContinue
    if (-not $git) { return $false }
    $dir = Split-Path (Split-Path $git.Source)            # ...\Git (from ...\Git\cmd\git.exe)
    foreach ($d in @($dir, (Split-Path $dir))) {         # or ...\Git\mingw64\bin\git.exe
        if ($d -and (Test-Path (Join-Path $d "bin\bash.exe"))) { return $true }
    }
    return $false
}

function Remove-StatuslineFiles {
    foreach ($f in "statusline.sh", "statusline.ps1", "statusline-next", "statusline-orig.json") {
        $p = Join-Path $SlDir $f
        if (Test-Path $p) { Remove-Item $p }
    }
    if ((Test-Path $SlDir) -and -not (Get-ChildItem -Force $SlDir)) { Remove-Item $SlDir }
}

# Sets (or on -Uninstall restores) settings.statusLine; called by Install-Claude before it
# writes settings.json, so one backup covers both.
function Update-Statusline($settings) {
    $utf8 = New-Object Text.UTF8Encoding $false
    $next = Join-Path $SlDir "statusline-next"
    $orig = Join-Path $SlDir "statusline-orig.json"
    $cur = if ($settings.PSObject.Properties["statusLine"]) { $settings.statusLine } else { $null }
    if ($Uninstall) {
        if (Test-OurStatusline $cur) {
            $settings.PSObject.Properties.Remove("statusLine")
            if (Test-Path $orig) {
                $settings | Add-Member -NotePropertyName statusLine -NotePropertyValue ([IO.File]::ReadAllText($orig, $utf8) | ConvertFrom-Json)
                Write-Host "Statusline: przywrocono Twoj poprzedni statusLine"
            } else {
                Write-Host "Statusline: usunieto"
            }
        }
        # Someone else's statusLine (changed after installing) is left as it is.
        Remove-StatuslineFiles
        return
    }
    try {
        $sh = Test-GitBash
        $file = if ($sh) { "statusline.sh" } else { "statusline.ps1" }
        $bytes = Get-RepoBytes "clients/claude/statusline/$file"
        New-Item -ItemType Directory -Force $SlDir | Out-Null
        foreach ($f in "statusline.sh", "statusline.ps1") { $p = Join-Path $SlDir $f; if (Test-Path $p) { Remove-Item $p } }
        [IO.File]::WriteAllBytes((Join-Path $SlDir $file), $bytes)
        $url = "$Server/hook/statusline?host=$([uri]::EscapeDataString($Name))"
        $cmd = if ($sh) { "sh ~/.claude/agent-status/statusline.sh '$url'" }
               else { "powershell -NoProfile -NonInteractive -ExecutionPolicy Bypass -File $((Join-Path $SlDir 'statusline.ps1').Replace('\', '/')) `"$url`"" }
        $chained = $false
        if (Test-OurStatusline $cur) {
            # Re-install: new address or variant; the saved original stays as it is.
            $cur | Add-Member -NotePropertyName command -NotePropertyValue $cmd -Force
            $chained = (Test-Path $next)
        } elseif ($cur) {
            # The user's own statusLine keeps running, after ours, with the same input.
            [IO.File]::WriteAllText($next, "$($cur.command)", $utf8)
            [IO.File]::WriteAllText($orig, (ConvertTo-Json -InputObject $cur -Depth 20), $utf8)
            $new = ConvertTo-Json -InputObject $cur -Depth 20 | ConvertFrom-Json
            $new | Add-Member -NotePropertyName type -NotePropertyValue "command" -Force
            $new | Add-Member -NotePropertyName command -NotePropertyValue $cmd -Force
            $settings.PSObject.Properties.Remove("statusLine")
            $settings | Add-Member -NotePropertyName statusLine -NotePropertyValue $new
            $chained = $true
        } else {
            foreach ($p in $next, $orig) { if (Test-Path $p) { Remove-Item $p } }
            $settings | Add-Member -NotePropertyName statusLine -NotePropertyValue ([pscustomobject]@{ type = "command"; command = $cmd }) -Force
        }
        $how = if ($chained) { " (Twoj poprzedni statusline dziala dalej)" } else { "" }
        Write-Host "Statusline: kontekst i koszt sesji wysylane na serwer$how"
    } catch {
        Write-Warning "Statusline: nie udalo sie ustawic: $($_.Exception.Message)"
    }
}

# The skill template is UTF-8 (Polish); it is handled as bytes so PS 5.1 never decodes it
# with a legacy code page. From a local checkout the file next to this script is used.
function Install-Skill {
    $dir = Join-Path $HOME ".claude\skills\$SkillName"
    $path = Join-Path $dir "SKILL.md"
    if ($Uninstall) {
        if (Test-Path $path) { Remove-Item $path; Write-Host "Skill: usunieto $path" }
        if ((Test-Path $dir) -and -not (Get-ChildItem -Force $dir)) { Remove-Item $dir }
        return
    }
    try {
        $bytes = Get-RepoBytes "clients/claude/skills/$SkillName/SKILL.md"
        $src = (New-Object Text.UTF8Encoding $false).GetString($bytes).TrimStart([char]0xFEFF)
        $src = $src.Replace("http://SERVER:7777", $Server)
        New-Item -ItemType Directory -Force $dir | Out-Null
        [IO.File]::WriteAllText($path, $src, (New-Object Text.UTF8Encoding $false))
        Write-Host "Skill: zapisany w $path - w Claude Code: /$SkillName (analiza statystyk i rekomendacje)"
    } catch {
        Write-Warning "Skill: nie udalo sie zapisac $path`: $($_.Exception.Message)"
    }
}

$HeartbeatTask = "agent-status-heartbeat"

function Get-HeartbeatUrl { "$Server/hook/heartbeat?host=$([uri]::EscapeDataString($Name))" }

# What the task runs. curl.exe ships with Windows 10 1803+; conhost --headless (1809+)
# keeps it from flashing a console window. Older systems get a hidden PowerShell.
function New-HeartbeatAction($url) {
    $sys = Join-Path $env:WINDIR "System32"
    $curl = Join-Path $sys "curl.exe"
    $conhost = Join-Path $sys "conhost.exe"
    if ((Test-Path $curl) -and (Test-Path $conhost) -and [Environment]::OSVersion.Version.Build -ge 17763) {
        return New-ScheduledTaskAction -Execute $conhost -Argument "--headless `"$curl`" -s -m 5 -X POST `"$url`""
    }
    $ps = Join-Path $sys "WindowsPowerShell\v1.0\powershell.exe"
    $cmd = "try { Invoke-WebRequest -UseBasicParsing -Method Post -TimeoutSec 5 '$($url.Replace("'", "''"))' | Out-Null } catch {}"
    return New-ScheduledTaskAction -Execute $ps -Argument "-NoProfile -NonInteractive -WindowStyle Hidden -Command `"$cmd`""
}

function New-HeartbeatTask {
    $user = [Security.Principal.WindowsIdentity]::GetCurrent().Name
    # Once + 1 min repetition without RepetitionDuration = repeat indefinitely
    # (TimeSpan.MaxValue is rejected on Windows 10/11). It keeps firing after reboots;
    # the logon trigger only makes the machine show up right after logging in.
    $triggers = @(
        (New-ScheduledTaskTrigger -AtLogOn -User $user),
        (New-ScheduledTaskTrigger -Once -At (Get-Date) -RepetitionInterval (New-TimeSpan -Minutes 1))
    )
    $settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries `
        -StartWhenAvailable -MultipleInstances IgnoreNew -ExecutionTimeLimit (New-TimeSpan -Minutes 1)
    $principal = New-ScheduledTaskPrincipal -UserId $user -LogonType Interactive -RunLevel Limited
    New-ScheduledTask -Action (New-HeartbeatAction (Get-HeartbeatUrl)) -Trigger $triggers -Settings $settings `
        -Principal $principal -Description "agent-status: co minute zglasza serwerowi $Server, ze maszyna '$Name' dziala."
}

function Install-Heartbeat {
    try {
        if ($Uninstall) {
            if (Get-ScheduledTask -TaskName $HeartbeatTask -ErrorAction SilentlyContinue) {
                Unregister-ScheduledTask -TaskName $HeartbeatTask -Confirm:$false
                Write-Host "Heartbeat: usunieto zadanie '$HeartbeatTask' z Harmonogramu zadan"
            }
            return
        }
        Register-ScheduledTask -TaskName $HeartbeatTask -InputObject (New-HeartbeatTask) -Force | Out-Null
        Write-Host "Heartbeat: zadanie '$HeartbeatTask' w Harmonogramie zadan (co minute)"
    } catch {
        Write-Warning "Heartbeat: nie udalo sie ustawic zadania '$HeartbeatTask': $($_.Exception.Message)"
    }
}

$claude = -not $SkipClaude -and ((Test-Path (Join-Path $HOME ".claude")) -or (Get-Command claude -ErrorAction SilentlyContinue))
$opencode = -not $SkipOpenCode -and ((Test-Path (Join-Path $HOME ".config\opencode")) -or (Get-Command opencode -ErrorAction SilentlyContinue))

if ($claude) { Install-Claude } else { Write-Host "Claude Code: pominieto (nie znaleziono)" }
if ($claude -and -not $SkipSkill) { Install-Skill }
elseif ($claude) { Write-Host "Skill: pominieto (-SkipSkill)" }
if ($opencode) { Install-OpenCode } else { Write-Host "OpenCode: pominieto (nie znaleziono)" }
if (-not $SkipHeartbeat) { Install-Heartbeat } else { Write-Host "Heartbeat: pominieto (-SkipHeartbeat)" }

if (-not $Uninstall) {
    try {
        Invoke-WebRequest -UseBasicParsing -TimeoutSec 3 "$Server/api/sessions" | Out-Null
        Write-Host "Serwer $Server odpowiada."
        # First heartbeat now, so the machine shows as online without waiting a minute.
        if (-not $SkipHeartbeat) {
            try { Invoke-WebRequest -UseBasicParsing -TimeoutSec 3 -Method Post (Get-HeartbeatUrl) | Out-Null }
            catch { Write-Warning "Serwer nie przyjal heartbeatu ($($_.Exception.Message)). Zaktualizuj serwer." }
        }
    } catch {
        Write-Warning "Serwer $Server nie odpowiada. Sprawdz adres i zapore na serwerze (port TCP)."
    }
    Write-Host "Gotowe. Nowe sesje beda widoczne jako '$Name'. Uruchom ponownie otwarte sesje Claude Code / OpenCode."
}
