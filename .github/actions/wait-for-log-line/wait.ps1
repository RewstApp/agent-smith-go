#!/usr/bin/env pwsh
#Requires -Version 7

<#
.SYNOPSIS
Poll a log file until it contains the supplied pattern or the attempt budget
is exhausted. Does not fail if the pattern never appears - asserting presence
is the caller's job.

.DESCRIPTION
Reading the agent's log file requires elevation now that its data directory
is locked owner-only (sc-108849): the shared implementation runs under sudo
on Unix and natively on the already-elevated Windows runner (see action.yml).
#>

param(
    [Parameter(Mandatory)][string]$LogFile,
    [Parameter(Mandatory)][string]$Pattern,
    [int]$MaxAttempts = 60,
    [int]$IntervalSeconds = 2
)

Write-Output "Waiting for '$Pattern' in $LogFile (up to $MaxAttempts attempts, $IntervalSeconds s interval)..."
for ($i = 1; $i -le $MaxAttempts; $i++) {
    $logContent = Get-Content $LogFile -Raw -ErrorAction SilentlyContinue
    if ($logContent -and ($logContent -match [regex]::Escape($Pattern))) {
        Write-Output "Pattern '$Pattern' found after $i attempt(s) (~$($i * $IntervalSeconds)s)"
        exit 0
    }
    Start-Sleep -Seconds $IntervalSeconds
}
Write-Output "Pattern '$Pattern' did not appear within $MaxAttempts attempts; downstream assertions will surface the failure"
