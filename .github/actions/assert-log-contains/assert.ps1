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
    [int]$WaitSeconds = 0,
    # A string rather than [bool]: PowerShell's string-to-bool coercion treats
    # any non-empty string (including the literal text "false") as $true, so
    # a [bool] parameter bound from a command-line "false" argument would
    # silently misbehave. Compared explicitly against "true" below instead.
    [string]$SubscribedTopicQos = "false"
)

if ($WaitSeconds -gt 0) {
    Start-Sleep -Seconds $WaitSeconds
}

if (-not (Test-Path $LogFile)) {
    Write-Error "Log file not found: $LogFile"
    exit 1
}
$logContent = Get-Content $LogFile -Raw
Write-Output $logContent

$patternList = $Patterns -split "`r?`n" | Where-Object { $_ -and $_.Trim() }
$failed = $false
foreach ($pattern in $patternList) {
    if ($logContent -notmatch [regex]::Escape($pattern)) {
        Write-Error "Expected log line not found: $pattern"
        $failed = $true
    } else {
        Write-Output "OK: found '$pattern'"
    }
}

if ($SubscribedTopicQos -eq "true") {
    $subscribedLine = ($logContent -split "`r?`n" | Where-Object { $_ -match "Subscribed to messages" } | Select-Object -First 1)
    if (-not $subscribedLine) {
        Write-Error "Expected 'Subscribed to messages' not found in logs"
        $failed = $true
    } else {
        foreach ($token in @("topic=", "qos=")) {
            if ($subscribedLine -notmatch [regex]::Escape($token)) {
                Write-Error "Expected '$token' not found in 'Subscribed to messages' log line"
                $failed = $true
            }
        }
        Write-Output "Subscribed messages log: $subscribedLine"
    }
}

if ($failed) { exit 1 }
Write-Output "All expected log patterns found"
