#!/usr/bin/env pwsh
#Requires -Version 7

<#
.SYNOPSIS
Assert that a log file contains every supplied pattern (substring match).

.DESCRIPTION
Reading the agent's log file requires elevation now that its data directory
is locked owner-only (sc-108849): the shared implementation runs under sudo
on Unix and natively on the already-elevated Windows runner (see action.yml).
#>

param(
    [Parameter(Mandatory)][string]$LogFile,
    [Parameter(Mandatory)][string]$Patterns,
    [int]$TimeoutSeconds = 60,
    [int]$IntervalSeconds = 2,
    # A string rather than [bool]: PowerShell's string-to-bool coercion treats
    # any non-empty string (including the literal text "false") as $true, so
    # a [bool] parameter bound from a command-line "false" argument would
    # silently misbehave. Compared explicitly against "true" below instead.
    [string]$SubscribedTopicQos = "false"
)

$patternList = $Patterns -split "`r?`n" | Where-Object { $_ -and $_.Trim() }
if ($IntervalSeconds -lt 1) { $IntervalSeconds = 1 }
$deadline = (Get-Date).AddSeconds($TimeoutSeconds)
$started = Get-Date

# Poll until every pattern (and the topic/qos tokens, when asked for) is
# present, or the deadline passes. Success returns at once on a fast runner;
# only the failure case waits the whole budget.
while ($true) {
    $logContent = if (Test-Path $LogFile) { Get-Content $LogFile -Raw -ErrorAction SilentlyContinue } else { $null }
    if (-not $logContent) { $logContent = "" }

    $missing = @()
    foreach ($pattern in $patternList) {
        if ($logContent -notmatch [regex]::Escape($pattern)) { $missing += $pattern }
    }
    $subscribedLine = $null
    $subscribedProblems = @()
    if ($SubscribedTopicQos -eq "true") {
        $subscribedLine = ($logContent -split "`r?`n" | Where-Object { $_ -match "Subscribed to messages" } | Select-Object -First 1)
        if (-not $subscribedLine) {
            $subscribedProblems += "Expected 'Subscribed to messages' not found in logs"
        } else {
            foreach ($token in @("topic=", "qos=")) {
                if ($subscribedLine -notmatch [regex]::Escape($token)) {
                    $subscribedProblems += "Expected '$token' not found in 'Subscribed to messages' log line"
                }
            }
        }
    }

    if ($missing.Count -eq 0 -and $subscribedProblems.Count -eq 0) {
        $elapsed = [int]((Get-Date) - $started).TotalSeconds
        foreach ($pattern in $patternList) { Write-Output "OK: found '$pattern'" }
        if ($subscribedLine) { Write-Output "Subscribed messages log: $subscribedLine" }
        Write-Output "All expected log patterns found after ${elapsed}s"
        exit 0
    }

    if ((Get-Date) -ge $deadline) {
        if (-not (Test-Path $LogFile)) { Write-Error "Log file not found: $LogFile" }
        Write-Output $logContent
        foreach ($pattern in $missing) { Write-Error "Expected log line not found after ${TimeoutSeconds}s: $pattern" }
        foreach ($problem in $subscribedProblems) { Write-Error $problem }
        exit 1
    }
    Start-Sleep -Seconds $IntervalSeconds
}
