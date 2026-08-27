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
#>

param(
    [Parameter(Mandatory)][string]$LogFile,
    [int]$WaitSeconds = 15,
    [string]$ItVersion = "version=v0.0.0-it"
)

Start-Sleep -Seconds $WaitSeconds
if (-not (Test-Path $LogFile)) {
    Write-Error "Log file not found: $LogFile"
    exit 1
}
$logContent = Get-Content $LogFile -Raw
Write-Output $logContent

$failed = $false
foreach ($pattern in @("Checking for updates", "Updating agent")) {
    if ($logContent -notmatch [regex]::Escape($pattern)) {
        Write-Error "Expected auto update log line not found: $pattern"
        $failed = $true
    }
}

$lastStartLine = ($logContent -split "`r?`n" | Where-Object { $_ -match "Agent Smith started" } | Select-Object -Last 1)
if ($lastStartLine -and ($lastStartLine -match [regex]::Escape($ItVersion))) {
    Write-Error "Agent is still running the integration test version after update: $lastStartLine"
    $failed = $true
}

if ($failed) { exit 1 }
Write-Output "Auto updater integration test passed"
