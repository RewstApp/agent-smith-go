#!/usr/bin/env pwsh
#Requires -Version 7

<#
.SYNOPSIS
Assert that the supplied list of paths no longer exists.

.DESCRIPTION
Checking these paths requires elevation now that the agent's data directory
is locked owner-only (sc-108849): Test-Path against a still-existing,
owner-only directory throws (rather than returning $false) on Unix, so an
unprivileged check cannot even tell "gone" apart from "exists but locked
down" - the shared implementation runs under sudo on Unix and natively on
the already-elevated Windows runner (see action.yml).
#>

$pathList = $env:PATHS -split "`r?`n" | Where-Object { $_ -and $_.Trim() }
$failed = $false
foreach ($path in $pathList) {
    $expanded = $ExecutionContext.InvokeCommand.ExpandString($path).Trim()
    if (Test-Path -Path $expanded) {
        Write-Error "Path still exists: $expanded"
        $failed = $true
    } else {
        Write-Output "OK: $expanded is gone"
    }
}
if ($failed) { exit 1 }
Write-Output "All directories have been deleted"
