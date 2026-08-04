#!/usr/bin/env pwsh
#Requires -Version 7

<#
.SYNOPSIS
Snapshots (mode snapshot) or verifies (mode assert) that a scenario left no
orphaned agent temp script files behind (sc-106105).

.DESCRIPTION
Every command writes its script to a temp file in the scripts directory and
relies on deferred cleanup to remove it. That cleanup runs on every path the
agent returns from - success, non-zero exit, and a command killed by its timeout
- but not when the process is terminated outright, which is what an OOM kill from
an unbounded output buffer does. So "the file count did not grow" is the
observable proof that the verbose commands in this scenario each exited cleanly
instead of taking the agent down with them.

Only the exact exec-<digits>.ps1 shape os.CreateTemp produces is counted, so
operator files sharing the directory are never mistaken for agent leftovers, and
the check is a delta against a snapshot rather than an absolute "directory is
empty": other scenarios in the same run legitimately seed files here.

The snapshot is written to -BaselineFile so it survives between steps.
#>

param(
    [Parameter(Mandatory)][ValidateSet('snapshot', 'assert')][string]$Mode,
    [Parameter(Mandatory)][string]$ScriptsDir,
    [Parameter(Mandatory)][string]$BaselineFile
)

$ErrorActionPreference = 'Stop'

function Get-ScriptFileNames {
    if (-not (Test-Path -LiteralPath $ScriptsDir)) {
        Write-Output "Scripts directory does not exist yet: $ScriptsDir"
        return @()
    }
    return @(
        Get-ChildItem -LiteralPath $ScriptsDir -File -Force -ErrorAction SilentlyContinue |
            Where-Object { $_.Name -match '^exec-\d+\.ps1$' } |
            Select-Object -ExpandProperty Name |
            Sort-Object
    )
}

$names = Get-ScriptFileNames
Write-Output "Agent temp script files in ${ScriptsDir}: $($names.Count)"
foreach ($name in $names) {
    Write-Output "  $name"
}

if ($Mode -eq 'snapshot') {
    $names | Out-File -FilePath $BaselineFile -Encoding utf8
    Write-Output "Wrote baseline of $($names.Count) file(s) to $BaselineFile"
    exit 0
}

if (-not (Test-Path -LiteralPath $BaselineFile)) {
    Write-Error "Baseline file not found: $BaselineFile (was the snapshot step run?)"
    exit 1
}

$baseline = @(
    Get-Content -LiteralPath $BaselineFile -ErrorAction SilentlyContinue |
        Where-Object { $_ -and $_.Trim() } |
        ForEach-Object { $_.Trim() }
)
Write-Output "Baseline had $($baseline.Count) file(s): $($baseline -join ', ')"

$orphans = @($names | Where-Object { $baseline -notcontains $_ })
if ($orphans.Count -gt 0) {
    Write-Error "Scenario orphaned $($orphans.Count) temp script file(s), so a command did not exit cleanly: $($orphans -join ', ')"
    exit 1
}

Write-Output "Confirmed no orphaned temp script files: deferred cleanup ran for every command in this scenario"
exit 0
