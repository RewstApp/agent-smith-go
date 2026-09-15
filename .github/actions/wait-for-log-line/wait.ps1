#!/usr/bin/env pwsh
#Requires -Version 7

<#
.SYNOPSIS
Poll a log file until it contains the supplied pattern (or, with -BaselineCount,
until it contains more occurrences than the baseline) or the attempt budget is
exhausted.

.DESCRIPTION
Reading the agent's log file requires elevation now that its data directory
is locked owner-only (sc-108849): the shared implementation runs under sudo
on Unix and natively on the already-elevated Windows runner (see action.yml).

Two modes:

- Bare match (no -BaselineCount): returns as soon as the pattern is present
  anywhere in the file, and does not fail if it never appears - asserting
  presence is the caller's job.

- Delta (-BaselineCount supplied): returns only once the occurrence count
  exceeds the baseline captured before whatever this is waiting on, and fails
  if that never happens.

The delta mode exists because the bare match is unsound for anything the agent
logs more than once. "Subscribed to messages" is written on every connect, so
after the first scenario the log always contains it and a bare match returns
immediately - on the first poll, before sleeping - no matter what the agent is
actually doing. A step named "wait for fresh subscription" then waits for
nothing, and a command dispatched straight after can reach the engine before
the restarted agent has resubscribed. The engine holds it, never dispatches,
and gives up at its own ceiling ~167s later, which surfaces as an unrelated
assertion failing on a device that never received the command. Passing a
baseline makes "fresh" mean fresh. Failing on exhaustion is deliberate: a
caller that captured a baseline is asserting the event happened, and letting
it through silently is what produced that misattribution.
#>

param(
    [Parameter(Mandatory)][string]$LogFile,
    [Parameter(Mandatory)][string]$Pattern,
    [int]$MaxAttempts = 60,
    [int]$IntervalSeconds = 2,
    [int]$BaselineCount = -1
)

$useBaseline = $BaselineCount -ge 0

if ($useBaseline) {
    Write-Output "Waiting for a new '$Pattern' in $LogFile (baseline $BaselineCount, up to $MaxAttempts attempts, $IntervalSeconds s interval)..."
} else {
    Write-Output "Waiting for '$Pattern' in $LogFile (up to $MaxAttempts attempts, $IntervalSeconds s interval)..."
}

for ($i = 1; $i -le $MaxAttempts; $i++) {
    $logContent = Get-Content $LogFile -Raw -ErrorAction SilentlyContinue

    if ($useBaseline) {
        $count = if ($logContent) { ([regex]::Matches($logContent, [regex]::Escape($Pattern))).Count } else { 0 }
        if ($count -gt $BaselineCount) {
            Write-Output "Pattern '$Pattern' appeared again (count $count > baseline $BaselineCount) after $i attempt(s) (~$($i * $IntervalSeconds)s)"
            exit 0
        }
    }
    elseif ($logContent -and ($logContent -match [regex]::Escape($Pattern))) {
        Write-Output "Pattern '$Pattern' found after $i attempt(s) (~$($i * $IntervalSeconds)s)"
        exit 0
    }

    Start-Sleep -Seconds $IntervalSeconds
}

if ($useBaseline) {
    Write-Error "Pattern '$Pattern' did not appear again within $MaxAttempts attempts (count stayed at $BaselineCount)"
    exit 1
}

Write-Output "Pattern '$Pattern' did not appear within $MaxAttempts attempts; downstream assertions will surface the failure"
