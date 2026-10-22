#!/usr/bin/env pwsh
#Requires -Version 7

<#
.SYNOPSIS
Assert that a log file does NOT contain the supplied pattern - or, with
-BaselineCount, that it contains no more occurrences than it did before the
step under test, so a line an earlier legitimate step produced is ignored.

.DESCRIPTION
Reading the agent's log file requires elevation now that its data directory
is locked owner-only (sc-108849): the shared implementation runs under sudo
on Unix and natively on the already-elevated Windows runner (see action.yml).
#>

param(
    [Parameter(Mandatory)][string]$LogFile,
    [Parameter(Mandatory)][string]$Pattern,
    [int]$BaselineCount = 0,
    [int]$WaitSeconds = 0,
    [int]$WaitForFileSeconds = 0
)

if ($WaitForFileSeconds -gt 0) {
    for ($i = 1; $i -le $WaitForFileSeconds; $i++) {
        if (Test-Path $LogFile) { break }
        Write-Output "Waiting for log file to be created... ($i/$WaitForFileSeconds)"
        Start-Sleep -Seconds 1
    }
}

if ($WaitSeconds -gt 0) {
    Start-Sleep -Seconds $WaitSeconds
}

if (-not (Test-Path $LogFile)) {
    Write-Error "Log file not found: $LogFile"
    exit 1
}
$logContent = Get-Content $LogFile -Raw
Write-Output $logContent

$count = ([regex]::Matches($logContent, [regex]::Escape($Pattern))).Count
if ($count -gt $BaselineCount) {
    Write-Error "Unexpected log line found: $Pattern (count $count, baseline $BaselineCount)"
    exit 1
}
if ($BaselineCount -gt 0) {
    Write-Output "Confirmed: '$Pattern' did not appear again (count $count, baseline $BaselineCount)"
} else {
    Write-Output "Confirmed: '$Pattern' not present in log"
}
