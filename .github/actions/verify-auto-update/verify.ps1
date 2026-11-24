#!/usr/bin/env pwsh
#Requires -Version 7

<#
.SYNOPSIS
Assert the agent's log shows the auto updater ran and that the agent is no
longer running the integration-test version.

.DESCRIPTION
Reading the agent's log file requires elevation now that its data directory
is locked owner-only (sc-108849): the shared implementation runs under sudo
on Unix and natively on the already-elevated Windows runner (see action.yml).

The update is not immediate once "Updating agent" is logged: the updater
holds the restart while a command is executing (sc-119836), polling every
30 s, and the live engine's on-connect automation can have one running.
Since sc-138191 the integration build's default command timeout is 20 s, so
such a command can hold the restart for up to about a minute (run
45298389070); a fixed 15 s sleep then failed a healthy update. The log is
therefore polled every 2 s for up to WaitSeconds, and the check passes as
soon as the last "Agent Smith started" line names another version.
#>

param(
    [Parameter(Mandatory)][string]$LogFile,
    [int]$WaitSeconds = 180,
    [string]$ItVersion = "version=v0.0.0-it"
)

$pollSeconds = 2

function Get-LastStartLine([string]$content) {
    $content -split "`r?`n" | Where-Object { $_ -match "Agent Smith started" } | Select-Object -Last 1
}

$deadline = (Get-Date).AddSeconds($WaitSeconds)
$logContent = $null
$lastStartLine = $null
while ($true) {
    if (Test-Path $LogFile) {
        $logContent = Get-Content $LogFile -Raw
        $lastStartLine = if ($logContent) { Get-LastStartLine $logContent } else { $null }
        if ($lastStartLine -and -not ($lastStartLine -match [regex]::Escape($ItVersion))) {
            break
        }
    }
    if ((Get-Date) -ge $deadline) { break }
    Start-Sleep -Seconds $pollSeconds
}

if ($null -eq $logContent) {
    Write-Error "Log file not found: $LogFile"
    exit 1
}
Write-Output $logContent

$failed = $false
foreach ($pattern in @("Checking for updates", "Updating agent")) {
    if ($logContent -notmatch [regex]::Escape($pattern)) {
        Write-Error "Expected auto update log line not found: $pattern"
        $failed = $true
    }
}

if ($lastStartLine -and ($lastStartLine -match [regex]::Escape($ItVersion))) {
    Write-Error "Agent is still running the integration test version $WaitSeconds s after the update was decided: $lastStartLine"
    $failed = $true
}

if ($failed) { exit 1 }
Write-Output "Auto updater integration test passed"
